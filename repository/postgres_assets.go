package repository

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/jackc/pgx/v5"

	"uniche/api/repositoryapi"
)

func (p *Postgres) CreateAsset(ctx context.Context, req *repositoryapi.CreateAssetRequest) (repositoryapi.CreateAssetRes, error) {
	assetID, err := newID()
	if err != nil {
		return nil, err
	}
	versionID, err := newID()
	if err != nil {
		return nil, err
	}

	tx, err := p.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("creating asset: %w", err)
	}
	defer tx.Rollback(ctx)
	if err := insertAsset(ctx, tx, newAsset{
		id:         assetID,
		versionID:  versionID,
		visibility: string(req.Visibility.Or(repositoryapi.VisibilityPublic)),
		source:     string(repositoryapi.AssetSourceLocal),
		metadata:   req.Metadata,
	}); err != nil {
		return nil, fmt.Errorf("creating asset: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("creating asset: %w", err)
	}

	asset, err := p.asset(ctx, assetID)
	if err != nil {
		return nil, err
	}
	return p.created(asset), nil
}

// UploadAsset creates an asset, its first version and its file from one
// multipart request. The whole body has arrived before this runs, so a dropped
// connection leaves nothing behind. Everything after the records exist is
// keyed by the Idempotency-Key: a retry returns the finished asset, or
// finishes the pending file with the bytes it carries. Errors after that point
// carry the upload's ids, so the client can also resume with uploadFile.
func (p *Postgres) UploadAsset(ctx context.Context, req *repositoryapi.UploadAssetRequestMultipart, params repositoryapi.UploadAssetParams) (repositoryapi.UploadAssetRes, error) {
	key := params.IdempotencyKey
	assetID, file, err := p.uploadByKey(ctx, key)
	if err != nil && !errors.Is(err, errNotFound) {
		return nil, err
	}
	if file != nil && file.status == string(repositoryapi.FileStatusAvailable) {
		return p.uploadedAsset(ctx, assetID, file)
	}

	partID, err := newID()
	if err != nil {
		return nil, err
	}
	got := received{part: filepath.Join(p.filesDir, "uploads", partID+".part")}
	defer os.Remove(got.part) // a no-op once the upload is stored
	if got.sum, got.size, err = receive(got.part, req.File.File, req.File.Size); err != nil {
		err = fmt.Errorf("receiving upload for key %s: %w", key, err)
		if file == nil {
			return nil, err
		}
		return p.uploadFailed(assetID, file, err), nil
	}

	if file == nil {
		if assetID, file, err = p.createUploadOnce(ctx, key, req, got); err != nil {
			return nil, err
		}
	}
	return p.finishUpload(ctx, assetID, file, got, req.SHA256.Or(got.sum))
}

// received is an upload's bytes on disk, not yet checked.
type received struct {
	part, sum string
	size      int64
}

// createUploadOnce creates the upload for key, or loads the one a concurrent
// retry created first.
func (p *Postgres) createUploadOnce(ctx context.Context, key string, req *repositoryapi.UploadAssetRequestMultipart, got received) (string, *fileRow, error) {
	assetID, file, err := p.createUpload(ctx, key, req, got)
	if isUniqueViolation(err) {
		return p.uploadByKey(ctx, key)
	}
	if err != nil {
		return "", nil, fmt.Errorf("creating upload for key %s: %w", key, err)
	}
	return assetID, file, nil
}

// finishUpload checks the received bytes against the checksum this request
// declares, or accepts them when it declares none, and stores them.
func (p *Postgres) finishUpload(ctx context.Context, assetID string, file *fileRow, got received, want string) (repositoryapi.UploadAssetRes, error) {
	if file.status == string(repositoryapi.FileStatusAvailable) { // a concurrent retry finished it
		return p.uploadedAsset(ctx, assetID, file)
	}
	// This request's checksum decides, not the recorded one: the first attempt
	// may have declared the wrong checksum or sent the wrong file, and its
	// retry must still be able to finish. complete records the actual values.
	if got.sum != want {
		c := repositoryapi.UploadAssetConflict(p.uploadProblem(
			"the file does not match the declared checksum; retry with the same Idempotency-Key and the correct file",
			assetID, file))
		c.Code = repositoryapi.NewOptProblemCode(repositoryapi.ProblemCodeChecksumMismatch)
		c.BytesReceived = repositoryapi.NewOptInt64(0)
		return &c, nil
	}
	if err := p.complete(ctx, file, got.part, got.sum, got.size); err != nil {
		return p.uploadFailed(assetID, file, err), nil
	}
	return p.uploadedAsset(ctx, assetID, file)
}

// uploadByKey finds the asset created with an Idempotency-Key and the file of
// that upload. It returns errNotFound when the key is new.
func (p *Postgres) uploadByKey(ctx context.Context, key string) (string, *fileRow, error) {
	var assetID string
	err := p.db.QueryRow(ctx, `SELECT id FROM assets WHERE idempotency_key = $1`, key).Scan(&assetID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil, errNotFound
	}
	if err != nil {
		return "", nil, fmt.Errorf("finding upload for key %s: %w", key, err)
	}
	file, err := scanFile(p.db.QueryRow(ctx,
		`SELECT `+fileCols+` FROM asset_files
		 WHERE version_id IN (SELECT id FROM asset_versions WHERE asset_id = $1)
		 ORDER BY registered_at, id LIMIT 1`, assetID))
	if err != nil {
		return "", nil, fmt.Errorf("loading file of asset %s: %w", assetID, err)
	}
	return assetID, file, nil
}

// createUpload writes the asset, its first version and the pending file in one
// transaction. Without a declared sha256, the checksum of the received bytes
// is recorded, so the upload cannot fail the check.
func (p *Postgres) createUpload(ctx context.Context, key string, req *repositoryapi.UploadAssetRequestMultipart, got received) (string, *fileRow, error) {
	assetID, err := newID()
	if err != nil {
		return "", nil, err
	}
	versionID, err := newID()
	if err != nil {
		return "", nil, err
	}

	tx, err := p.db.Begin(ctx)
	if err != nil {
		return "", nil, err
	}
	defer tx.Rollback(ctx)
	if err := insertAsset(ctx, tx, newAsset{
		id:             assetID,
		versionID:      versionID,
		visibility:     string(req.Visibility.Or(repositoryapi.VisibilityPublic)),
		source:         string(repositoryapi.AssetSourceLocal),
		idempotencyKey: &key,
		metadata:       req.Metadata,
	}); err != nil {
		return "", nil, err
	}
	file, err := insertFile(ctx, tx, versionID, &repositoryapi.RegisterFileRequest{
		MediaType: cmp.Or(req.File.Header.Get("Content-Type"), "application/octet-stream"),
		SHA256:    req.SHA256.Or(got.sum),
		SizeBytes: got.size,
	})
	if err != nil {
		return "", nil, err
	}
	return assetID, file, tx.Commit(ctx)
}

// newAsset is an asset and its first version, as CreateAsset and UploadAsset
// write them.
type newAsset struct {
	id, versionID, visibility, source string
	idempotencyKey                    *string
	metadata                          repositoryapi.AssetMetadata
}

func insertAsset(ctx context.Context, tx pgx.Tx, a newAsset) error {
	if _, err := tx.Exec(ctx,
		`INSERT INTO assets (id, visibility, source, idempotency_key) VALUES ($1, $2, $3, $4)`,
		a.id, a.visibility, a.source, a.idempotencyKey); err != nil {
		return err
	}
	m := a.metadata
	if _, err := tx.Exec(ctx,
		`INSERT INTO asset_versions (id, asset_id, title, description, type, creator, date, location, rights)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		a.versionID, a.id, m.Title, nullable(m.Description), string(m.Type),
		nullable(m.Creator), nullable(m.Date), nullable(m.Location), nullable(m.Rights)); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE assets SET current_version_id = $1 WHERE id = $2`, a.versionID, a.id)
	return err
}

// uploadedAsset answers a finished upload, including a replayed one.
func (p *Postgres) uploadedAsset(ctx context.Context, assetID string, file *fileRow) (repositoryapi.UploadAssetRes, error) {
	asset, err := p.asset(ctx, assetID)
	if err != nil {
		return p.uploadFailed(assetID, file, err), nil
	}
	return p.created(asset), nil
}

// uploadFailed logs err and answers 500 with the ids needed to resume.
func (p *Postgres) uploadFailed(assetID string, file *fileRow, err error) *repositoryapi.UploadAssetInternalServerError {
	slog.Error("uploading asset", "asset", assetID, "upload", file.uploadID, "err", err)
	e := repositoryapi.UploadAssetInternalServerError(p.uploadProblem(
		"the file could not be stored; retry with the same Idempotency-Key, or send it to uploadUrl", assetID, file))
	return &e
}

func (p *Postgres) uploadProblem(title, assetID string, file *fileRow) repositoryapi.Problem {
	return repositoryapi.Problem{
		Title:     title,
		AssetId:   repositoryapi.NewOptString(assetID),
		UploadId:  repositoryapi.NewOptString(file.uploadID),
		UploadUrl: repositoryapi.NewOptString(p.uploadURL(file)),
	}
}

func (p *Postgres) created(asset *repositoryapi.Asset) *repositoryapi.AssetHeaders {
	return &repositoryapi.AssetHeaders{
		Etag:     etag(asset),
		Location: repositoryapi.NewOptString(p.basePath + "/assets/" + asset.ID),
		Response: *asset,
	}
}

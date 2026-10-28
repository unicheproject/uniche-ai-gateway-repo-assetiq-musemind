package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5"

	"uniche/api/repositoryapi"
)

// fileRow is one asset_files row.
type fileRow struct {
	id, uploadID, versionID, mediaType, status, sha256 string
	sizeBytes, bytesReceived                           int64
	path                                               *string
	registeredAt                                       *time.Time
}

const fileCols = `id, upload_id, version_id, media_type, status, sha256, size_bytes, bytes_received, path, registered_at`

func scanFile(row scanner) (*fileRow, error) {
	var f fileRow
	err := row.Scan(&f.id, &f.uploadID, &f.versionID, &f.mediaType, &f.status, &f.sha256,
		&f.sizeBytes, &f.bytesReceived, &f.path, &f.registeredAt)
	return &f, err
}

// toOAS renders the file; basePath is where the API is served.
func (f *fileRow) toOAS(basePath string) repositoryapi.AssetFile {
	out := repositoryapi.AssetFile{
		ID:        f.id,
		VersionId: f.versionID,
		Status:    repositoryapi.FileStatus(f.status),
		URL:       repositoryapi.NewOptString(basePath + "/files/" + f.id),
		MediaType: repositoryapi.NewOptString(f.mediaType),
		SHA256:    repositoryapi.NewOptString(f.sha256),
		SizeBytes: repositoryapi.NewOptInt64(f.sizeBytes),
	}
	if f.registeredAt != nil {
		out.RegisteredAt = repositoryapi.NewOptDateTime(f.registeredAt.UTC())
	}
	return out
}

// files lists a version's files, oldest first.
func (p *Postgres) files(ctx context.Context, versionID string) ([]repositoryapi.AssetFile, error) {
	rows, err := p.db.Query(ctx,
		`SELECT `+fileCols+` FROM asset_files WHERE version_id = $1 ORDER BY registered_at, id`, versionID)
	if err != nil {
		return nil, fmt.Errorf("listing files of version %s: %w", versionID, err)
	}
	defer rows.Close()

	out := []repositoryapi.AssetFile{}
	for rows.Next() {
		f, err := scanFile(rows)
		if err != nil {
			return nil, fmt.Errorf("reading files of version %s: %w", versionID, err)
		}
		out = append(out, f.toOAS(p.basePath))
	}
	return out, rows.Err()
}

// RegisterFile is idempotent for unfinished uploads: registering the same
// file on the same version again returns the pending registration instead of
// piling up orphans after a failed upload.
func (p *Postgres) RegisterFile(ctx context.Context, req *repositoryapi.RegisterFileRequest, params repositoryapi.RegisterFileParams) (repositoryapi.RegisterFileRes, error) {
	if _, err := p.version(ctx, params.ID, params.VersionId); errors.Is(err, errNotFound) {
		return &repositoryapi.RegisterFileNotFound{Title: "version not found"}, nil
	} else if err != nil {
		return nil, err
	}

	file, err := scanFile(p.db.QueryRow(ctx,
		`SELECT `+fileCols+` FROM asset_files
		 WHERE version_id = $1 AND status = 'pending' AND sha256 = $2 AND size_bytes = $3 AND media_type = $4
		 ORDER BY registered_at LIMIT 1`,
		params.VersionId, req.SHA256, req.SizeBytes, req.MediaType))
	if errors.Is(err, pgx.ErrNoRows) {
		file, err = insertFile(ctx, p.db, params.VersionId, req)
	}
	if err != nil {
		return nil, fmt.Errorf("registering file on version %s: %w", params.VersionId, err)
	}
	return &repositoryapi.FileRegistration{File: file.toOAS(p.basePath), UploadId: file.uploadID, UploadUrl: p.uploadURL(file)}, nil
}

func insertFile(ctx context.Context, db DBTX, versionID string, req *repositoryapi.RegisterFileRequest) (*fileRow, error) {
	fileID, err := newID()
	if err != nil {
		return nil, err
	}
	uploadID, err := newID()
	if err != nil {
		return nil, err
	}
	return scanFile(db.QueryRow(ctx,
		`INSERT INTO asset_files (id, upload_id, version_id, media_type, sha256, size_bytes)
		 VALUES ($1, $2, $3, $4, $5, $6) RETURNING `+fileCols,
		fileID, uploadID, versionID, req.MediaType, req.SHA256, req.SizeBytes))
}

func (p *Postgres) GetUploadStatus(ctx context.Context, params repositoryapi.GetUploadStatusParams) (repositoryapi.GetUploadStatusRes, error) {
	f, err := scanFile(p.db.QueryRow(ctx,
		`SELECT `+fileCols+` FROM asset_files WHERE upload_id = $1`, params.UploadId))
	if errors.Is(err, pgx.ErrNoRows) {
		return &repositoryapi.GetUploadStatusNotFound{Title: "upload not found"}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("loading upload %s: %w", params.UploadId, err)
	}
	return &repositoryapi.UploadStatus{
		UploadId:      f.uploadID,
		FileId:        f.id,
		Status:        repositoryapi.FileStatus(f.status),
		BytesReceived: f.bytesReceived,
		SizeBytes:     f.sizeBytes,
	}, nil
}

// UploadFile takes the whole file in one request. Every attempt starts at
// byte 0, so an interrupted upload is simply sent again.
func (p *Postgres) UploadFile(ctx context.Context, req repositoryapi.UploadFileReq, params repositoryapi.UploadFileParams) (repositoryapi.UploadFileRes, error) {
	if params.ContentRange.Set {
		return &repositoryapi.UploadFileBadRequest{Title: "chunked uploads are not supported yet; send the whole file without Content-Range"}, nil
	}
	file, err := scanFile(p.db.QueryRow(ctx,
		`SELECT `+fileCols+` FROM asset_files WHERE upload_id = $1`, params.UploadId))
	if errors.Is(err, pgx.ErrNoRows) {
		return &repositoryapi.UploadFileNotFound{Title: "upload not found"}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("loading upload %s: %w", params.UploadId, err)
	}
	if file.status == string(repositoryapi.FileStatusAvailable) {
		return &repositoryapi.UploadFileNoContent{}, nil
	}

	part := filepath.Join(p.filesDir, "uploads", file.uploadID+".part")
	sum, size, err := receive(part, req.Data, file.sizeBytes)
	if err != nil {
		os.Remove(part)
		return nil, fmt.Errorf("receiving upload %s: %w", file.uploadID, err)
	}
	if size != file.sizeBytes || sum != file.sha256 {
		os.Remove(part)
		return &repositoryapi.UploadFileConflict{
			Title:         "the file does not match the declared size or checksum; send it again from byte 0",
			Code:          repositoryapi.NewOptProblemCode(repositoryapi.ProblemCodeChecksumMismatch),
			BytesReceived: repositoryapi.NewOptInt64(0),
		}, nil
	}

	if err := p.complete(ctx, file, part, sum, size); err != nil {
		return nil, err
	}
	return &repositoryapi.UploadFileNoContent{}, nil
}

// complete stores a verified upload and marks its file available, recording
// its actual checksum and size. Only a pending file is changed: when an
// overlapping attempt finished first, its file stays as it is.
func (p *Postgres) complete(ctx context.Context, file *fileRow, part, sum string, size int64) error {
	rel, err := p.store(part, sum)
	if err != nil {
		return fmt.Errorf("storing upload %s: %w", file.uploadID, err)
	}
	if _, err := p.db.Exec(ctx,
		`UPDATE asset_files SET status = 'available', sha256 = $3, size_bytes = $4, bytes_received = $4, path = $1 WHERE id = $2 AND status = 'pending'`,
		rel, file.id, sum, size); err != nil {
		return fmt.Errorf("marking file %s available: %w", file.id, err)
	}
	return nil
}

func (p *Postgres) uploadURL(file *fileRow) string {
	return p.basePath + "/uploads/" + file.uploadID
}

// receive writes at most size+1 bytes of r to path, so an oversized upload is
// detected without being stored in full. It returns the sha256 (hex) and the
// number of bytes written.
func receive(path string, r io.Reader, size int64) (sum string, n int64, err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", 0, err
	}
	out, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return "", 0, err
	}
	h := sha256.New()
	n, err = io.Copy(io.MultiWriter(out, h), io.LimitReader(r, size+1))
	if err == nil {
		err = out.Sync()
	}
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	return hex.EncodeToString(h.Sum(nil)), n, err
}

// store moves a verified upload to blobs/{sha[0:2]}/{sha}. Files are named by
// checksum, so identical content is stored once and never overwritten. It
// returns the path relative to the files dir.
func (p *Postgres) store(part, sum string) (string, error) {
	rel := filepath.Join("blobs", sum[:2], sum)
	dst := filepath.Join(p.filesDir, rel)
	if _, err := os.Stat(dst); err == nil {
		return rel, os.Remove(part)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", err
	}
	return rel, os.Rename(part, dst)
}

func (p *Postgres) GetFile(ctx context.Context, params repositoryapi.GetFileParams) (repositoryapi.GetFileRes, error) {
	f, err := scanFile(p.db.QueryRow(ctx,
		`SELECT `+fileCols+` FROM asset_files WHERE id = $1`, params.FileId))
	if errors.Is(err, pgx.ErrNoRows) {
		return &repositoryapi.GetFileNotFound{Title: "file not found"}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("loading file %s: %w", params.FileId, err)
	}
	if f.status != string(repositoryapi.FileStatusAvailable) || f.path == nil {
		return &repositoryapi.GetFileConflict{
			Title: "the file has not finished uploading",
			Code:  repositoryapi.NewOptProblemCode(repositoryapi.ProblemCodeIncompleteUpload),
		}, nil
	}
	data, err := os.Open(filepath.Join(p.filesDir, *f.path))
	if err != nil {
		return nil, fmt.Errorf("opening file %s: %w", f.id, err)
	}
	// ogen closes the *os.File once the body is written.
	return &repositoryapi.GetFileOKHeaders{ContentType: f.mediaType, Response: repositoryapi.GetFileOK{Data: data}}, nil
}

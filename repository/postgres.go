package repository

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"uniche/api/repositoryapi"
)

const (
	defaultPageSize = 20
	maxPageSize     = 100
)

// errNotFound marks a missing (or withdrawn) row inside this package; callers
// translate it into the endpoint's 404 response.
var errNotFound = errors.New("not found")

// Postgres is the real Repository: records in Postgres, file bytes on disk.
//
// This slice implements asset creation (alone or with its file in one request),
// ingest, reads and listing of assets and versions, and the file
// register/upload/download path. Every other operation is inherited from
// repositoryapi.UnimplementedHandler and answers 501 rather than fake data.
type Postgres struct {
	repositoryapi.UnimplementedHandler

	db       DBTX
	filesDir string
	basePath string
}

// Option configures a Postgres.
type Option func(*Postgres)

// WithBasePath sets the path the public API is served under, e.g.
// "/api/v1/repository", so the links in responses resolve for clients.
func WithBasePath(path string) Option {
	return func(p *Postgres) { p.basePath = path }
}

var _ repositoryapi.Handler = (*Postgres)(nil)

// NewPostgres creates the files directory and returns a Repository over db,
// which is a *pgxpool.Pool in production or a pgx.Tx in tests.
func NewPostgres(db DBTX, filesDir string, opts ...Option) (*Postgres, error) {
	if err := os.MkdirAll(filesDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating repository files dir: %w", err)
	}
	p := &Postgres{db: db, filesDir: filesDir}
	for _, opt := range opts {
		opt(p)
	}
	return p, nil
}

func (p *Postgres) IngestAsset(ctx context.Context, req *repositoryapi.IngestRequest) (repositoryapi.IngestAssetRes, error) {
	assetID, err := newID()
	if err != nil {
		return nil, err
	}
	versionID, err := newID()
	if err != nil {
		return nil, err
	}

	err = p.ingest(ctx, assetID, versionID, req)
	if isUniqueViolation(err) {
		existing, lookupErr := p.assetIDByExternalID(ctx, req.ExternalId.Provider, req.ExternalId.Identifier)
		if lookupErr != nil {
			return nil, fmt.Errorf("finding existing asset for %s %s: %w", req.ExternalId.Provider, req.ExternalId.Identifier, lookupErr)
		}
		return &repositoryapi.IngestAssetConflict{
			Title:           "this record was imported before",
			Code:            repositoryapi.NewOptProblemCode(repositoryapi.ProblemCodeDuplicateAsset),
			ExistingAssetId: repositoryapi.NewOptString(existing),
		}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("ingesting %s %s: %w", req.ExternalId.Provider, req.ExternalId.Identifier, err)
	}

	asset, err := p.asset(ctx, assetID)
	if err != nil {
		return nil, err
	}
	return &repositoryapi.AssetHeaders{Etag: etag(asset), Response: *asset}, nil
}

// ingest writes the asset, its first version and its external ID in one
// transaction. The unique (provider, identifier) constraint is what rejects a
// repeat import, even under concurrent requests.
func (p *Postgres) ingest(ctx context.Context, assetID, versionID string, req *repositoryapi.IngestRequest) error {
	tx, err := p.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	m := req.Metadata
	if _, err := tx.Exec(ctx,
		`INSERT INTO assets (id, visibility, read_only, source) VALUES ($1, 'public', $2, $3)`,
		assetID, req.ReadOnly.Value, string(req.Source)); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO asset_versions (id, asset_id, title, description, type, creator, date, location, rights, raw_source)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		versionID, assetID, m.Title, nullable(m.Description), string(m.Type),
		nullable(m.Creator), nullable(m.Date), nullable(m.Location), nullable(m.Rights), nullable(req.RawSource)); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE assets SET current_version_id = $1 WHERE id = $2`, versionID, assetID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO asset_external_ids (asset_id, provider, identifier) VALUES ($1, $2, $3)`,
		assetID, req.ExternalId.Provider, req.ExternalId.Identifier); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ListAssets pages through visible assets, newest first. Asset IDs are UUIDv7,
// so ordering by ID is ordering by creation time, and the last ID of a page
// is a stable keyset cursor.
//
// Until auth exists there is no caller identity, so only public assets are
// returned; other users' private assets are never leaked.
func (p *Postgres) ListAssets(ctx context.Context, params repositoryapi.ListAssetsParams) (repositoryapi.ListAssetsRes, error) {
	after, err := decodeAssetCursor(params.Cursor)
	if err != nil {
		return &repositoryapi.ListAssetsBadRequest{Title: "invalid cursor"}, nil
	}
	limit := min(params.Limit.Or(defaultPageSize), maxPageSize)

	rows, err := p.db.Query(ctx,
		`SELECT a.id FROM assets a
		 LEFT JOIN asset_versions v ON v.id = a.current_version_id
		 WHERE a.withdrawn_at IS NULL
		   AND a.visibility = 'public'
		   AND ($1::text IS NULL OR a.id < $1)
		   AND ($2::text IS NULL OR a.source = $2)
		   AND ($3::text IS NULL OR v.type = $3)
		   AND ($4::text IS NULL OR a.owner_id = $4)
		   AND ($5::text IS NULL OR a.visibility = $5)
		   AND ($6::text IS NULL OR v.title ILIKE $6 OR v.creator ILIKE $6)
		 ORDER BY a.id DESC LIMIT $7`,
		after, optArg(params.Source), optArg(params.Type), optArg(params.OwnerId), optArg(params.Visibility), containsPattern(params.Q), limit+1)
	if err != nil {
		return nil, fmt.Errorf("listing assets: %w", err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("reading asset list: %w", err)
	}

	page := &repositoryapi.AssetPage{Items: []repositoryapi.Asset{}}
	if len(ids) > limit {
		ids = ids[:limit]
		page.NextCursor = repositoryapi.NewOptString(base64.RawURLEncoding.EncodeToString([]byte(ids[limit-1])))
	}
	for _, id := range ids {
		asset, err := p.asset(ctx, id)
		if errors.Is(err, errNotFound) { // withdrawn since the list query
			continue
		}
		if err != nil {
			return nil, err
		}
		page.Items = append(page.Items, *asset)
	}
	return page, nil
}

// containsPattern turns the search words into an ILIKE pattern that matches
// them anywhere, taking % and _ literally. An unset or blank q means no filter.
func containsPattern(q repositoryapi.OptString) *string {
	words := strings.TrimSpace(q.Value)
	if !q.Set || words == "" {
		return nil
	}
	escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(words)
	pattern := "%" + escaped + "%"
	return &pattern
}

// LookupExternalIds finds the visible assets behind provider/identifier pairs
// in one query. Pairs with no visible asset are left out, so an asset that is
// private or withdrawn is indistinguishable from one that was never imported.
func (p *Postgres) LookupExternalIds(ctx context.Context, req *repositoryapi.LookupExternalIdsRequest) (repositoryapi.LookupExternalIdsRes, error) {
	providers := make([]string, len(req.Items))
	identifiers := make([]string, len(req.Items))
	for i, item := range req.Items {
		providers[i], identifiers[i] = item.Provider, item.Identifier
	}
	rows, err := p.db.Query(ctx,
		`SELECT e.provider, e.identifier, e.asset_id
		 FROM asset_external_ids e
		 JOIN assets a ON a.id = e.asset_id
		 JOIN unnest($1::text[], $2::text[]) AS want(provider, identifier)
		   ON want.provider = e.provider AND want.identifier = e.identifier
		 WHERE a.withdrawn_at IS NULL AND a.visibility = 'public'`,
		providers, identifiers)
	if err != nil {
		return nil, fmt.Errorf("looking up external ids: %w", err)
	}
	found, err := pgx.CollectRows(rows, pgx.RowToStructByPos[repositoryapi.LookupExternalIdsResponseItemsItem])
	if err != nil {
		return nil, fmt.Errorf("reading external id lookup: %w", err)
	}
	if found == nil {
		found = []repositoryapi.LookupExternalIdsResponseItemsItem{}
	}
	return &repositoryapi.LookupExternalIdsResponse{Items: found}, nil
}

// decodeAssetCursor reads the opaque cursor from ListAssets. An unset cursor
// means the first page.
func decodeAssetCursor(cursor repositoryapi.OptString) (*string, error) {
	if !cursor.Set {
		return nil, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(cursor.Value)
	if err != nil {
		return nil, err
	}
	id := string(b)
	return &id, nil
}

// optArg maps an unset optional query parameter to SQL NULL.
func optArg[T ~string](o interface {
	Get() (T, bool)
}) *string {
	v, ok := o.Get()
	if !ok {
		return nil
	}
	s := string(v)
	return &s
}

func (p *Postgres) GetAsset(ctx context.Context, params repositoryapi.GetAssetParams) (repositoryapi.GetAssetRes, error) {
	asset, err := p.asset(ctx, params.ID)
	if errors.Is(err, errNotFound) {
		return &repositoryapi.GetAssetNotFound{Title: "asset not found"}, nil
	}
	if err != nil {
		return nil, err
	}
	return &repositoryapi.AssetHeaders{Etag: etag(asset), Response: *asset}, nil
}

func (p *Postgres) GetVersion(ctx context.Context, params repositoryapi.GetVersionParams) (repositoryapi.GetVersionRes, error) {
	v, err := p.version(ctx, params.ID, params.VersionId)
	if errors.Is(err, errNotFound) {
		return &repositoryapi.GetVersionNotFound{Title: "version not found"}, nil
	}
	if err != nil {
		return nil, err
	}
	return v, nil
}

func (p *Postgres) ListVersions(ctx context.Context, params repositoryapi.ListVersionsParams) (repositoryapi.ListVersionsRes, error) {
	cursor, err := parseCursor(params.Cursor)
	if err != nil {
		return &repositoryapi.ListVersionsBadRequest{Title: "invalid cursor"}, nil
	}
	limit := min(params.Limit.Or(defaultPageSize), maxPageSize)

	var exists bool
	if err := p.db.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM assets WHERE id = $1 AND withdrawn_at IS NULL)`, params.ID).Scan(&exists); err != nil {
		return nil, fmt.Errorf("checking asset %s: %w", params.ID, err)
	}
	if !exists {
		return &repositoryapi.ListVersionsNotFound{Title: "asset not found"}, nil
	}

	items, seqs, err := p.versionsBefore(ctx, params.ID, cursor, limit+1)
	if err != nil {
		return nil, err
	}
	page := &repositoryapi.VersionPage{Items: []repositoryapi.AssetVersion{}}
	if len(items) > limit {
		items, seqs = items[:limit], seqs[:limit]
		page.NextCursor = repositoryapi.NewOptString(strconv.FormatInt(seqs[limit-1], 10))
	}
	for i := range items {
		if items[i].Files, err = p.files(ctx, items[i].ID); err != nil {
			return nil, err
		}
	}
	page.Items = append(page.Items, items...)
	return page, nil
}

// parseCursor reads the opaque cursor, which is the seq of the last version of
// the previous page. An unset cursor means the first page.
func parseCursor(cursor repositoryapi.OptString) (*int64, error) {
	if !cursor.Set {
		return nil, nil
	}
	seq, err := strconv.ParseInt(cursor.Value, 10, 64)
	if err != nil {
		return nil, err
	}
	return &seq, nil
}

// versionsBefore returns up to limit versions older than cursor, newest first,
// with their seq values. Files are not loaded.
func (p *Postgres) versionsBefore(ctx context.Context, assetID string, cursor *int64, limit int) ([]repositoryapi.AssetVersion, []int64, error) {
	rows, err := p.db.Query(ctx,
		`SELECT `+versionCols+`, seq FROM asset_versions
		 WHERE asset_id = $1 AND ($2::bigint IS NULL OR seq < $2)
		 ORDER BY seq DESC LIMIT $3`, assetID, cursor, limit)
	if err != nil {
		return nil, nil, fmt.Errorf("listing versions of %s: %w", assetID, err)
	}
	defer rows.Close()

	var items []repositoryapi.AssetVersion
	var seqs []int64
	for rows.Next() {
		version, seq, err := scanVersion(rows)
		if err != nil {
			return nil, nil, fmt.Errorf("reading versions of %s: %w", assetID, err)
		}
		items = append(items, *version)
		seqs = append(seqs, seq)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("reading versions of %s: %w", assetID, err)
	}
	return items, seqs, nil
}

// asset loads a visible asset with its current version.
func (p *Postgres) asset(ctx context.Context, assetID string) (*repositoryapi.Asset, error) {
	var (
		asset     repositoryapi.Asset
		ownerID   *string
		versionID *string
		visib     string
		source    string
	)
	err := p.db.QueryRow(ctx,
		`SELECT id, owner_id, visibility, read_only, source, current_version_id, created_at
		 FROM assets WHERE id = $1 AND withdrawn_at IS NULL`, assetID).
		Scan(&asset.ID, &ownerID, &visib, &asset.ReadOnly, &source, &versionID, &asset.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("loading asset %s: %w", assetID, err)
	}
	asset.CreatedAt = asset.CreatedAt.UTC()
	asset.Visibility = repositoryapi.Visibility(visib)
	asset.Source = repositoryapi.AssetSource(source)
	if ownerID != nil {
		asset.OwnerId = repositoryapi.NewOptNilString(*ownerID)
	}
	if asset.ExternalIds, err = p.externalIDs(ctx, assetID); err != nil {
		return nil, err
	}
	if versionID != nil {
		asset.CurrentVersionId = repositoryapi.NewOptNilString(*versionID)
		version, err := p.version(ctx, assetID, *versionID)
		if err != nil {
			return nil, err
		}
		asset.CurrentVersion = repositoryapi.NewOptAssetVersion(*version)
	}
	return &asset, nil
}

// version loads one version of a visible asset, with its files.
func (p *Postgres) version(ctx context.Context, assetID, versionID string) (*repositoryapi.AssetVersion, error) {
	v, _, err := scanVersion(p.db.QueryRow(ctx,
		`SELECT `+versionCols+`, seq FROM asset_versions
		 WHERE id = $1 AND asset_id = $2
		   AND EXISTS (SELECT 1 FROM assets WHERE id = $2 AND withdrawn_at IS NULL)`, versionID, assetID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("loading version %s: %w", versionID, err)
	}
	if v.Files, err = p.files(ctx, versionID); err != nil {
		return nil, err
	}
	return v, nil
}

func (p *Postgres) externalIDs(ctx context.Context, assetID string) ([]repositoryapi.AssetExternalIdsItem, error) {
	rows, err := p.db.Query(ctx,
		`SELECT provider, identifier FROM asset_external_ids WHERE asset_id = $1 ORDER BY provider`, assetID)
	if err != nil {
		return nil, fmt.Errorf("loading external ids of %s: %w", assetID, err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowToStructByPos[repositoryapi.AssetExternalIdsItem])
	if err != nil {
		return nil, fmt.Errorf("loading external ids of %s: %w", assetID, err)
	}
	return ids, nil
}

func (p *Postgres) assetIDByExternalID(ctx context.Context, provider, identifier string) (string, error) {
	var id string
	err := p.db.QueryRow(ctx,
		`SELECT asset_id FROM asset_external_ids WHERE provider = $1 AND identifier = $2`,
		provider, identifier).Scan(&id)
	return id, err
}

const versionCols = `id, asset_id, parent_id, title, description, type, creator, date, location, rights, raw_source, created_at`

type scanner interface{ Scan(dest ...any) error }

// scanVersion reads versionCols followed by seq.
func scanVersion(row scanner) (*repositoryapi.AssetVersion, int64, error) {
	var (
		version                                                     repositoryapi.AssetVersion
		parentID, title, desc, typ, creator, date, loc, rights, raw *string
		seq                                                         int64
	)
	if err := row.Scan(&version.ID, &version.AssetId, &parentID, &title, &desc, &typ, &creator, &date, &loc, &rights, &raw, &version.CreatedAt, &seq); err != nil {
		return nil, 0, err
	}
	version.CreatedAt = version.CreatedAt.UTC()
	if parentID != nil {
		version.ParentId = repositoryapi.NewOptNilString(*parentID)
	}
	if raw != nil {
		version.RawSource = repositoryapi.NewOptNilString(*raw)
	}
	if typ != nil {
		version.Type = repositoryapi.NewOptAssetType(repositoryapi.AssetType(*typ))
	}
	version.Title = optString(title)
	version.Description = optString(desc)
	version.Creator = optString(creator)
	version.Date = optString(date)
	version.Location = optString(loc)
	version.Rights = optString(rights)
	return &version, seq, nil
}

func optString(s *string) repositoryapi.OptString {
	if s == nil {
		return repositoryapi.OptString{}
	}
	return repositoryapi.NewOptString(*s)
}

// nullable maps an unset optional string to SQL NULL.
func nullable(s repositoryapi.OptString) *string {
	if !s.Set {
		return nil
	}
	return &s.Value
}

func etag(a *repositoryapi.Asset) repositoryapi.OptString {
	return repositoryapi.NewOptString(`"` + a.CurrentVersionId.Value + `"`)
}

func newID() (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("generating id: %w", err)
	}
	return id.String(), nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

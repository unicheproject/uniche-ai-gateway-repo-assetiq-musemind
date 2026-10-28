package assetiq

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"uniche/api/repositoryapi"
	"uniche/connectors/eccch"
	"uniche/connectors/europeana"
)

var (
	// ErrInvalidURI means the assetiq:// handle is malformed or not importable.
	ErrInvalidURI = errors.New("invalid assetiq uri")
	// ErrRecordNotFound means the source system has no such record.
	ErrRecordNotFound = errors.New("record not found at source")
	// ErrSourceNotConfigured means the handle is valid but this server is
	// not set up to import from its source.
	ErrSourceNotConfigured = errors.New("import source not configured")

	// handlePart is the allowed shape of each ID segment in a handle.
	handlePart = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

	// Licences that allow changes: Public Domain Mark, CC0, CC BY, CC BY-SA.
	openRightsURL = regexp.MustCompile(`^http://creativecommons\.org/(publicdomain/(mark|zero)/1\.0|licenses/(by|by-sa)/[0-9.]+)/?$`)
)

// openRights reports whether rights names an open licence. Everything else,
// including ownership statements and no value, is imported read-only.
func openRights(rights string) bool {
	return openRightsURL.MatchString(strings.Replace(rights, "https://", "http://", 1))
}

// source is a record mapped to an ingest request, plus where its main file
// lives at the source.
type source struct {
	ingest        *repositoryapi.IngestRequest
	fileURL       string // empty if none
	fileMediaType string
}

// tempFile is a downloaded file waiting to be uploaded to the Repository.
type tempFile struct {
	path      string
	mediaType string
	sha256    string // hex
	size      int64
}

// Result is the outcome of an import.
type Result struct {
	AssetID   string
	VersionID string
	Duplicate bool
}

// Importer turns a transient assetiq:// search result into a Repository asset.
// repo is the Repository in process, or a *repositoryapi.Client over HTTP.
type Importer struct {
	europeana *europeana.Client
	eccch     *eccch.Client // nil when ECCCH import is not configured
	repoAPI   repositoryapi.Handler
	http      *http.Client
}

func NewImporter(eu *europeana.Client, kb *eccch.Client, repoAPI repositoryapi.Handler) *Importer {
	return &Importer{europeana: eu, eccch: kb, repoAPI: repoAPI, http: &http.Client{Timeout: 5 * time.Minute}}
}

// Import saves the record behind uri and its main file. Importing the same
// record again returns the existing asset, re-attaching the file only if an
// earlier attempt saved the metadata but not the file.
func (i *Importer) Import(ctx context.Context, uri string) (*Result, error) {
	// TODO: look the asset up by its external ID first, so repeat imports
	// skip fetching from Europeana. Needs an externalId filter on the
	// Repository's listAssets (see api/repository.yaml).
	src, err := i.fetch(ctx, uri)
	if err != nil {
		return nil, err
	}
	res, err := i.ingest(ctx, src.ingest)
	if err != nil {
		return nil, fmt.Errorf("saving %s: %w", uri, err)
	}
	if src.fileURL == "" {
		return res, nil
	}
	if res.Duplicate {
		has, err := i.hasFile(ctx, res)
		if err != nil {
			return nil, fmt.Errorf("checking file of asset %s: %w", res.AssetID, err)
		}
		if has {
			return res, nil
		}
	}
	if err := i.attach(ctx, res, src); err != nil {
		return nil, fmt.Errorf("attaching file to asset %s: %w", res.AssetID, err)
	}
	return res, nil
}

// fetch resolves the handle and maps the source record to an ingest request.
func (i *Importer) fetch(ctx context.Context, uri string) (*source, error) {
	switch {
	case strings.HasPrefix(uri, europeanaItemPrefix):
		return i.fetchEuropeana(ctx, uri)
	case strings.HasPrefix(uri, eccchHDTPrefix):
		return i.fetchEccch(ctx, uri)
	default:
		return nil, fmt.Errorf("%w: want %s... or %s..., got %q", ErrInvalidURI, europeanaItemPrefix, eccchHDTPrefix, uri)
	}
}

func (i *Importer) fetchEccch(ctx context.Context, uri string) (*source, error) {
	hdtURI, err := parseEccchURI(uri)
	if err != nil {
		return nil, err
	}
	if i.eccch == nil {
		return nil, fmt.Errorf("%w: eccch needs EGI_REFRESH_TOKEN", ErrSourceNotConfigured)
	}
	hdt, err := i.eccch.HDT(ctx, hdtURI)
	if errors.Is(err, eccch.ErrNotFound) {
		return nil, fmt.Errorf("%w: eccch %s", ErrRecordNotFound, hdtURI)
	}
	if err != nil {
		return nil, err
	}
	src, err := mapEccch(hdt)
	if err != nil {
		return nil, fmt.Errorf("mapping eccch %s: %w", hdtURI, err)
	}
	return src, nil
}

func (i *Importer) fetchEuropeana(ctx context.Context, uri string) (*source, error) {
	id, err := parseEuropeanaURI(uri)
	if err != nil {
		return nil, err
	}
	rec, err := i.europeana.Record(ctx, id)
	if errors.Is(err, europeana.ErrNotFound) {
		return nil, fmt.Errorf("%w: europeana %s", ErrRecordNotFound, id)
	}
	if err != nil {
		return nil, err
	}
	src, err := mapEuropeana(rec)
	if err != nil {
		return nil, fmt.Errorf("mapping europeana %s: %w", id, err)
	}
	return src, nil
}

// ingest saves the record. A record imported before resolves to the existing
// asset, marked Duplicate.
func (i *Importer) ingest(ctx context.Context, req *repositoryapi.IngestRequest) (*Result, error) {
	res, err := i.repoAPI.IngestAsset(ctx, req)
	if err != nil {
		return nil, err
	}
	switch res := res.(type) {
	case *repositoryapi.AssetHeaders:
		return &Result{AssetID: res.Response.ID, VersionID: res.Response.CurrentVersionId.Value}, nil
	case *repositoryapi.IngestAssetConflict:
		return i.existing(ctx, res)
	default:
		return nil, fmt.Errorf("ingest rejected: unexpected response %T", res)
	}
}

// existing resolves a duplicate_asset conflict to the asset already stored.
func (i *Importer) existing(ctx context.Context, conflict *repositoryapi.IngestAssetConflict) (*Result, error) {
	assetID := conflict.ExistingAssetId.Value
	if assetID == "" {
		return nil, fmt.Errorf("conflict without existing asset: %s", conflict.Title)
	}
	got, err := i.repoAPI.GetAsset(ctx, repositoryapi.GetAssetParams{ID: assetID})
	if err != nil {
		return nil, err
	}
	asset, ok := got.(*repositoryapi.AssetHeaders)
	if !ok {
		return nil, fmt.Errorf("loading existing asset %s: unexpected response %T", assetID, got)
	}
	return &Result{AssetID: assetID, VersionID: asset.Response.CurrentVersionId.Value, Duplicate: true}, nil
}

// hasFile reports whether the version has a fully uploaded file.
func (i *Importer) hasFile(ctx context.Context, res *Result) (bool, error) {
	got, err := i.repoAPI.GetVersion(ctx, repositoryapi.GetVersionParams{ID: res.AssetID, VersionId: res.VersionID})
	if err != nil {
		return false, err
	}
	v, ok := got.(*repositoryapi.AssetVersion)
	if !ok {
		return false, fmt.Errorf("loading version %s: unexpected response %T", res.VersionID, got)
	}
	for _, f := range v.Files {
		if f.Status == repositoryapi.FileStatusAvailable {
			return true, nil
		}
	}
	return false, nil
}

// attach downloads the record's main file, then registers and uploads it.
func (i *Importer) attach(ctx context.Context, res *Result, src *source) error {
	f, err := i.download(ctx, src)
	if err != nil {
		return err
	}
	defer os.Remove(f.path)

	reg, err := i.repoAPI.RegisterFile(ctx, &repositoryapi.RegisterFileRequest{
		MediaType: f.mediaType,
		SHA256:    f.sha256,
		SizeBytes: f.size,
	}, repositoryapi.RegisterFileParams{ID: res.AssetID, VersionId: res.VersionID})
	if err != nil {
		return err
	}
	registration, ok := reg.(*repositoryapi.FileRegistration)
	if !ok {
		return fmt.Errorf("registering file: unexpected response %T", reg)
	}

	data, err := os.Open(f.path)
	if err != nil {
		return fmt.Errorf("opening download: %w", err)
	}
	defer data.Close()

	up, err := i.repoAPI.UploadFile(ctx, repositoryapi.UploadFileReq{Data: data}, repositoryapi.UploadFileParams{UploadId: registration.UploadId})
	if err != nil {
		return err
	}
	if _, ok := up.(*repositoryapi.UploadFileNoContent); !ok {
		return fmt.Errorf("uploading file: unexpected response %T", up)
	}
	return nil
}

// download streams the record's main file to a temp file, hashing as it goes.
func (i *Importer) download(ctx context.Context, src *source) (*tempFile, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src.fileURL, nil)
	if err != nil {
		return nil, fmt.Errorf("building download request: %w", err)
	}
	resp, err := i.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("downloading %s: %w", src.fileURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("downloading %s: unexpected status %s", src.fileURL, resp.Status)
	}

	f, err := saveTemp(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("downloading %s: %w", src.fileURL, err)
	}
	f.mediaType = cmp.Or(src.fileMediaType, mediaTypeOf(resp), "application/octet-stream")
	return f, nil
}

// saveTemp writes r to a temp file, computing its sha256 and size on the way.
// mediaType is left for the caller.
func saveTemp(r io.Reader) (*tempFile, error) {
	tmp, err := os.CreateTemp("", "assetiq-*")
	if err != nil {
		return nil, fmt.Errorf("creating temp file: %w", err)
	}
	h := sha256.New()
	size, err := io.Copy(io.MultiWriter(tmp, h), r)
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err == nil && size == 0 {
		err = errors.New("empty file")
	}
	if err != nil {
		os.Remove(tmp.Name())
		return nil, err
	}
	return &tempFile{path: tmp.Name(), sha256: hex.EncodeToString(h.Sum(nil)), size: size}, nil
}

func mediaTypeOf(resp *http.Response) string {
	mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	return mediaType
}

package repository

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/textproto"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5"
	ht "github.com/ogen-go/ogen/http"
	"github.com/stretchr/testify/require"

	"uniche/api/repositoryapi"
)

// TestCreateAssetSavesItsFirstVersion checks that creating an asset from
// metadata alone gives it a first version carrying that metadata.
func TestCreateAssetSavesItsFirstVersion(t *testing.T) {
	db, closeDB := SetupTestDB(t)
	defer closeDB()
	RunWithRollback(t, db, func(ctx context.Context, tx pgx.Tx) {
		//GIVEN: an empty repository.
		repo := newTestRepo(t, tx)

		//WHEN: an asset titled "Amphora" is created.
		res, err := repo.CreateAsset(ctx, &repositoryapi.CreateAssetRequest{
			Metadata: repositoryapi.AssetMetadata{Title: "Amphora", Type: repositoryapi.AssetTypeImage},
		})
		require.NoError(t, err)

		//THEN: it is a local, public asset whose current version is titled "Amphora".
		asset := res.(*repositoryapi.AssetHeaders).Response
		require.Equal(t, repositoryapi.AssetSourceLocal, asset.Source)
		require.Equal(t, repositoryapi.VisibilityPublic, asset.Visibility)
		require.Equal(t, "Amphora", asset.CurrentVersion.Value.Title.Value)
	})
}

// TestUploadAssetStoresTheFileInOneRequest checks that one upload request
// creates the asset and makes its file downloadable straight away.
func TestUploadAssetStoresTheFileInOneRequest(t *testing.T) {
	db, closeDB := SetupTestDB(t)
	defer closeDB()
	RunWithRollback(t, db, func(ctx context.Context, tx pgx.Tx) {
		//GIVEN: a photo of an amphora.
		repo := newTestRepo(t, tx)
		photo := []byte("amphora photo")

		//WHEN: it is uploaded together with its metadata.
		asset := uploadOK(t, ctx, repo, "key-one", uploadRequest(photo, ""))

		//THEN: the asset has one available file with exactly those bytes.
		files := asset.CurrentVersion.Value.Files
		require.Len(t, files, 1)
		require.Equal(t, repositoryapi.FileStatusAvailable, files[0].Status)
		require.Equal(t, "image/jpeg", files[0].MediaType.Value)
		require.Equal(t, photo, download(t, ctx, repo, files[0].ID))
	})
}

// TestUploadAssetRetryDoesNotDuplicate checks that sending the same upload
// twice, e.g. after a lost response, returns the same asset instead of a copy.
func TestUploadAssetRetryDoesNotDuplicate(t *testing.T) {
	db, closeDB := SetupTestDB(t)
	defer closeDB()
	RunWithRollback(t, db, func(ctx context.Context, tx pgx.Tx) {
		//GIVEN: an asset uploaded with key "key-retry".
		repo := newTestRepo(t, tx)
		first := uploadOK(t, ctx, repo, "key-retry", uploadRequest([]byte("photo"), ""))

		//WHEN: the same upload is sent again with the same key.
		second := uploadOK(t, ctx, repo, "key-retry", uploadRequest([]byte("photo"), ""))

		//THEN: both answers name the same asset, and only one exists.
		require.Equal(t, first.ID, second.ID)
		var n int
		require.NoError(t, tx.QueryRow(ctx, `SELECT count(*) FROM assets WHERE idempotency_key = 'key-retry'`).Scan(&n))
		require.Equal(t, 1, n)
	})
}

// TestUploadAssetMismatchCanBeResumed checks that a file damaged on the way
// is rejected with the upload's address, and that sending the right bytes
// there finishes the asset.
func TestUploadAssetMismatchCanBeResumed(t *testing.T) {
	db, closeDB := SetupTestDB(t)
	defer closeDB()
	RunWithRollback(t, db, func(ctx context.Context, tx pgx.Tx) {
		//GIVEN: a photo whose checksum is declared, but damaged bytes of the same size are sent.
		repo, err := NewPostgres(tx, t.TempDir(), WithBasePath("/api/v1/repository"))
		require.NoError(t, err)
		photo := []byte("the real photo")

		res, err := repo.UploadAsset(ctx, uploadRequest([]byte("the fake photo"), sha(photo)),
			repositoryapi.UploadAssetParams{IdempotencyKey: "key-mismatch"})
		require.NoError(t, err)
		conflict, ok := res.(*repositoryapi.UploadAssetConflict)
		require.True(t, ok, "want 409, got %T", res)
		require.Equal(t, repositoryapi.ProblemCodeChecksumMismatch, conflict.Code.Value)
		require.True(t, conflict.AssetId.Set)
		require.Equal(t, "/api/v1/repository/uploads/"+conflict.UploadId.Value, conflict.UploadUrl.Value)

		//WHEN: the real photo is sent to the upload it named.
		up, err := repo.UploadFile(ctx, repositoryapi.UploadFileReq{Data: bytes.NewReader(photo)},
			repositoryapi.UploadFileParams{UploadId: conflict.UploadId.Value})
		require.NoError(t, err)

		//THEN: the upload completes and the asset's file is the real photo.
		require.IsType(t, &repositoryapi.UploadFileNoContent{}, up)
		asset, err := repo.asset(ctx, conflict.AssetId.Value)
		require.NoError(t, err)
		require.Equal(t, photo, download(t, ctx, repo, asset.CurrentVersion.Value.Files[0].ID))
	})
}

// TestUploadAssetRetryFinishesPendingFile checks that after a damaged
// upload, simply retrying with the same key and the right bytes finishes the
// same asset rather than creating a new one.
func TestUploadAssetRetryFinishesPendingFile(t *testing.T) {
	db, closeDB := SetupTestDB(t)
	defer closeDB()
	RunWithRollback(t, db, func(ctx context.Context, tx pgx.Tx) {
		//GIVEN: an upload with key "key-pending" that failed its checksum.
		repo := newTestRepo(t, tx)
		photo := []byte("the real photo")
		res, err := repo.UploadAsset(ctx, uploadRequest([]byte("damaged"), sha(photo)),
			repositoryapi.UploadAssetParams{IdempotencyKey: "key-pending"})
		require.NoError(t, err)
		conflict := res.(*repositoryapi.UploadAssetConflict)

		//WHEN: the upload is retried with the same key and the real photo.
		asset := uploadOK(t, ctx, repo, "key-pending", uploadRequest(photo, sha(photo)))

		//THEN: the same asset now holds the real photo.
		require.Equal(t, conflict.AssetId.Value, asset.ID)
		files := asset.CurrentVersion.Value.Files
		require.Len(t, files, 1)
		require.Equal(t, photo, download(t, ctx, repo, files[0].ID))
	})
}

// TestUploadAssetRetryFixesAWrongChecksum checks that when an app sends the
// right file but declares the wrong checksum, retrying with the right
// checksum finishes the asset instead of failing forever.
func TestUploadAssetRetryFixesAWrongChecksum(t *testing.T) {
	db, closeDB := SetupTestDB(t)
	defer closeDB()
	RunWithRollback(t, db, func(ctx context.Context, tx pgx.Tx) {
		//GIVEN: an upload with key "key-wrong-sum" that declared another file's checksum.
		repo := newTestRepo(t, tx)
		photo := []byte("the real photo")
		res, err := repo.UploadAsset(ctx, uploadRequest(photo, sha([]byte("another file"))),
			repositoryapi.UploadAssetParams{IdempotencyKey: "key-wrong-sum"})
		require.NoError(t, err)
		conflict := res.(*repositoryapi.UploadAssetConflict)

		//WHEN: the same file is sent again with the same key and its real checksum.
		asset := uploadOK(t, ctx, repo, "key-wrong-sum", uploadRequest(photo, sha(photo)))

		//THEN: the same asset holds the photo, recorded under its real checksum.
		require.Equal(t, conflict.AssetId.Value, asset.ID)
		file := asset.CurrentVersion.Value.Files[0]
		require.Equal(t, repositoryapi.FileStatusAvailable, file.Status)
		require.Equal(t, sha(photo), file.SHA256.Value)
	})
}

// TestUploadAssetLateRetryKeepsTheFinishedFile checks that when two retries
// of the same upload overlap, the one finishing second cannot replace the
// file the first one already stored.
func TestUploadAssetLateRetryKeepsTheFinishedFile(t *testing.T) {
	db, closeDB := SetupTestDB(t)
	defer closeDB()
	RunWithRollback(t, db, func(ctx context.Context, tx pgx.Tx) {
		//GIVEN: a pending upload, seen by a slow retry before a fast retry finishes it with the photo.
		repo := newTestRepo(t, tx)
		photo := []byte("the real photo")
		_, err := repo.UploadAsset(ctx, uploadRequest([]byte("the fake photo"), sha(photo)),
			repositoryapi.UploadAssetParams{IdempotencyKey: "key-overlap"})
		require.NoError(t, err)
		assetID, stale, err := repo.uploadByKey(ctx, "key-overlap")
		require.NoError(t, err)
		asset := uploadOK(t, ctx, repo, "key-overlap", uploadRequest(photo, sha(photo)))

		//WHEN: the slow retry finishes with different bytes.
		other := []byte("another photo")
		part := filepath.Join(t.TempDir(), "slow.part")
		require.NoError(t, os.WriteFile(part, other, 0o644))
		_, err = repo.finishUpload(ctx, assetID, stale, received{part: part, sum: sha(other), size: int64(len(other))}, sha(other))
		require.NoError(t, err)

		//THEN: the asset still holds the photo.
		require.Equal(t, photo, download(t, ctx, repo, asset.CurrentVersion.Value.Files[0].ID))
	})
}

func uploadOK(t *testing.T, ctx context.Context, repo *Postgres, key string, req *repositoryapi.UploadAssetRequestMultipart) repositoryapi.Asset {
	t.Helper()
	res, err := repo.UploadAsset(ctx, req, repositoryapi.UploadAssetParams{IdempotencyKey: key})
	require.NoError(t, err)
	created, ok := res.(*repositoryapi.AssetHeaders)
	require.True(t, ok, "want 201, got %T: %+v", res, res)
	return created.Response
}

func uploadRequest(data []byte, declared string) *repositoryapi.UploadAssetRequestMultipart {
	req := &repositoryapi.UploadAssetRequestMultipart{
		Metadata: repositoryapi.AssetMetadata{Title: "Amphora", Type: repositoryapi.AssetTypeImage},
		File: ht.MultipartFile{
			Name:   "amphora.jpg",
			File:   bytes.NewReader(data),
			Size:   int64(len(data)),
			Header: textproto.MIMEHeader{"Content-Type": {"image/jpeg"}},
		},
	}
	if declared != "" {
		req.SHA256 = repositoryapi.NewOptString(declared)
	}
	return req
}

func download(t *testing.T, ctx context.Context, repo *Postgres, fileID string) []byte {
	t.Helper()
	res, err := repo.GetFile(ctx, repositoryapi.GetFileParams{FileId: fileID})
	require.NoError(t, err)
	ok, isOK := res.(*repositoryapi.GetFileOKHeaders)
	require.True(t, isOK, "want 200, got %T", res)
	defer ok.Response.Data.(io.Closer).Close()
	data, err := io.ReadAll(ok.Response.Data)
	require.NoError(t, err)
	return data
}

func sha(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

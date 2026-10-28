package repository

import (
	"context"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"uniche/api/repositoryapi"
)

// TestListAssetsPagesNewestFirst checks that listing walks through assets
// from the newest to the oldest, and that "next page" picks up exactly where
// the previous page stopped.
func TestListAssetsPagesNewestFirst(t *testing.T) {
	db, closeDB := SetupTestDB(t)
	defer closeDB()
	RunWithRollback(t, db, func(ctx context.Context, tx pgx.Tx) {
		//GIVEN: three freshly imported assets.
		repo := newTestRepo(t, tx)
		a := ingestTestAsset(t, ctx, repo, "list-a", repositoryapi.AssetTypeImage)
		b := ingestTestAsset(t, ctx, repo, "list-b", repositoryapi.AssetTypeImage)
		c := ingestTestAsset(t, ctx, repo, "list-c", repositoryapi.AssetTypeImage)

		//WHEN: they are read two at a time.
		first := listAssets(t, ctx, repo, repositoryapi.ListAssetsParams{Limit: repositoryapi.NewOptInt(2)})
		second := listAssets(t, ctx, repo, repositoryapi.ListAssetsParams{
			Limit: repositoryapi.NewOptInt(2), Cursor: first.NextCursor,
		})

		//THEN: the first page holds the two newest, the second starts with the oldest.
		require.Equal(t, []string{c, b}, assetIDs(first))
		require.True(t, first.NextCursor.Set)
		require.Equal(t, a, second.Items[0].ID)
		require.Equal(t, "list-c", first.Items[0].CurrentVersion.Value.Title.Value)
	})
}

// TestListAssetsFiltersAndHidesPrivate checks that type filters narrow the
// list, and that private assets never appear in it.
func TestListAssetsFiltersAndHidesPrivate(t *testing.T) {
	db, closeDB := SetupTestDB(t)
	defer closeDB()
	RunWithRollback(t, db, func(ctx context.Context, tx pgx.Tx) {
		//GIVEN: a public image, a public video and a private image.
		repo := newTestRepo(t, tx)
		image := ingestTestAsset(t, ctx, repo, "filter-image", repositoryapi.AssetTypeImage)
		video := ingestTestAsset(t, ctx, repo, "filter-video", repositoryapi.AssetTypeVideo)
		private := ingestTestAsset(t, ctx, repo, "filter-private", repositoryapi.AssetTypeImage)
		_, err := tx.Exec(ctx, `UPDATE assets SET visibility = 'private' WHERE id = $1`, private)
		require.NoError(t, err)

		//WHEN: images only, and everything, are listed.
		images := listAssets(t, ctx, repo, repositoryapi.ListAssetsParams{
			Limit: repositoryapi.NewOptInt(100), Type: repositoryapi.NewOptAssetType(repositoryapi.AssetTypeImage),
		})
		all := listAssets(t, ctx, repo, repositoryapi.ListAssetsParams{Limit: repositoryapi.NewOptInt(100)})

		//THEN: the video is not among the images, and the private asset is nowhere.
		require.Contains(t, assetIDs(images), image)
		require.NotContains(t, assetIDs(images), video)
		require.Contains(t, assetIDs(all), video)
		require.NotContains(t, assetIDs(all), private)
	})
}

// TestListAssetsRejectsAGarbledCursor checks that a made-up cursor is
// answered with a bad-request problem rather than an empty or broken page.
func TestListAssetsRejectsAGarbledCursor(t *testing.T) {
	db, closeDB := SetupTestDB(t)
	defer closeDB()
	RunWithRollback(t, db, func(ctx context.Context, tx pgx.Tx) {
		//GIVEN: a Repository.
		repo := newTestRepo(t, tx)

		//WHEN: a list is requested with a cursor that is not valid.
		res, err := repo.ListAssets(ctx, repositoryapi.ListAssetsParams{Cursor: repositoryapi.NewOptString("!!not-a-cursor")})

		//THEN: the answer is a bad request.
		require.NoError(t, err)
		require.IsType(t, &repositoryapi.ListAssetsBadRequest{}, res)
	})
}

func newTestRepo(t *testing.T, tx pgx.Tx) *Postgres {
	t.Helper()
	repo, err := NewPostgres(tx, t.TempDir())
	require.NoError(t, err)
	return repo
}

func ingestTestAsset(t *testing.T, ctx context.Context, repo *Postgres, name string, typ repositoryapi.AssetType) string {
	t.Helper()
	res, err := repo.IngestAsset(ctx, &repositoryapi.IngestRequest{
		Source:     repositoryapi.IngestRequestSourceEuropeana,
		ExternalId: repositoryapi.IngestRequestExternalId{Provider: "test-list", Identifier: name + t.Name()},
		Metadata:   repositoryapi.AssetMetadata{Title: name, Type: typ},
	})
	require.NoError(t, err)
	out, ok := res.(*repositoryapi.AssetHeaders)
	require.True(t, ok, fmt.Sprintf("unexpected ingest result %T", res))
	return out.Response.ID
}

func listAssets(t *testing.T, ctx context.Context, repo *Postgres, params repositoryapi.ListAssetsParams) *repositoryapi.AssetPage {
	t.Helper()
	res, err := repo.ListAssets(ctx, params)
	require.NoError(t, err)
	page, ok := res.(*repositoryapi.AssetPage)
	require.True(t, ok, fmt.Sprintf("unexpected list result %T", res))
	return page
}

func assetIDs(page *repositoryapi.AssetPage) []string {
	ids := make([]string, len(page.Items))
	for i, a := range page.Items {
		ids[i] = a.ID
	}
	return ids
}

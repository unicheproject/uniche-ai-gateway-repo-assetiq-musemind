package repository

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"uniche/api/repositoryapi"
)

// TestListAssetsFindsWordsInTitleOrCreator checks that a text query matches
// titles and creators regardless of case, and that % and _ typed by a user
// are taken literally instead of matching everything.
func TestListAssetsFindsWordsInTitleOrCreator(t *testing.T) {
	db, closeDB := SetupTestDB(t)
	defer closeDB()
	RunWithRollback(t, db, func(ctx context.Context, tx pgx.Tx) {
		//GIVEN: one asset titled "The Milkmaid", one by creator "Vermeer", and one unrelated.
		repo := newTestRepo(t, tx)
		milkmaid := ingestTestAsset(t, ctx, repo, "The Milkmaid", repositoryapi.AssetTypeImage)
		other := ingestTestAsset(t, ctx, repo, "Girl with a Pearl Earring", repositoryapi.AssetTypeImage)
		_, err := tx.Exec(ctx, `UPDATE asset_versions SET creator = 'Johannes Vermeer' WHERE asset_id = $1`, other)
		require.NoError(t, err)
		unrelated := ingestTestAsset(t, ctx, repo, "Windmill", repositoryapi.AssetTypeImage)

		//WHEN: the list is searched by a title word, by a creator word, and by a bare wildcard.
		byTitle := listAssets(t, ctx, repo, repositoryapi.ListAssetsParams{Q: repositoryapi.NewOptString("MILKMAID")})
		byCreator := listAssets(t, ctx, repo, repositoryapi.ListAssetsParams{Q: repositoryapi.NewOptString("vermeer")})
		wildcard := listAssets(t, ctx, repo, repositoryapi.ListAssetsParams{Q: repositoryapi.NewOptString("%")})

		//THEN: each finds only its own asset, and "%" matches nothing.
		require.Equal(t, []string{milkmaid}, assetIDs(byTitle))
		require.Equal(t, []string{other}, assetIDs(byCreator))
		require.NotContains(t, assetIDs(wildcard), unrelated)
		require.Empty(t, assetIDs(wildcard))
	})
}

// TestLookupExternalIdsFindsVisibleAssetsOnly checks that a batch of source
// identifiers resolves to their assets in one call, and that unknown and
// private ones are simply left out.
func TestLookupExternalIdsFindsVisibleAssetsOnly(t *testing.T) {
	db, closeDB := SetupTestDB(t)
	defer closeDB()
	RunWithRollback(t, db, func(ctx context.Context, tx pgx.Tx) {
		//GIVEN: a public asset and a private asset, each with an external ID.
		repo := newTestRepo(t, tx)
		public := ingestTestAsset(t, ctx, repo, "lookup-public", repositoryapi.AssetTypeImage)
		private := ingestTestAsset(t, ctx, repo, "lookup-private", repositoryapi.AssetTypeImage)
		_, err := tx.Exec(ctx, `UPDATE assets SET visibility = 'private' WHERE id = $1`, private)
		require.NoError(t, err)
		ext := func(name string) repositoryapi.LookupExternalIdsRequestItemsItem {
			return repositoryapi.LookupExternalIdsRequestItemsItem{Provider: "test-list", Identifier: name + t.Name()}
		}

		//WHEN: both, plus an identifier nobody imported, are looked up together.
		res, err := repo.LookupExternalIds(ctx, &repositoryapi.LookupExternalIdsRequest{
			Items: []repositoryapi.LookupExternalIdsRequestItemsItem{ext("lookup-public"), ext("lookup-private"), ext("never-imported")},
		})
		require.NoError(t, err)

		//THEN: only the public asset comes back, with the identifier it was asked by.
		found, ok := res.(*repositoryapi.LookupExternalIdsResponse)
		require.True(t, ok)
		require.Equal(t, []repositoryapi.LookupExternalIdsResponseItemsItem{
			{Provider: "test-list", Identifier: "lookup-public" + t.Name(), AssetId: public},
		}, found.Items)
	})
}

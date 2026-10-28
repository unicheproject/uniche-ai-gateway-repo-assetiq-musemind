package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"uniche/api/repositoryapi"
	"uniche/repository/browser/views"
)

// TestAFailedSourceDoesNotBreakTheOthers checks that each search section is
// answered on its own: a source that errors, or is not set up, says so in its
// own section while the other sections still show their results.
func TestAFailedSourceDoesNotBreakTheOthers(t *testing.T) {
	//GIVEN: a working Europeana, an ECCCH that fails, and a source that is not set up.
	srv := searchBrowser(t, searchRepo{},
		WithSource("europeana", "Europeana", func(context.Context, string, int) ([]views.RemoteHit, error) {
			return []views.RemoteHit{{Provider: "europeana", Identifier: "/1/a", Title: "The Milkmaid"}}, nil
		}),
		WithSource("eccch", "ECCCH", func(context.Context, string, int) ([]views.RemoteHit, error) {
			return nil, errors.New("kbms timed out")
		}),
		WithSource("other", "Other", func(context.Context, string, int) ([]views.RemoteHit, error) {
			return nil, ErrNotConfigured
		}),
	)

	//WHEN: every section is requested.
	okStatus, okBody := get(t, srv, "/search/europeana?q=milkmaid")
	failStatus, failBody := get(t, srv, "/search/eccch?q=milkmaid")
	offStatus, offBody := get(t, srv, "/search/other?q=milkmaid")

	//THEN: all answer 200 (HTMX only swaps successful responses); the failures carry a message.
	assert.Equal(t, http.StatusOK, okStatus)
	assert.Contains(t, okBody, "The Milkmaid")
	assert.Equal(t, http.StatusOK, failStatus)
	assert.Contains(t, failBody, "did not answer")
	assert.NotContains(t, failBody, "kbms timed out")
	assert.Equal(t, http.StatusOK, offStatus)
	assert.Contains(t, offBody, "Not set up")
}

// TestImportedHitsAreMarkedByExternalID checks that a cloud hit already in the
// Repository shows an Imported badge linking to its asset, matched on source
// and identifier, while a hit with the same title but another identifier does
// not.
func TestImportedHitsAreMarkedByExternalID(t *testing.T) {
	//GIVEN: Europeana returns two hits titled alike; only /1/a was imported, as asset a1.
	repo := searchRepo{imported: map[string]string{views.ExternalKey("europeana", "/1/a"): "a1"}}
	srv := searchBrowser(t, repo,
		WithSource("europeana", "Europeana", func(context.Context, string, int) ([]views.RemoteHit, error) {
			return []views.RemoteHit{
				{Provider: "europeana", Identifier: "/1/a", Title: "The Milkmaid", ImportURI: "assetiq://europeana/item/1/a"},
				{Provider: "europeana", Identifier: "/1/b", Title: "The Milkmaid", ImportURI: "assetiq://europeana/item/1/b"},
			}, nil
		}),
		WithImporter(func(context.Context, string) (string, error) { return "a2", nil }),
	)

	//WHEN: the Europeana section is loaded.
	_, body := get(t, srv, "/search/europeana?q=milkmaid")

	//THEN: the first links to its asset, and only the second offers an import.
	assert.Contains(t, body, `href="/repo/assets/a1"`)
	assert.Equal(t, 1, strings.Count(body, ">Imported<"))
	assert.Equal(t, 1, strings.Count(body, `value="assetiq://europeana/item/1/b"`))
	assert.Equal(t, 0, strings.Count(body, `value="assetiq://europeana/item/1/a"`))
}

// TestLocalResultsShowTheOriginalAsAChildRow checks that an imported asset in
// the Repository section lists its original record beneath it.
func TestLocalResultsShowTheOriginalAsAChildRow(t *testing.T) {
	//GIVEN: an imported asset with a Europeana external ID.
	version := repositoryapi.AssetVersion{ID: "v1", Title: repositoryapi.NewOptString("The Milkmaid")}
	asset := repositoryapi.Asset{
		ID:             "a1",
		CurrentVersion: repositoryapi.NewOptAssetVersion(version),
		ExternalIds:    []repositoryapi.AssetExternalIdsItem{{Provider: "europeana", Identifier: "/90402/SK_A_2344"}},
	}
	srv := searchBrowser(t, searchRepo{assets: []repositoryapi.Asset{asset}})

	//WHEN: the Repository section is loaded.
	_, body := get(t, srv, "/search/local?q=milkmaid")

	//THEN: the asset is marked imported and its original is listed under it.
	assert.Contains(t, body, "(imported)")
	assert.Contains(t, body, "europeana: /90402/SK_A_2344")
}

type searchRepo struct {
	repositoryapi.UnimplementedHandler
	assets   []repositoryapi.Asset
	imported map[string]string // views.ExternalKey -> asset ID
}

func (f searchRepo) ListAssets(context.Context, repositoryapi.ListAssetsParams) (repositoryapi.ListAssetsRes, error) {
	return &repositoryapi.AssetPage{Items: f.assets}, nil
}

func (f searchRepo) LookupExternalIds(_ context.Context, req *repositoryapi.LookupExternalIdsRequest) (repositoryapi.LookupExternalIdsRes, error) {
	res := &repositoryapi.LookupExternalIdsResponse{Items: []repositoryapi.LookupExternalIdsResponseItemsItem{}}
	for _, it := range req.Items {
		if id, ok := f.imported[views.ExternalKey(it.Provider, it.Identifier)]; ok {
			res.Items = append(res.Items, repositoryapi.LookupExternalIdsResponseItemsItem{Provider: it.Provider, Identifier: it.Identifier, AssetId: id})
		}
	}
	return res, nil
}

func searchBrowser(t *testing.T, repo searchRepo, opts ...Option) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(New(repo, "u", "p", opts...))
	t.Cleanup(srv.Close)
	return srv
}

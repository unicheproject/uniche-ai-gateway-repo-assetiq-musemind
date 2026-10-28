package web

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"uniche/api/repositoryapi"
)

// TestGraphOpensOnTheSourceRecordsRoot checks that, with no subject chosen,
// the graph page opens on the resource that describes the asset: the HDT for
// ECCCH, the provider proxy (not the empty ProvidedCHO) for Europeana.
func TestGraphOpensOnTheSourceRecordsRoot(t *testing.T) {
	tests := []struct {
		name       string
		source     repositoryapi.AssetSource
		identifier string
		fixture    string
		want       string
	}{
		{"eccch", repositoryapi.AssetSourceEccch, "http://echoes-eccch.eu/HDT/Fkp1JTtfVek", "../../../rdf/testdata/eccch-construct.json", "hdto:HP33_contains"},
		{"europeana", repositoryapi.AssetSourceEuropeana, "/90402/SK_A_2344", "../../../rdf/testdata/europeana-SK_A_2344.jsonld", "The Milkmaid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			//GIVEN: an imported asset whose raw source is a recorded provider answer.
			raw, err := os.ReadFile(tt.fixture)
			require.NoError(t, err)
			srv := browserFor(t, tt.source, tt.identifier, string(raw))

			//WHEN: its graph page is opened without a subject.
			status, body := get(t, srv, "/assets/a1/graph")

			//THEN: the page shows the root's own properties.
			assert.Equal(t, http.StatusOK, status)
			assert.Contains(t, body, tt.want)
			assert.Contains(t, body, "Outgoing")
		})
	}
}

// TestGraphOfALocalAssetIsNotFound checks that an asset with no imported
// source record answers 404 with a readable message instead of an empty page.
func TestGraphOfALocalAssetIsNotFound(t *testing.T) {
	//GIVEN: an asset uploaded locally, so it has no source graph.
	srv := browserFor(t, repositoryapi.AssetSourceLocal, "", "")

	//WHEN: its graph page is requested.
	status, body := get(t, srv, "/assets/a1/graph")

	//THEN: the answer is 404 with an explanation.
	assert.Equal(t, http.StatusNotFound, status)
	assert.Contains(t, body, "no source graph for this asset")
}

type fakeRepo struct {
	repositoryapi.UnimplementedHandler
	asset repositoryapi.Asset
}

func (f fakeRepo) GetAsset(context.Context, repositoryapi.GetAssetParams) (repositoryapi.GetAssetRes, error) {
	return &repositoryapi.AssetHeaders{Response: f.asset}, nil
}

func browserFor(t *testing.T, source repositoryapi.AssetSource, identifier, raw string) *httptest.Server {
	t.Helper()
	asset := repositoryapi.Asset{ID: "a1", Source: source}
	if identifier != "" {
		asset.ExternalIds = []repositoryapi.AssetExternalIdsItem{{Provider: string(source), Identifier: identifier}}
	}
	version := repositoryapi.AssetVersion{ID: "v1"}
	if raw != "" {
		version.RawSource = repositoryapi.NewOptNilString(raw)
	}
	asset.CurrentVersion = repositoryapi.NewOptAssetVersion(version)
	srv := httptest.NewServer(New(fakeRepo{asset: asset}, "u", "p"))
	t.Cleanup(srv.Close)
	return srv
}

func get(t *testing.T, srv *httptest.Server, path string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, srv.URL+path, nil)
	require.NoError(t, err)
	req.SetBasicAuth("u", "p")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, string(body)
}

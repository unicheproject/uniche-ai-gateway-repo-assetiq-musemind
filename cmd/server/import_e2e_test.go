package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"uniche/api/gatewayapi"
	"uniche/api/repositoryapi"
	"uniche/assetiq"
	"uniche/connectors/eccch"
	"uniche/connectors/europeana"
	"uniche/repository"
	"uniche/web"
)

const (
	milkmaidURI    = "assetiq://europeana/item/90402/SK_A_2344"
	nonCommercial  = "assetiq://europeana/item/90402/SK_NC"
	pdmRights      = "http://creativecommons.org/publicdomain/mark/1.0/"
	byNCRights     = "http://creativecommons.org/licenses/by-nc/4.0/"
	repositoryBase = "/api/v1/repository"
	fakeJSONLD     = `{"@graph":[{"@id":"fake-jsonld"}]}`
	galassiURI     = "assetiq://eccch/hdt/Fkp1JTtfVek"
	galassiHDT     = "http://echoes-eccch.eu/HDT/Fkp1JTtfVek"
	galassiStore   = "6a2abf6b5d6646ff24522299"
	galassiRights  = "proprietà Ente pubblico territoriale"
)

// TestImportingARecordStoresItsMetadataAndFile checks the whole curator
// journey: import a Europeana record, then read back its metadata and
// download its image from the Repository.
func TestImportingARecordStoresItsMetadataAndFile(t *testing.T) {
	withImportAPI(t, func(api string, src *fakeSource) {
		//GIVEN: Europeana holds a public-domain painting and a non-commercial one.

		//WHEN: a curator imports the painting.
		status, imp := importRecord(t, api, milkmaidURI)

		//THEN: it is imported, with its mapped metadata and the verbatim JSON-LD.
		require.Equal(t, http.StatusOK, status)
		require.Equal(t, gatewayapi.ImportResponseStatusImported, imp.Status.Value)
		require.NotEmpty(t, imp.AssetId.Value)
		require.NotEmpty(t, imp.VersionId.Value)
		v := getVersion(t, api, imp)
		assert.Equal(t, "The Milkmaid", v.Title.Value)
		assert.Equal(t, "Vermeer, Johannes", v.Creator.Value)
		assert.Equal(t, "1660", v.Date.Value)
		assert.Equal(t, repositoryapi.AssetTypeImage, v.Type.Value)
		assert.Equal(t, pdmRights, v.Rights.Value)
		assert.Equal(t, fakeJSONLD, v.RawSource.Value)

		//THEN: its image is stored and downloads byte for byte.
		require.Len(t, v.Files, 1)
		f := v.Files[0]
		assert.Equal(t, repositoryapi.FileStatusAvailable, f.Status)
		assert.Equal(t, "image/jpeg", f.MediaType.Value)
		assert.Equal(t, int64(len(src.image)), f.SizeBytes.Value)
		assert.Equal(t, src.imageSHA, f.SHA256.Value)
		dl := get(t, api+repositoryBase+f.URL.Value)
		assert.Equal(t, http.StatusOK, dl.status)
		assert.Equal(t, "image/jpeg", dl.contentType)
		assert.Equal(t, src.image, dl.body)

		//THEN: the painting stays editable and the non-commercial one is read-only.
		assert.False(t, readOnly(t, api, imp.AssetId.Value), "public-domain asset should be editable")
		_, nc := importRecord(t, api, nonCommercial)
		assert.True(t, readOnly(t, api, nc.AssetId.Value), "CC BY-NC asset should be read-only")
	})
}

// TestImportingTwiceNeverDuplicatesAndHealsAFailedDownload checks that a
// repeat import returns the same asset, and that an import whose image
// download failed is completed by the next attempt.
func TestImportingTwiceNeverDuplicatesAndHealsAFailedDownload(t *testing.T) {
	withImportAPI(t, func(api string, src *fakeSource) {
		//GIVEN: the image host breaks halfway through the first import.
		src.failImage.Store(true)
		status, _ := importRecord(t, api, milkmaidURI)
		require.Equal(t, http.StatusInternalServerError, status)

		//WHEN: the host recovers and the curator imports again, twice.
		src.failImage.Store(false)
		_, healed := importRecord(t, api, milkmaidURI)
		_, again := importRecord(t, api, milkmaidURI)

		//THEN: the asset saved by the failed attempt is reused, not copied.
		assert.Equal(t, gatewayapi.ImportResponseStatusDuplicate, healed.Status.Value)
		assert.Equal(t, gatewayapi.ImportResponseStatusDuplicate, again.Status.Value)
		assert.Equal(t, healed.AssetId, again.AssetId)
		assert.Equal(t, healed.VersionId, again.VersionId)

		//THEN: it has its image, exactly once, and one version.
		v := getVersion(t, api, again)
		require.Len(t, v.Files, 1)
		assert.Equal(t, repositoryapi.FileStatusAvailable, v.Files[0].Status)
		var versions repositoryapi.VersionPage
		getJSON(t, api+repositoryBase+"/assets/"+again.AssetId.Value+"/versions", &versions)
		assert.Len(t, versions.Items, 1)
	})
}

// TestBadImportRequestsAreRejected checks that a curator who pastes a bad
// handle, or one Europeana does not know, gets a clear 400 and nothing is saved.
func TestBadImportRequestsAreRejected(t *testing.T) {
	withImportAPI(t, func(api string, _ *fakeSource) {
		for name, uri := range map[string]string{
			"not an assetiq handle": "https://example.org/not-a-handle",
			"unknown record":        "assetiq://europeana/item/90402/NOPE",
		} {
			//GIVEN: a handle that cannot be imported.

			//WHEN: the curator imports it.
			status, imp := importRecord(t, api, uri)

			//THEN: the request is rejected and no asset exists.
			assert.Equal(t, http.StatusBadRequest, status, name)
			assert.False(t, imp.AssetId.Set, name)
		}
	})
}

// TestImportingAnECCCHDigitalTwinStoresItsModel checks the ECCCH journey: a
// curator imports a digital twin using only an EGI refresh token, gets a
// read-only 3D asset with its model, and a second import finds the same asset.
func TestImportingAnECCCHDigitalTwinStoresItsModel(t *testing.T) {
	withImportAPI(t, func(api string, src *fakeSource) {
		//GIVEN: the knowledge base holds the Galassi monument and the gateway has only a refresh token.

		//WHEN: a curator imports it.
		status, imp := importRecord(t, api, galassiURI)

		//THEN: an access token is fetched and the twin is imported with its mapped metadata.
		require.Equal(t, http.StatusOK, status)
		require.Equal(t, gatewayapi.ImportResponseStatusImported, imp.Status.Value)
		require.NotEmpty(t, imp.AssetId.Value)
		require.NotEmpty(t, imp.VersionId.Value)
		v := getVersion(t, api, imp)
		assert.Equal(t, "Allegoria della disperazione - Elio Galassi", v.Title.Value)
		assert.Equal(t, "Monumento ai caduti", v.Description.Value)
		assert.Equal(t, repositoryapi.AssetType3d, v.Type.Value)
		assert.Equal(t, galassiRights, v.Rights.Value)
		assert.True(t, readOnly(t, api, imp.AssetId.Value), "an ownership statement should import read-only")

		//THEN: its model is stored with the file server's media type and downloads byte for byte.
		require.Len(t, v.Files, 1)
		f := v.Files[0]
		assert.Equal(t, repositoryapi.FileStatusAvailable, f.Status)
		assert.Equal(t, "model/gltf-binary", f.MediaType.Value)
		assert.Equal(t, int64(len(src.model)), f.SizeBytes.Value)
		assert.Equal(t, src.modelSHA, f.SHA256.Value)
		dl := get(t, api+repositoryBase+f.URL.Value)
		assert.Equal(t, http.StatusOK, dl.status)
		assert.Equal(t, "model/gltf-binary", dl.contentType)
		assert.Equal(t, src.model, dl.body)

		//WHEN: the curator imports it again.
		_, again := importRecord(t, api, galassiURI)

		//THEN: the existing asset is returned.
		assert.Equal(t, gatewayapi.ImportResponseStatusDuplicate, again.Status.Value)
		assert.Equal(t, imp.AssetId, again.AssetId)
	})
}

// TestImportingAnUnknownECCCHDigitalTwinIsRejected checks that a handle the
// knowledge base does not know gets a clear 400 and nothing is saved.
func TestImportingAnUnknownECCCHDigitalTwinIsRejected(t *testing.T) {
	withImportAPI(t, func(api string, _ *fakeSource) {
		//GIVEN: a well-formed handle for an HDT the knowledge base does not hold.

		//WHEN: the curator imports it.
		status, imp := importRecord(t, api, "assetiq://eccch/hdt/NOPE")

		//THEN: the request is rejected and no asset exists.
		assert.Equal(t, http.StatusBadRequest, status)
		assert.False(t, imp.AssetId.Set)
	})
}

// TestECCCHImportIsUnavailableWithoutAnEGIToken checks that on a server with
// no EGI refresh token, an ECCCH import gets 503 "not enabled here" rather
// than a 400 that blames the curator's valid handle.
func TestECCCHImportIsUnavailableWithoutAnEGIToken(t *testing.T) {
	//GIVEN: a gateway started without an ECCCH client.
	importer := assetiq.NewImporter(nil, nil, nil)
	handler, err := web.NewServer(nil, nil, nil, importer)
	require.NoError(t, err)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	//WHEN: a curator imports a valid ECCCH handle.
	status, imp := importRecord(t, srv.URL, galassiURI)

	//THEN: the server says the source is unavailable and nothing is imported.
	assert.Equal(t, http.StatusServiceUnavailable, status)
	assert.False(t, imp.AssetId.Set)
}

// withImportAPI wires the real importer, API and Postgres Repository
// (inside a rolled-back transaction) against a fake Europeana, ECCCH
// Knowledge Base, EGI token endpoint and file host.
func withImportAPI(t *testing.T, fn func(api string, src *fakeSource)) {
	t.Helper()
	db, cleanup := repository.SetupTestDB(t)
	defer cleanup()

	repository.RunWithRollback(t, db, func(_ context.Context, tx pgx.Tx) {
		src := newFakeSource(t)
		repo, err := repository.NewPostgres(tx, t.TempDir())
		require.NoError(t, err)
		tokens := eccch.NewTokenSource(src.URL+"/token", "token-portal", "refresh")
		kb := eccch.NewClient(src.URL, galassiStore, tokens)
		importer := assetiq.NewImporter(europeana.NewClient("test-key", europeana.WithBaseURL(src.URL)), kb, repo)
		handler, err := web.NewServer(nil, nil, repo, importer)
		require.NoError(t, err)
		srv := httptest.NewServer(handler)
		defer srv.Close()

		fn(srv.URL, src)
	})
}

// fakeSource stands in for the Europeana Record API, the ECCCH Knowledge
// Base, the EGI token endpoint and the image and model host.
type fakeSource struct {
	*httptest.Server
	image     []byte
	imageSHA  string
	failImage atomic.Bool
	model     []byte
	modelSHA  string
}

func newFakeSource(t *testing.T) *fakeSource {
	t.Helper()
	src := &fakeSource{image: bytes.Repeat([]byte("milkmaid"), 32<<10)}
	sum := sha256.Sum256(src.image)
	src.imageSHA = hex.EncodeToString(sum[:])
	src.model = bytes.Repeat([]byte("glTF"), 64<<10)
	sum = sha256.Sum256(src.model)
	src.modelSHA = hex.EncodeToString(sum[:])

	rights := map[string]string{"/90402/SK_A_2344": pdmRights, "/90402/SK_NC": byNCRights}
	mux := http.NewServeMux()
	mux.HandleFunc("/image", func(w http.ResponseWriter, _ *http.Request) {
		if src.failImage.Load() {
			// Promise 1000 bytes, send 10, then drop the connection.
			w.Header().Set("Content-Length", "1000")
			w.Write(src.image[:10])
			rc := http.NewResponseController(w)
			rc.Flush()
			if conn, _, err := rc.Hijack(); err == nil {
				conn.Close()
			}
			return
		}
		w.Write(src.image)
	})
	mux.HandleFunc("/record/v2/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/record/v2")
		id, ext, _ := strings.Cut(path, ".")
		lic, ok := rights[id]
		switch {
		case !ok:
			http.NotFound(w, r)
		case ext == "jsonld":
			io.WriteString(w, fakeJSONLD)
		default:
			json.NewEncoder(w).Encode(europeanaRecord(id, lic, src.URL+"/image"))
		}
	})
	mux.HandleFunc("/model.glb", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "model/gltf-binary")
		w.Write(src.model)
	})
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		if r.PostFormValue("grant_type") != "refresh_token" || r.PostFormValue("refresh_token") != "refresh" {
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"access_token": "fresh"})
	})
	mux.HandleFunc("POST /repository/singleTripleStoreQuery", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fresh" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var q struct{ Query, ExecutorTripleStoreId string }
		if err := json.NewDecoder(r.Body).Decode(&q); err != nil || q.ExecutorTripleStoreId != galassiStore {
			http.Error(w, "bad query request", http.StatusBadRequest)
			return
		}
		json.NewEncoder(w).Encode(kbAnswer(q.Query, src.URL+"/model.glb"))
	})
	src.Server = httptest.NewServer(mux)
	t.Cleanup(src.Close)
	return src
}

func europeanaRecord(about, rights, imageURL string) map[string]any {
	lang := func(l, v string) map[string][]string { return map[string][]string{l: {v}} }
	return map[string]any{
		"success": true,
		"object": map[string]any{
			"about": about,
			"type":  "IMAGE",
			"proxies": []any{
				map[string]any{"europeanaProxy": true, "dctermsCreated": lang("def", "#1660")},
				map[string]any{"europeanaProxy": false, "dcTitle": lang("en", "The Milkmaid"), "dctermsCreated": lang("def", "1660")},
			},
			"aggregations": []any{map[string]any{
				"edmRights":    lang("def", rights),
				"edmIsShownBy": imageURL,
				"webResources": []any{map[string]any{"about": imageURL, "ebucoreHasMimeType": "image/jpeg"}},
			}},
			"agents": []any{map[string]any{"prefLabel": lang("en", "Vermeer, Johannes")}},
		},
	}
}

// kbAnswer answers the record and models queries for the Galassi HDT only.
func kbAnswer(query, modelURL string) map[string]any {
	lit := func(v string) map[string]string { return map[string]string{"type": "literal", "value": v} }
	var bindings []any
	switch {
	case !strings.Contains(query, "<"+galassiHDT+">"):
	case strings.Contains(query, "SELECT DISTINCT ?src"):
		bindings = []any{map[string]any{"src": lit(modelURL), "format": lit("application/octet-stream")}}
	default:
		bindings = []any{map[string]any{
			"label":       lit("Allegoria della disperazione - Elio Galassi"),
			"entity":      lit("https://w3id.org/arco/resource/HistoricOrArtisticProperty/0901078520"),
			"description": lit("Monumento ai caduti"),
			"rights":      lit(galassiRights),
		}}
	}
	return map[string]any{"succeed": true, "results": map[string]any{"results": map[string]any{"bindings": bindings}}}
}

func importRecord(t *testing.T, api, uri string) (int, gatewayapi.ImportResponse) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"uri": uri})
	req, _ := http.NewRequest(http.MethodPost, api+"/api/v1/assets/import", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	var res gatewayapi.ImportResponse
	_ = json.NewDecoder(resp.Body).Decode(&res)
	return resp.StatusCode, res
}

func getVersion(t *testing.T, api string, imp gatewayapi.ImportResponse) repositoryapi.AssetVersion {
	t.Helper()
	var v repositoryapi.AssetVersion
	getJSON(t, api+repositoryBase+"/assets/"+imp.AssetId.Value+"/versions/"+imp.VersionId.Value, &v)
	return v
}

func readOnly(t *testing.T, api, assetID string) bool {
	t.Helper()
	var a repositoryapi.Asset
	getJSON(t, api+repositoryBase+"/assets/"+assetID, &a)
	return a.ReadOnly
}

func getJSON(t *testing.T, url string, into any) {
	t.Helper()
	res := get(t, url)
	require.Equal(t, http.StatusOK, res.status, "GET %s: %s", url, res.body)
	require.NoError(t, json.Unmarshal(res.body, into), "decoding %s", url)
}

// reply is a fully read HTTP response.
type reply struct {
	status      int
	contentType string
	body        []byte
}

func get(t *testing.T, url string) reply {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer test")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return reply{status: resp.StatusCode, contentType: resp.Header.Get("Content-Type"), body: body}
}

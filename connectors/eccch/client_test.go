package eccch

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAFailedQueryIsAnError checks that when the knowledge base answers
// "succeed: false" with HTTP 200, the caller gets an error, not empty results.
func TestAFailedQueryIsAnError(t *testing.T) {
	//GIVEN: a knowledge base that reports every query as failed.
	kb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"succeed":false,"message":"store unavailable"}`)
	}))
	defer kb.Close()
	tokens := NewTokenSource("unused", "client", "")
	tokens.access = "token"
	c := NewClient(kb.URL, "store", tokens)

	//WHEN: a query runs.
	rows, err := c.Select(context.Background(), "SELECT * WHERE {}")

	//THEN: it fails with the knowledge base's message.
	require.Error(t, err)
	assert.Contains(t, err.Error(), "store unavailable")
	assert.Nil(t, rows)
}

// TestParallelRejectionsRefreshTheTokenOnce checks that when several queries
// are rejected with the same expired token at once, EGI is asked for a new
// token only once and every query still succeeds.
func TestParallelRejectionsRefreshTheTokenOnce(t *testing.T) {
	//GIVEN: a knowledge base that holds back every expired-token request until all have arrived.
	const parallel = 5
	idp := newFakeIdP(t, "refresh-1", "fresh", "")
	var arrived sync.WaitGroup
	arrived.Add(parallel)
	kb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fresh" {
			arrived.Done()
			arrived.Wait()
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		io.WriteString(w, `{"succeed":true,"results":{"results":{"bindings":[{"x":{"value":"1"}}]}}}`)
	}))
	t.Cleanup(kb.Close)
	c := NewClient(kb.URL, "store", expiredTokens(idp.URL))

	//WHEN: the queries run at the same time.
	errs := make([]error, parallel)
	var done sync.WaitGroup
	for i := range parallel {
		done.Go(func() {
			_, errs[i] = c.Select(context.Background(), "SELECT ?x WHERE {}")
		})
	}
	done.Wait()

	//THEN: all succeed after a single refresh.
	for _, err := range errs {
		require.NoError(t, err)
	}
	assert.Equal(t, int32(1), idp.calls.Load())
}

// TestAnExpiredTokenIsRefreshedOnce checks that an expired access token is
// renewed once with the refresh token, the query is retried once and then
// succeeds, and that a second rejection is reported instead of looping.
func TestAnExpiredTokenIsRefreshedOnce(t *testing.T) {
	//GIVEN: a knowledge base that only accepts "fresh" and an EGI token endpoint that issues it.
	idp := newFakeIdP(t, "refresh-1", "fresh", "")
	var kbCalls atomic.Int32
	kb := newFakeKB(t, &kbCalls, "fresh")
	c := NewClient(kb.URL, "store", expiredTokens(idp.URL))

	//WHEN: a query runs with the expired token.
	rows, err := c.Select(context.Background(), "SELECT ?x WHERE {}")

	//THEN: one refresh and one retry make it succeed.
	require.NoError(t, err)
	assert.Equal(t, []map[string]string{{"x": "1"}}, rows)
	assert.Equal(t, int32(1), idp.calls.Load())
	assert.Equal(t, int32(2), kbCalls.Load())

	//WHEN: the knowledge base rejects even the refreshed token.
	kbCalls.Store(0)
	rejecting := newFakeKB(t, &kbCalls, "never")
	c = NewClient(rejecting.URL, "store", expiredTokens(idp.URL))
	_, err = c.Select(context.Background(), "SELECT ?x WHERE {}")

	//THEN: it gives up after one retry and names the env vars to update.
	require.Error(t, err)
	assert.Contains(t, err.Error(), "EGI_REFRESH_TOKEN")
	assert.Equal(t, int32(2), kbCalls.Load())
}

// TestARotatedRefreshTokenIsKept checks that when EGI hands out a new refresh
// token, the next refresh uses it instead of the old, now invalid one.
func TestARotatedRefreshTokenIsKept(t *testing.T) {
	//GIVEN: an EGI token endpoint that rotates "refresh-1" into "refresh-2".
	idp := newFakeIdP(t, "refresh-1", "access-1", "refresh-2")
	tokens := NewTokenSource(idp.URL, "client", "refresh-1")
	first, err := tokens.Refresh(context.Background(), "")
	require.NoError(t, err)

	//WHEN: the token is refreshed again.
	idp.wantRefresh.Store("refresh-2")
	_, err = tokens.Refresh(context.Background(), first)

	//THEN: the rotated refresh token was sent and accepted.
	require.NoError(t, err)
	assert.Equal(t, int32(2), idp.calls.Load())
}

// TestAnHDTComesWithItsDatePlaceAndRawTriples checks that loading a digital
// twin also brings the monument's date and place, and every triple ECHOES
// holds for it, stored exactly as the knowledge base sent them.
func TestAnHDTComesWithItsDatePlaceAndRawTriples(t *testing.T) {
	//GIVEN: a knowledge base holding the Galassi monument, dated 1924, in Pisa.
	kb := newFakeHDTKB(t, rawTriples)
	c := NewClient(kb.URL, "store", validTokens())

	//WHEN: the HDT is loaded.
	hdt, err := c.HDT(context.Background(), "http://echoes-eccch.eu/HDT/Fkp1JTtfVek")

	//THEN: it has the date, the place and the triples byte for byte.
	require.NoError(t, err)
	assert.Equal(t, "1924-1924", hdt.Date)
	assert.Equal(t, "Pisa (PI)", hdt.Coverage)
	assert.Equal(t, rawTriples, string(hdt.Raw))
}

// TestAnHDTWhoseTriplesCannotBeReadIsNotLoaded checks that when ECHOES fails
// to return a twin's triples, loading the twin fails instead of carrying on
// without its raw record.
func TestAnHDTWhoseTriplesCannotBeReadIsNotLoaded(t *testing.T) {
	//GIVEN: a knowledge base that answers the record queries but fails the triples query.
	kb := newFakeHDTKB(t, `{"succeed":false,"message":"construct timed out"}`)
	c := NewClient(kb.URL, "store", validTokens())

	//WHEN: the HDT is loaded.
	hdt, err := c.HDT(context.Background(), "http://echoes-eccch.eu/HDT/Fkp1JTtfVek")

	//THEN: it fails with the knowledge base's message.
	require.Error(t, err)
	assert.Contains(t, err.Error(), "construct timed out")
	assert.Nil(t, hdt)
}

const rawTriples = `{"succeed":true,"results":{"head":{"vars":["s","p","o"]},"results":{"bindings":[{"s":{"type":"uri","value":"http://echoes-eccch.eu/HDT/Fkp1JTtfVek"},"p":{"type":"uri","value":"http://www.w3.org/2000/01/rdf-schema#label"},"o":{"type":"literal","value":"Allegoria della disperazione - Elio Galassi"}}]}}}`

// newFakeHDTKB is a knowledge base that answers the record and model queries
// for the Galassi HDT, and the triples (CONSTRUCT) query with construct.
func newFakeHDTKB(t *testing.T, construct string) *httptest.Server {
	t.Helper()
	kb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Query string `json:"query"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		switch {
		case strings.Contains(req.Query, "CONSTRUCT"):
			io.WriteString(w, construct)
		case strings.Contains(req.Query, "HC8_3D_Model"):
			io.WriteString(w, `{"succeed":true,"results":{"results":{"bindings":[{"src":{"value":"https://example.org/galassi.glb"}}]}}}`)
		default:
			io.WriteString(w, `{"succeed":true,"results":{"results":{"bindings":[{
				"label":{"value":"Allegoria della disperazione - Elio Galassi"},
				"date":{"value":"1924-1924"},"coverage":{"value":"Pisa (PI)"}}]}}}`)
		}
	}))
	t.Cleanup(kb.Close)
	return kb
}

// validTokens is a token source whose access token is still accepted.
func validTokens() *TokenSource {
	tokens := NewTokenSource("unused", "client", "")
	tokens.access = "token"
	return tokens
}

// expiredTokens is a token source whose access token the knowledge base no
// longer accepts, as after the process has run for over an hour.
func expiredTokens(idpURL string) *TokenSource {
	tokens := NewTokenSource(idpURL, "client", "refresh-1")
	tokens.access = "expired"
	return tokens
}

// fakeIdP is an EGI token endpoint that accepts only wantRefresh.
type fakeIdP struct {
	*httptest.Server
	wantRefresh atomic.Value
	calls       atomic.Int32
}

func newFakeIdP(t *testing.T, wantRefresh, access, rotated string) *fakeIdP {
	t.Helper()
	idp := &fakeIdP{}
	idp.wantRefresh.Store(wantRefresh)
	idp.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idp.calls.Add(1)
		if r.PostFormValue("grant_type") != "refresh_token" || r.PostFormValue("client_id") != "client" ||
			r.PostFormValue("refresh_token") != idp.wantRefresh.Load() {
			w.WriteHeader(http.StatusBadRequest)
			io.WriteString(w, `{"error":"invalid_grant"}`)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"access_token": access, "refresh_token": rotated})
	}))
	t.Cleanup(idp.Close)
	return idp
}

// newFakeKB is a knowledge base that answers one row for the accepted token
// and 401 for any other.
func newFakeKB(t *testing.T, calls *atomic.Int32, accept string) *httptest.Server {
	t.Helper()
	kb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer "+accept {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		io.WriteString(w, `{"succeed":true,"results":{"head":{"vars":["x"]},"results":{"bindings":[{"x":{"type":"literal","value":"1"}}]}}}`)
	}))
	t.Cleanup(kb.Close)
	return kb
}

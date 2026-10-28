package eccch

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSearchKeepsUserTextInsideTheLiteral checks that quotes and backslashes
// typed into the search box are escaped, so they cannot end the SPARQL
// string and change the query.
func TestSearchKeepsUserTextInsideTheLiteral(t *testing.T) {
	//GIVEN: a knowledge base that records the query it receives.
	var got string
	kb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query string `json:"query"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		got = body.Query
		io.WriteString(w, `{"succeed":true,"results":{"results":{"bindings":[{"hdt":{"value":"http://echoes-eccch.eu/HDT/x"},"label":{"value":"Galassi"}}]}}}`)
	}))
	defer kb.Close()
	tokens := NewTokenSource("unused", "client", "")
	tokens.access = "token"
	c := NewClient(kb.URL, "store", tokens)

	//WHEN: someone searches for text that tries to close the literal.
	hits, err := c.Search(context.Background(), `a") } # \`, 5)

	//THEN: the quote and backslash are escaped, and the hit is mapped.
	require.NoError(t, err)
	assert.Contains(t, got, `"a\") } # \\"`)
	assert.Equal(t, []Hit{{URI: "http://echoes-eccch.eu/HDT/x", Label: "Galassi"}}, hits)
}

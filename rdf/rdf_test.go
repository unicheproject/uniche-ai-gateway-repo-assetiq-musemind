package rdf

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	xsdString = "http://www.w3.org/2001/XMLSchema#string"
	dcTitle   = "http://purl.org/dc/elements/1.1/title"
	proxy     = "http://data.europeana.eu/proxy/provider/90402/SK_A_2344"
)

// TestSPARQLJSONKeepsTermsAsReceived checks that every row of a knowledge
// base answer becomes one triple and that typed literals keep their type.
func TestSPARQLJSONKeepsTermsAsReceived(t *testing.T) {
	//GIVEN: a recorded knowledge base answer for HDT Fkp1JTtfVek with 136 rows.
	raw, err := os.ReadFile("testdata/eccch-construct.json")
	require.NoError(t, err)

	//WHEN: it is converted to triples.
	triples, err := FromSPARQLJSON(raw)

	//THEN: there are 136 triples and a known literal is still an xsd:string.
	require.NoError(t, err)
	assert.Len(t, triples, 136)
	assert.Contains(t, triples, Triple{
		S: Term{Kind: IRI, Value: "https://w3id.org/arco/resource/HistoricOrArtisticProperty/0901078520"},
		P: Term{Kind: IRI, Value: "http://purl.org/dc/elements/1.1/date"},
		O: Term{Kind: Literal, Value: "1924-1924", Datatype: xsdString},
	})
}

// TestSPARQLJSONRejectsBadAnswers checks that a failed query or an
// incomplete row is an error, not a partial graph.
func TestSPARQLJSONRejectsBadAnswers(t *testing.T) {
	tests := map[string]string{
		"failed query": `{"succeed":false,"message":"store unavailable"}`,
		"missing o":    `{"succeed":true,"results":{"results":{"bindings":[{"s":{"type":"uri","value":"a"},"p":{"type":"uri","value":"b"}}]}}}`,
		"unknown type": `{"succeed":true,"results":{"results":{"bindings":[{"s":{"type":"x","value":"a"},"p":{"type":"uri","value":"b"},"o":{"type":"uri","value":"c"}}]}}}`,
	}
	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			//GIVEN: an answer that cannot be shown as a graph.
			//WHEN: it is converted.
			triples, err := FromSPARQLJSON([]byte(raw))

			//THEN: conversion fails.
			require.Error(t, err)
			assert.Nil(t, triples)
		})
	}
}

// TestJSONLDYieldsTheProviderProxy checks that a real Europeana record turns
// into triples with a language-tagged title and blank quality annotations.
func TestJSONLDYieldsTheProviderProxy(t *testing.T) {
	//GIVEN: a Europeana record with an inline context.
	raw, err := os.ReadFile("testdata/europeana-SK_A_2344.jsonld")
	require.NoError(t, err)

	//WHEN: it is converted to triples.
	triples, err := FromJSONLD(raw)

	//THEN: the provider proxy has a title with a language, and annotations are blank nodes.
	require.NoError(t, err)
	var titled, blankAnnotation bool
	for _, tr := range triples {
		if tr.S.Value == proxy && tr.P.Value == dcTitle && tr.O.Lang != "" {
			titled = true
		}
		if tr.S.Kind == BlankNode && tr.O.Value == "http://www.w3.org/ns/dqv#QualityAnnotation" {
			blankAnnotation = true
		}
	}
	assert.True(t, titled, "provider proxy should have a language-tagged dc:title")
	assert.True(t, blankAnnotation, "QualityAnnotation nodes should be blank nodes")
}

// TestJSONLDIsStableAcrossParses checks that the same record always gives the
// same triples in the same order, even when its nodes come in another order,
// so a link to a blank node still works after the page is reloaded.
func TestJSONLDIsStableAcrossParses(t *testing.T) {
	//GIVEN: a Europeana record, and the same record with its @graph nodes reversed.
	raw, err := os.ReadFile("testdata/europeana-SK_A_2344.jsonld")
	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, json.Unmarshal(raw, &doc))
	nodes, ok := doc["@graph"].([]any)
	require.True(t, ok, "record should have an @graph array")
	slices.Reverse(nodes)
	reversed, err := json.Marshal(doc)
	require.NoError(t, err)

	//WHEN: the original is converted once, then each version several times.
	want, err := FromJSONLD(raw)
	require.NoError(t, err)

	//THEN: every conversion matches the first, blank node labels included.
	for range 5 {
		for _, input := range [][]byte{raw, reversed} {
			got, err := FromJSONLD(input)
			require.NoError(t, err)
			require.Equal(t, want, got)
		}
	}
}

// TestJSONLDNeverFetchesARemoteContext checks that a record pointing at a
// remote context fails without any network request.
func TestJSONLDNeverFetchesARemoteContext(t *testing.T) {
	//GIVEN: a server that fails the test if it is contacted, and a record whose context lives there.
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("remote context was fetched")
	}))
	defer srv.Close()
	doc := `{"@context":"` + srv.URL + `/ctx.jsonld","@id":"http://example.org/a","name":"x"}`

	//WHEN: the record is converted.
	_, err := FromJSONLD([]byte(doc))

	//THEN: conversion fails because the loader refused the fetch, and the server was never hit.
	require.ErrorIs(t, err, errRemoteFetch)
}

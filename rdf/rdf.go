// Package rdf turns the raw records the Repository keeps (a KBMS CONSTRUCT
// result, a Europeana JSON-LD record) into plain triples for display. It
// imports nothing from this module.
package rdf

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/piprate/json-gold/ld"
)

// TermKind says what a Term holds.
type TermKind int

// The kinds of RDF term.
const (
	IRI TermKind = iota
	BlankNode
	Literal
)

// Term is one node of a triple.
type Term struct {
	Kind     TermKind
	Value    string // IRI, blank node label without "_:", or the literal text
	Datatype string // literals only; empty for plain strings
	Lang     string // literals only
}

// Triple is one subject-predicate-object statement.
type Triple struct{ S, P, O Term }

const rdfLangString = "http://www.w3.org/1999/02/22-rdf-syntax-ns#langString"

// FromSPARQLJSON reads the KBMS envelope (results.results.bindings with s, p
// and o SPARQL 1.1 JSON terms) and returns one triple per row.
func FromSPARQLJSON(raw []byte) ([]Triple, error) {
	type term struct {
		Type     string `json:"type"`
		Value    string `json:"value"`
		Datatype string `json:"datatype"`
		Lang     string `json:"xml:lang"`
	}
	var env struct {
		Succeed bool   `json:"succeed"`
		Message string `json:"message"`
		Results struct {
			Results struct {
				Bindings []map[string]*term `json:"bindings"`
			} `json:"results"`
		} `json:"results"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("decoding SPARQL JSON: %w", err)
	}
	if !env.Succeed {
		return nil, fmt.Errorf("knowledge base reported failure: %s", env.Message)
	}
	rows := env.Results.Results.Bindings
	triples := make([]Triple, 0, len(rows))
	for i, row := range rows {
		var terms [3]Term
		for j, name := range []string{"s", "p", "o"} {
			t := row[name]
			if t == nil {
				return nil, fmt.Errorf("binding %d has no %q", i, name)
			}
			kind, err := sparqlKind(t.Type)
			if err != nil {
				return nil, fmt.Errorf("binding %d %q: %w", i, name, err)
			}
			terms[j] = Term{Kind: kind, Value: t.Value, Datatype: t.Datatype, Lang: t.Lang}
		}
		triples = append(triples, Triple{S: terms[0], P: terms[1], O: terms[2]})
	}
	return triples, nil
}

func sparqlKind(typ string) (TermKind, error) {
	switch typ {
	case "uri":
		return IRI, nil
	case "bnode":
		return BlankNode, nil
	case "literal", "typed-literal":
		return Literal, nil
	}
	return 0, fmt.Errorf("unknown term type %q", typ)
}

// errRemoteFetch is what the document loader answers to every request.
var errRemoteFetch = errors.New("remote documents are not fetched")

// noRemote refuses every fetch: records carry their context inline, and
// rendering a page must never touch the network.
type noRemote struct{}

func (noRemote) LoadDocument(u string) (*ld.RemoteDocument, error) {
	return nil, fmt.Errorf("loading %s: %w", u, errRemoteFetch)
}

// FromJSONLD converts a JSON-LD document to the triples of its default graph.
// A remote @context is an error; nothing is fetched. The result is canonical
// (URDNA2015): the same document always gives the same blank node labels and
// triple order, so links to blank nodes survive a reload.
func FromJSONLD(raw []byte) ([]Triple, error) {
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("decoding JSON-LD: %w", err)
	}
	opts := ld.NewJsonLdOptions("")
	opts.DocumentLoader = noRemote{}
	opts.Algorithm = ld.AlgorithmURDNA2015
	res, err := ld.NewJsonLdProcessor().Normalize(doc, opts)
	if err != nil {
		return nil, fmt.Errorf("converting JSON-LD to RDF: %w", err)
	}
	ds, ok := res.(*ld.RDFDataset)
	if !ok {
		return nil, fmt.Errorf("converting JSON-LD to RDF: unexpected result %T", res)
	}
	quads := ds.Graphs["@default"]
	triples := make([]Triple, 0, len(quads))
	for _, q := range quads {
		triples = append(triples, Triple{S: fromNode(q.Subject), P: fromNode(q.Predicate), O: fromNode(q.Object)})
	}
	return triples, nil
}

func fromNode(n ld.Node) Term {
	switch v := n.(type) {
	case ld.BlankNode:
		return Term{Kind: BlankNode, Value: strings.TrimPrefix(v.Attribute, "_:")}
	case ld.Literal:
		t := Term{Kind: Literal, Value: v.Value, Datatype: v.Datatype, Lang: v.Language}
		if t.Lang != "" && t.Datatype == rdfLangString {
			t.Datatype = ""
		}
		return t
	default:
		return Term{Kind: IRI, Value: n.GetValue()}
	}
}

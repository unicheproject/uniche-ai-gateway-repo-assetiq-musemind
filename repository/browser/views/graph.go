package views

import (
	"maps"
	"net/url"
	"slices"
	"strings"

	"uniche/api/repositoryapi"
	"uniche/rdf"
)

const (
	rdfType     = "http://www.w3.org/1999/02/22-rdf-syntax-ns#type"
	rdfsLabel   = "http://www.w3.org/2000/01/rdf-schema#label"
	skosLabel   = "http://www.w3.org/2004/02/skos/core#prefLabel"
	xsdString   = "http://www.w3.org/2001/XMLSchema#string"
	blankPrefix = "_:"
)

// prefixes shorten IRIs for display only; the full IRI stays in the title.
var prefixes = []struct{ name, ns string }{
	{"rdf", "http://www.w3.org/1999/02/22-rdf-syntax-ns#"},
	{"rdfs", "http://www.w3.org/2000/01/rdf-schema#"},
	{"owl", "http://www.w3.org/2002/07/owl#"},
	{"xsd", "http://www.w3.org/2001/XMLSchema#"},
	{"dc", "http://purl.org/dc/elements/1.1/"},
	{"dcterms", "http://purl.org/dc/terms/"},
	{"skos", "http://www.w3.org/2004/02/skos/core#"},
	{"foaf", "http://xmlns.com/foaf/0.1/"},
	{"edm", "http://www.europeana.eu/schemas/edm/"},
	{"ore", "http://www.openarchives.org/ore/terms/"},
	{"dqv", "http://www.w3.org/ns/dqv#"},
	{"oa", "http://www.w3.org/ns/oa#"},
	{"wgs84_pos", "http://www.w3.org/2003/01/geo/wgs84_pos#"},
	{"hdto", "http://isl.ics.forth.gr/ontology/echoes/"},
}

// TermView is one RDF term ready to show: text, an optional link and a muted
// suffix (language or datatype).
type TermView struct {
	Text     string
	Title    string // full IRI when Text is shortened
	Href     string // in-page link to another subject
	External string // outside link for other http(s) IRIs
	Note     string
}

// GraphRow is one line of the Outgoing or Incoming table. Outgoing rows show
// Predicate and Other as the object; Incoming rows show Other as the subject.
type GraphRow struct {
	Predicate TermView
	Other     TermView
}

// SubjectRow is one entry of the subject index.
type SubjectRow struct {
	Subject TermView
	Type    TermView
	Label   string
}

// Graph is the resource page of one asset's source graph.
type Graph struct {
	AssetID  string
	Subject  TermView // zero when the subject is not in the graph
	Found    bool
	Outgoing []GraphRow
	Incoming []GraphRow
	Subjects []SubjectRow
}

// SubjectKey is how a subject travels in the ?s= parameter.
func SubjectKey(t rdf.Term) string {
	if t.Kind == rdf.BlankNode {
		return blankPrefix + t.Value
	}
	return t.Value
}

func parseKey(key string) rdf.Term {
	if label, ok := strings.CutPrefix(key, blankPrefix); ok {
		return rdf.Term{Kind: rdf.BlankNode, Value: label}
	}
	return rdf.Term{Kind: rdf.IRI, Value: key}
}

// NewGraph builds the page for subject (a SubjectKey) out of the asset's
// triples. When the subject has no triples, Found is false and only the
// subject index is filled.
func NewGraph(assetID string, triples []rdf.Triple, subject string) *Graph {
	graph := &Graph{AssetID: assetID}
	subjects := map[rdf.Term]bool{}
	for _, t := range triples {
		subjects[t.S] = true
	}
	view := func(t rdf.Term) TermView { return termView(assetID, t, subjects) }

	want := parseKey(subject)
	for _, t := range triples {
		if t.S == want {
			graph.Outgoing = append(graph.Outgoing, GraphRow{Predicate: view(t.P), Other: view(t.O)})
		}
		if t.O == want {
			graph.Incoming = append(graph.Incoming, GraphRow{Predicate: view(t.P), Other: view(t.S)})
		}
	}
	graph.Found = len(graph.Outgoing)+len(graph.Incoming) > 0
	if graph.Found {
		graph.Subject = view(want)
		graph.Subject.Href = ""
	}
	byRow := func(a, b GraphRow) int {
		return strings.Compare(a.Predicate.Title+a.Predicate.Text+"\x00"+a.Other.Text, b.Predicate.Title+b.Predicate.Text+"\x00"+b.Other.Text)
	}
	slices.SortFunc(graph.Outgoing, byRow)
	slices.SortFunc(graph.Incoming, byRow)
	graph.Subjects = subjectIndex(triples, subjects, view)
	return graph
}

func subjectIndex(triples []rdf.Triple, subjects map[rdf.Term]bool, view func(rdf.Term) TermView) []SubjectRow {
	rows := map[rdf.Term]*SubjectRow{}
	for s := range subjects {
		rows[s] = &SubjectRow{Subject: view(s)}
	}
	for _, t := range triples {
		if row := rows[t.S]; t.P.Value == rdfType && row.Type.Text == "" {
			row.Type = view(t.O)
		}
	}
	for s, label := range subjectLabels(triples) {
		rows[s].Label = label
	}
	out := make([]SubjectRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, *row)
	}
	slices.SortFunc(out, func(a, b SubjectRow) int {
		return strings.Compare(a.Subject.Title+a.Subject.Text, b.Subject.Title+b.Subject.Text)
	})
	return out
}

// subjectLabels picks one rdfs:label or skos:prefLabel literal per subject.
func subjectLabels(triples []rdf.Triple) map[rdf.Term]string {
	byLang := map[rdf.Term]map[string]string{} // subject -> language -> label
	for _, t := range triples {
		if (t.P.Value != rdfsLabel && t.P.Value != skosLabel) || t.O.Kind != rdf.Literal {
			continue
		}
		if byLang[t.S] == nil {
			byLang[t.S] = map[string]string{}
		}
		if _, seen := byLang[t.S][t.O.Lang]; !seen {
			byLang[t.S][t.O.Lang] = t.O.Value
		}
	}
	labels := make(map[rdf.Term]string, len(byLang))
	for s, m := range byLang {
		labels[s] = pickLabel(m)
	}
	return labels
}

// pickLabel prefers English, then an untagged label, then the first language
// in alphabetical order, as assetiq does for Europeana fields.
func pickLabel(byLang map[string]string) string {
	for _, lang := range []string{"en", ""} {
		if v, ok := byLang[lang]; ok {
			return v
		}
	}
	return byLang[slices.Sorted(maps.Keys(byLang))[0]]
}

func termView(assetID string, term rdf.Term, subjects map[rdf.Term]bool) TermView {
	switch term.Kind {
	case rdf.Literal:
		v := TermView{Text: term.Value}
		switch {
		case term.Lang != "":
			v.Note = "@" + term.Lang
		case term.Datatype != "" && term.Datatype != xsdString:
			v.Note = "^^" + shorten(term.Datatype)
		}
		return v
	case rdf.BlankNode:
		v := TermView{Text: blankPrefix + term.Value}
		if subjects[term] {
			v.Href = graphURL(assetID, SubjectKey(term))
		}
		return v
	}
	v := TermView{Text: shorten(term.Value)}
	if v.Text != term.Value {
		v.Title = term.Value
	}
	switch {
	case subjects[term]:
		v.Href = graphURL(assetID, term.Value)
	case strings.HasPrefix(term.Value, "http://"), strings.HasPrefix(term.Value, "https://"):
		v.External = term.Value
	}
	return v
}

func shorten(iri string) string {
	for _, p := range prefixes {
		if rest, ok := strings.CutPrefix(iri, p.ns); ok {
			return p.name + ":" + rest
		}
	}
	return iri
}

func graphURL(assetID, subject string) string {
	u := assetURL(assetID) + "/graph"
	if subject == "" {
		return u
	}
	return u + "?" + url.Values{"s": {subject}}.Encode()
}

// HasSourceGraph reports whether the asset's raw source can be shown as a
// graph: only ECCCH and Europeana records are parsed.
func HasSourceGraph(a *repositoryapi.Asset) bool {
	switch a.Source {
	case repositoryapi.AssetSourceEccch, repositoryapi.AssetSourceEuropeana:
		return a.CurrentVersion.Value.RawSource.Value != ""
	}
	return false
}

// Package eccch is a client for the ECHOES Knowledge Base (KBMS), which holds
// ECCCH Heritage Digital Twins (HDTs) as HDTO RDF. It only reads, through
// SPARQL against a single triplestore, and knows nothing about AssetIQ or the
// Repository.
package eccch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const maxResponseBytes = 10 << 20

// ErrNotFound means the knowledge base has no HDT with the requested URI.
var ErrNotFound = errors.New("eccch hdt not found")

type Client struct {
	baseURL       string
	triplestoreID string
	tokens        *TokenSource
	http          *http.Client
}

func NewClient(baseURL, triplestoreID string, tokens *TokenSource) *Client {
	return &Client{
		baseURL:       strings.TrimSuffix(baseURL, "/"),
		triplestoreID: triplestoreID,
		tokens:        tokens,
		http:          &http.Client{Timeout: 30 * time.Second},
	}
}

// HDT is a Heritage Digital Twin with the heritage entity it describes.
type HDT struct {
	URI         string
	Label       string
	Entity      string
	Description string
	Rights      string
	Date        string  // entity dc:date, free text, e.g. "1924-1924"
	Coverage    string  // entity dc:coverage, a place name, e.g. "Pisa (PI)"
	Models      []Model // distinct by URL, ordered by URL
	Raw         []byte  // every triple in the HDT's graphs, as the knowledge base returned them
}

// Model is a 3D model file attached to an HDT.
type Model struct {
	URL    string
	Format string // as recorded; often "application/octet-stream"
}

const recordQuery = `PREFIX hdto: <http://isl.ics.forth.gr/ontology/echoes/>
PREFIX rdfs: <http://www.w3.org/2000/01/rdf-schema#>
PREFIX dc:   <http://purl.org/dc/elements/1.1/>
SELECT ?label ?entity ?description ?rights ?date ?coverage WHERE {
  BIND(<%[1]s> AS ?hdt)
  ?hdt hdto:HP33_contains ?g .
  GRAPH ?g {
    ?hdt rdfs:label ?label .
    OPTIONAL { ?entity hdto:HP1_has_digital_twin ?hdt .
               OPTIONAL { ?entity dc:description ?description }
               OPTIONAL { ?entity dc:rights ?rights }
               OPTIONAL { ?entity dc:date ?date }
               OPTIONAL { ?entity dc:coverage ?coverage } }
  }
  FILTER(?g != <http://echoes-eccch.eu/kb/catalogue/HDT/maintenance>)
}
ORDER BY DESC(BOUND(?entity)) DESC(BOUND(?description)) DESC(BOUND(?rights)) ?g
LIMIT 1`

// Contributors often add the same model more than once, so select by
// distinct file URL, never by model node.
const modelsQuery = `PREFIX hdto: <http://isl.ics.forth.gr/ontology/echoes/>
PREFIX dc:   <http://purl.org/dc/elements/1.1/>
SELECT DISTINCT ?src ?format WHERE {
  <%[1]s> hdto:HP33_contains ?g .
  GRAPH ?g { ?m a hdto:HC8_3D_Model ; dc:source ?src . OPTIONAL { ?m dc:format ?format } }
} ORDER BY ?src LIMIT 10`

// rawQuery collects every triple across the HDT's graphs. Contributors repeat
// the same triples in each graph; CONSTRUCT returns each triple once.
const rawQuery = `PREFIX hdto: <http://isl.ics.forth.gr/ontology/echoes/>
CONSTRUCT { ?s ?p ?o } WHERE { <%[1]s> hdto:HP33_contains ?g . GRAPH ?g { ?s ?p ?o } }`

// HDT loads the HDT with the given URI, e.g.
// "http://echoes-eccch.eu/HDT/Fkp1JTtfVek".
func (c *Client) HDT(ctx context.Context, uri string) (*HDT, error) {
	// The URI goes into the query verbatim; refuse anything that could end
	// the IRI early.
	if uri == "" || strings.ContainsAny(uri, "<>\"{}|\\^` \t\r\n") {
		return nil, fmt.Errorf("invalid hdt uri %q", uri)
	}
	rows, err := c.Select(ctx, fmt.Sprintf(recordQuery, uri))
	if err != nil {
		return nil, fmt.Errorf("loading hdt %s: %w", uri, err)
	}
	if len(rows) == 0 {
		return nil, ErrNotFound
	}
	r := rows[0]
	hdt := &HDT{
		URI: uri, Label: r["label"], Entity: r["entity"], Description: r["description"],
		Rights: r["rights"], Date: r["date"], Coverage: r["coverage"],
	}

	rows, err = c.Select(ctx, fmt.Sprintf(modelsQuery, uri))
	if err != nil {
		return nil, fmt.Errorf("loading models of hdt %s: %w", uri, err)
	}
	seen := map[string]bool{}
	for _, r := range rows {
		// DISTINCT is over (src, format), so one file with two formats
		// still yields two rows.
		if seen[r["src"]] {
			continue
		}
		seen[r["src"]] = true
		hdt.Models = append(hdt.Models, Model{URL: r["src"], Format: r["format"]})
	}

	if hdt.Raw, err = c.query(ctx, fmt.Sprintf(rawQuery, uri)); err != nil {
		return nil, fmt.Errorf("loading triples of hdt %s: %w", uri, err)
	}
	return hdt, nil
}

// Select runs a SPARQL SELECT query and flattens each binding to var -> value.
// Unbound variables are absent from the row.
func (c *Client) Select(ctx context.Context, query string) ([]map[string]string, error) {
	raw, err := c.query(ctx, query)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Results struct {
			Results struct {
				Bindings []map[string]struct {
					Value string `json:"value"`
				} `json:"bindings"`
			} `json:"results"`
		} `json:"results"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("decoding query response: %w", err)
	}
	rows := make([]map[string]string, 0, len(resp.Results.Results.Bindings))
	for _, b := range resp.Results.Results.Bindings {
		row := make(map[string]string, len(b))
		for k, v := range b {
			row[k] = v.Value
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// query runs a SPARQL query and returns the response body verbatim, once the
// knowledge base has reported success. CONSTRUCT results come back as s/p/o
// bindings in the same envelope as SELECT results.
func (c *Client) query(ctx context.Context, query string) ([]byte, error) {
	body, err := json.Marshal(map[string]string{"query": query, "executorTripleStoreId": c.triplestoreID})
	if err != nil {
		return nil, fmt.Errorf("encoding query: %w", err)
	}
	raw, err := c.post(ctx, "/repository/singleTripleStoreQuery", body)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Succeed bool   `json:"succeed"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("decoding query response: %w", err)
	}
	if !resp.Succeed {
		return nil, fmt.Errorf("knowledge base reported failure: %s", resp.Message)
	}
	return raw, nil
}

// post sends body with the current access token. On a 401 it refreshes the
// token once and retries once.
func (c *Client) post(ctx context.Context, path string, body []byte) ([]byte, error) {
	token, err := c.tokens.Token(ctx)
	if err != nil {
		return nil, err
	}
	resp, err := c.send(ctx, path, body, token)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		if token, err = c.tokens.Refresh(ctx, token); err != nil {
			return nil, err
		}
		if resp, err = c.send(ctx, path, body, token); err != nil {
			return nil, err
		}
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return nil, fmt.Errorf("calling knowledge base %s: still unauthorized after refreshing the token; get a new EGI_REFRESH_TOKEN", path)
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("calling knowledge base %s: unexpected status %s", path, resp.Status)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("reading knowledge base %s: %w", path, err)
	}
	return raw, nil
}

func (c *Client) send(ctx context.Context, path string, body []byte, token string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("building knowledge base request %s: %w", path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling knowledge base %s: %w", path, err)
	}
	return resp, nil
}

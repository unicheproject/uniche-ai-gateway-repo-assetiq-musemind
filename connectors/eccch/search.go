package eccch

import (
	"context"
	"fmt"
	"strings"
)

// Hit is one HDT found by a search.
type Hit struct {
	URI   string
	Label string
}

// literalEscaper makes free text safe inside a double-quoted SPARQL literal.
var literalEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`, "\t", `\t`)

const searchQuery = `PREFIX hdto: <http://isl.ics.forth.gr/ontology/echoes/>
PREFIX rdfs: <http://www.w3.org/2000/01/rdf-schema#>
SELECT ?hdt (MIN(STR(?l)) AS ?label) WHERE {
  ?hdt hdto:HP33_contains ?g .
  GRAPH ?g { ?hdt rdfs:label ?l . }
  FILTER(?g != <http://echoes-eccch.eu/kb/catalogue/HDT/maintenance>)
  FILTER(CONTAINS(LCASE(STR(?l)), "%s"))
}
GROUP BY ?hdt
ORDER BY ?label LIMIT %d`

// Search finds HDTs whose label contains query, ignoring case. Each HDT comes
// back once, with its first matching label, so duplicates never use up the limit. The knowledge
// base has no text index, so this scans labels and can be slow (see
// docs/eccch-kbms.md); callers should bound it with a context deadline.
func (c *Client) Search(ctx context.Context, query string, limit int) ([]Hit, error) {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return nil, nil
	}
	rows, err := c.Select(ctx, fmt.Sprintf(searchQuery, literalEscaper.Replace(query), limit))
	if err != nil {
		return nil, fmt.Errorf("searching hdts: %w", err)
	}
	hits := make([]Hit, 0, len(rows))
	for _, r := range rows {
		hits = append(hits, Hit{URI: r["hdt"], Label: r["label"]})
	}
	return hits, nil
}

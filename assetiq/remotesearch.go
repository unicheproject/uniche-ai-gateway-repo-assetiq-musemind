package assetiq

import (
	"context"
	"fmt"
	"strings"
	"time"

	"uniche/connectors/eccch"
	"uniche/connectors/europeana"
)

// searchTimeout bounds one source, so a slow cloud never holds up the others.
const searchTimeout = 10 * time.Second

// Hit is a record found at a source. Provider and Identifier are exactly what
// the Importer stores as the asset's external ID, so a hit can be matched to
// an imported asset without comparing titles.
type Hit struct {
	Provider   string
	Identifier string
	ImportURI  string // assetiq:// handle for Importer.Import; empty if not importable
	Title      string
	Type       string // repository asset type, e.g. "image"
	Thumbnail  string // empty if none
}

// Searcher queries one outside source at a time. Each method has its own
// deadline and fails on its own.
type Searcher struct {
	europeana *europeana.Client
	eccch     *eccch.Client // nil when ECCCH is not configured
}

func NewSearcher(eu *europeana.Client, kb *eccch.Client) *Searcher {
	return &Searcher{europeana: eu, eccch: kb}
}

// Europeana searches Europeana for query.
func (s *Searcher) Europeana(ctx context.Context, query string, limit int) ([]Hit, error) {
	if s.europeana == nil {
		return nil, fmt.Errorf("%w: europeana", ErrSourceNotConfigured)
	}
	ctx, cancel := context.WithTimeout(ctx, searchTimeout)
	defer cancel()
	found, err := s.europeana.Search(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	hits := make([]Hit, 0, len(found))
	for _, f := range found {
		hits = append(hits, europeanaHit(f.ID, f.Title, f.Type, f.Thumbnail))
	}
	return hits, nil
}

// Eccch searches the ECHOES Knowledge Base for query.
func (s *Searcher) Eccch(ctx context.Context, query string, limit int) ([]Hit, error) {
	if s.eccch == nil {
		return nil, fmt.Errorf("%w: eccch needs EGI_REFRESH_TOKEN", ErrSourceNotConfigured)
	}
	ctx, cancel := context.WithTimeout(ctx, searchTimeout)
	defer cancel()
	found, err := s.eccch.Search(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	hits := make([]Hit, 0, len(found))
	for _, f := range found {
		hit := Hit{Provider: "eccch", Identifier: f.URI, Title: f.Label, Type: "3d"}
		if local, ok := strings.CutPrefix(f.URI, eccchHDTNamespace); ok && handlePart.MatchString(local) {
			hit.ImportURI = eccchHDTPrefix + local
		}
		hits = append(hits, hit)
	}
	return hits, nil
}

func europeanaHit(id, title, typ, thumbnail string) Hit {
	hit := Hit{Provider: "europeana", Identifier: id, Title: title, Thumbnail: thumbnail}
	if t, ok := europeanaTypes[typ]; ok {
		hit.Type = string(t)
	}
	if local, ok := strings.CutPrefix(id, "/"); ok {
		hit.ImportURI = europeanaItemPrefix + local
	}
	return hit
}

package web

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/a-h/templ"
	"github.com/go-chi/chi/v5"

	"uniche/api/repositoryapi"
	"uniche/repository/browser/views"
)

const (
	maxSectionSize = 50
	// sectionTimeout is longer than a connector's own deadline, leaving room
	// for the Repository lookup after a slow source answers.
	sectionTimeout = 15 * time.Second
	localKey       = "local"
)

var localRef = views.SectionRef{Key: localKey, Label: "Repository"}

// ErrNotConfigured is what a SearchFunc or ImportFunc returns when its source
// is not set up on this server.
var ErrNotConfigured = errors.New("source not configured")

// SearchFunc queries one cloud source. The browser never imports the AI or
// connector packages; cmd/server adapts them to this shape.
type SearchFunc func(ctx context.Context, query string, limit int) ([]views.RemoteHit, error)

// ImportFunc saves the record behind an import handle and returns its asset ID.
type ImportFunc func(ctx context.Context, uri string) (assetID string, err error)

// Option configures the browser.
type Option func(*browser)

// WithSource adds a cloud source to the search page, after the sources added
// before it. key is its URL segment, label its heading.
func WithSource(key, label string, search SearchFunc) Option {
	return func(b *browser) {
		b.sources = append(b.sources, source{ref: views.SectionRef{Key: key, Label: label}, search: search})
	}
}

// WithImporter lets hits of cloud sources be imported from the search page.
func WithImporter(f ImportFunc) Option {
	return func(b *browser) { b.importer = f }
}

type source struct {
	ref    views.SectionRef
	search SearchFunc
}

// sections lists the search page's sections: the Repository first, then the
// cloud sources in the order they were added.
func (b *browser) sections() []views.SectionRef {
	refs := make([]views.SectionRef, 0, 1+len(b.sources))
	refs = append(refs, localRef)
	for _, s := range b.sources {
		refs = append(refs, s.ref)
	}
	return refs
}

func (b *browser) searchPage(w http.ResponseWriter, r *http.Request) error {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	sections := b.sections()
	if q == "" {
		sections = nil
	}
	return render(w, r, views.SearchPage(q, sections))
}

// searchSection answers one section's fragment. A failure is a 200 with a
// message: HTMX does not swap error responses, so a failed source would
// otherwise keep its "Searching..." text forever.
func (b *browser) searchSection(w http.ResponseWriter, r *http.Request) error {
	key := chi.URLParam(r, "source")
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	limit := sectionLimit(r.URL.Query().Get("limit"))

	// One deadline covers the whole section, remote search and Repository
	// lookups alike, so no stalled call leaves "Searching..." up forever.
	ctx, cancel := context.WithTimeout(r.Context(), sectionTimeout)
	defer cancel()

	var ref views.SectionRef
	var build func() (templ.Component, error)
	if key == localKey {
		ref = localRef
		build = func() (templ.Component, error) { return b.localSection(ctx, ref, query, limit) }
	} else {
		i := b.sourceIndex(key)
		if i < 0 {
			return &statusError{http.StatusNotFound, "unknown source"}
		}
		src := b.sources[i]
		ref = src.ref
		build = func() (templ.Component, error) { return b.remoteSection(ctx, src, query, limit) }
	}
	if query == "" {
		return &statusError{http.StatusBadRequest, "q is required"}
	}

	c, err := build()
	switch {
	case errors.Is(err, ErrNotConfigured):
		c = views.SectionMessage(ref, "Not set up on this server.")
	case err != nil:
		slog.Error("repository browser search failed", "source", key, "err", err)
		c = views.SectionMessage(ref, "This source did not answer. Try again in a moment.")
	}
	return render(w, r, c)
}

func (b *browser) sourceIndex(key string) int {
	for i, s := range b.sources {
		if s.ref.Key == key {
			return i
		}
	}
	return -1
}

func (b *browser) localSection(ctx context.Context, ref views.SectionRef, q string, limit int) (templ.Component, error) {
	res, err := b.repo.ListAssets(ctx, repositoryapi.ListAssetsParams{
		Q:     repositoryapi.NewOptString(q),
		Limit: repositoryapi.NewOptInt(limit),
	})
	if err != nil {
		return nil, err
	}
	page, ok := res.(*repositoryapi.AssetPage)
	if !ok {
		return nil, errors.New("repository refused the search")
	}
	return views.LocalSection(ref, page.Items, moreURL(ref, q, limit, len(page.Items) == limit && page.NextCursor.Set)), nil
}

func (b *browser) remoteSection(ctx context.Context, src source, q string, limit int) (templ.Component, error) {
	hits, err := src.search(ctx, q, limit)
	if err != nil {
		return nil, err
	}
	imported, err := b.importedAssets(ctx, hits)
	if err != nil {
		return nil, err
	}
	more := moreURL(src.ref, q, limit, len(hits) == limit)
	return views.RemoteSection(src.ref, hits, imported, b.importer != nil, more), nil
}

// importedAssets resolves all hits of a section in one Repository call and
// returns the asset ID of each imported hit, keyed by views.ExternalKey.
func (b *browser) importedAssets(ctx context.Context, hits []views.RemoteHit) (map[string]string, error) {
	if len(hits) == 0 {
		return nil, nil
	}
	req := &repositoryapi.LookupExternalIdsRequest{}
	for _, h := range hits {
		req.Items = append(req.Items, repositoryapi.LookupExternalIdsRequestItemsItem{Provider: h.Provider, Identifier: h.Identifier})
	}
	res, err := b.repo.LookupExternalIds(ctx, req)
	if err != nil {
		return nil, err
	}
	found, ok := res.(*repositoryapi.LookupExternalIdsResponse)
	if !ok {
		return nil, errors.New("repository refused the external id lookup")
	}
	imported := make(map[string]string, len(found.Items))
	for _, it := range found.Items {
		imported[views.ExternalKey(it.Provider, it.Identifier)] = it.AssetId
	}
	return imported, nil
}

// importRecord imports one hit, then opens its asset.
func (b *browser) importRecord(w http.ResponseWriter, r *http.Request) error {
	if b.importer == nil {
		return &statusError{http.StatusServiceUnavailable, "import is not available"}
	}
	uri := r.PostFormValue("uri")
	if uri == "" {
		return &statusError{http.StatusBadRequest, "uri is required"}
	}
	id, err := b.importer(r.Context(), uri)
	if errors.Is(err, ErrNotConfigured) {
		return &statusError{http.StatusServiceUnavailable, "this source is not set up for import"}
	}
	if err != nil {
		return err
	}
	http.Redirect(w, r, views.AssetURL(id), http.StatusSeeOther)
	return nil
}

// sectionLimit reads the requested size, bounded to what a section may show.
func sectionLimit(v string) int {
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return views.DefaultSectionSize
	}
	return min(n, maxSectionSize)
}

// moreURL is the "Show more" link of a section, or "" when nothing more can
// be asked for.
func moreURL(ref views.SectionRef, q string, limit int, hasMore bool) string {
	if !hasMore || limit >= maxSectionSize {
		return ""
	}
	return views.SectionURL(ref.Key, q, limit+views.DefaultSectionSize)
}

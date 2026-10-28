// Package web is the HTTP side of the Repository browser: routes, basic auth
// and handlers. It is a small password-protected web UI for paging through the
// assets in the Repository, to check that imports landed with their files.
//
// It talks to the Repository only through repositoryapi.Handler, so the page
// and the REST API share one query path.
package web

import (
	"cmp"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/a-h/templ"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"uniche/api/repositoryapi"
	"uniche/rdf"
	"uniche/repository/browser/views"
)

const pageSize = 20

// europeanaProxy plus a record path ("/90402/SK_A_2344") names the provider
// ore:Proxy, which carries the descriptive fields; the ProvidedCHO has none.
const europeanaProxy = "http://data.europeana.eu/proxy/provider"

type handlerFunc func(w http.ResponseWriter, r *http.Request) error

// statusError is a failure with the status and safe message to show.
type statusError struct {
	status int
	msg    string
}

func (e *statusError) Error() string { return e.msg }

//go:embed static
var staticFS embed.FS

// New returns the browser, protected by basic auth.
func New(repo repositoryapi.Handler, user, password string, opts ...Option) http.Handler {
	b := &browser{repo: repo}
	for _, opt := range opts {
		opt(b)
	}
	r := chi.NewRouter()
	r.Use(middleware.Recoverer, basicAuth(user, password))
	r.Get("/", handle(b.list))
	r.Get("/search", handle(b.searchPage))
	r.Get("/search/{source}", handle(b.searchSection))
	// Basic auth is sent by the browser on cross-site requests too, so a
	// state-changing route must refuse them.
	r.With(http.NewCrossOriginProtection().Handler).Post("/import", handle(b.importRecord))
	r.Get("/static/*", b.static)
	r.Get("/assets/{id}", handle(b.detail))
	r.Get("/assets/{id}/graph", handle(b.graph))
	r.Get("/files/{fileId}", handle(b.file))
	return r
}

type browser struct {
	repo     repositoryapi.Handler
	sources  []source
	importer ImportFunc
}

// static serves the embedded scripts. The path comes from chi's route, not
// URL.Path, which still carries the mount prefix.
func (b *browser) static(w http.ResponseWriter, r *http.Request) {
	http.ServeFileFS(w, r, staticFS, "static/"+chi.URLParam(r, "*"))
}

func (b *browser) list(w http.ResponseWriter, r *http.Request) error {
	query := r.URL.Query()
	params, err := listParams(query)
	if err != nil {
		return err
	}
	res, err := b.repo.ListAssets(r.Context(), params)
	if err != nil {
		return err
	}
	page, ok := res.(*repositoryapi.AssetPage)
	if !ok {
		return &statusError{http.StatusBadRequest, "cannot list assets with these filters"}
	}
	return render(w, r, views.ListPage(page.Items, pageLinks(query, page)))
}

// listParams reads the cursor and the optional source/type filters.
func listParams(query url.Values) (repositoryapi.ListAssetsParams, error) {
	params := repositoryapi.ListAssetsParams{Limit: repositoryapi.NewOptInt(pageSize)}
	if v := query.Get("cursor"); v != "" {
		params.Cursor = repositoryapi.NewOptString(v)
	}
	if v := repositoryapi.AssetSource(query.Get("source")); v != "" {
		if v.Validate() != nil {
			return params, &statusError{http.StatusBadRequest, "unknown source"}
		}
		params.Source = repositoryapi.NewOptAssetSource(v)
	}
	if v := repositoryapi.AssetType(query.Get("type")); v != "" {
		if v.Validate() != nil {
			return params, &statusError{http.StatusBadRequest, "unknown type"}
		}
		params.Type = repositoryapi.NewOptAssetType(v)
	}
	return params, nil
}

// pageLinks builds the filter state and Next/Previous links. "back" in the
// query holds the cursors of the pages visited so far.
func pageLinks(query url.Values, page *repositoryapi.AssetPage) views.PageNav {
	nav := views.PageNav{Source: query.Get("source"), Type: query.Get("type")}
	var back []string
	if trail := query.Get("back"); trail != "" {
		back = strings.Split(trail, ",")
	}
	if page.NextCursor.Set {
		here := cmp.Or(query.Get("cursor"), views.FirstPage)
		nav.NextURL = views.ListURL(nav.Source, nav.Type, page.NextCursor.Value, append(back, here))
	}
	if n := len(back); n > 0 {
		nav.PrevURL = views.ListURL(nav.Source, nav.Type, back[n-1], back[:n-1])
	}
	return nav
}

func (b *browser) detail(w http.ResponseWriter, r *http.Request) error {
	asset, err := b.asset(r)
	if err != nil {
		return err
	}
	return render(w, r, views.DetailPage(asset))
}

// graph shows the triples of an asset's source record as a resource page: the
// properties of one subject, and the statements that point at it.
func (b *browser) graph(w http.ResponseWriter, r *http.Request) error {
	asset, err := b.asset(r)
	if err != nil {
		return err
	}
	triples, root, err := sourceGraph(asset)
	if err != nil {
		return err
	}
	g := views.NewGraph(asset.ID, triples, cmp.Or(r.URL.Query().Get("s"), root))
	return render(w, r, views.GraphPage(asset, g))
}

func (b *browser) asset(r *http.Request) (*repositoryapi.Asset, error) {
	res, err := b.repo.GetAsset(r.Context(), repositoryapi.GetAssetParams{ID: chi.URLParam(r, "id")})
	if err != nil {
		return nil, err
	}
	headers, ok := res.(*repositoryapi.AssetHeaders)
	if !ok {
		return nil, &statusError{http.StatusNotFound, "asset not found"}
	}
	return &headers.Response, nil
}

// sourceGraph parses the asset's raw source into triples and names the
// subject to open on. A raw source that cannot be parsed is an internal error.
func sourceGraph(a *repositoryapi.Asset) ([]rdf.Triple, string, error) {
	if !views.HasSourceGraph(a) {
		return nil, "", &statusError{http.StatusNotFound, "no source graph for this asset"}
	}
	parse, rootPrefix := rdf.FromSPARQLJSON, ""
	if a.Source == repositoryapi.AssetSourceEuropeana {
		parse, rootPrefix = rdf.FromJSONLD, europeanaProxy
	}
	triples, err := parse([]byte(a.CurrentVersion.Value.RawSource.Value))
	if err != nil {
		return nil, "", fmt.Errorf("parsing source graph of asset %s: %w", a.ID, err)
	}
	for _, x := range a.ExternalIds {
		if x.Provider == string(a.Source) {
			return triples, rootPrefix + x.Identifier, nil
		}
	}
	return triples, "", nil
}

// file streams a file's bytes. It sits behind the browser's basic auth, so
// thumbnails load without the gateway's bearer token.
func (b *browser) file(w http.ResponseWriter, r *http.Request) error {
	res, err := b.repo.GetFile(r.Context(), repositoryapi.GetFileParams{FileId: chi.URLParam(r, "fileId")})
	if err != nil {
		return err
	}
	file, ok := res.(*repositoryapi.GetFileOKHeaders)
	if !ok {
		return &statusError{http.StatusNotFound, "file not available"}
	}
	if c, ok := file.Response.Data.(io.Closer); ok {
		defer c.Close()
	}
	// Uploaded bytes are untrusted: never let them run as a page of ours.
	w.Header().Set("Content-Type", file.ContentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox")
	_, err = io.Copy(w, file.Response.Data)
	return err
}

func render(w http.ResponseWriter, r *http.Request, c templ.Component) error {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	return c.Render(r.Context(), w)
}

func handle(f handlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		err := f(w, r)
		if err == nil {
			return
		}
		if se, ok := errors.AsType[*statusError](err); ok {
			http.Error(w, se.msg, se.status)
			return
		}
		slog.Error("repository browser request failed", "route", chi.RouteContext(r.Context()).RoutePattern(), "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

func basicAuth(user, password string) func(http.Handler) http.Handler {
	wantUser, wantPass := sha256.Sum256([]byte(user)), sha256.Sum256([]byte(password))
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			u, p, ok := r.BasicAuth()
			gotUser, gotPass := sha256.Sum256([]byte(u)), sha256.Sum256([]byte(p))
			userOK := subtle.ConstantTimeCompare(gotUser[:], wantUser[:])
			passOK := subtle.ConstantTimeCompare(gotPass[:], wantPass[:])
			if !ok || userOK&passOK != 1 {
				w.Header().Set("WWW-Authenticate", `Basic realm="UNICHE Repository", charset="UTF-8"`)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r.WithContext(views.WithUser(r.Context(), u)))
		})
	}
}

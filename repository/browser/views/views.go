// Package views holds the templ pages of the Repository browser.
package views

import (
	"context"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/a-h/templ"

	"uniche/api/repositoryapi"
)

// FirstPage stands for the empty cursor of page one inside the back trail.
const FirstPage = "."

// BasePath is where cmd/server mounts the browser. Links in the pages are
// built from it.
const BasePath = "/repo"

// Nav names the rail item a page lights up.
type Nav string

const (
	NavAssets Nav = "assets"
	NavSearch Nav = "search"
)

type userKey struct{}

// WithUser returns ctx carrying the signed-in user's name, for the top bar.
func WithUser(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, userKey{}, name)
}

// UserFrom returns the name stored by WithUser, or "" when there is none.
func UserFrom(ctx context.Context) string {
	name, _ := ctx.Value(userKey{}).(string)
	return name
}

// CardData is what one card of the asset grid shows.
type CardData struct {
	Href     string // title link; empty leaves the title plain
	External bool   // Href leaves the Repository, so open it in a new tab
	Title    string
	Type     string // asset type vocabulary; may be empty
	Thumb    string // thumbnail URL; may be empty
}

func (c CardData) linkAttrs() templ.Attributes {
	if c.External {
		return templ.Attributes{"target": "_blank", "rel": "noreferrer"}
	}
	return nil
}

// PageNav is what the list page needs to render its filters and paging links.
type PageNav struct {
	Source  string
	Type    string
	NextURL string
	PrevURL string
}

// DefaultSectionSize is how many results a search section shows at first, and
// how many more "Show more" adds.
const DefaultSectionSize = 10

// SectionRef names one section of the search page.
type SectionRef struct {
	Key   string // URL segment: "local", "europeana", "eccch"
	Label string
}

// ID is the section's element id, so a fragment can replace its placeholder.
func (s SectionRef) ID() string { return "section-" + s.Key }

// RemoteHit is a record found at a cloud source. Provider and Identifier match
// the external ID the Repository stores for an imported record.
type RemoteHit struct {
	Provider   string
	Identifier string
	ImportURI  string // empty if the hit cannot be imported
	Title      string
	Type       string
	Thumbnail  string
}

// Key identifies the hit among external IDs, never by title.
func (h RemoteHit) Key() string { return ExternalKey(h.Provider, h.Identifier) }

// card is the hit as a grid card: linked to its asset once imported, else to
// the record at its source when that has a public page.
func (h RemoteHit) card(imported map[string]string) CardData {
	c := CardData{Title: h.Title, Type: h.Type, Thumb: h.Thumbnail}
	if id, ok := imported[h.Key()]; ok {
		c.Href = assetURL(id)
	} else if link := externalLink(repositoryapi.AssetExternalIdsItem{Provider: h.Provider, Identifier: h.Identifier}); link != "" {
		c.Href, c.External = link, true
	}
	return c
}

// ExternalKey joins a provider and an identifier into one map key.
func ExternalKey(provider, identifier string) string { return provider + "\x00" + identifier }

// SectionURL is the fragment endpoint of one section of a search.
func SectionURL(key, q string, limit int) string {
	v := url.Values{"q": {q}, "limit": {strconv.Itoa(limit)}}
	return BasePath + "/search/" + url.PathEscape(key) + "?" + v.Encode()
}

// listURL builds a list page link. back is the trail of cursors of the pages
// already visited, so "Previous" can return to them.
func ListURL(source, typ, cursor string, back []string) string {
	q := url.Values{}
	if source != "" {
		q.Set("source", source)
	}
	if typ != "" {
		q.Set("type", typ)
	}
	if cursor != "" && cursor != FirstPage {
		q.Set("cursor", cursor)
	}
	if len(back) > 0 {
		q.Set("back", strings.Join(back, ","))
	}
	if len(q) == 0 {
		return BasePath + "/"
	}
	return BasePath + "/?" + q.Encode()
}

// AssetURL is the detail page of an asset.
func AssetURL(id string) string { return assetURL(id) }

func assetURL(id string) string { return BasePath + "/assets/" + url.PathEscape(id) }

func fileURL(id string) string { return BasePath + "/files/" + url.PathEscape(id) }

func title(a repositoryapi.Asset) string {
	if t := a.CurrentVersion.Value.Title.Value; t != "" {
		return t
	}
	return a.ID
}

func assetType(a repositoryapi.Asset) string { return string(a.CurrentVersion.Value.Type.Value) }

// thumbURL is the URL of the asset's preview, or "" when there is none.
func thumbURL(a repositoryapi.Asset) string {
	if id := thumbnailID(a); id != "" {
		return fileURL(id)
	}
	return ""
}

// thumbnailID returns the file to show as the asset's preview: the first
// ready image file. Empty when there is none.
func thumbnailID(a repositoryapi.Asset) string {
	for _, f := range a.CurrentVersion.Value.Files {
		if f.Status == repositoryapi.FileStatusAvailable && strings.HasPrefix(f.MediaType.Value, "image/") {
			return f.ID
		}
	}
	return ""
}

// externalLink returns the public page of an external record, or "" when
// there is none. Europeana identifiers are record paths ("/90402/SK_A_2344");
// ECCCH identifiers are already URIs.
func externalLink(x repositoryapi.AssetExternalIdsItem) string {
	switch {
	case x.Provider == "europeana" && strings.HasPrefix(x.Identifier, "/"):
		return "https://www.europeana.eu/item" + x.Identifier
	case strings.HasPrefix(x.Identifier, "https://"), strings.HasPrefix(x.Identifier, "http://"):
		return x.Identifier
	}
	return ""
}

// originalLink is the public page of the record the asset was imported from:
// the external ID of the asset's own source, when it maps to a web page.
func originalLink(a *repositoryapi.Asset) string {
	for _, x := range a.ExternalIds {
		if x.Provider == string(a.Source) {
			return externalLink(x)
		}
	}
	return ""
}

// sourceLabel is the name shown for a source value.
func sourceLabel(s string) string {
	switch s {
	case "local":
		return "Uploaded"
	case "partner":
		return "Partner"
	case "eccch":
		return "ECCCH"
	case "europeana":
		return "Europeana"
	}
	return s
}

// typeLabel is the name shown for an asset type; empty for an unknown type.
func typeLabel(t string) string {
	switch t {
	case "":
		return ""
	case "3d", "360":
		return strings.ToUpper(t)
	}
	return strings.ToUpper(t[:1]) + t[1:]
}

// typeIcon returns the SVG path of the icon for an asset type, drawn on a
// 24x24 outline grid. Unknown types get the generic photo icon.
func typeIcon(t string) string {
	switch t {
	case "3d":
		return "m21 7.5-9-5.25L3 7.5m18 0-9 5.25m9-5.25v9l-9 5.25M3 7.5l9 5.25M3 7.5v9l9 5.25m0-9v9"
	case "audio":
		return "m9 9 10.5-3m0 6.553v3.75a2.25 2.25 0 0 1-1.632 2.163l-1.32.377a1.803 1.803 0 1 1-.99-3.467l2.31-.66a2.25 2.25 0 0 0 1.632-2.163Zm0 0V2.25L9 5.25v10.303m0 0v3.75a2.25 2.25 0 0 1-1.632 2.163l-1.32.377a1.803 1.803 0 0 1-.99-3.467l2.31-.66A2.25 2.25 0 0 0 9 15.553Z"
	case "document":
		return "M19.5 14.25v-2.625a3.375 3.375 0 0 0-3.375-3.375h-1.5A1.125 1.125 0 0 1 13.5 7.125v-1.5a3.375 3.375 0 0 0-3.375-3.375H8.25m0 12.75h7.5m-7.5 3H12M10.5 2.25H5.625c-.621 0-1.125.504-1.125 1.125v17.25c0 .621.504 1.125 1.125 1.125h12.75c.621 0 1.125-.504 1.125-1.125V11.25a9 9 0 0 0-9-9Z"
	case "video":
		return "m15.75 10.5 4.72-4.72a.75.75 0 0 1 1.28.53v11.38a.75.75 0 0 1-1.28.53l-4.72-4.72M4.5 18.75h9a2.25 2.25 0 0 0 2.25-2.25v-9a2.25 2.25 0 0 0-2.25-2.25h-9A2.25 2.25 0 0 0 2.25 7.5v9a2.25 2.25 0 0 0 2.25 2.25Z"
	case "360":
		return "M12 21a9.004 9.004 0 0 0 8.716-6.747M12 21a9.004 9.004 0 0 1-8.716-6.747M12 21c2.485 0 4.5-4.03 4.5-9S14.485 3 12 3m0 18c-2.485 0-4.5-4.03-4.5-9S9.515 3 12 3m0 0a8.997 8.997 0 0 1 7.843 4.582M12 3a8.997 8.997 0 0 0-7.843 4.582m15.686 0A11.953 11.953 0 0 1 12 10.5c-2.998 0-5.74-1.1-7.843-2.918m15.686 0A8.959 8.959 0 0 1 21 12c0 .778-.099 1.533-.284 2.253m0 0A17.919 17.919 0 0 1 12 16.5c-3.162 0-6.133-.815-8.716-2.247m0 0A9.015 9.015 0 0 1 3 12c0-1.605.42-3.113 1.157-4.418"
	}
	return "M2.25 15.75l5.159-5.159a2.25 2.25 0 0 1 3.182 0l5.159 5.159m-1.5-1.5 1.409-1.409a2.25 2.25 0 0 1 3.182 0l2.909 2.909M3.75 21h16.5A2.25 2.25 0 0 0 22.5 18.75V5.25A2.25 2.25 0 0 0 20.25 3H3.75A2.25 2.25 0 0 0 1.5 5.25v13.5A2.25 2.25 0 0 0 3.75 21Z"
}

// initial is the first letter of a name, upper-cased, for the avatar.
func initial(name string) string {
	r, _ := utf8.DecodeRuneInString(name)
	if r == utf8.RuneError {
		return ""
	}
	return strings.ToUpper(string(r))
}

// filterLabel is the option text of one value of the named filter.
func filterLabel(filter, value string) string {
	if filter == "source" {
		return sourceLabel(value)
	}
	return typeLabel(value)
}

func sourceOptions() []string {
	return []string{"eccch", "europeana", "local", "partner"}
}

func typeOptions() []string {
	return []string{"3d", "360", "audio", "document", "image", "video"}
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return strconv.FormatInt(n, 10) + " B"
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return strconv.FormatFloat(float64(n)/float64(div), 'f', 1, 64) + " " + string("KMGT"[exp]) + "iB"
}

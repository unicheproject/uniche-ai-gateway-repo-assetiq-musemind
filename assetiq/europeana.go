package assetiq

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"uniche/api/repositoryapi"
	"uniche/connectors/europeana"
)

const europeanaItemPrefix = "assetiq://europeana/item/"

var europeanaTypes = map[string]repositoryapi.AssetType{
	"IMAGE": repositoryapi.AssetTypeImage,
	"3D":    repositoryapi.AssetType3d,
	"VIDEO": repositoryapi.AssetTypeVideo,
	"SOUND": repositoryapi.AssetTypeAudio,
	"TEXT":  repositoryapi.AssetTypeDocument,
}

// parseEuropeanaURI turns assetiq://europeana/item/{dataset}/{local} into the
// Europeana record ID "/{dataset}/{local}".
func parseEuropeanaURI(uri string) (string, error) {
	rest, ok := strings.CutPrefix(uri, europeanaItemPrefix)
	dataset, local, found := strings.Cut(rest, "/")
	if !ok || !found || !handlePart.MatchString(dataset) || !handlePart.MatchString(local) {
		return "", fmt.Errorf("%w: want %s{dataset}/{local}, got %q", ErrInvalidURI, europeanaItemPrefix, uri)
	}
	return "/" + dataset + "/" + local, nil
}

func mapEuropeana(rec *europeana.Record) (*source, error) {
	obj := &rec.Object

	typ, ok := europeanaTypes[obj.Type]
	if !ok {
		return nil, fmt.Errorf("unsupported type %q", obj.Type)
	}
	provider := providerProxy(obj)
	if provider == nil || len(obj.Aggregations) == 0 {
		return nil, errors.New("record has no provider proxy or aggregation")
	}
	agg := &obj.Aggregations[0]

	title := firstValue(provider.DcTitle)
	if title == "" {
		return nil, errors.New("record has no title")
	}
	creator := firstValue(provider.DcCreator)
	if creator == "" && len(obj.Agents) > 0 {
		creator = firstValue(obj.Agents[0].PrefLabel)
	}
	rights := firstValue(agg.EdmRights)

	src := &source{
		ingest: &repositoryapi.IngestRequest{
			Source:     repositoryapi.IngestRequestSourceEuropeana,
			ExternalId: repositoryapi.IngestRequestExternalId{Provider: "europeana", Identifier: obj.About},
			ReadOnly:   repositoryapi.NewOptBool(!openRights(rights)),
			Metadata: repositoryapi.AssetMetadata{
				Title:   title,
				Type:    typ,
				Creator: optString(creator),
				Date:    optString(cmp.Or(firstValue(provider.DcDate), firstValue(provider.DctermsCreated))),
				Rights:  optString(rights),
			},
			RawSource: optString(string(rec.JSONLD)),
		},
		fileURL: agg.EdmIsShownBy,
	}
	for _, w := range agg.WebResources {
		if w.About == agg.EdmIsShownBy {
			src.fileMediaType = w.MimeType
		}
	}
	return src, nil
}

func optString(s string) repositoryapi.OptString {
	if s == "" {
		return repositoryapi.OptString{}
	}
	return repositoryapi.NewOptString(s)
}

// providerProxy is the proxy holding the data provider's own metadata, as
// opposed to Europeana's enrichments.
func providerProxy(obj *europeana.Object) *europeana.Proxy {
	for i := range obj.Proxies {
		if !obj.Proxies[i].EuropeanaProxy {
			return &obj.Proxies[i]
		}
	}
	return nil
}

// firstValue prefers English, then language-less ("def"), then the first
// language in key order.
func firstValue(m europeana.LangMap) string {
	for _, lang := range []string{"en", "def"} {
		if v := m[lang]; len(v) > 0 {
			return v[0]
		}
	}
	for _, lang := range slices.Sorted(maps.Keys(m)) {
		if v := m[lang]; len(v) > 0 {
			return v[0]
		}
	}
	return ""
}

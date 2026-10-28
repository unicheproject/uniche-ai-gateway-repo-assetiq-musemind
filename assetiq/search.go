package assetiq

import (
	"context"
	"encoding/json"
)

// SearchResource is registered as a static resource so assetiq://search is
// discoverable via resources/list. SearchTemplate (below) handles actual
// parameterized reads. See docs/ai-tools-modeling.md 2.1.
type SearchResource struct{}

func (r *SearchResource) URI() string  { return "assetiq://search" }
func (r *SearchResource) Name() string { return "search" }
func (r *SearchResource) Description() string {
	return "Semantic retrieval of 3D, 360°, and multimodal assets from the UNICHE repository"
}
func (r *SearchResource) MIMEType() string { return "application/json" }

func (r *SearchResource) Fetch(_ context.Context) (string, error) {
	return `{"results": [], "message": "[assetiq] search: stub response"}`, nil
}

// SearchTemplate exposes assetiq://search as a parameterized MCP resource
// template (query, types, audience, language, collectionScope, expandIntent).
type SearchTemplate struct{}

func (t *SearchTemplate) URITemplate() string {
	return "assetiq://search{?query,types,audience,language,collectionScope,expandIntent}"
}
func (t *SearchTemplate) Name() string        { return "search" }
func (t *SearchTemplate) Description() string { return (&SearchResource{}).Description() }
func (t *SearchTemplate) MIMEType() string    { return "application/json" }

func (t *SearchTemplate) Fetch(_ context.Context, args map[string]any) (string, error) {
	query, _ := args["query"].(string)

	resp := map[string]any{
		"results": []any{},
		"message": "[assetiq] search: stub response",
	}
	if expandIntent, _ := args["expandIntent"].(string); expandIntent == "true" {
		resp["expandedQuery"] = query
	}

	out, err := json.Marshal(resp)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

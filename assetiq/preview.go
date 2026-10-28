package assetiq

import (
	"context"
	"encoding/json"
)

// PreviewResource is registered as a static resource so assetiq://preview
// is discoverable via resources/list. PreviewTemplate (below) handles
// actual parameterized reads. See docs/ai-tools-modeling.md 2.3.
type PreviewResource struct{}

func (r *PreviewResource) URI() string  { return "assetiq://preview" }
func (r *PreviewResource) Name() string { return "preview" }
func (r *PreviewResource) Description() string {
	return "Low-resolution URI resources for authoring canvas previews"
}
func (r *PreviewResource) MIMEType() string { return "application/json" }

func (r *PreviewResource) Fetch(_ context.Context) (string, error) {
	return `{"uri": "", "message": "[assetiq] preview: stub response"}`, nil
}

// PreviewTemplate exposes assetiq://preview as a parameterized MCP resource
// template - resolves an asset uri returned by assetiq://search to a
// low-resolution preview.
type PreviewTemplate struct{}

func (t *PreviewTemplate) URITemplate() string { return "assetiq://preview{?uri}" }
func (t *PreviewTemplate) Name() string        { return "preview" }
func (t *PreviewTemplate) Description() string { return (&PreviewResource{}).Description() }
func (t *PreviewTemplate) MIMEType() string    { return "application/json" }

func (t *PreviewTemplate) Fetch(_ context.Context, args map[string]any) (string, error) {
	uri, _ := args["uri"].(string)

	out, err := json.Marshal(map[string]any{
		"uri":       uri,
		"lowResUrl": "",
		"mimeType":  "",
	})
	if err != nil {
		return "", err
	}
	return string(out), nil
}

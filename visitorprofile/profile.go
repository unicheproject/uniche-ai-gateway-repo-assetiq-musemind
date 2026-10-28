// Package visitorprofile exposes read-only, consent-gated visitor context
// to ICAgent for personalization. See docs/ai-tools-modeling.md 2.9.
//
// The visitorprofile:// URI scheme is a placeholder pending the ownership
// question in docs/ai-tools-modeling.md section 7 - it may be renamed to
// whichever service's scheme owns visitor profile data (e.g. vibe://).
package visitorprofile

import (
	"context"
	"encoding/json"
)

// Template exposes visitorprofile://{visitorId} as a parameterized MCP
// resource template. Callers must NOT import the mcp package - this type
// satisfies uniche/mcp.ResourceTemplate structurally.
type Template struct{}

func (t *Template) URITemplate() string { return "visitorprofile://{visitorId}" }
func (t *Template) Name() string        { return "visitor-profile" }
func (t *Template) Description() string {
	return "Read-only, consent-gated visitor context (language, accessibility needs, interests, history)"
}
func (t *Template) MIMEType() string { return "application/json" }

func (t *Template) Fetch(_ context.Context, args map[string]any) (string, error) {
	visitorID, _ := args["visitorId"].(string)

	// Degrade gracefully to an empty, non-personalized profile - this is
	// the stub's permanent behavior until a real consent store exists.
	out, err := json.Marshal(map[string]any{
		"visitorId":                visitorID,
		"language":                 "",
		"accessibilityPreferences": map[string]any{},
		"interests":                []any{},
		"visitedAssets":            []any{},
		"consentLevel":             "none",
	})
	if err != nil {
		return "", err
	}
	return string(out), nil
}

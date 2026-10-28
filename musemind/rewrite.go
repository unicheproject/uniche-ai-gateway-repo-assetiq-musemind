package musemind

import (
	"context"
	"encoding/json"
	"time"
)

// RewriteTool adapts existing HTML content for a different audience,
// language, or institutional tone. See docs/ai-tools-modeling.md 2.5.
type RewriteTool struct{}

func (t *RewriteTool) Name() string { return "musemind.rewrite" }
func (t *RewriteTool) Description() string {
	return "Adapt content for specific audiences (children, experts, general public)"
}

func (t *RewriteTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"content":      { "type": "string", "description": "HTML content to rewrite" },
			"audience":     { "type": "string", "enum": ["children", "experts", "general"] },
			"language":     { "type": "string", "description": "BCP-47 language tag" },
			"brandProfile": { "type": "object", "description": "Optional institutional tone/style guidelines" },
			"format":       { "type": "object", "description": "Optional JSON Schema describing the desired output shape" }
		}
	}`)
}

func (t *RewriteTool) Handle(_ context.Context, params map[string]any) (string, error) {
	audience, _ := params["audience"].(string)
	language, _ := params["language"].(string)

	out, err := json.Marshal(map[string]any{
		"text":     "[musemind] rewrite: stub response",
		"audience": audience,
		"language": language,
		"aiTag":    stubAITag(),
	})
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func stubAITag() map[string]any {
	return map[string]any{
		"generated": true,
		"model":     "musemind-stub",
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	}
}

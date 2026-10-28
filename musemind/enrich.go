package musemind

import (
	"context"
	"encoding/json"
)

// EnrichTool suggests metadata improvements for a repository asset, for a
// curator to review and accept or reject. See docs/ai-tools-modeling.md 2.7.
type EnrichTool struct{}

func (t *EnrichTool) Name() string { return "musemind.enrich" }
func (t *EnrichTool) Description() string {
	return "Suggest metadata improvements (tags, descriptions, fields) for a repository asset"
}

func (t *EnrichTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"assetUri":        { "type": "string" },
			"existingMetadata": { "type": "object", "description": "Current repository record fields" }
		}
	}`)
}

func (t *EnrichTool) Handle(_ context.Context, _ map[string]any) (string, error) {
	out, err := json.Marshal(map[string]any{
		"suggestions": map[string]any{
			"tags":        []any{},
			"description": "[musemind] enrich: stub response",
			"fields":      map[string]any{},
		},
		"confidence":     0.0,
		"aiTag":          stubAITag(),
		"requiresReview": true,
	})
	if err != nil {
		return "", err
	}
	return string(out), nil
}

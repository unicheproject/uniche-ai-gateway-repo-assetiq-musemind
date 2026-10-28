package musemind

import (
	"context"
	"encoding/json"
)

// BrainstormTool generates a draft narrative/experience structure - a graph
// of scenes and the links between them - from a topic and asset context.
// See docs/ai-tools-modeling.md 2.4.
type BrainstormTool struct{}

func (t *BrainstormTool) Name() string { return "musemind.brainstorm" }
func (t *BrainstormTool) Description() string {
	return "Generate creativity-driven suggestions for exhibition themes and narratives"
}

func (t *BrainstormTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"scene":     { "type": "string", "description": "Topic or scene to brainstorm around" },
			"audiences": { "type": "array", "items": { "type": "string" } },
			"assetContext": {
				"type": "array",
				"description": "Results from assetiq://search, used as grounding",
				"items": {
					"type": "object",
					"properties": {
						"uri":       { "type": "string" },
						"versionId": { "type": "string" },
						"title":     { "type": "string" }
					}
				}
			},
			"existingStructure": { "type": "object", "description": "Prior scenes, for continuation" },
			"format":            { "type": "object", "description": "Optional JSON Schema describing the desired output shape" }
		}
	}`)
}

func (t *BrainstormTool) Handle(_ context.Context, params map[string]any) (string, error) {
	title, _ := params["scene"].(string)
	if title == "" {
		title = "[musemind] brainstorm: stub response"
	}

	out, err := json.Marshal(map[string]any{
		"title":       title,
		"scenes":      []any{},
		"citedAssets": []any{},
		"aiTag":       stubAITag(),
	})
	if err != nil {
		return "", err
	}
	return string(out), nil
}

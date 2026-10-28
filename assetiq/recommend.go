package assetiq

import (
	"context"
	"encoding/json"
)

// RecommendTool suggests repository assets related to a scene being
// authored or to a visitor's profile/history, with a mandatory explanation
// of why each item was suggested. See docs/ai-tools-modeling.md 2.2.
type RecommendTool struct{}

func (t *RecommendTool) Name() string { return "assetiq.recommend" }
func (t *RecommendTool) Description() string {
	return "Suggest repository assets related to a scene or a visitor's profile/history, with an explanation"
}

func (t *RecommendTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"context": {
				"type": "object",
				"description": "{ storyId, sceneId } for authoring, or { visitorProfileId } for experiencing",
				"properties": {
					"storyId":          { "type": "string" },
					"sceneId":          { "type": "string" },
					"visitorProfileId": { "type": "string" }
				}
			},
			"basis":    { "type": "string", "enum": ["similarity", "theme", "visitor-history"] },
			"limit":    { "type": "integer" },
			"audience": { "type": "string" }
		}
	}`)
}

func (t *RecommendTool) Handle(_ context.Context, _ map[string]any) (string, error) {
	out, err := json.Marshal(map[string]any{
		"recommendations": []any{},
	})
	if err != nil {
		return "", err
	}
	return string(out), nil
}

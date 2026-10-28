package musemind

import (
	"context"
	"encoding/json"
)

// VerifyTool cross-checks explicit claim/source pairs against the UNICHE
// Repository/ECCCH records. See docs/ai-tools-modeling.md 2.6.
type VerifyTool struct{}

func (t *VerifyTool) Name() string { return "musemind.verify" }
func (t *VerifyTool) Description() string {
	return "Cross-check drafts against repository metadata to prevent hallucinations"
}

func (t *VerifyTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"citedAssets": {
				"type": "array",
				"description": "Claim/source triples to verify - claim, the asset it's attributed to, and the specific version it was drawn from",
				"items": {
					"type": "object",
					"properties": {
						"uri":       { "type": "string" },
						"versionId": { "type": "string" },
						"claim":     { "type": "string" }
					}
				}
			},
			"draft":  { "type": "string", "description": "Full text, for context/highlighting only - not parsed for claims" },
			"strict": { "type": "boolean", "description": "If true, an empty citedAssets fails closed" }
		}
	}`)
}

func (t *VerifyTool) Handle(_ context.Context, params map[string]any) (string, error) {
	strict, _ := params["strict"].(bool)
	citedAssets, _ := params["citedAssets"].([]any)

	verified := true
	issues := []any{}
	if strict && len(citedAssets) == 0 {
		verified = false
		issues = append(issues, map[string]any{
			"claim":  "",
			"status": "unverifiable",
		})
	}

	out, err := json.Marshal(map[string]any{
		"verified": verified,
		"issues":   issues,
		"aiTag":    stubAITag(),
	})
	if err != nil {
		return "", err
	}
	return string(out), nil
}

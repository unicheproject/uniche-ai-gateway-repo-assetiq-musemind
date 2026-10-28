package musemind

import (
	"context"
	"encoding/json"
)

// SummarizeFeedbackTool clusters and summarizes a batch of visitor feedback
// entries into themes with sentiment and sample quotes, for curator
// dashboards. See docs/ai-tools-modeling.md 2.8.
type SummarizeFeedbackTool struct{}

func (t *SummarizeFeedbackTool) Name() string { return "musemind.summarize_feedback" }
func (t *SummarizeFeedbackTool) Description() string {
	return "Cluster and summarize visitor feedback entries into themes with sentiment and sample quotes"
}

func (t *SummarizeFeedbackTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"feedbackEntries": {
				"type": "array",
				"description": "Pulled from UNICHE.VIBE by the caller",
				"items": {
					"type": "object",
					"properties": {
						"text":      { "type": "string" },
						"rating":    { "type": "number" },
						"locale":    { "type": "string" },
						"timestamp": { "type": "string" },
						"context":   { "type": "object" }
					}
				}
			},
			"groupBy": { "type": "string", "description": "Optional - by exhibit, by story, by time window" }
		}
	}`)
}

func (t *SummarizeFeedbackTool) Handle(_ context.Context, _ map[string]any) (string, error) {
	out, err := json.Marshal(map[string]any{
		"clusters": []any{},
		"aiTag":    stubAITag(),
	})
	if err != nil {
		return "", err
	}
	return string(out), nil
}

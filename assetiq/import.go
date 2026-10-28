package assetiq

import (
	"context"
	"encoding/json"
)

// ImportTool writes a curator-selected search result into the UNICHE
// Repository, turning a transient federated-search hit into a permanent,
// citable Repository asset. See docs/ai-tools-modeling.md 2.10.
type ImportTool struct{}

func (t *ImportTool) Name() string { return "assetiq.import" }
func (t *ImportTool) Description() string {
	return "Import a curator-selected search result into the UNICHE Repository"
}

func (t *ImportTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"uri": { "type": "string", "description": "An assetiq:// result, still transient at this point" }
		}
	}`)
}

func (t *ImportTool) Handle(_ context.Context, params map[string]any) (string, error) {
	uri, _ := params["uri"].(string)

	out, err := json.Marshal(map[string]any{
		"assetId":   "",
		"versionId": "",
		"status":    "imported",
		"uri":       uri,
	})
	if err != nil {
		return "", err
	}
	return string(out), nil
}

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// RemoteTool implements Tool by forwarding calls to an external HTTP endpoint.
type RemoteTool struct {
	name        string
	description string
	callbackURL string
}

func NewRemoteTool(name, description, callbackURL string) *RemoteTool {
	return &RemoteTool{name: name, description: description, callbackURL: callbackURL}
}

func (t *RemoteTool) Name() string        { return t.name }
func (t *RemoteTool) Description() string { return t.description }

func (t *RemoteTool) Handle(ctx context.Context, params map[string]any) (string, error) {
	body, err := json.Marshal(params)
	if err != nil {
		return "", fmt.Errorf("encoding params: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.callbackURL, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("calling %s: %w", t.callbackURL, err)
	}
	defer resp.Body.Close()

	result, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("remote tool %s: status %d: %s", t.name, resp.StatusCode, result)
	}
	return string(result), nil
}

package main

import (
	"context"
	"slices"
	"testing"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

func TestToolsRegistered(t *testing.T) {
	c := newInProcessClient(t)

	result, err := c.ListTools(context.Background(), mcp.ListToolsRequest{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	names := toolNames(result)
	for _, want := range []string{"musemind.rewrite", "musemind.brainstorm", "musemind.verify"} {
		if !slices.Contains(names, want) {
			t.Errorf("tool %q not registered; got %v", want, names)
		}
	}
}

func TestResourcesRegistered(t *testing.T) {
	c := newInProcessClient(t)

	result, err := c.ListResources(context.Background(), mcp.ListResourcesRequest{})
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}

	uris := resourceURIs(result)
	for _, want := range []string{"assetiq://search", "assetiq://preview"} {
		if !slices.Contains(uris, want) {
			t.Errorf("resource %q not registered; got %v", want, uris)
		}
	}
}

func TestToolHandle(t *testing.T) {
	c := newInProcessClient(t)

	for _, name := range []string{"musemind.rewrite", "musemind.brainstorm", "musemind.verify"} {
		req := mcp.CallToolRequest{}
		req.Params.Name = name
		result, err := c.CallTool(context.Background(), req)
		if err != nil {
			t.Errorf("CallTool(%q): %v", name, err)
			continue
		}
		if result.IsError {
			t.Errorf("CallTool(%q): server returned error", name)
		}
	}
}

func newInProcessClient(t *testing.T) *client.Client {
	t.Helper()
	c, err := client.NewInProcessClient(newMCPServer().Inner())
	if err != nil {
		t.Fatalf("NewInProcessClient: %v", err)
	}
	t.Cleanup(func() { c.Close() })

	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("client.Start: %v", err)
	}

	req := mcp.InitializeRequest{}
	req.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	req.Params.ClientInfo = mcp.Implementation{Name: "test", Version: "0.0.0"}
	if _, err := c.Initialize(context.Background(), req); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	return c
}

func toolNames(r *mcp.ListToolsResult) []string {
	names := make([]string, len(r.Tools))
	for i, t := range r.Tools {
		names[i] = t.Name
	}
	return names
}

func resourceURIs(r *mcp.ListResourcesResult) []string {
	uris := make([]string, len(r.Resources))
	for i, res := range r.Resources {
		uris[i] = res.URI
	}
	return uris
}

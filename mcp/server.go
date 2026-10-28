package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"uniche/config"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Tool is satisfied by any type that wants to register an MCP tool.
// Implementors (musemind, assetiq) must NOT import this package.
type Tool interface {
	Name() string
	Description() string
	Handle(ctx context.Context, params map[string]any) (string, error)
}

// SchemaProvider is an optional extension of Tool for types that can
// describe their input shape as a JSON Schema (2020-12, object/array/
// string/number/boolean/enum subset - no $ref/$defs, see
// docs/ai-tools-modeling.md section 3). RemoteTool (partner-registered
// callback tools) intentionally does not implement this - their schema
// isn't known at registration time.
type SchemaProvider interface {
	InputSchema() json.RawMessage
}

// Resource is satisfied by any type that wants to register a fixed-URI
// MCP resource (no parameters). Implementors (musemind, assetiq) must NOT
// import this package.
type Resource interface {
	URI() string
	Name() string
	Description() string
	MIMEType() string
	Fetch(ctx context.Context) (string, error)
}

// ResourceTemplate is satisfied by resources that accept parameters via an
// RFC 6570 URI template, e.g. "assetiq://search{?query,types,...}". Reads
// against a URI matching the template arrive with the parsed variables in
// args. Implementors (musemind, assetiq) must NOT import this package.
type ResourceTemplate interface {
	URITemplate() string
	Name() string
	Description() string
	MIMEType() string
	Fetch(ctx context.Context, args map[string]any) (string, error)
}

type Server struct {
	inner *server.MCPServer
}

// Inner returns the underlying mcp-go server, needed for in-process test clients.
func (s *Server) Inner() *server.MCPServer { return s.inner }

func NewServer(name, version string) *Server {
	s := server.NewMCPServer(name, version,
		server.WithToolCapabilities(true),
		server.WithResourceCapabilities(true, false),
	)
	return &Server{inner: s}
}

func (s *Server) RegisterTool(t Tool) {
	// NewToolWithRawSchema and NewTool+ToolOptions are mutually exclusive
	// (a Tool can't carry both InputSchema and RawInputSchema), so branch
	// on whether the tool supplies a schema at all.
	var tool mcplib.Tool
	if sp, ok := t.(SchemaProvider); ok {
		tool = mcplib.NewToolWithRawSchema(t.Name(), t.Description(), sp.InputSchema())
	} else {
		tool = mcplib.NewTool(t.Name(), mcplib.WithDescription(t.Description()))
	}
	s.inner.AddTool(
		tool,
		func(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
			result, err := t.Handle(ctx, req.GetArguments())
			if err != nil {
				return nil, fmt.Errorf("%s: %w", t.Name(), err)
			}
			return mcplib.NewToolResultText(result), nil
		},
	)
	slog.Info("registered tool", "name", t.Name())
}

func (s *Server) RegisterResource(r Resource) {
	s.inner.AddResource(
		mcplib.NewResource(r.URI(), r.Name(),
			mcplib.WithResourceDescription(r.Description()),
			mcplib.WithMIMEType(r.MIMEType()),
		),
		func(ctx context.Context, req mcplib.ReadResourceRequest) ([]mcplib.ResourceContents, error) {
			content, err := r.Fetch(ctx)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", r.URI(), err)
			}
			return []mcplib.ResourceContents{
				mcplib.TextResourceContents{
					URI:      r.URI(),
					MIMEType: r.MIMEType(),
					Text:     content,
				},
			}, nil
		},
	)
	slog.Info("registered resource", "uri", r.URI())
}

func (s *Server) RegisterResourceTemplate(rt ResourceTemplate) {
	s.inner.AddResourceTemplate(
		mcplib.NewResourceTemplate(rt.URITemplate(), rt.Name(),
			mcplib.WithTemplateDescription(rt.Description()),
			mcplib.WithTemplateMIMEType(rt.MIMEType()),
		),
		func(ctx context.Context, req mcplib.ReadResourceRequest) ([]mcplib.ResourceContents, error) {
			content, err := rt.Fetch(ctx, req.Params.Arguments)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", rt.Name(), err)
			}
			return []mcplib.ResourceContents{
				mcplib.TextResourceContents{
					URI:      req.Params.URI,
					MIMEType: rt.MIMEType(),
					Text:     content,
				},
			}, nil
		},
	)
	slog.Info("registered resource template", "template", rt.URITemplate())
}

// Start launches the server using the transport from config.
// "sse" starts an HTTP/SSE server (remote clients, Claude.ai).
// Anything else (default) uses stdio (local clients: Claude Desktop, Cursor).
func (s *Server) Start() error {
	cfg := config.Get()
	switch cfg.Transport {
	case "sse":
		slog.Info("starting MCP SSE server", "addr", cfg.MCPAddr)
		sse := server.NewSSEServer(s.inner, server.WithBaseURL(cfg.MCPBaseURL))
		return sse.Start(cfg.MCPAddr)
	default:
		slog.Info("starting MCP stdio server")
		return server.ServeStdio(s.inner)
	}
}

// Package api holds the OpenAPI specs and the contracts generated from them.
// The HTTP server that implements them lives in web.
package api

import _ "embed"

//go:generate ogen --target gatewayapi --clean --package gatewayapi openapi.yaml
//go:generate ogen --target repositoryapi --clean --package repositoryapi repository.yaml

//go:embed openapi.yaml
var GatewaySpec []byte

//go:embed mcp-transport.yaml
var MCPTransportSpec []byte

//go:embed repository.yaml
var RepositorySpec []byte

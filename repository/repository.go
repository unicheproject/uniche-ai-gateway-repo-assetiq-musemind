// Package repository is the UNICHE Repository service: storage, versioning,
// and identity for heritage assets. See docs/repository.md.
//
// Nothing here is an MCP tool or resource. The contract is plain REST,
// specified in api/repository.yaml and generated into api/repositoryapi.
// Implementations satisfy repositoryapi.Handler, and callers depend on that
// interface only, so swapping in a repositoryapi.Client is the whole
// extraction step.
package repository

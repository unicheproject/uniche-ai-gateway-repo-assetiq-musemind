package web

import (
	"context"

	"uniche/api/gatewayapi"
	"uniche/api/repositoryapi"
)

// HandleBearerAuth implements gatewayapi.SecurityHandler for the gateway spec.
//
// STUB: ogen already rejects requests with no bearer token (401), but the
// token itself is not validated. Real validation - issuer, signature, expiry,
// and the uniche-platform audience, per docs/authentication.md - lands with
// the M24 JWT work.
func (s *Server) HandleBearerAuth(ctx context.Context, _ gatewayapi.OperationName, _ gatewayapi.BearerAuth) (context.Context, error) {
	return ctx, nil
}

// repositorySecurity is the same stub for the Repository spec's generated
// SecurityHandler.
type repositorySecurity struct{}

// HandleBearerAuth is a STUB, see Server.HandleBearerAuth.
func (repositorySecurity) HandleBearerAuth(ctx context.Context, _ repositoryapi.OperationName, _ repositoryapi.BearerAuth) (context.Context, error) {
	return ctx, nil
}

// HandleDeveloperLogin is a STUB, see HandleBearerAuth. It covers tokens obtained
// through Swagger UI's dev login (DevTokenProxy); they are the same Keycloak
// access tokens, sent as a bearer header.
func (s *Server) HandleDeveloperLogin(ctx context.Context, _ gatewayapi.OperationName, _ gatewayapi.DeveloperLogin) (context.Context, error) {
	return ctx, nil
}

// HandleDeveloperLogin is a STUB, see Server.HandleDeveloperLogin.
func (repositorySecurity) HandleDeveloperLogin(ctx context.Context, _ repositoryapi.OperationName, _ repositoryapi.DeveloperLogin) (context.Context, error) {
	return ctx, nil
}

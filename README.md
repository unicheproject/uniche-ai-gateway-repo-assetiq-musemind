# UnicheAIGateway

The UNICHE platform is a no-code collaborative environment for creating interactive cultural heritage experiences. This repository contains **UnicheAIGateway** - an AI integration layer built on the [Model Context Protocol](https://modelcontextprotocol.io) (MCP) that connects authoring tools and visitor-facing agents to AI services and the UNICHE Repository.

All services are currently developed in this one repository and deployed as a single binary - a modulith. Each service lives in its own Go package with strict import boundaries, so any of them can be split out into a separate deployable service later, as the need arises.

## TL;DR quickstart

```bash
cp .env.example .env   # set POSTGRES_USER and POSTGRES_PASSWORD
```

Locally (Go 1.26 + a running PostgreSQL):

```bash
make dev               # -> http://localhost:8080/docs
```

With Docker (server + PostgreSQL):

```bash
CI_REGISTRY_IMAGE=uniche CI_COMMIT_REF_SLUG=local docker compose up --build
                       # -> http://localhost:9819/docs
```

## What it does

Authoring tools (StoryMaker, PlaceMaker) and the visitor-facing ICAgent reach these services over MCP or REST:

- **MuseMind** - generative AI assistant: `rewrite`, `brainstorm`, `verify`, `enrich`, `summarize_feedback`
- **AssetIQ** - asset discovery across Europeana and the ECCCH Knowledge Base: search, preview, recommend, and import of selected records into the Repository
- **Repository** - stores asset records (PostgreSQL) and their files (filesystem), with versions, relations, external IDs, and resumable uploads
- **Visitor profile** - consent-gated `visitorprofile://{visitorId}` resource for ICAgent
- **Tool Registry** - partner services register their own tools at runtime; the gateway forwards MCP calls to their callback URL

## Configuration

See [`.env.example`](.env.example) for every variable and its default. `MCP_TRANSPORT=sse` serves MCP over HTTP; leave it unset for stdio (Claude Desktop, Cursor, etc.).

| Endpoint | Default address |
| :-- | :-- |
| MCP (SSE) | `http://localhost:8081/sse` |
| API docs (Swagger UI) | `http://localhost:8080/docs` |
| Gateway REST API | `http://localhost:8080/api/...` |
| Repository REST API | `http://localhost:8080/api/v1/repository/...` |
| Repository browser | `http://localhost:8080/repo` (only when `BROWSER_USER` and `BROWSER_PASSWORD` are set) |
| Health check | `http://localhost:8080/status` |

Optional integrations:

- **Europeana** - set `EUROPEANA_API_KEY` (defaults to the demo key `api2demo`).
- **ECCCH Knowledge Base** - set `EGI_REFRESH_TOKEN` to an EGI Check-in refresh token; ECCCH search and import stay off without it.

## Authentication

Access tokens are issued by the UNICHE Keycloak realm (`OIDC_ISSUER`, default `https://idp.uniche-eccch.eu/realms/uniche`). Project and organisation authorization is decided by the platform Catalogue.

To try authenticated endpoints from `/docs`, set `DEV_LOGIN_CLIENT_ID=dev-cli` and use Authorize -> `Developer-login` with your Keycloak username and password. The gateway proxies the login to `OIDC_ISSUER`. Dev only - it handles user passwords, so leave it unset in production.

## API

The APIs are spec-first. The OpenAPI specs in `api/` are the contract; [ogen](https://ogen.dev) generates the Go server and client code from them on `make build`.

- `api/openapi.yaml` - gateway: MuseMind, AssetIQ, Tool Registry, visitor profile
- `api/repository.yaml` - Repository
- `api/mcp-transport.yaml` - MCP transport

All three are served at `/docs`, and as raw YAML at `/openapi.yaml`, `/repository.yaml`, and `/mcp-transport.yaml`.

### Registering a tool

```bash
curl -X POST http://localhost:8080/api/tools/register \
  -H 'Content-Type: application/json' \
  -d '{"name":"my-tool","description":"Does X","callback_url":"http://my-service:9000/call"}'
```

Registered tools are stored in SQLite and re-registered automatically on restart.

## Development

```bash
make build   # lint, templ + ogen generation, tests, compile to ./bin/uniche
make run     # run without rebuilding
```

## Stack

Go + [mcp-go](https://github.com/mark3labs/mcp-go) + [ogen](https://ogen.dev) + [chi](https://github.com/go-chi/chi) + [templ](https://templ.guide) + HTMX + PostgreSQL ([pgx](https://github.com/jackc/pgx), Repository) + [SQLite](https://modernc.org/sqlite) (Tool Registry) + [goose](https://github.com/pressly/goose) + [godotenv](https://github.com/joho/godotenv).

package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"sync"
)

type Config struct {
	// Transport selects the MCP transport: "sse" or "stdio" (default).
	Transport string

	// MCPAddr is the listen address for the MCP SSE server.
	MCPAddr string

	// MCPBaseURL is the public base URL advertised to SSE clients.
	MCPBaseURL string

	// DocsAddr is the listen address for the docs + registration API server.
	DocsAddr string

	// DBPath is the SQLite database file path.
	DBPath string

	// OIDCIssuer is the Keycloak realm that issues access tokens.
	OIDCIssuer string

	// DevLoginClientID enables the /docs/token password-grant proxy for
	// Swagger UI when set (e.g. "dev-cli"). Leave empty outside development.
	DevLoginClientID string

	// BrowserUser and BrowserPassword protect the Repository browser at /repo
	// with basic auth. The browser is not mounted unless both are set.
	BrowserUser     string
	BrowserPassword string

	// Postgres connection for the Repository. User and password are required.
	PostgresUser     string
	PostgresPassword string
	PostgresDB       string
	PostgresHost     string
	PostgresPort     string
	PostgresSSLMode  string

	// RepositoryFilesDir is where the Repository keeps uploaded file bytes.
	RepositoryFilesDir string

	// EuropeanaAPIKey authenticates calls to the Europeana Record API.
	EuropeanaAPIKey string

	// ECCCH Knowledge Base (KBMS) and the triplestore HDTs are read from.
	EccchKBURL         string
	EccchTriplestoreID string

	// EGI Check-in refresh token for the KBMS. ECCCH import is off when it
	// is empty. Known shortcoming: it is one person's token, also used on
	// deployed servers because there is no other way yet to test the
	// integration. Every call runs as that person, and a rotated token
	// stops working after a restart. See docs/authentication.md.
	EGITokenURL     string
	EGIClientID     string
	EGIRefreshToken string
}

var (
	once     sync.Once
	instance Config
)

func Get() Config {
	once.Do(func() {
		mcpAddr := ":" + envOr("MCP_PORT", "8081")
		instance = Config{
			Transport:          envOr("MCP_TRANSPORT", "stdio"),
			MCPAddr:            mcpAddr,
			MCPBaseURL:         envOr("MCP_BASE_URL", "http://localhost"+mcpAddr),
			DocsAddr:           ":" + envOr("DOCS_PORT", "8080"),
			DBPath:             envOr("DB_PATH", "uniche.db"),
			OIDCIssuer:         envOr("OIDC_ISSUER", "https://idp.uniche-eccch.eu/realms/uniche"),
			DevLoginClientID:   os.Getenv("DEV_LOGIN_CLIENT_ID"),
			BrowserUser:        os.Getenv("BROWSER_USER"),
			BrowserPassword:    os.Getenv("BROWSER_PASSWORD"),
			PostgresUser:       os.Getenv("POSTGRES_USER"),
			PostgresPassword:   os.Getenv("POSTGRES_PASSWORD"),
			PostgresDB:         envOr("POSTGRES_DB", "uniche"),
			PostgresHost:       envOr("POSTGRES_HOST", "localhost"),
			PostgresPort:       envOr("POSTGRES_PORT", "5432"),
			PostgresSSLMode:    envOr("POSTGRES_SSLMODE", "disable"),
			RepositoryFilesDir: envOr("REPOSITORY_FILES_DIR", "uniche-data/repository-files"),
			EuropeanaAPIKey:    envOr("EUROPEANA_API_KEY", "api2demo"),
			EccchKBURL:         envOr("ECCCH_KB_URL", "https://echoes-kb-api-route-echoes-graphs-production.apps.dcw1.paas.psnc.pl"),
			EccchTriplestoreID: envOr("ECCCH_TRIPLESTORE_ID", "6a2abf6b5d6646ff24522299"),
			EGITokenURL:        envOr("EGI_TOKEN_URL", "https://aai-demo.egi.eu/auth/realms/egi/protocol/openid-connect/token"),
			EGIClientID:        envOr("EGI_CLIENT_ID", "token-portal"),
			EGIRefreshToken:    os.Getenv("EGI_REFRESH_TOKEN"),
		}
	})
	return instance
}

// PostgresURL builds a postgres:// URL from the POSTGRES_* settings.
func (c Config) PostgresURL() (string, error) {
	if c.PostgresUser == "" || c.PostgresPassword == "" {
		return "", errors.New("POSTGRES_USER and POSTGRES_PASSWORD are required")
	}
	return fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=%s",
		url.QueryEscape(c.PostgresUser), url.QueryEscape(c.PostgresPassword),
		c.PostgresHost, c.PostgresPort, url.PathEscape(c.PostgresDB), c.PostgresSSLMode), nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

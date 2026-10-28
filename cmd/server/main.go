package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	_ "github.com/joho/godotenv/autoload"

	"uniche/api/repositoryapi"
	"uniche/assetiq"
	"uniche/config"
	"uniche/connectors/eccch"
	"uniche/connectors/europeana"
	"uniche/database"
	"uniche/mcp"
	"uniche/musemind"
	"uniche/repository"
	"uniche/repository/browser/views"
	browserweb "uniche/repository/browser/web"
	"uniche/visitorprofile"
	"uniche/web"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg := config.Get()

	db, err := database.New(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("init db: %w", err)
	}
	defer db.Close()

	if err := db.Migrate(); err != nil {
		return fmt.Errorf("migrations: %w", err)
	}

	mcpSrv := newMCPServer()

	// Re-register any tools that were persisted from previous runs.
	if err := loadRemoteTools(db, mcpSrv); err != nil {
		return fmt.Errorf("loading remote tools: %w", err)
	}

	repo, closeRepo, err := newRepository(context.Background(), cfg)
	if err != nil {
		return err
	}
	defer closeRepo()
	europeanaClient, eccchClient := europeana.NewClient(cfg.EuropeanaAPIKey), newEccchClient(cfg)
	importer := assetiq.NewImporter(europeanaClient, eccchClient, repo)
	searcher := assetiq.NewSearcher(europeanaClient, eccchClient)

	var opts []web.Option
	if cfg.DevLoginClientID != "" {
		opts = append(opts, web.WithDevLogin(cfg.OIDCIssuer+"/protocol/openid-connect/token", cfg.DevLoginClientID))
		slog.Warn("dev login proxy enabled", "client_id", cfg.DevLoginClientID)
	}

	if cfg.BrowserUser != "" && cfg.BrowserPassword != "" {
		opts = append(opts, web.WithMount(views.BasePath, browserweb.New(repo, cfg.BrowserUser, cfg.BrowserPassword,
			browserweb.WithSource("europeana", "Europeana", browserSearch(searcher.Europeana)),
			browserweb.WithSource("eccch", "ECCCH", browserSearch(searcher.Eccch)),
			browserweb.WithImporter(browserImport(importer)),
		)))
	} else {
		slog.Info("repository browser disabled: set BROWSER_USER and BROWSER_PASSWORD")
	}

	apiSrv, err := web.NewServer(db, mcpSrv, repo, importer, opts...)
	if err != nil {
		return fmt.Errorf("init API server: %w", err)
	}

	go func() {
		slog.Info("starting API server", "addr", cfg.DocsAddr)
		if err := http.ListenAndServe(cfg.DocsAddr, apiSrv); err != nil {
			slog.Error("API server stopped", "err", err)
		}
	}()

	return mcpSrv.Start()
}

// browserSearch adapts an AssetIQ source search to the browser's types, which
// it owns so that it never imports assetiq.
func browserSearch(search func(ctx context.Context, query string, limit int) ([]assetiq.Hit, error)) browserweb.SearchFunc {
	return func(ctx context.Context, query string, limit int) ([]views.RemoteHit, error) {
		found, err := search(ctx, query, limit)
		if errors.Is(err, assetiq.ErrSourceNotConfigured) {
			return nil, browserweb.ErrNotConfigured
		}
		if err != nil {
			return nil, err
		}
		hits := make([]views.RemoteHit, 0, len(found))
		for _, h := range found {
			hits = append(hits, views.RemoteHit(h))
		}
		return hits, nil
	}
}

func browserImport(importer *assetiq.Importer) browserweb.ImportFunc {
	return func(ctx context.Context, uri string) (string, error) {
		res, err := importer.Import(ctx, uri)
		if errors.Is(err, assetiq.ErrSourceNotConfigured) {
			return "", browserweb.ErrNotConfigured
		}
		if err != nil {
			return "", err
		}
		return res.AssetID, nil
	}
}

// newRepository returns the Postgres-backed Repository.
func newRepository(ctx context.Context, cfg config.Config) (repositoryapi.Handler, func(), error) {
	url, err := cfg.PostgresURL()
	if err != nil {
		return nil, nil, err
	}
	db, err := repository.NewDB(ctx, url)
	if err != nil {
		return nil, nil, fmt.Errorf("init repository database: %w", err)
	}
	if err := db.RunMigrations(ctx); err != nil {
		db.Close()
		return nil, nil, fmt.Errorf("repository migrations: %w", err)
	}
	repo, err := repository.NewPostgres(db.Pool, cfg.RepositoryFilesDir, repository.WithBasePath(web.RepositoryPrefix))
	if err != nil {
		db.Close()
		return nil, nil, err
	}
	return repo, db.Close, nil
}

// newEccchClient returns the ECCCH Knowledge Base client, or nil when no EGI
// refresh token is set.
func newEccchClient(cfg config.Config) *eccch.Client {
	if cfg.EGIRefreshToken == "" {
		slog.Info("ECCCH import disabled: set EGI_REFRESH_TOKEN")
		return nil
	}
	tokens := eccch.NewTokenSource(cfg.EGITokenURL, cfg.EGIClientID, cfg.EGIRefreshToken)
	return eccch.NewClient(cfg.EccchKBURL, cfg.EccchTriplestoreID, tokens)
}

func newMCPServer() *mcp.Server {
	s := mcp.NewServer("UNICHE MCP", "0.1.0")

	s.RegisterTool(&musemind.RewriteTool{})
	s.RegisterTool(&musemind.BrainstormTool{})
	s.RegisterTool(&musemind.VerifyTool{})
	s.RegisterTool(&musemind.EnrichTool{})
	s.RegisterTool(&musemind.SummarizeFeedbackTool{})

	s.RegisterTool(&assetiq.RecommendTool{})
	s.RegisterTool(&assetiq.ImportTool{})

	// Static resources - kept for resources/list discoverability.
	s.RegisterResource(&assetiq.SearchResource{})
	s.RegisterResource(&assetiq.PreviewResource{})

	// Parameterized resource templates - handle actual reads with args.
	s.RegisterResourceTemplate(&assetiq.SearchTemplate{})
	s.RegisterResourceTemplate(&assetiq.PreviewTemplate{})
	s.RegisterResourceTemplate(&visitorprofile.Template{})

	return s
}

func loadRemoteTools(db *database.DB, s *mcp.Server) error {
	tools, err := db.ListTools(context.Background())
	if err != nil {
		return err
	}
	for _, t := range tools {
		s.RegisterTool(mcp.NewRemoteTool(t.Name, t.Description, t.CallbackURL))
	}
	return nil
}

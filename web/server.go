package web

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"uniche/api"
	"uniche/api/gatewayapi"
	"uniche/api/repositoryapi"
	"uniche/assetiq"
	"uniche/database"
	"uniche/mcp"
	"uniche/views/pages"
)

// RepositoryPrefix is where the gateway serves the Repository's public
// endpoints. The /internal/v1 endpoints are never routed.
const RepositoryPrefix = "/api/v1/repository"

type Server struct {
	db       *database.DB
	mcp      *mcp.Server
	importer *assetiq.Importer
}

// Option customizes the router built by NewServer.
type Option func(*options)

type options struct {
	devTokenURL      string
	devTokenClientID string
	mounts           []mount
}

type mount struct {
	pattern string
	handler http.Handler
}

// WithDevLogin enables the Swagger UI password-grant proxy at POST /docs/token.
func WithDevLogin(tokenURL, clientID string) Option {
	return func(o *options) { o.devTokenURL, o.devTokenClientID = tokenURL, clientID }
}

// WithMount serves h under pattern (e.g. "/repo"), including its subpaths.
func WithMount(pattern string, h http.Handler) Option {
	return func(o *options) { o.mounts = append(o.mounts, mount{pattern, h}) }
}

func NewServer(db *database.DB, mcp *mcp.Server, repo repositoryapi.Handler, importer *assetiq.Importer, opts ...Option) (http.Handler, error) {
	var cfg options
	for _, opt := range opts {
		opt(&cfg)
	}

	gw := &Server{db: db, mcp: mcp, importer: importer}
	oasSrv, err := gatewayapi.NewServer(gw, gw)
	if err != nil {
		return nil, err
	}

	repoSrv, err := repositoryapi.NewServer(repo, repositorySecurity{}, repositoryapi.WithPathPrefix(RepositoryPrefix))
	if err != nil {
		return nil, err
	}

	r := chi.NewRouter()
	// GetHead: the Docker HEALTHCHECK probes / with wget --spider, which sends HEAD.
	r.Use(middleware.Recoverer, middleware.GetHead, requestLogger)

	// The ogen servers keep their own error handling.
	r.Handle("/api/*", oasSrv)
	r.Handle(RepositoryPrefix+"/*", repoSrv)
	r.Handle(RepositoryPrefix+"/internal/*", http.NotFoundHandler())

	r.Get("/", NewHandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
		http.Redirect(w, r, "/docs", http.StatusFound)
		return nil
	}))
	r.Get("/status", NewHandlerFunc(func(w http.ResponseWriter, _ *http.Request) error {
		_, err := w.Write([]byte("OK"))
		return err
	}))
	r.Get("/openapi.yaml", specHandler(api.GatewaySpec))
	r.Get("/repository.yaml", specHandler(api.RepositorySpec))
	r.Get("/mcp-transport.yaml", specHandler(api.MCPTransportSpec))
	r.Get("/docs", NewHandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		return pages.Docs().Render(r.Context(), w)
	}))
	// Deep links into the spec selector; kept so existing URLs do not 404.
	r.Get("/docs/repository", redirectHandler("/docs?urls.primaryName=UNICHE%20Repository"))
	r.Get("/docs/mcp", redirectHandler("/docs?urls.primaryName=MCP%20Transport"))

	if cfg.devTokenClientID != "" {
		r.Post("/docs/token", DevTokenProxy(cfg.devTokenURL, cfg.devTokenClientID))
	} else {
		// Answer in OAuth error form; otherwise Swagger UI fails with a JSON
		// parse error.
		r.Post("/docs/token", NewHandlerFunc(func(w http.ResponseWriter, _ *http.Request) error {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, err := w.Write([]byte(`{"error":"unsupported","error_description":"dev login disabled: set DEV_LOGIN_CLIENT_ID"}`))
			return err
		}))
	}

	for _, m := range cfg.mounts {
		r.Mount(m.pattern, m.handler)
	}
	return r, nil
}

func specHandler(spec []byte) http.HandlerFunc {
	return NewHandlerFunc(func(w http.ResponseWriter, _ *http.Request) error {
		w.Header().Set("Content-Type", "application/yaml")
		_, err := w.Write(spec)
		return err
	})
}

func redirectHandler(target string) http.HandlerFunc {
	return NewHandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
		http.Redirect(w, r, target, http.StatusFound)
		return nil
	})
}

// requestLogger logs one line per request, with the matched route pattern.
func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		start := time.Now()
		next.ServeHTTP(ww, r)
		slog.Info("request", "method", r.Method, "path", r.URL.Path, "route", routePattern(r),
			"status", ww.Status(), "bytes", ww.BytesWritten(), "duration", time.Since(start))
	})
}

func (s *Server) RegisterTool(ctx context.Context, req *gatewayapi.RegisterToolRequest) (gatewayapi.RegisterToolRes, error) {
	tool, err := s.db.InsertTool(ctx, req.Name, req.Description, req.CallbackURL.String())
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return &gatewayapi.RegisterToolConflict{Message: "a tool named " + req.Name + " is already registered"}, nil
		}
		return &gatewayapi.RegisterToolInternalServerError{Message: "internal error"}, nil
	}

	s.mcp.RegisterTool(mcp.NewRemoteTool(tool.Name, tool.Description, tool.CallbackURL))

	return &gatewayapi.RegisterToolResponse{
		ID:           tool.ID,
		Name:         tool.Name,
		RegisteredAt: tool.RegisteredAt,
	}, nil
}

// stubAiTag mirrors the aiTag stub every MCP generative tool returns - see
// stubAITag in musemind/rewrite.go. Kept separate since the two packages
// must not import each other's types.
func stubAiTag() gatewayapi.OptAiTag {
	return gatewayapi.NewOptAiTag(gatewayapi.AiTag{
		Generated: true,
		Model:     gatewayapi.NewOptString("musemind-stub"),
		Timestamp: gatewayapi.NewOptDateTime(time.Now().UTC()),
	})
}

// SearchAssets implements searchAssets operation. REST form of
// assetiq://search, see docs/ai-tools-modeling.md 2.1.
func (s *Server) SearchAssets(_ context.Context, req *gatewayapi.SearchRequest) (gatewayapi.SearchAssetsRes, error) {
	resp := &gatewayapi.SearchResponse{Results: []gatewayapi.SearchResult{}}
	if req.ExpandIntent.Value {
		resp.ExpandedQuery = req.Query
	}
	return resp, nil
}

// RecommendAssets implements recommendAssets operation. REST form of
// assetiq.recommend, see docs/ai-tools-modeling.md 2.2.
func (s *Server) RecommendAssets(_ context.Context, _ *gatewayapi.RecommendRequest) (gatewayapi.RecommendAssetsRes, error) {
	return &gatewayapi.RecommendResponse{Recommendations: []gatewayapi.Recommendation{}}, nil
}

// PreviewAsset implements previewAsset operation. REST form of
// assetiq://preview, see docs/ai-tools-modeling.md 2.3.
func (s *Server) PreviewAsset(_ context.Context, params gatewayapi.PreviewAssetParams) (gatewayapi.PreviewAssetRes, error) {
	return &gatewayapi.PreviewResponse{URI: gatewayapi.NewOptString(params.URI)}, nil
}

// ImportAsset implements importAsset operation. REST form of
// assetiq.import, see docs/ai-tools-modeling.md 2.10.
func (s *Server) ImportAsset(ctx context.Context, req *gatewayapi.ImportRequest) (gatewayapi.ImportAssetRes, error) {
	res, err := s.importer.Import(ctx, req.URI)
	switch {
	case errors.Is(err, assetiq.ErrInvalidURI), errors.Is(err, assetiq.ErrRecordNotFound):
		return &gatewayapi.ImportAssetBadRequest{Message: err.Error()}, nil
	case errors.Is(err, assetiq.ErrSourceNotConfigured):
		slog.Warn("importing asset", "uri", req.URI, "err", err)
		return &gatewayapi.ImportAssetServiceUnavailable{Message: "importing from this source is not enabled on this server"}, nil
	case err != nil:
		slog.Error("importing asset", "uri", req.URI, "err", err)
		return &gatewayapi.ImportAssetInternalServerError{Message: "internal error"}, nil
	}

	status := gatewayapi.ImportResponseStatusImported
	if res.Duplicate {
		status = gatewayapi.ImportResponseStatusDuplicate
	}
	return &gatewayapi.ImportResponse{
		AssetId:   gatewayapi.NewOptString(res.AssetID),
		VersionId: gatewayapi.NewOptString(res.VersionID),
		Status:    gatewayapi.NewOptImportResponseStatus(status),
	}, nil
}

// Brainstorm implements brainstorm operation. REST form of
// musemind.brainstorm, see docs/ai-tools-modeling.md 2.4.
func (s *Server) Brainstorm(_ context.Context, req *gatewayapi.BrainstormRequest) (gatewayapi.BrainstormRes, error) {
	title := req.Scene
	if !title.Set {
		title = gatewayapi.NewOptString("[musemind] brainstorm: stub response")
	}
	return &gatewayapi.BrainstormResponse{
		Title:       title,
		Scenes:      []gatewayapi.Scene{},
		CitedAssets: []gatewayapi.CitedAsset{},
		AiTag:       stubAiTag(),
	}, nil
}

// Rewrite implements rewrite operation. REST form of musemind.rewrite, see
// docs/ai-tools-modeling.md 2.5.
func (s *Server) Rewrite(_ context.Context, req *gatewayapi.RewriteRequest) (gatewayapi.RewriteRes, error) {
	return &gatewayapi.RewriteResponse{
		Text:     gatewayapi.NewOptString("[musemind] rewrite: stub response"),
		Audience: gatewayapi.NewOptString(string(req.Audience.Value)),
		Language: req.Language,
		AiTag:    stubAiTag(),
	}, nil
}

// Verify implements verify operation. REST form of musemind.verify, see
// docs/ai-tools-modeling.md 2.6.
func (s *Server) Verify(_ context.Context, req *gatewayapi.VerifyRequest) (gatewayapi.VerifyRes, error) {
	verified := true
	issues := []gatewayapi.VerifyIssue{}
	if req.Strict.Value && len(req.CitedAssets) == 0 {
		verified = false
		issues = append(issues, gatewayapi.VerifyIssue{
			Status: gatewayapi.NewOptVerifyIssueStatus(gatewayapi.VerifyIssueStatusUnverifiable),
		})
	}
	return &gatewayapi.VerifyResponse{
		Verified: gatewayapi.NewOptBool(verified),
		Issues:   issues,
		AiTag:    stubAiTag(),
	}, nil
}

// EnrichAsset implements enrichAsset operation. REST form of
// musemind.enrich, see docs/ai-tools-modeling.md 2.7.
func (s *Server) EnrichAsset(_ context.Context, _ *gatewayapi.EnrichRequest, _ gatewayapi.EnrichAssetParams) (gatewayapi.EnrichAssetRes, error) {
	return &gatewayapi.EnrichResponse{
		Suggestions: gatewayapi.NewOptEnrichSuggestions(gatewayapi.EnrichSuggestions{
			Tags:        []string{},
			Description: gatewayapi.NewOptString("[musemind] enrich: stub response"),
		}),
		AiTag:          stubAiTag(),
		RequiresReview: gatewayapi.NewOptBool(true),
	}, nil
}

// SummarizeFeedback implements summarizeFeedback operation. REST form of
// musemind.summarize_feedback, see docs/ai-tools-modeling.md 2.8.
func (s *Server) SummarizeFeedback(_ context.Context, _ *gatewayapi.SummarizeFeedbackRequest) (gatewayapi.SummarizeFeedbackRes, error) {
	return &gatewayapi.SummarizeFeedbackResponse{
		Clusters: []gatewayapi.FeedbackCluster{},
		AiTag:    stubAiTag(),
	}, nil
}

// GetVisitorProfile implements getVisitorProfile operation. REST form of
// visitorprofile://{visitorId}, see docs/ai-tools-modeling.md 2.9.
func (s *Server) GetVisitorProfile(_ context.Context, _ gatewayapi.GetVisitorProfileParams) (gatewayapi.GetVisitorProfileRes, error) {
	// Degrade gracefully to an empty, non-personalized profile - this is
	// the stub's permanent behavior until a real consent store exists.
	return &gatewayapi.VisitorProfileResponse{
		Interests:     []string{},
		VisitedAssets: []string{},
		ConsentLevel:  gatewayapi.NewOptString("none"),
	}, nil
}

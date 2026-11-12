package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"canopy/server/internal/agentaccess"
	"canopy/server/internal/agentapi"
	"canopy/server/internal/aiconnection"
	"canopy/server/internal/identity"
	"canopy/server/internal/insights"
	"canopy/server/internal/knowledge"
	"canopy/server/internal/media"
	"canopy/server/internal/media/processor"
	"canopy/server/internal/platform"
	platformmedia "canopy/server/internal/platform/media"
	"canopy/server/internal/portability"
	gqlx "canopy/server/internal/transport/graphql"
	httpx "canopy/server/internal/transport/http"
	mcptransport "canopy/server/internal/transport/mcp"
	"strings"
)

func main() {
	cfg, err := platform.LoadConfig(os.LookupEnv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		os.Exit(1)
	}
	log := platform.NewLogger(cfg)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := platform.NewPool(ctx, cfg)
	if err != nil {
		log.Error("database unavailable", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	mux := platform.NewMux(pool.Ping, log)

	q := identity.NewQueries(pool)
	ids := platform.UUIDv4{}
	sessCfg := identity.DefaultSessionConfig(cfg.CookieSecure, cfg.TrustProxyProto)
	sessions := identity.NewSessionManager(q, ids, platform.SystemClock{}, sessCfg)
	owners := identity.NewOwnerService(q, sessions, identity.NewPasswordHasher(identity.ProductionParams), ids, log)
	auth := httpx.NewAuth(owners, sessions, identity.NewLoginLimiter(int(cfg.LoginLimit)), sessCfg, log)
	auth.Register(mux)
	users := identity.NewUsers(pool, sessions)
	publicURL := os.Getenv("CANOPY_PUBLIC_URL")
	if publicURL == "" {
		publicURL = "http://localhost:8080"
	}
	oidcAuth, err := identity.NewOIDC(users, filepath.Join(cfg.StorageRoot, "keys", "oidc.key"), publicURL)
	if err != nil {
		log.Error("sign-in configuration unavailable")
		os.Exit(1)
	}
	auth.EnableUsers(mux, users, oidcAuth)

	// Upload lifecycle (T13): private ObjectStore + transactional queue
	// link. The API only inserts jobs; the worker process executes them.
	if err := platform.MigrateRiver(ctx, pool); err != nil {
		log.Error("river migrate", "err", err)
		os.Exit(1)
	}
	store, err := platformmedia.NewFSStore(cfg.StorageRoot)
	if err != nil {
		log.Error("media store", "err", err)
		os.Exit(1)
	}
	riverClient, err := platform.NewInsertOnlyRiverClient(pool)
	if err != nil {
		log.Error("river client", "err", err)
		os.Exit(1)
	}
	mediaSvc := media.New(pool, log, store, riverClient, cfg.QuotaBytes)

	// Portability (T38.a): the API requests export jobs and serves their
	// downloads; the maintenance flag gates writes during a backup.
	portSvc := portability.New(pool, log, cfg.StorageRoot, riverClient)
	maintenance := func(ctx context.Context) bool { return portability.IsMaintenance(ctx, pool) }

	// AI analysis lane (T29): capabilities are configured independently —
	// an empty CANOPY_AI_BASE_URL is text no-provider mode only, and
	// vision keeps its own settings. The API only gates + enqueues; the
	// worker executes.
	insightLimits := insights.Limits{
		DailyRunLimit:        int32(cfg.AIDailyRunLimit),
		ConcurrentRunLimit:   1, // SDL: max one live run per provider
		MonthCeilingMicroUSD: cfg.AIMonthCeilingMicro,
		AttemptCostMicroUSD:  cfg.AIAttemptCostMicro,
	}
	keyFile, defaults := aiconnection.FromDeployment(cfg)
	aiConnections, err := aiconnection.New(pool, keyFile, defaults)
	if err != nil {
		log.Error("AI credential storage unavailable")
		os.Exit(1)
	}
	provider, err := insights.BuildRouter(pool, store,
		&processor.DockerSandbox{Image: cfg.MediaImage, Memory: cfg.MediaMemory, PIDs: 64},
		insights.RouterConfig{
			AIBaseURL: cfg.AIBaseURL, AIAPIKey: cfg.AIAPIKey, AIModel: cfg.AIModel,
			AITimeout: cfg.AITimeout, AIMaxResponseBytes: cfg.AIMaxResponseBytes,
			VisionBaseURL: cfg.VisionBaseURL, VisionAPIKey: cfg.VisionAPIKey,
			VisionModel: cfg.VisionModel, VisionTimeout: cfg.VisionTimeout,
			VisionMaxRespBytes: cfg.VisionMaxRespBytes, VisionMaxImageB: cfg.VisionMaxImageB,
			VisionLocal: cfg.VisionLocal, VisionReasoningEffort: cfg.VisionReasoningEffort,
			PlantNetBaseURL: cfg.PlantNetBaseURL, PlantNetAPIKey: cfg.PlantNetAPIKey,
			OCRLangs: cfg.OCRLangs, OCRMaxPages: int(cfg.OCRMaxPages),
		})
	if err != nil {
		log.Error("ai adapter config", "err", err)
		os.Exit(1)
	}
	if cfg.AIBaseURL == "" {
		log.Info("text analysis provider not configured")
	}
	connectionProvider := insights.WithConnections(aiConnections, provider, pool, store, &processor.DockerSandbox{Image: cfg.MediaImage, Memory: cfg.MediaMemory, PIDs: 64}, cfg.OCRLangs, int(cfg.OCRMaxPages))
	insightsSvc := insights.New(pool, log, insightLimits, connectionProvider)
	insightsSvc.Queue = riverClient
	insightsSvc.PromptVersion = cfg.AIPromptVersion
	insightsSvc.SchemaVersion = cfg.AIResultSchemaVer
	insightsSvc.AttemptTimeout = cfg.AITimeout
	insightsSvc.MaxResultBytes = cfg.AIMaxResponseBytes

	gqlRoot := gqlx.NewResolverFromPool(pool, log)
	gqlRoot.Media = mediaSvc
	gqlRoot.Port = portSvc
	gqlRoot.Insights = insightsSvc
	if cfg.KnowledgeBaseURL != "" {
		gqlRoot.Knowledge.Model = knowledge.NewModel(cfg.KnowledgeBaseURL, cfg.KnowledgeAPIKey, cfg.KnowledgeModel, cfg.KnowledgeReasoningEffort)
	} else if cfg.VisionBaseURL != "" {
		gqlRoot.Knowledge.Model = knowledge.NewModel(cfg.VisionBaseURL, cfg.VisionAPIKey, cfg.VisionModel, cfg.VisionReasoningEffort)
	}
	gqlRoot.AIConnections = aiConnections
	gqlRoot.Knowledge.UseConnections(aiConnections)
	gqlRoot.Coll.Queue = riverClient
	agentAccess := agentaccess.New(pool)
	gqlRoot.AgentAccess = agentAccess
	gqlRoot.AgentEnabled = cfg.MCPEnabled
	gqlRoot.AgentEndpoint = cfg.MCPPublicURL
	agentEngine := gqlx.NewAgentExecutor(gqlRoot)
	agentEngine.Handler.Maintenance = maintenance
	agentAPI, err := agentapi.New(agentAccess, agentEngine, store)
	if err != nil {
		log.Error("MCP tool catalog unavailable")
		os.Exit(1)
	}
	if cfg.MCPEnabled {
		hosts := []string{"localhost:8080", "127.0.0.1:8080", "host.docker.internal:8080"}
		if cfg.MCPAllowedHosts != "" {
			hosts = strings.Split(cfg.MCPAllowedHosts, ",")
		}
		mcpHandler := mcptransport.New(agentAccess, agentAPI, hosts)
		mux.Handle("/mcp", mcpHandler)
		agentUploads := httpx.NewUploads(pool, store, log)
		agentUploads.Maintenance = maintenance
		mediaHandler := httpx.NewAgentUploads(agentAPI, agentUploads)
		agentExports := httpx.AgentExportDelivery(agentAPI, httpx.NewExportDelivery(pool, portSvc.ExportsDir(), log))
		mux.HandleFunc("GET /agent/exports/{id}/download", func(w http.ResponseWriter, r *http.Request) {
			request, ok := mcpHandler.Authenticate(w, r)
			if ok {
				agentExports(w, request)
			}
		})
		mux.HandleFunc("PUT /agent/uploads/{assetID}", func(w http.ResponseWriter, r *http.Request) {
			request, ok := mcpHandler.Authenticate(w, r)
			if ok {
				mediaHandler.ServeHTTP(w, request)
			}
		})
	}
	gqlHandler := gqlx.NewHandler(gqlRoot, gqlx.DefaultLimits(), log)
	gqlHandler.Maintenance = maintenance
	mux.Handle("POST /api/graphql", auth.RequirePrincipal(gqlHandler.ServeHTTP))
	uploads := httpx.NewUploads(pool, store, log)
	uploads.Maintenance = maintenance
	mux.Handle("PUT /api/uploads/{assetID}", auth.RequirePrincipal(uploads.Handler()))
	// Authorized delivery (T15): fixed variants, owner-only, 404 for
	// unknown/cross-workspace/non-READY/wrong-purpose requests.
	mux.Handle("GET /api/media/{assetID}/{variant}", auth.RequirePrincipal(
		httpx.NewMediaDelivery(pool, store, log).Handler()))
	// Export download (T38.a): owner-scoped job row, READY bytes only,
	// lazy expiry; the auth guard pattern covers other export paths.
	mux.Handle("GET /api/exports/{id}/download", auth.RequirePrincipal(
		httpx.NewExportDelivery(pool, portSvc.ExportsDir(), log).Handler()))

	srv := platform.NewServer(cfg, mux, log)
	log.Info("api listening", "addr", cfg.Addr, "env", cfg.Env)
	if err := srv.Run(ctx, srv.ListenAndServe); err != nil {
		log.Error("server stopped with error", "err", err)
		os.Exit(1)
	}
}

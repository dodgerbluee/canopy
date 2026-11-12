package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/riverqueue/river"

	"canopy/server/internal/aiconnection"
	"canopy/server/internal/care"
	"canopy/server/internal/care/weather"
	"canopy/server/internal/email"
	"canopy/server/internal/insights"
	"canopy/server/internal/knowledge"
	"canopy/server/internal/media"
	"canopy/server/internal/media/processor"
	"canopy/server/internal/platform"
	platformmedia "canopy/server/internal/platform/media"
	"canopy/server/internal/portability"
)

// NoopArgs is the reference job kind proving the registration + atomic
// insertion seam; feature jobs (export, purge) register the same way.
type NoopArgs struct{ Note string }

func (NoopArgs) Kind() string { return "noop" }

type noopWorker struct{ river.WorkerDefaults[NoopArgs] }

func (w *noopWorker) Work(ctx context.Context, job *river.Job[NoopArgs]) error {
	return nil
}

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

	if err := platform.MigrateRiver(ctx, pool); err != nil {
		log.Error("river migrate", "err", err)
		os.Exit(1)
	}

	workers := river.NewWorkers()
	river.AddWorker(workers, &noopWorker{})

	store, err := platformmedia.NewFSStore(cfg.StorageRoot)
	if err != nil {
		log.Error("media store", "err", err)
		os.Exit(1)
	}
	mediaSB, err := processor.NewProcessSandbox(cfg.MediaMemory, cfg.MediaTimeout)
	if err != nil {
		log.Error("invalid media process limits", "err", err)
		os.Exit(1)
	}
	var mediaRunner processor.Sandbox = mediaSB
	// Optional legacy adapter for host-development harnesses only. Bundled
	// deployments always default to installed tools and never need Docker.
	if os.Getenv("CANOPY_MEDIA_BACKEND") == "docker" {
		mediaRunner = &processor.DockerSandbox{Image: cfg.MediaImage, Memory: cfg.MediaMemory, PIDs: 64}
	}
	river.AddWorker(workers, &processor.Worker{
		Pool:       pool,
		Store:      store,
		SB:         mediaRunner,
		MaxPixels:  cfg.MediaMaxPixels,
		JobTimeout: cfg.MediaTimeout,
		Log:        log,
	})
	river.AddWorker(workers, &processor.CoverQualityWorker{Pool: pool, Store: store})
	// grace-period cleanup (T15): removes stored objects of unreferenced
	// DELETING assets after re-checking references at run time
	river.AddWorker(workers, &media.PurgeWorker{Pool: pool, Store: store, Log: log})

	// export pipeline (T38.a): snapshot build + revoked/expired archive
	// file cleanup
	portSvc := portability.New(pool, log, cfg.StorageRoot, nil)
	river.AddWorker(workers, &portability.ExportBuildWorker{Svc: portSvc})
	river.AddWorker(workers, &portability.ExportCleanupWorker{Svc: portSvc})

	// care scheduler (T23): occurrence materialization tick; RunOnStart
	// on the periodic job is the startup catch-up after an outage
	careSvc := care.New(pool, log)
	keyFile, defaults := aiconnection.FromDeployment(cfg)
	aiConnections, err := aiconnection.New(pool, keyFile, defaults)
	if err != nil {
		log.Error("AI credential storage unavailable")
		os.Exit(1)
	}
	knowledgeSvc := knowledge.New(pool)
	knowledgeSvc.Fetch = knowledge.NewFetcher(mediaSB)
	if cfg.KnowledgeBaseURL != "" {
		knowledgeSvc.Model = knowledge.NewModel(cfg.KnowledgeBaseURL, cfg.KnowledgeAPIKey, cfg.KnowledgeModel, cfg.KnowledgeReasoningEffort)
	} else if cfg.VisionBaseURL != "" {
		knowledgeSvc.Model = knowledge.NewModel(cfg.VisionBaseURL, cfg.VisionAPIKey, cfg.VisionModel, cfg.VisionReasoningEffort)
	}
	knowledgeSvc.UseConnections(aiConnections)
	river.AddWorker(workers, &knowledge.Worker{Service: knowledgeSvc})
	river.AddWorker(workers, &care.CareTickWorker{Svc: careSvc, Log: log})

	// AI analysis runs (T29): claim → bounded provider attempt → validate
	// → generation-fenced commit. Empty CANOPY_AI_BASE_URL keeps the
	// adapter in no-provider mode: claimed runs fail honestly, nothing is
	// ever fetched, and the API side never queues new runs anyway.
	insightLimits := insights.Limits{
		DailyRunLimit:        int32(cfg.AIDailyRunLimit),
		ConcurrentRunLimit:   1,
		MonthCeilingMicroUSD: cfg.AIMonthCeilingMicro,
		AttemptCostMicroUSD:  cfg.AIAttemptCostMicro,
	}
	provider, err := insights.BuildRouter(pool, store, mediaSB, insights.RouterConfig{
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
	connectionProvider := insights.WithConnections(aiConnections, provider, pool, store, mediaRunner, cfg.OCRLangs, int(cfg.OCRMaxPages))
	insightsSvc := insights.New(pool, log, insightLimits, connectionProvider)
	insightsSvc.PromptVersion = cfg.AIPromptVersion
	insightsSvc.SchemaVersion = cfg.AIResultSchemaVer
	// A vision attempt may legitimately outlast a text attempt (local
	// inference); the per-attempt bound must cover the slowest port.
	insightsSvc.AttemptTimeout = max(cfg.AITimeout, cfg.VisionTimeout)
	insightsSvc.MaxResultBytes = cfg.AIMaxResponseBytes
	river.AddWorker(workers, &insights.RunWorker{Svc: insightsSvc})
	river.AddWorker(workers, &insights.SuggestionExpiryWorker{Svc: insightsSvc})

	// weather fetch (T26): periodic snapshot refresh for opted-in
	// (owner-entered coordinates) locations; provider failures never
	// gate care, and the 6h cadence doubles as the cache window
	careSvc.Weather = weather.New(cfg.WeatherBaseURL, cfg.WeatherTimeout)
	river.AddWorker(workers, &care.WeatherFetchWorker{Svc: careSvc, Log: log})

	// email delivery (T27): daily digest intents over SMTP with bounded
	// retry; empty CANOPY_SMTP_HOST means the deployment opted out and
	// EMAIL intents stay pending (in-app notification unaffected)
	periodics := []*river.PeriodicJob{
		care.CarePeriodicJob(), care.WeatherFetchPeriodicJob(), knowledge.PeriodicJob(), processor.CoverQualityPeriodicJob(),
		insights.SuggestionExpiryPeriodicJob(),
	}
	if cfg.SMTPHost != "" {
		careSvc.Email = &email.SMTPSender{
			Host: cfg.SMTPHost, Port: int(cfg.SMTPPort), Username: cfg.SMTPUser,
			Password: cfg.SMTPPass, From: cfg.SMTPFrom, Timeout: cfg.SMTPTimeout,
		}
		careSvc.EmailTo = cfg.SMTPTo
		river.AddWorker(workers, &care.EmailDeliveryWorker{Svc: careSvc, Log: log})
		periodics = append(periodics, care.EmailDeliveryPeriodicJob())
	} else {
		log.Info("SMTP not configured: email digests stay pending, in-app only")
	}

	client, err := platform.NewRiverClient(pool, workers, periodics...)
	if err != nil {
		log.Error("river client", "err", err)
		os.Exit(1)
	}
	if err := client.Start(ctx); err != nil {
		log.Error("worker start", "err", err)
		os.Exit(1)
	}
	<-ctx.Done()
	sctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := client.Stop(sctx); err != nil {
		log.Error("worker stop", "err", err)
		os.Exit(1)
	}
}

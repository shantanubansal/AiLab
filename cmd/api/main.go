// Command api is the public REST + (future) gRPC entrypoint for the platform.
//
// It owns auth, tenant-scoped CRUD over agents/runs/secrets/triggers, and is
// the single writer to Postgres for control-plane state. It publishes
// run.requested / build.requested events to NATS and consumes run.started /
// run.completed back from the controller to update the runs table.
package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.uber.org/zap"

	"github.com/shantanubansal/AiLab/internal/agents"
	"github.com/shantanubansal/AiLab/internal/audit"
	"github.com/shantanubansal/AiLab/internal/auth"
	"github.com/shantanubansal/AiLab/internal/builds"
	"github.com/shantanubansal/AiLab/internal/config"
	"github.com/shantanubansal/AiLab/internal/cryptobox"
	"github.com/shantanubansal/AiLab/internal/db"
	"github.com/shantanubansal/AiLab/internal/eventbus"
	"github.com/shantanubansal/AiLab/internal/kube"
	"github.com/shantanubansal/AiLab/internal/loki"
	"github.com/shantanubansal/AiLab/internal/runs"
	"github.com/shantanubansal/AiLab/internal/secrets"
	"github.com/shantanubansal/AiLab/internal/telemetry"
	"github.com/shantanubansal/AiLab/internal/tenants"
	"github.com/shantanubansal/AiLab/internal/triggers"
	"github.com/shantanubansal/AiLab/internal/usage"
)

// version is overridden via -ldflags by the release pipeline.
var version = "dev"

func main() {
	logger := mustLogger()
	defer func() { _ = logger.Sync() }()

	cfg := config.LoadAPI()
	if cfg.DevMode() {
		logger.Warn("WORKOS_JWKS_URL not set; running in dev-mode auth (dev:<tenantId>:<userId>)")
	}

	rootCtx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	shutdownTraces, err := telemetry.Init(rootCtx, "api", version)
	if err != nil {
		logger.Fatal("telemetry init", zap.Error(err))
	}
	defer func() {
		flush, cancelFlush := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelFlush()
		_ = shutdownTraces(flush)
	}()

	pool, err := db.Open(rootCtx, cfg.DatabaseURL)
	if err != nil {
		logger.Fatal("postgres open", zap.Error(err))
	}
	defer pool.Close()

	bus, err := eventbus.Connect(rootCtx, cfg.NATSURL)
	if err != nil {
		logger.Fatal("nats connect", zap.Error(err))
	}
	defer bus.Close()

	agentRepo := agents.NewRepo(pool)
	runRepo := runs.NewRepo(pool)

	box, err := cryptobox.NewFromHex(cfg.SecretsKeyHex)
	if err != nil {
		logger.Fatal("cryptobox", zap.Error(err))
	}
	triggerRepo := triggers.NewRepo(pool, box)
	buildRepo := builds.NewRepo(pool)
	secretRepo := secrets.NewRepo(pool, box)
	auditRepo := audit.NewRepo(pool)
	audit.SetGlobal(auditRepo)

	lokiClient := loki.New(cfg.LokiURL)
	if cfg.LokiURL == "" {
		logger.Info("LOKI_URL unset; runs/logs falls back to error when pod has been GC'd")
	}

	// k8s client is optional — without it the api still serves CRUD but
	// GET /v1/runs/{id}/logs returns 503. Failing to load it should not
	// take the api down in environments (e.g. tests) without a cluster.
	k8sClient, err := kube.New()
	if err != nil {
		logger.Warn("k8s client unavailable; logs endpoint disabled", zap.Error(err))
	}

	usageRepo := usage.NewRepo(pool)
	runHub := runs.NewHub()

	// Status-feedback consumer: applies run.started / run.completed messages
	// from the controller back into the runs table, writes usage metering
	// rows, and fans events out to the in-process Hub that powers the
	// /v1/runs/{id}/events SSE endpoint.
	statusCons := &runs.StatusConsumer{Repo: runRepo, Usage: usageRepo, Hub: runHub, Logger: logger}
	if err := statusCons.Start(rootCtx, bus); err != nil {
		logger.Fatal("status consumer", zap.Error(err))
	}

	var verifier auth.Verifier = auth.DevVerifier{}
	if !cfg.DevMode() {
		v, err := auth.NewWorkOSVerifier(rootCtx, cfg.WorkOSJWKSURL, cfg.WorkOSClientID)
		if err != nil {
			logger.Fatal("workos verifier", zap.Error(err))
		}
		verifier = v
		logger.Info("workos verifier active", zap.String("jwks", cfg.WorkOSJWKSURL))
	}

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(otelhttp.NewMiddleware("api"))
	r.Use(auth.CORS(auth.DefaultCORSOrigins()))
	r.Use(middleware.Timeout(30 * time.Second))

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	triggerH := &triggers.Handlers{
		Triggers: triggerRepo,
		Agents:   agentRepo,
		Runs:     runRepo,
		Bus:      bus,
	}
	// Unauthenticated webhook receiver lives outside /v1's auth middleware.
	triggerH.PublicRoutes(r)

	// WorkOS organization webhook also lives outside the JWT auth middleware
	// — its own HMAC verification gates the writes.
	if cfg.WorkOSWebhookSecret != "" {
		tenantRepo := tenants.NewRepo(pool)
		workosH := &tenants.WebhookHandler{Repo: tenantRepo, SigningSecret: cfg.WorkOSWebhookSecret}
		r.Route("/v1/webhooks", workosH.Routes)
		logger.Info("workos webhook receiver enabled at /v1/webhooks/workos")
	} else {
		logger.Info("WORKOS_WEBHOOK_SECRET unset; tenant provisioning is manual")
	}

	r.Route("/v1", func(r chi.Router) {
		r.Use(auth.Middleware(verifier))

		agentH := &agents.Handlers{Repo: agentRepo}
		r.Route("/agents", agentH.Routes)

		deployH := &agents.DeployHandlers{Repo: agentRepo, Bus: bus, K8s: k8sClient, Secrets: secretRepo}
		r.Route("/agents/{agentId}/deploy", deployH.Routes)

		runH := &runs.Handlers{Runs: runRepo, Agents: agentRepo, Bus: bus, K8s: k8sClient, Secrets: secretRepo, Loki: lokiClient, Hub: runHub}
		runH.Routes(r)

		r.Route("/agents/{agentId}/triggers", triggerH.AuthRoutes)

		buildH := &builds.Handlers{Builds: buildRepo, Agents: agentRepo, Bus: bus}
		r.Route("/agents/{agentId}/builds", buildH.Routes)

		secretH := &secrets.Handlers{Repo: secretRepo}
		r.Route("/secrets", secretH.Routes)

		auditH := &audit.Handlers{Repo: auditRepo}
		r.Route("/audit", auditH.Routes)

		tenantRepoForMe := tenants.NewRepo(pool)
		meH := &tenants.MeHandler{Repo: tenantRepoForMe}
		r.Route("/me", meH.Routes)
	})

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		logger.Info("api listening", zap.String("addr", cfg.ListenAddr))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Fatal("listen", zap.Error(err))
		}
	}()

	<-rootCtx.Done()
	logger.Info("shutdown requested")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown", zap.Error(err))
	}
}

func mustLogger() *zap.Logger {
	l, err := zap.NewProduction()
	if err != nil {
		panic(err)
	}
	return l
}

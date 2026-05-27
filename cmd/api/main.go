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
	"go.uber.org/zap"

	"github.com/shantanubansal/AiLab/internal/agents"
	"github.com/shantanubansal/AiLab/internal/auth"
	"github.com/shantanubansal/AiLab/internal/builds"
	"github.com/shantanubansal/AiLab/internal/config"
	"github.com/shantanubansal/AiLab/internal/cryptobox"
	"github.com/shantanubansal/AiLab/internal/db"
	"github.com/shantanubansal/AiLab/internal/eventbus"
	"github.com/shantanubansal/AiLab/internal/kube"
	"github.com/shantanubansal/AiLab/internal/runs"
	"github.com/shantanubansal/AiLab/internal/triggers"
	"github.com/shantanubansal/AiLab/internal/usage"
)

func main() {
	logger := mustLogger()
	defer func() { _ = logger.Sync() }()

	cfg := config.LoadAPI()
	if cfg.DevMode() {
		logger.Warn("WORKOS_JWKS_URL not set; running in dev-mode auth (dev:<tenantId>:<userId>)")
	}

	rootCtx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

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

	// k8s client is optional — without it the api still serves CRUD but
	// GET /v1/runs/{id}/logs returns 503. Failing to load it should not
	// take the api down in environments (e.g. tests) without a cluster.
	k8sClient, err := kube.New()
	if err != nil {
		logger.Warn("k8s client unavailable; logs endpoint disabled", zap.Error(err))
	}

	usageRepo := usage.NewRepo(pool)

	// Status-feedback consumer: applies run.started / run.completed messages
	// from the controller back into the runs table, and also writes usage
	// metering rows. Runs in a goroutine for the process lifetime; errors
	// are logged but do not crash the api.
	statusCons := &runs.StatusConsumer{Repo: runRepo, Usage: usageRepo, Logger: logger}
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

	r.Route("/v1", func(r chi.Router) {
		r.Use(auth.Middleware(verifier))

		agentH := &agents.Handlers{Repo: agentRepo}
		r.Route("/agents", agentH.Routes)

		deployH := &agents.DeployHandlers{Repo: agentRepo, Bus: bus}
		r.Route("/agents/{agentId}/deploy", deployH.Routes)

		runH := &runs.Handlers{Runs: runRepo, Agents: agentRepo, Bus: bus, K8s: k8sClient}
		runH.Routes(r)

		r.Route("/agents/{agentId}/triggers", triggerH.AuthRoutes)

		buildH := &builds.Handlers{Builds: buildRepo, Agents: agentRepo, Bus: bus}
		r.Route("/agents/{agentId}/builds", buildH.Routes)
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

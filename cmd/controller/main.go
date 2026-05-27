// Command controller runs the AgentRun and AgentDeployment reconcilers,
// and subscribes to run.requested events to materialize CRs.
//
// It joins the cluster identified by the current kubeconfig context (or, in
// cluster, the projected service account token), registers the platform's
// CRDs in the scheme, and starts the controller-runtime manager.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/shantanubansal/AiLab/internal/config"
	"github.com/shantanubansal/AiLab/internal/eventbus"
	"github.com/shantanubansal/AiLab/internal/runs"
	"github.com/shantanubansal/AiLab/internal/telemetry"
)

// version is overridden via -ldflags by the release pipeline.
var version = "dev"

func main() {
	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))
	logger := ctrl.Log.WithName("controller")

	cfg := config.LoadController()
	apiCfg := config.LoadAPI() // shares NATS_URL with the api

	scheme := clientgoscheme.Scheme
	if err := runs.AddToScheme(scheme); err != nil {
		logger.Error(err, "register scheme")
		os.Exit(1)
	}

	rootCtx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	shutdownTraces, err := telemetry.Init(rootCtx, "controller", version)
	if err != nil {
		logger.Error(err, "telemetry init")
		os.Exit(1)
	}
	defer func() {
		flush, cancelFlush := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelFlush()
		_ = shutdownTraces(flush)
	}()

	bus, err := eventbus.Connect(rootCtx, apiCfg.NATSURL)
	if err != nil {
		logger.Error(err, "nats connect")
		os.Exit(1)
	}
	defer bus.Close()

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:           scheme,
		Metrics:          metricsserver.Options{BindAddress: cfg.MetricsAddr},
		LeaderElection:   cfg.LeaderElect,
		LeaderElectionID: "ailab-controller",
	})
	if err != nil {
		logger.Error(err, "manager init")
		os.Exit(1)
	}

	if err := (&runs.AgentRunReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
		Bus:    bus,
	}).SetupWithManager(mgr); err != nil {
		logger.Error(err, "setup AgentRun reconciler")
		os.Exit(1)
	}
	if err := (&runs.AgentDeploymentReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		logger.Error(err, "setup AgentDeployment reconciler")
		os.Exit(1)
	}

	quota := runs.QuotaSpec{
		CPU:    envOr("CONTROLLER_TENANT_QUOTA_CPU", "8"),
		Memory: envOr("CONTROLLER_TENANT_QUOTA_MEMORY", "16Gi"),
		Pods:   envOr("CONTROLLER_TENANT_QUOTA_PODS", "50"),
	}
	limit := runs.LimitSpec{
		DefaultCPU:    envOr("CONTROLLER_TENANT_LIMIT_DEFAULT_CPU", "200m"),
		DefaultMemory: envOr("CONTROLLER_TENANT_LIMIT_DEFAULT_MEMORY", "256Mi"),
		MaxCPU:        envOr("CONTROLLER_TENANT_LIMIT_MAX_CPU", "4"),
		MaxMemory:     envOr("CONTROLLER_TENANT_LIMIT_MAX_MEMORY", "8Gi"),
	}

	// Dispatch consumers need a working client; they run alongside the
	// manager and start once it's elected.
	go func() {
		<-mgr.Elected()
		runDC := &runs.DispatchConsumer{Client: mgr.GetClient(), Quota: quota, Limit: limit}
		if err := runDC.Start(rootCtx, bus); err != nil {
			logger.Error(err, "run dispatch consumer")
		} else {
			logger.Info("run dispatch consumer subscribed")
		}
		depDC := &runs.DeploymentDispatchConsumer{Client: mgr.GetClient(), Quota: quota, Limit: limit}
		if err := depDC.Start(rootCtx, bus); err != nil {
			logger.Error(err, "deployment dispatch consumer")
		} else {
			logger.Info("deployment dispatch consumer subscribed")
		}
	}()

	logger.Info("starting manager")
	if err := mgr.Start(rootCtx); err != nil {
		logger.Error(err, "manager exited with error")
		os.Exit(1)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

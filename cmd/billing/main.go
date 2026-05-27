// Command billing ships usage_events to Orb. Runs as a long-lived
// process — restart-safe via the usage_shipper_state checkpoint table.
//
// Stripe meter events are the same shape; when we wire them, drop in a
// second Destination in internal/billing and either run two shippers
// (one per destination) or a single shipper with fan-out. The v1 cut
// is Orb-only.

package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/shantanubansal/AiLab/internal/billing"
	"github.com/shantanubansal/AiLab/internal/config"
	"github.com/shantanubansal/AiLab/internal/db"
)

func main() {
	apiKey := os.Getenv("ORB_API_KEY")
	if apiKey == "" {
		log.Fatal("ORB_API_KEY is required")
	}
	baseURL := os.Getenv("ORB_API_URL") // optional; defaults to api.withorb.com
	cfg := config.LoadAPI()

	rootCtx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	pool, err := db.Open(rootCtx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("postgres open: %v", err)
	}
	defer pool.Close()

	shipper := billing.NewShipper(pool, billing.NewOrb(apiKey, baseURL))
	log.Printf("billing shipper running (dest=%s batch=%d interval=%s)",
		shipper.Destination.Name(), shipper.BatchSize, shipper.Interval)
	_ = shipper.Run(rootCtx)
}

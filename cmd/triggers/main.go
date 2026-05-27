// Command triggers is the cron scheduler.
//
// It reads cron triggers from Postgres into an in-process scheduler and
// publishes run.requested to NATS on schedule. The triggers table is the
// source of truth — the scheduler refreshes its view periodically to pick
// up new and deleted triggers.
//
// Webhook triggers are served by the api process; this service only
// handles cron.
package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/robfig/cron/v3"

	"github.com/shantanubansal/AiLab/internal/agents"
	"github.com/shantanubansal/AiLab/internal/config"
	"github.com/shantanubansal/AiLab/internal/cryptobox"
	"github.com/shantanubansal/AiLab/internal/db"
	"github.com/shantanubansal/AiLab/internal/eventbus"
	"github.com/shantanubansal/AiLab/internal/runs"
	"github.com/shantanubansal/AiLab/internal/telemetry"
	"github.com/shantanubansal/AiLab/internal/triggers"
	"github.com/shantanubansal/AiLab/pkg/events"
)

// version is overridden via -ldflags by the release pipeline.
var version = "dev"

func main() {
	cfg := config.LoadAPI()

	rootCtx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	shutdownTraces, err := telemetry.Init(rootCtx, "triggers", version)
	if err != nil {
		log.Fatalf("telemetry init: %v", err)
	}
	defer func() {
		flush, cancelFlush := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelFlush()
		_ = shutdownTraces(flush)
	}()

	pool, err := db.Open(rootCtx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("postgres open: %v", err)
	}
	defer pool.Close()

	bus, err := eventbus.Connect(rootCtx, cfg.NATSURL)
	if err != nil {
		log.Fatalf("nats connect: %v", err)
	}
	defer bus.Close()

	box, err := cryptobox.NewFromHex(cfg.SecretsKeyHex)
	if err != nil {
		log.Fatalf("cryptobox: %v", err)
	}

	repo := triggers.NewRepo(pool, box)
	agentRepo := agents.NewRepo(pool)
	runRepo := runs.NewRepo(pool)

	sched := &scheduler{
		cron:      cron.New(cron.WithLocation(time.UTC)),
		triggers:  repo,
		agents:    agentRepo,
		runs:      runRepo,
		bus:       bus,
		registered: make(map[string]registered),
	}
	sched.cron.Start()
	defer sched.cron.Stop()

	if err := sched.refresh(rootCtx); err != nil {
		log.Printf("initial refresh: %v", err)
	}
	log.Printf("triggers: cron scheduler running (%d entries)", len(sched.registered))

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-rootCtx.Done():
			return
		case <-ticker.C:
			if err := sched.refresh(rootCtx); err != nil {
				log.Printf("refresh: %v", err)
			}
		}
	}
}

// registered tracks a cron entry alongside a checksum of its source row.
// If the checksum changes, the entry is replaced; if the row disappears,
// the entry is removed.
type registered struct {
	entryID  cron.EntryID
	checksum string
}

type scheduler struct {
	cron       *cron.Cron
	triggers   *triggers.Repo
	agents     *agents.Repo
	runs       *runs.Repo
	bus        *eventbus.Bus
	mu         sync.Mutex
	registered map[string]registered // keyed by trigger id
}

func (s *scheduler) refresh(ctx context.Context) error {
	rows, err := s.triggers.ListAllCron(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	seen := make(map[string]struct{}, len(rows))
	for _, t := range rows {
		seen[t.ID] = struct{}{}
		sum := checksum(t.AgentID, t.CronExpr)
		if cur, ok := s.registered[t.ID]; ok && cur.checksum == sum {
			continue
		}
		if cur, ok := s.registered[t.ID]; ok {
			s.cron.Remove(cur.entryID)
		}

		tr := t // capture for the closure
		id, err := s.cron.AddFunc(t.CronExpr, func() {
			fireCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := s.fire(fireCtx, tr); err != nil {
				log.Printf("fire %s: %v", tr.ID, err)
			}
		})
		if err != nil {
			log.Printf("cron schedule %q (trigger %s): %v", t.CronExpr, t.ID, err)
			continue
		}
		s.registered[t.ID] = registered{entryID: id, checksum: sum}
	}

	for id, cur := range s.registered {
		if _, ok := seen[id]; !ok {
			s.cron.Remove(cur.entryID)
			delete(s.registered, id)
		}
	}
	return nil
}

func (s *scheduler) fire(ctx context.Context, t triggers.Trigger) error {
	a, err := s.agents.Get(ctx, t.TenantID, t.AgentID)
	if err != nil {
		return fmt.Errorf("resolve agent: %w", err)
	}
	if a.Runtime != "container" || a.Image == nil || *a.Image == "" {
		return fmt.Errorf("agent %s not runnable in v1 spine", a.ID)
	}

	traceID := uuid.NewString()
	run, err := s.runs.Create(ctx, t.TenantID, a.ID, nil, traceID)
	if err != nil {
		return fmt.Errorf("insert run: %w", err)
	}

	if err := s.bus.Publish(ctx, events.SubjectRunRequested, events.RunRequested{
		TenantID:     t.TenantID,
		AgentID:      a.ID,
		RunID:        run.ID,
		Image:        *a.Image,
		TraceID:      traceID,
		TraceContext: telemetry.Inject(ctx),
		At:           time.Now().UTC(),
	}); err != nil {
		return fmt.Errorf("publish: %w", err)
	}
	log.Printf("cron trigger=%s agent=%s run=%s queued", t.Name, a.Name, run.ID)
	return nil
}

func checksum(agentID, cronExpr string) string {
	h := sha256.Sum256([]byte(agentID + "|" + cronExpr))
	return fmt.Sprintf("%x", h[:8])
}

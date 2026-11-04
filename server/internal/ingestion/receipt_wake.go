package ingestion

import (
	"context"
	"sync"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
	workerloop "github.com/PorticoMediaServer/portico/server/internal/worker"
)

type receiptWaiter struct {
	receipt          compactcatalog.InfoInputReceipt
	ready, delivered bool
}

// A generation owns its subscription and pending observations. Nothing here
// acknowledges an input or replaces the accepting writer's authority check.
type receiptWakeGeneration struct {
	ctx     context.Context
	mu      sync.Mutex
	pending map[acceptedKey]*receiptWaiter
	changed *workerloop.Signal
	notify  func()
	check   func(context.Context, acceptedKey, compactcatalog.InfoInputReceipt) bool
}

func (s *Service) startReceiptWake(ctx context.Context, urgent *workerloop.Signal) func() {
	life, cancel := context.WithCancel(ctx)
	g := &receiptWakeGeneration{ctx: life, pending: map[acceptedKey]*receiptWaiter{}, changed: workerloop.NewSignal(), notify: urgent.Wake, check: s.receiptApplied}
	// Activation directly advances catalog_assets.info_epoch. Read-only snapshots
	// and unrelated trigger neighbours must not become a self-wake loop.
	unregister := dbwork.WakeOnTableWrites(g.changed, "catalog_assets")
	s.reuseMu.Lock()
	s.receiptWake = g
	for key, a := range s.accepted {
		g.register(key, a.receipt)
	}
	s.reuseMu.Unlock()
	done := make(chan struct{})
	supervise.Go("ingestion.receipt-wake", func() {
		defer close(done)
		for g.changed.Wait(life) {
			g.inspect()
		}
	})
	return func() {
		s.reuseMu.Lock()
		if s.receiptWake == g {
			s.receiptWake = nil
		}
		s.reuseMu.Unlock()
		cancel()
		unregister()
		<-done
	}
}
func sameReceipt(a, b compactcatalog.InfoInputReceipt) bool {
	return a.ChangeID == b.ChangeID && a.InputRevision == b.InputRevision && a.ExpectedEpoch == b.ExpectedEpoch
}
func (s *Service) currentReceiptWake() *receiptWakeGeneration {
	s.reuseMu.Lock()
	defer s.reuseMu.Unlock()
	return s.receiptWake
}
func (s *Service) keepPendingReceipt(key acceptedKey, facts acceptedAnalysis, receipt compactcatalog.InfoInputReceipt) {
	s.keepAccepted(key, facts.facts, facts.completed)
	s.reuseMu.Lock()
	a, retained := s.accepted[key]
	if retained {
		a.receipt = receipt
		s.accepted[key] = a
	}
	g := s.receiptWake
	s.reuseMu.Unlock()
	if retained && g != nil {
		g.register(key, receipt)
	}
}
func (g *receiptWakeGeneration) register(key acceptedKey, receipt compactcatalog.InfoInputReceipt) {
	if g.ctx.Err() != nil || receipt.State != compactcatalog.InfoInputAcceptedPending || receipt.ChangeID <= 0 {
		return
	}
	g.mu.Lock()
	if old := g.pending[key]; old != nil && sameReceipt(old.receipt, receipt) {
		g.mu.Unlock()
		return
	}
	_, exists := g.pending[key]
	if exists || len(g.pending) < maxAcceptedAnalyses {
		g.pending[key] = &receiptWaiter{receipt: receipt}
	}
	g.mu.Unlock()
	// Covers apply-before-registration without another timer.
	g.changed.Wake()
}
func (g *receiptWakeGeneration) inspect() {
	if g.ctx.Err() != nil {
		return
	}
	g.mu.Lock()
	todo := make(map[acceptedKey]*receiptWaiter)
	for key, w := range g.pending {
		if !w.ready {
			todo[key] = w
		}
	}
	g.mu.Unlock()
	for key, w := range todo {
		if g.ctx.Err() != nil {
			return
		}
		if !g.check(g.ctx, key, w.receipt) {
			continue
		}
		g.mu.Lock()
		if g.ctx.Err() == nil && g.pending[key] == w && !w.ready {
			w.ready = true
			g.notify()
		}
		g.mu.Unlock()
	}
}
func (s *Service) claimReceiptWake(job string) {
	if g := s.currentReceiptWake(); g != nil {
		g.mu.Lock()
		for key, w := range g.pending {
			if key.job == job && w.ready {
				w.delivered = true
			}
		}
		g.mu.Unlock()
	}
}
func (s *Service) releaseReceiptWake(job string) {
	if g := s.currentReceiptWake(); g != nil && g.ctx.Err() == nil {
		g.mu.Lock()
		defer g.mu.Unlock()
		for key, w := range g.pending {
			if key.job == job && w.ready && !w.delivered {
				g.notify()
				return
			}
		}
	}
}
func (s *Service) forgetReceiptWake(key acceptedKey) {
	if g := s.currentReceiptWake(); g != nil {
		g.mu.Lock()
		delete(g.pending, key)
		g.mu.Unlock()
	}
}

func (s *Service) receiptApplied(ctx context.Context, key acceptedKey, want compactcatalog.InfoInputReceipt) bool {
	ready := false
	err := dbwork.WithReadSnapshot(ctx, s.db, func(ctx context.Context) error {
		tx := dbwork.Snapshot(ctx)
		var matches bool
		err := tx.QueryRowContext(ctx, `SELECT EXISTS(
 SELECT 1 FROM catalog_info_changes c
 JOIN catalog_info_input_witnesses w ON w.change_id=c.id AND w.ordinal=0
 JOIN catalog_assets a ON a.id=c.asset_id AND a.token=c.asset_token
 JOIN inventory_runs r ON r.job_id=w.job_id
 JOIN jobs j ON j.id=r.job_id
 JOIN library_sources source ON source.id=r.source_id
 JOIN library_scan_policies policy ON policy.library_id=source.library_id
 JOIN inventory_objects object ON object.id=w.object_id
 WHERE c.id=? AND c.input_revision=? AND c.expected_epoch=? AND c.phase>=4
 AND a.info_epoch=c.expected_epoch+1
 AND w.kind=? AND w.job_id=? AND w.object_id=? AND w.object_revision=? AND w.policy_revision=?
 AND j.status='running' AND source.enabled=1
 AND r.source_generation=source.generation AND r.root_incarnation=source.incarnation AND r.root_identity=source.root_identity
 AND w.source_id=source.id AND w.source_generation=source.generation AND w.root_incarnation=source.incarnation
 AND object.source_id=source.id AND object.root_incarnation=source.incarnation AND object.revision=w.object_revision
 AND object.asset_id=a.token AND object.state='available' AND object.retired=0 AND policy.revision=w.policy_revision)`, want.ChangeID, want.InputRevision, want.ExpectedEpoch, compactcatalog.InfoWitnessInventory, key.job, key.object, key.revision, key.policy).Scan(&matches)
		if err != nil || !matches {
			return err
		}
		receipt, err := compactcatalog.InfoInputReceiptTx(ctx, tx, want.ChangeID)
		ready = err == nil && receipt.State == compactcatalog.InfoInputApplied && sameReceipt(receipt, want)
		return err
	})
	return err == nil && ready && ctx.Err() == nil
}

func (s *Service) forgetReceiptJob(job string) {
	if g := s.currentReceiptWake(); g != nil {
		g.mu.Lock()
		for key := range g.pending {
			if key.job == job {
				delete(g.pending, key)
			}
		}
		g.mu.Unlock()
	}
}

package ingestion

import (
	"context"
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/mediaanalysis"
	workerloop "github.com/PorticoMediaServer/portico/server/internal/worker"
	"sync/atomic"
	"testing"
	"time"
)

func receiptWakeFixture(t *testing.T) (*loopFixture, Job, acceptedKey, assets.Facts, compactcatalog.InfoInputReceipt) {
	t.Helper()
	f := newLoopFixture(t, "movie", "Film (2000).mkv")
	f.finish(f.queue())
	f.exec(`UPDATE library_scan_policies SET tier='basic',operations_json='["probe"]' WHERE library_id=?`, f.library)
	job := f.queue()
	f.until(job, "basic_pending")
	key := acceptedKey{job: job.ID}
	var size, modified int64
	if err := f.db.QueryRow(`SELECT id,revision,size,modified_ns FROM inventory_objects LIMIT 1`).Scan(&key.object, &key.revision, &size, &modified); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRow(`SELECT revision FROM library_scan_policies WHERE library_id=?`, f.library).Scan(&key.policy); err != nil {
		t.Fatal(err)
	}
	facts := assets.Facts{Container: "mkv", VideoCodec: "h264", AudioCodec: "aac", Duration: 120, ObservedSize: size, ObservedModifiedNS: modified, AnalysisOperations: []string{"probe"}, AnalysisAlgorithm: mediaanalysis.Algorithm, Streams: []assets.Stream{{Index: 0, Type: "video", Codec: "h264"}, {Index: 1, Type: "audio", Codec: "aac"}}}
	receipt, err := f.s.catalog.AcceptInventoryAnalysis(context.Background(), key.job, key.object, key.revision, key.policy, facts)
	if err != nil || receipt.State != compactcatalog.InfoInputAcceptedPending || receipt.ChangeID <= 0 {
		t.Fatal("fixture receipt", receipt, err)
	}
	return f, job, key, facts, receipt.InfoInputReceipt
}
func applyReceiptForWake(t *testing.T, f *loopFixture, key acceptedKey, receipt compactcatalog.InfoInputReceipt) {
	t.Helper()
	worker := compactcatalog.NewWorker(f.db)
	for turn := 0; turn < 1000; turn++ {
		if f.s.receiptApplied(context.Background(), key, receipt) {
			return
		}
		if _, err := worker.Step(context.Background(), 1); err != nil {
			t.Fatal(err)
		}
	}
	t.Fatal("exact receipt never applied before GC")
}
func waitForReceiptCondition(t *testing.T, what string, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal(what)
}
func TestReceiptWakeRequiresAppliedExactLiveInput(t *testing.T) {
	f, job, key, _, receipt := receiptWakeFixture(t)
	if f.s.receiptApplied(context.Background(), key, receipt) {
		t.Fatal("accepted-pending woke scanner")
	}
	applyReceiptForWake(t, f, key, receipt)
	for _, bad := range []acceptedKey{{job: "other", object: key.object, revision: key.revision, policy: key.policy}, {job: key.job, object: "other", revision: key.revision, policy: key.policy}, {job: key.job, object: key.object, revision: "other", policy: key.policy}, {job: key.job, object: key.object, revision: key.revision, policy: key.policy + 1}} {
		if f.s.receiptApplied(context.Background(), bad, receipt) {
			t.Fatal("wrong witness woke", bad)
		}
	}
	for field := 0; field < 3; field++ {
		bad := receipt
		switch field {
		case 0:
			bad.ChangeID++
		case 1:
			bad.InputRevision++
		case 2:
			bad.ExpectedEpoch++
		}
		if f.s.receiptApplied(context.Background(), key, bad) {
			t.Fatal("wrong receipt woke", field)
		}
	}
	f.exec(`UPDATE jobs SET status='canceled' WHERE id=?`, job.ID)
	if f.s.receiptApplied(context.Background(), key, receipt) {
		t.Fatal("canceled job woke")
	}
}
func TestReceiptWakeRejectsReplacedSourceGeneration(t *testing.T) {
	f, _, key, _, receipt := receiptWakeFixture(t)
	applyReceiptForWake(t, f, key, receipt)
	f.exec(`UPDATE library_sources SET generation=generation+1 WHERE id=?`, f.source)
	if f.s.receiptApplied(context.Background(), key, receipt) {
		t.Fatal("old source generation woke")
	}
}
func TestReceiptWakeDuplicateHintAndActiveReleaseAreHarmless(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	signal := workerloop.NewSignal()
	key := acceptedKey{job: "job", object: "object", revision: "r", policy: 1}
	g := &receiptWakeGeneration{ctx: ctx, pending: map[acceptedKey]*receiptWaiter{key: {receipt: compactcatalog.InfoInputReceipt{ChangeID: 1, InputRevision: 1, ExpectedEpoch: 1}}}, changed: workerloop.NewSignal(), notify: signal.Wake, check: func(context.Context, acceptedKey, compactcatalog.InfoInputReceipt) bool { return true }}
	s := &Service{receiptWake: g}
	g.inspect()
	if !signal.Take() {
		t.Fatal("no exact wake")
	}
	for n := 0; n < 8; n++ {
		g.inspect()
	}
	if signal.Take() {
		t.Fatal("duplicate hint re-woke input")
	}
	s.releaseReceiptWake("job")
	if !signal.Take() {
		t.Fatal("early wake lost across active release")
	}
	s.claimReceiptWake("job")
	for n := 0; n < 8; n++ {
		s.releaseReceiptWake("job")
		g.inspect()
	}
	if signal.Take() {
		t.Fatal("refused resume spun")
	}
	s.forgetReceiptJob("job")
	if len(g.pending) != 0 {
		t.Fatal("job cleanup retained waiter")
	}
}
func TestReceiptWakeOldGenerationCannotSignalReplacement(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	signal := workerloop.NewSignal()
	key := acceptedKey{job: "job"}
	g := &receiptWakeGeneration{ctx: ctx, pending: map[acceptedKey]*receiptWaiter{key: {}}, notify: signal.Wake, check: func(context.Context, acceptedKey, compactcatalog.InfoInputReceipt) bool { return true }}
	cancel()
	g.inspect()
	if signal.Take() {
		t.Fatal("canceled worker generation woke")
	}
}
func TestReceiptWakeResumesAppliedAnalysisWithoutTimers(t *testing.T) {
	withoutTimers(t)
	// Park the loop after a genuine no-progress turn. Otherwise a previous
	// inventory continuation can accidentally satisfy a missing-receipt wake.
	originalQuantum := quantumRun
	var resume atomic.Bool
	var attempts atomic.Int64
	quantumRun = func(s *Service, ctx context.Context, job, library string) bool {
		attempts.Add(1)
		if !resume.Load() {
			return false
		}
		return originalQuantum(s, ctx, job, library)
	}
	t.Cleanup(func() { quantumRun = originalQuantum })
	f, job, key, facts, receipt := receiptWakeFixture(t)
	f.s.keepPendingReceipt(key, acceptedAnalysis{facts: facts, completed: []string{"probe"}}, receipt)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); f.s.Run(ctx) }()
	defer func() { cancel(); <-done }()
	waitForReceiptCondition(t, "no pending receipt", func() bool {
		g := f.s.currentReceiptWake()
		if g == nil {
			return false
		}
		g.mu.Lock()
		defer g.mu.Unlock()
		return g.pending[key] != nil
	})
	waitForReceiptCondition(t, "initial no-progress quantum did not settle", func() bool { f.s.mu.Lock(); defer f.s.mu.Unlock(); return attempts.Load() > 0 && len(f.s.active) == 0 })
	resume.Store(true)
	applyReceiptForWake(t, f, key, receipt)
	waitForReceiptCondition(t, "applied input waited on fallback", func() bool { j, e := f.s.Get(job.ID); return e == nil && j.Analyzed == 1 })
	g := f.s.currentReceiptWake()
	g.mu.Lock()
	for n := 0; n < 8; n++ {
		g.notify()
	}
	g.mu.Unlock()
	waitForReceiptCondition(t, "duplicate wake stopped completion", func() bool { j, e := f.s.Get(job.ID); return e == nil && j.Status == "complete" && j.Analyzed == 1 })
	if n := f.count(`SELECT count(*) FROM inventory_analysis_queue WHERE job_id=? AND state='done'`, job.ID); n != 1 {
		t.Fatal("not one ACK", n)
	}
}
func TestReceiptWakeLostSignalUsesUnchangedFallback(t *testing.T) {
	if quantumPoll != 300*time.Millisecond {
		t.Fatal("poll interval changed", quantumPoll)
	}
	f, job, key, facts, receipt := receiptWakeFixture(t)
	f.s.keepPendingReceipt(key, acceptedAnalysis{facts: facts, completed: []string{"probe"}}, receipt)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); f.s.Run(ctx) }()
	defer func() { cancel(); <-done }()
	waitForReceiptCondition(t, "no waiter", func() bool {
		g := f.s.currentReceiptWake()
		if g == nil {
			return false
		}
		g.mu.Lock()
		defer g.mu.Unlock()
		if g.pending[key] == nil {
			return false
		}
		g.notify = func() {}
		return true
	})
	applyReceiptForWake(t, f, key, receipt)
	waitForReceiptCondition(t, "lost wake prevented fallback", func() bool { j, e := f.s.Get(job.ID); return e == nil && j.Analyzed == 1 })
	if n := f.count(`SELECT count(*) FROM inventory_analysis_queue WHERE job_id=? AND state='done'`, job.ID); n != 1 {
		t.Fatal("lost wake duplicated ACK", n)
	}
}

func TestReceiptWakeApplyBeforeRegistrationIsNotLost(t *testing.T) {
	f, _, key, facts, receipt := receiptWakeFixture(t)
	applyReceiptForWake(t, f, key, receipt)
	signal := workerloop.NewSignal()
	stop := f.s.startReceiptWake(context.Background(), signal)
	defer stop()
	f.s.keepPendingReceipt(key, acceptedAnalysis{facts: facts, completed: []string{"probe"}}, receipt)
	waitForReceiptCondition(t, "already-applied input lost at registration", signal.Take)
}
func TestReceiptWakeCanceledInFlightCheckCannotNotify(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	signal := workerloop.NewSignal()
	key := acceptedKey{job: "old"}
	entered, release := make(chan struct{}), make(chan struct{})
	g := &receiptWakeGeneration{ctx: ctx, pending: map[acceptedKey]*receiptWaiter{key: {}}, notify: signal.Wake, check: func(context.Context, acceptedKey, compactcatalog.InfoInputReceipt) bool {
		close(entered)
		<-release
		return true
	}}
	done := make(chan struct{})
	go func() { defer close(done); g.inspect() }()
	<-entered
	cancel()
	close(release)
	<-done
	if signal.Take() {
		t.Fatal("old generation's late check emitted wake")
	}
}
func TestReceiptWakeRechecksCurrentPolicy(t *testing.T) {
	f, _, key, _, receipt := receiptWakeFixture(t)
	applyReceiptForWake(t, f, key, receipt)
	f.exec(`UPDATE library_scan_policies SET revision=revision+1 WHERE library_id=?`, f.library)
	if f.s.receiptApplied(context.Background(), key, receipt) {
		t.Fatal("old policy input woke")
	}
}

func TestReceiptWakeDoesNotFollowSupersededRegistration(t *testing.T) {
	signal := workerloop.NewSignal()
	key := acceptedKey{job: "job"}
	entered, release := make(chan struct{}), make(chan struct{})
	g := &receiptWakeGeneration{ctx: context.Background(), pending: map[acceptedKey]*receiptWaiter{key: {receipt: compactcatalog.InfoInputReceipt{ChangeID: 1}}}, notify: signal.Wake, check: func(context.Context, acceptedKey, compactcatalog.InfoInputReceipt) bool {
		close(entered)
		<-release
		return true
	}}
	done := make(chan struct{})
	go func() { defer close(done); g.inspect() }()
	<-entered
	g.mu.Lock()
	g.pending[key] = &receiptWaiter{receipt: compactcatalog.InfoInputReceipt{ChangeID: 2}}
	g.mu.Unlock()
	close(release)
	<-done
	if signal.Take() {
		t.Fatal("superseded receipt fired")
	}
	g.check = func(_ context.Context, _ acceptedKey, r compactcatalog.InfoInputReceipt) bool { return r.ChangeID == 2 }
	g.inspect()
	if !signal.Take() {
		t.Fatal("replacement receipt did not wake")
	}
}

func TestReceiptWakeReplacementRetainsExistingBound(t *testing.T) {
	g := &receiptWakeGeneration{ctx: context.Background(), pending: map[acceptedKey]*receiptWaiter{}, changed: workerloop.NewSignal()}
	for n := 0; n < maxAcceptedAnalyses; n++ {
		g.pending[acceptedKey{policy: int64(n)}] = &receiptWaiter{receipt: compactcatalog.InfoInputReceipt{ChangeID: 1}}
	}
	key := acceptedKey{policy: 0}
	replacement := compactcatalog.InfoInputReceipt{State: compactcatalog.InfoInputAcceptedPending, ChangeID: 2, InputRevision: 2, ExpectedEpoch: 3}
	g.register(key, replacement)
	if g.pending[key].receipt.ChangeID != 2 || len(g.pending) != maxAcceptedAnalyses {
		t.Fatal("bounded replacement lost")
	}
	g.register(acceptedKey{job: "new"}, replacement)
	if len(g.pending) != maxAcceptedAnalyses {
		t.Fatal("retention capacity grew")
	}
}

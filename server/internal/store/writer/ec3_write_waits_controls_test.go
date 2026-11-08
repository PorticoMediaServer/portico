package writer_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/qual"
	"github.com/PorticoMediaServer/portico/server/internal/store/qual/workload"
	"github.com/PorticoMediaServer/portico/server/internal/store/writer"
)

// Entirely fabricated owner facts; no native command or qualification samples.
func writeWaitControl(op workload.EC2Operation, start time.Time, queue, service, tail time.Duration) workload.Observation {
	hold := start.Add(queue)
	attempt := hold.Add(service / 2)
	commit := hold.Add(service)
	physical := commit.Add(tail)
	returned := physical.Add(time.Nanosecond)
	purpose, class, durability := writer.ProgressAccept, governor.Foreground, writer.Normal
	if op == workload.EC2Security {
		purpose, class, durability = writer.SecurityAccept, governor.Recovery, writer.Full
	}
	ref := writer.ReceiptReference{Identity: writer.CommandIdentity{Sequence: 1}, RequestDigest: writer.RequestDigest{1}}
	sync := 1
	if durability == writer.Full {
		sync = 2
	}
	e := &writer.ExecutionEvidence{Purpose: purpose, Class: class, Durability: durability, HoldStartedAt: hold, CommitAttemptedAt: &attempt, CommitReturnedAt: &commit, PhysicalReturnedAt: &physical, Commit: writer.CommitSucceeded, Policy: &sqliteconn.Settings{Engine: "synthetic", Synchronous: sync}}
	return workload.Observation{Requested: &ref, CallStarted: &start, CallReturned: &returned, Evidence: e, Outcome: &writer.ReceiptOutcome{State: writer.Applied, Reference: ref}}
}
func TestEC3WriteWaitSplitAndZeroQueue(t *testing.T) {
	start := time.Now()
	o := writeWaitControl(workload.EC2Security, start, 6*time.Millisecond, 29*time.Millisecond, 5*time.Millisecond)
	f := foregroundWait(workload.EC2Security, workload.Offer{ScheduledAt: start.Add(-time.Millisecond)}, start, o)
	if !f.Complete || *f.QueueWaitNS != 6*time.Millisecond || *f.HoldToCommitNS != 29*time.Millisecond || *f.SubmissionToCommitNS != 35*time.Millisecond || *f.PhysicalHoldNS != 34*time.Millisecond || *f.QueueWaitNS+*f.HoldToCommitNS != *f.SubmissionToCommitNS {
		t.Fatal(f)
	}
	o = writeWaitControl(workload.EC2ProgressDrain, start, 0, time.Millisecond, 0)
	zero := foregroundWait(workload.EC2ProgressDrain, workload.Offer{ScheduledAt: start}, start, o)
	if !zero.Complete || zero.QueueWaitNS == nil || *zero.QueueWaitNS != 0 {
		t.Fatal(zero)
	}
	ledger := newForegroundWriteLedger()
	ledger.facts = []foregroundWriteWait{f, zero}
	report := ledger.report(2, true)
	if !report.CoverageComplete || report.QueueWait.P50NS != 0 || report.SubmissionToCommit.P99NS != 35*time.Millisecond || report.ObservedP99WithinTarget == nil || !*report.ObservedP99WithinTarget || report.ReadsUnaffected != nil {
		t.Fatal(report)
	}
	if partial := ledger.report(3, false); partial.CoverageComplete || partial.ObservedP99WithinTarget != nil {
		t.Fatal(partial)
	}
}
func TestEC3WriteWaitGapsAndReplayNeverBecomeZero(t *testing.T) {
	start := time.Now()
	offer := workload.Offer{Stream: "foreground-writes", ScheduledAt: start, Slot: 1}
	failed := foregroundWait(workload.EC2Security, offer, start, workload.Observation{CallError: "ENTIREZERO error"})
	if failed.Complete || failed.QueueWaitNS != nil || failed.SubmissionToCommitNS != nil {
		t.Fatal(failed)
	}
	o := writeWaitControl(workload.EC2Security, start, time.Millisecond, 80*time.Millisecond, 0)
	o.Outcome.Replayed = true
	replay := foregroundWait(workload.EC2Security, offer, start, o)
	ledger := newForegroundWriteLedger()
	ledger.facts = []foregroundWriteWait{failed, replay}
	report := ledger.report(2, true)
	if report.CoverageComplete || report.Incomplete != 2 || report.SubmissionToCommit.P99NS != 81*time.Millisecond || report.ObservedP99WithinTarget != nil {
		t.Fatal(report)
	}
	stream, l, err := foregroundWriteStream(time.Millisecond, []workload.EC2Operation{workload.EC2Security}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var raw workload.Observation
	err = stream.Pattern[0].Run(t.Context(), offer, func(o workload.Observation) error { raw = o; return nil })
	if !errors.Is(err, workload.ErrWCComposition) || !raw.Stub || len(l.facts) != 1 || l.facts[0].SubmissionToCommitNS != nil || l.report(1, true).ObservedP99WithinTarget != nil {
		t.Fatal(raw, l, err)
	}
}

func TestEC3SecurityTailCannotHideInProgressPopulation(t *testing.T) {
	ledger := newForegroundWriteLedger()
	start := time.Now()
	offer := workload.Offer{ScheduledAt: start}
	for i := 0; i < 99; i++ {
		ledger.facts = append(ledger.facts, foregroundWait(workload.EC2ProgressDrain, offer, start, writeWaitControl(workload.EC2ProgressDrain, start, 0, time.Millisecond, 0)))
	}
	ledger.facts = append(ledger.facts, foregroundWait(workload.EC2Security, offer, start, writeWaitControl(workload.EC2Security, start, 0, 100*time.Millisecond, 0)))
	r := ledger.report(100, true)
	if r.SubmissionToCommit.P99NS != time.Millisecond || r.ObservedP99WithinTarget == nil || *r.ObservedP99WithinTarget || r.ByOperation[workload.EC2Security].SubmissionToCommit.P99NS != 100*time.Millisecond {
		t.Fatal(r)
	}
}

type writeWaitControlSink struct{ records []workload.Record }

func (s *writeWaitControlSink) Append(r workload.Record) error {
	s.records = append(s.records, r)
	return nil
}
func TestEC3IndependentSubmissionLeavesReadsRunning(t *testing.T) {
	scanStarted, submitted, readDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	stop := errors.New("synthetic stop, no native samples")
	fore := func(_ context.Context, _ workload.Offer, emit func(workload.Observation) error) error {
		<-scanStarted
		close(submitted)
		<-readDone
		return errors.Join(stop, emit(workload.Observation{Phase: "CONTROL", CallError: stop.Error()}))
	}
	stream, ledger, err := foregroundWriteStream(time.Second, []workload.EC2Operation{workload.EC2Security}, workload.EC2Bindings{workload.EC2Security: fore})
	if err != nil {
		t.Fatal(err)
	}
	scan := func(_ context.Context, _ workload.Offer, emit func(workload.Observation) error) error {
		close(scanStarted)
		<-submitted
		<-readDone
		return emit(workload.Observation{Phase: "SCAN_CONTROL"})
	}
	read := func(_ context.Context, _ workload.Offer, emit func(workload.Observation) error) error {
		<-submitted
		close(readDone)
		return emit(workload.Observation{Phase: "READ_CONTROL"})
	}
	cfg := workload.Config{RunID: "EC3_CONTROL", Duration: time.Second, Cell: qual.RestartCell{ID: "CONTROL", HostID: "CONTROL", DatabaseID: "CONTROL", Profile: "fabricated control", ManifestSHA256: strings.Repeat("a", 64), CacheState: "warm", CadenceMode: "accelerated"}}
	sink := &writeWaitControlSink{}
	err = workload.Run(t.Context(), cfg, []workload.Stream{{Name: "scan", Every: time.Second, Pattern: []workload.Step{{Name: "scan", Run: scan}}}, stream, {Name: "reads", Every: time.Second, Pattern: []workload.Step{{Name: "read", Run: read}}}}, sink)
	if !errors.Is(err, stop) || len(ledger.facts) != 1 || ledger.report(1, false).ObservedP99WithinTarget != nil {
		t.Fatal(err, ledger)
	}
	select {
	case <-readDone:
	default:
		t.Fatal("foreground submission blocked reads behind scan")
	}
}

func TestEC3PreparationSnapshotCannotStandInForAccept(t *testing.T) {
	ledger := newForegroundWriteLedger()
	action := ledger.action(workload.EC2ProgressCapture, func(ctx context.Context, offer workload.Offer, emit func(workload.Observation) error) error {
		snapshot := writeWaitControl(workload.EC2ProgressCapture, time.Now(), 0, time.Millisecond, 0)
		snapshot.Phase = "SNAPSHOT"
		snapshot.Requested = &writer.ReceiptReference{Identity: writer.CommandIdentity{Sequence: 11}}
		if err := emit(snapshot); err != nil {
			return err
		}
		accepted := writeWaitControl(workload.EC2ProgressCapture, time.Now(), 0, 2*time.Millisecond, 0)
		accepted.Phase = "ACCEPT"
		accepted.Requested = &writer.ReceiptReference{Identity: writer.CommandIdentity{Sequence: 12}}
		return emit(accepted)
	})
	err := action(t.Context(), workload.Offer{Stream: "control", Slot: 1, ScheduledAt: time.Now()}, func(workload.Observation) error { return nil })
	if err != nil || len(ledger.facts) != 1 || ledger.facts[0].HoldToCommitNS == nil || *ledger.facts[0].HoldToCommitNS != 2*time.Millisecond {
		t.Fatal("preparation selected as user write", ledger, err)
	}
}

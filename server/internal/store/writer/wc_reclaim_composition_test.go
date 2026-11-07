package writer

import (
	"context"
	"crypto/rand"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"reflect"
	"testing"
	"time"
)

func wcReclaimCompositionOpening(t *testing.T, principalLifetimes ...context.Context) (*Writer, *WCProgressDriver, *governor.Authority) {
	t.Helper()
	initial, path := testCanonicalExecutor(t)
	if err := initial.close(); err != nil {
		t.Fatal(err)
	}
	opening := openScanFixture(t, path, scanOpeningOwner(t, path))
	e := opening.executor
	defs := append(enrichmentEffectiveBeginDefinitions(), enrichmentEffectiveProducerDefinitions()...)
	defs = append(defs, effectiveDispatchDefinitions()...)
	defs = append(defs, wcProgressCommandDefinitions()...)
	defs = append(defs, wcProgressBirthDefinitions()...)
	for _, def := range wcReclaimCommandDefinitions() {
		found := false
		for _, old := range defs {
			if old.ID == def.ID {
				if !reflect.DeepEqual(old, def) {
					t.Fatal("conflicting definition", def.ID)
				}
				found = true
				break
			}
		}
		if !found {
			defs = append(defs, def)
		}
	}
	conn, err := e.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	golden := captureEffectiveFixture(t, conn, e.config, defs, "wc_reclaim_composition_test.go", "wc_progress_fixture.go")
	if err = conn.Close(); err != nil {
		t.Fatal(err)
	}
	p := effectiveFixturePlan()
	p.registration = golden.Receipt()
	wc := p
	wc.steps = nil
	wc.wcProgressMemory = commandMemoryBudget{64 << 20, 64 << 20}
	for _, d := range wcProgressCommandDefinitions() {
		wc.steps = append(wc.steps, statementStep{d.ID, Work{16, 4000, 16, 65536, 100 * time.Microsecond}})
	}
	wal, err := governor.NewWALGate(governor.WALPolicy{HardBytes: 1 << 29, ReserveBytes: 1 << 20, SoftDebtBytes: 1 << 26, ActivePressureBytes: 1 << 28, MaxSampleAge: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	var partitions [4]governor.Amounts
	for i := range partitions {
		partitions[i] = p.need
		partitions[i][governor.MemoryBytes] += 1 << 20
	}
	g, a, err := governor.New(governor.Config{Partitions: partitions, MaxGrantsPerClass: 2, Now: time.Now, WAL: wal})
	if err != nil {
		t.Fatal(err)
	}
	r, err := newEffectiveRuntime(e, path, g, a, wal, 1, p)
	if err != nil {
		t.Fatal(err)
	}
	birth := wc
	birth.steps = nil
	for _, d := range wcProgressBirthDefinitions() {
		birth.steps = append(birth.steps, statementStep{d.ID, Work{16, 4000, 16, 65536, 100 * time.Microsecond}})
	}
	if err = composeWCProgress(t.Context(), r, wc, birth); err != nil {
		t.Fatal(err)
	}
	w := &Writer{effective: r}
	member, err := wcRandomBirth()
	if err != nil {
		t.Fatal(err)
	}
	profile, err := wcRandomBirth()
	if err != nil {
		t.Fatal(err)
	}
	scope := wcProgressScope{primary: 1, primaryProfile: "profile-one", membershipRole: "member", horizon: time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano), expires: time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano), playbackID: "playback-音", profileKey: "viewer-one", accountID: "account-one", profileID: "profile-one", memberBirth: member, profileBirth: profile, libraryKey: "library-one", libraryID: 7, subjectID: 11, serverID: "server", familyID: "wc-family", tokenHash: "wc-token", epoch: 1, tokenGeneration: 1, memberRevision: 1, profileRevision: 1, role: "member", memberLibraries: `["library-one"]`}
	scope.profileKey = wcFixturePersonalOwnerKey("local", scope.accountID, scope.profileID)
	scope.expires = scope.horizon
	for _, b := range []*[16]byte{&scope.incarnation, &scope.clock, &scope.subjectPublicID} {
		if _, err = rand.Read(b[:]); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := wcSnapshot{}
	text := func(i int, s string) { snapshot[i] = wcSnapshotCell{Kind: 2, Text: s} }
	integer := func(i int, n int64) { snapshot[i] = wcSnapshotCell{Kind: 1, Integer: n} }
	text(0, time.Now().UTC().Format(time.RFC3339Nano))
	text(1, "started")
	text(2, scope.accountID)
	text(3, scope.profileID)
	text(4, scope.playbackID)
	integer(5, 1)
	integer(6, 0)
	snapshot[7] = wcSnapshotCell{Kind: 3, Blob: append([]byte(nil), scope.subjectPublicID[:]...)}
	text(8, scope.libraryKey)
	integer(9, 1)
	for _, i := range []int{21, 22, 23, 25, 26} {
		integer(i, 0)
	}
	integer(24, 100000)
	integer(32, scope.subjectID)
	integer(33, 1)
	integer(34, time.Now().UnixMilli())
	text(39, member)
	text(40, profile)
	preparation := governor.Request{Key: governor.JobKey{Domain: uint16(ProgressAccept), ID: scope.playbackID}, Owner: r.owner, Class: governor.Foreground, Deadline: time.Now().Add(time.Minute), Footprint: []governor.Scope{{Library: scope.libraryID, Domain: uint16(ProgressAccept)}}}
	preparation.Need[governor.MemoryBytes] = 1 << 20
	decision := g.Try(preparation)
	if decision.Grant == nil {
		t.Fatal(decision.Refusal)
	}
	principal := t.Context()
	if len(principalLifetimes) == 1 {
		principal = principalLifetimes[0]
	}
	d, err := newWCProgressDriver(t.Context(), principal, w, scope, snapshot, wcProgressPolicy{StartedPercent: 5, PlayedPercent: 95, VideoCompletion: "earliest"}, preparation, decision.Grant)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := d.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	if err = initializeWCProgressFixture(t.Context(), d); err != nil {
		t.Fatal(err)
	}
	// Establish a real positive playing interval before offering the first
	// report. This is fixture preparation, outside every measured Action call.
	timer := time.NewTimer(2 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-t.Context().Done():
		t.Fatal(t.Context().Err())
	}
	return w, d, a
}
func wcReclaimCompositionFixture(t *testing.T, principals ...context.Context) (*Writer, *WCReclaimDriver) {
	w, _, d := wcReclaimCompositionProgressFixture(t, principals...)
	return w, d
}

func wcReclaimCompositionProgressFixture(t *testing.T, principals ...context.Context) (*Writer, *WCProgressDriver, *WCReclaimDriver) {
	t.Helper()
	w, pd, _ := wcReclaimCompositionOpening(t, principals...)
	ctx := t.Context()
	capture, err := pd.Capture(ctx, 0, 5, 100, "playing")
	if err != nil {
		t.Fatal(err)
	}
	q, _ := ProgressReference(capture)
	if _, err = w.AcceptProgress(ctx, pd.Disclosure(), capture); err != nil {
		t.Fatal(err)
	}
	if _, err = w.AckProgress(ctx, pd.Disclosure(), q); err != nil {
		t.Fatal(err)
	}
	finished := false
	for i := 0; i < 2; i++ {
		c, e := pd.Drain(ctx)
		if e != nil {
			t.Fatal(e)
		}
		result, e := pd.ExecuteDrain(ctx, c)
		if e != nil {
			t.Fatal(e)
		}
		ref, _ := ProgressReference(c)
		if _, e = w.AckProgress(ctx, pd.Disclosure(), ref); e != nil {
			t.Fatal(e)
		}
		if result.Value.Finished.Int64 == 1 {
			finished = true
			break
		}
	}
	if !finished {
		t.Fatal("short native source did not finish within two actual quarters")
	}
	return w, pd, wcReclaimForProgressFixture(t, w, pd)
}

func wcReclaimForProgressFixture(t *testing.T, w *Writer, pd *WCProgressDriver) *WCReclaimDriver {
	t.Helper()
	ctx := t.Context()
	var err error
	r := w.effective
	p := *r.wcProgressPlan
	p.steps = nil
	p.wcProgressMemory = commandMemoryBudget{}
	p.wcReclaimMemory = commandMemoryBudget{64 << 20, 64 << 20}
	for _, d := range wcReclaimCommandDefinitions() {
		p.steps = append(p.steps, statementStep{d.ID, Work{16, 4000, 16, 65536, 100 * time.Microsecond}})
	}
	if err = composeWCReclaim(ctx, r, p); err != nil {
		t.Fatal(err)
	}
	expected := pd.expected
	expected.Key.Domain = uint16(ProgressReceiptReclaim)
	expected.Class = governor.Background
	expected.Footprint = []governor.Scope{{Library: pd.scope.libraryID, Domain: uint16(ProgressReceiptReclaim)}}
	decision := r.governor.Try(expected)
	if decision.Grant == nil {
		t.Fatal(decision.Refusal)
	}
	d, err := newWCReclaimDriver(ctx, w, pd, expected, decision.Grant)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if e := d.Close(context.Background()); e != nil {
			t.Error(e)
		}
	})
	return d
}
func TestWCReclaimCompositionOriginalReplayAckAndActualChildAbsence(t *testing.T) {
	w, d := wcReclaimCompositionFixture(t)
	before, err := w.CurrentReclaim(t.Context(), d.Disclosure())
	if err != nil {
		t.Fatal(err)
	}
	if before.ReceiptRows < 2 || !before.Due.Valid || before.Last != before.Acknowledged {
		t.Fatal(before)
	}
	for i := int64(0); i < before.ReceiptRows; i++ {
		prior, err := w.CurrentReclaim(t.Context(), d.Disclosure())
		if err != nil {
			t.Fatal(err)
		}
		c, err := d.Prepare(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		q, _ := ReclaimReference(c)
		result, err := w.AcceptReclaim(t.Context(), d.Disclosure(), c)
		if err != nil || result.Outcome.State != Applied || result.Outcome.Replayed || result.Value.RemovedCount != 1 || result.Value.RemovedSequence.Int64 != i+1 || q.Receipt.Identity.Sequence != prior.Last+1 {
			t.Fatal(result, err)
		}
		e := result.Outcome.Evidence
		if e == nil || e.Purpose != ProgressReceiptReclaim || e.Class != governor.Background || e.Durability != Normal || e.Policy == nil || e.Policy.Synchronous != 1 || e.Commit != CommitSucceeded {
			t.Fatal("not actual BG/NORMAL", e)
		}
		native, err := w.CurrentReclaim(t.Context(), d.Disclosure())
		if err != nil || native.LastRemovedChildPresent || native.ReceiptRows != prior.ReceiptRows-1 || prior.ReceiptBytes-native.ReceiptBytes != result.Value.RemovedVariableBytes || native.AfterSequence != i+1 || native.LastEvent != before.LastEvent {
			t.Fatal("physical child deletion/debit missing", native, err)
		}
		saved, err := w.ResolveReclaim(t.Context(), d.Disclosure(), q)
		if err != nil || !saved.Outcome.Replayed || saved.Value != result.Value || saved.Digest != result.Digest {
			t.Fatal(saved, err)
		}
		after, err := w.CurrentReclaim(t.Context(), d.Disclosure())
		if err != nil {
			t.Fatal(err)
		}
		native.Evidence, after.Evidence = nil, nil
		if native != after {
			t.Fatal("replay changed effects", native, after)
		}
		ack, err := w.AckReclaim(t.Context(), d.Disclosure(), q)
		if err != nil || ack.AcknowledgedThrough != q.Receipt.Identity.Sequence {
			t.Fatal(ack, err)
		}
		after, err = w.CurrentReclaim(t.Context(), d.Disclosure())
		if err != nil || after.ReceiptRows != native.ReceiptRows || after.ReceiptBytes != native.ReceiptBytes || after.ControlBytes != native.ControlBytes || after.Due != native.Due || after.Slot != native.Slot || after.LastEvent != native.LastEvent {
			t.Fatal("cleanup ACK cleared slot/refunded/redue", after, err)
		}
		retired, err := w.ResolveReclaim(t.Context(), d.Disclosure(), q)
		if err != nil || retired.Outcome.State != ResultRetired || retired.Value != (ReclaimValue{}) {
			t.Fatal(retired, err)
		}
	}
	final, err := w.CurrentReclaim(t.Context(), d.Disclosure())
	if err != nil || final.ReceiptRows != 0 || final.ReceiptBytes != 0 || final.Due.Valid || !final.SlotPresent || !final.Slot.ResultNoNext || final.LastRemovedChildPresent {
		t.Fatal(final, err)
	}
	if _, err = d.Prepare(t.Context()); !errors.Is(err, ErrConflict) {
		t.Fatal("idle loop minted cleanup work", err)
	}
}
func TestWCReclaimCompositionRetainedOwnerOutlivesCurrentUser(t *testing.T) {
	principal, cancel := context.WithCancel(t.Context())
	defer cancel()
	w, d := wcReclaimCompositionFixture(t, principal)
	cancel()
	c, err := d.Prepare(t.Context())
	if err != nil {
		t.Fatal("retained claim lost cleanup", err)
	}
	if out, e := w.AcceptReclaim(t.Context(), d.Disclosure(), c); e != nil || out.Outcome.State != Applied {
		t.Fatal(out, e)
	}
}

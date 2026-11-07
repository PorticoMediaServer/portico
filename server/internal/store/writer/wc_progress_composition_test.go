package writer

import (
	"context"
	"crypto/rand"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"reflect"
	"testing"
	"time"
)

// REAL owned opening, real clocks, native birth and real grants. This is a
// component smoke fixture with explicit prospective forecasts, not qualification.
func wcProgressCompositionFixture(t *testing.T) (*Writer, *WCProgressDriver, *governor.Authority) {
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
	conn, err := e.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	golden := captureEffectiveFixture(t, conn, e.config, defs, "wc_progress_composition_test.go", "wc_progress_fixture.go")
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
	}
	g, a, err := governor.New(governor.Config{Partitions: partitions, MaxGrantsPerClass: 2, Now: time.Now, WAL: wal})
	if err != nil {
		t.Fatal(err)
	}
	r, err := newEffectiveRuntime(e, path, g, a, wal, 1, p)
	if err != nil {
		t.Fatal(err)
	}
	if err = composeWCProgress(t.Context(), r, wc); err != nil {
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
	scope := wcProgressScope{playbackID: "playback-音", profileKey: "viewer-one", accountID: "account-one", profileID: "profile-one", memberBirth: member, profileBirth: profile, libraryKey: "library-one", libraryID: 7, subjectID: 11, serverID: "server", familyID: "wc-family", tokenHash: "wc-token", epoch: 1, tokenGeneration: 1, memberRevision: 1, profileRevision: 1, role: "member", memberLibraries: `["library-one"]`}
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
	d, err := newWCProgressDriver(t.Context(), w, scope, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Close)
	if err = initializeWCProgressFixture(t.Context(), d); err != nil {
		t.Fatal(err)
	}
	return w, d, a
}
func TestWCProgressCompositionOriginalResolveAckAndDrain(t *testing.T) {
	w, d, a := wcProgressCompositionFixture(t)
	ctx := t.Context()
	before, err := w.CurrentProgress(ctx, d.Disclosure())
	if err != nil || before.Pending || before.Last != 0 || before.PlayedMS != 0 {
		t.Fatal(before, err)
	}
	command, err := d.Capture(ctx, 1, 100, 1, false, "playing")
	if err != nil {
		t.Fatal(err)
	}
	reference, err := ProgressReference(command)
	if err != nil {
		t.Fatal(err)
	}
	first, err := w.AcceptProgress(ctx, d.Disclosure(), command)
	if err != nil || first.Outcome.State != Applied || first.Outcome.Replayed || first.Value.CommandKind != "capture_report" {
		t.Fatal(first, err)
	}
	if first.Outcome.Evidence == nil || first.Outcome.Evidence.Purpose != ProgressAccept || first.Outcome.Evidence.Class != governor.Foreground || first.Outcome.Evidence.Policy.Synchronous != 1 || first.Outcome.Evidence.Commit != CommitSucceeded {
		t.Fatal(first.Outcome.Evidence)
	}
	replay, err := w.ResolveProgress(ctx, d.Disclosure(), reference)
	if err != nil || !replay.Outcome.Replayed || !reflect.DeepEqual(first.Value, replay.Value) || first.Digest != replay.Digest {
		t.Fatal(replay, err)
	}
	floor, err := w.AckProgress(ctx, d.Disclosure(), reference)
	if err != nil || floor.AcknowledgedThrough != 1 {
		t.Fatal(floor, err)
	}
	pending, err := w.CurrentProgress(ctx, d.Disclosure())
	if err != nil || !pending.Pending || pending.PlayedMS != 0 || pending.ReceiptRows != 1 || !pending.Due.Valid {
		t.Fatal(pending, err)
	}
	drain, err := d.Drain(ctx)
	if err != nil {
		t.Fatal(err)
	}
	drained, err := w.AcceptProgress(ctx, d.Disclosure(), drain)
	if err != nil || drained.Outcome.State != Applied || drained.Value.CreditedMs.Int64 <= 0 {
		t.Fatal(drained, err)
	}
	request, _ := ProgressReference(drain)
	saved, err := w.ResolveProgress(ctx, d.Disclosure(), request)
	if err != nil || saved.Value != drained.Value || saved.Digest != drained.Digest {
		t.Fatal(saved, err)
	}
	floor, err = w.AckProgress(ctx, d.Disclosure(), request)
	if err != nil || floor.AcknowledgedThrough != 2 {
		t.Fatal(floor, err)
	}
	after, err := w.CurrentProgress(ctx, d.Disclosure())
	if err != nil || after.Pending || after.PlayedMS != drained.Value.CreditedMs.Int64 || after.ReceiptRows != 2 || after.Observation != pending.Observation+1 || after.Viewer != pending.Viewer {
		t.Fatal(after, err)
	}
	retired, err := w.ResolveProgress(ctx, d.Disclosure(), request)
	if err != nil || retired.Outcome.State != ResultRetired || retired.Value != (ProgressValue{}) {
		t.Fatal(retired, err)
	}
	if a.Usage() != ([4]governor.Amounts{}) || w.effective.executor.db.Stats().InUse != 0 {
		t.Fatal("physical custody leaked")
	}
}
func TestWCProgressProtocolDefinitionsAreClosed(t *testing.T) {
	if _, err := stmt.Describe(wcProgressCommandDefinitions()); err != nil {
		t.Fatal(err)
	}
	if _, err := newWCProgressDriver(context.Background(), &Writer{}, wcProgressScope{}, wcSnapshot{}); err == nil {
		t.Fatal("unowned opening issued driver")
	}
}

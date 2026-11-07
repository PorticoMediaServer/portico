package writer

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"reflect"
	"testing"
	"time"
)

// The persisted owner tuple is JSON [authority, account, profile] encoded as
// unpadded base64url. Keep this fixture value-only: importing legacy persistence
// also imports dbwork and globally registers legacy SQLite scalar functions.
func wcFixturePersonalOwnerKey(authority, account, profile string) string {
	raw, _ := json.Marshal([3]string{authority, account, profile})
	return "viewer:" + base64.RawURLEncoding.EncodeToString(raw)
}

// REAL owned opening, real clocks, native birth and real grants. This is a
// component smoke fixture with explicit prospective forecasts, not qualification.
func wcProgressCompositionFixture(t *testing.T, principalLifetimes ...context.Context) (*Writer, *WCProgressDriver, *governor.Authority) {
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
func TestWCProgressCompositionOriginalResolveAckAndDrain(t *testing.T) {
	w, d, a := wcProgressCompositionFixture(t)
	ctx := t.Context()
	before, err := w.CurrentProgress(ctx, d.Disclosure())
	if err != nil || before.Pending || before.Last != 0 || before.PlayedMS != 0 {
		t.Fatal(before, err)
	}
	command, err := d.Capture(ctx, 0, 5, 100, "playing")
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
	beforeReplay, err := w.CurrentProgress(ctx, d.Disclosure())
	if err != nil {
		t.Fatal(err)
	}
	replay, err := w.ResolveProgress(ctx, d.Disclosure(), reference)
	if err != nil || !replay.Outcome.Replayed || !reflect.DeepEqual(first.Value, replay.Value) || first.Digest != replay.Digest {
		t.Fatal(replay, err)
	}
	afterReplay, err := w.CurrentProgress(ctx, d.Disclosure())
	if err != nil {
		t.Fatal(err)
	}
	beforeReplay.Evidence = nil
	afterReplay.Evidence = nil
	if beforeReplay != afterReplay || !afterReplay.LastEvent.Valid || afterReplay.LastEvent.Int64 != 3 {
		t.Fatal("replay changed native state/outbox", beforeReplay, afterReplay)
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
	drained, err := d.ExecuteCurrentDrain(ctx, drain)
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
	if err = d.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if a.Usage() != ([4]governor.Amounts{}) || w.effective.executor.db.Stats().InUse != 0 {
		t.Fatal("physical custody leaked")
	}
}
func TestWCProgressProtocolDefinitionsAreClosed(t *testing.T) {
	if _, err := stmt.Describe(wcProgressCommandDefinitions()); err != nil {
		t.Fatal(err)
	}
	if _, err := newWCProgressDriver(context.Background(), context.Background(), &Writer{}, wcProgressScope{}, wcSnapshot{}, wcProgressPolicy{}, governor.Request{}, nil); err == nil {
		t.Fatal("unowned opening issued driver")
	}
}

// Revoking current user disclosure does not turn a retained pending capability
// into capture authority or public historical disclosure. Driver life remains.
func TestWCProgressCompositionRetainedDrainAndCurrentUserFence(t *testing.T) {
	principal, cancel := context.WithCancel(t.Context())
	defer cancel()
	w, d, _ := wcProgressCompositionFixture(t, principal)
	current := d.Disclosure()
	capture, err := d.Capture(t.Context(), 0, 5, 100, "playing")
	if err != nil {
		t.Fatal(err)
	}
	original, _ := ProgressReference(capture)
	if _, err = w.AcceptProgress(t.Context(), current, capture); err != nil {
		t.Fatal(err)
	}
	if _, err = w.AckProgress(t.Context(), current, original); err != nil {
		t.Fatal(err)
	}
	cancel()
	for _, call := range []func() error{
		func() error { _, e := w.CurrentProgress(t.Context(), current); return e },
		func() error { _, e := w.ResolveProgress(t.Context(), current, original); return e },
		func() error { _, e := w.AckProgress(t.Context(), current, original); return e },
		func() error { _, e := d.Capture(t.Context(), 1, 6, 100, "playing"); return e },
	} {
		if e := call(); !errors.Is(e, ErrDisclosureDenied) {
			t.Fatal("revoked principal crossed public boundary", e)
		}
	}
	drain, err := d.Drain(t.Context())
	if err != nil {
		t.Fatal("owned pending capability lost", err)
	}
	if _, err = w.AcceptProgress(t.Context(), current, drain); !errors.Is(err, ErrDisclosureDenied) {
		t.Fatal("public Accept consumed retained drain", err)
	}
	result, err := d.ExecuteDrain(t.Context(), drain)
	if err != nil || result.Outcome.State != Applied || result.Value.Finished.Int64 != 1 || result.Value.CreditedMs.Int64 <= 0 {
		t.Fatal(result, err)
	}
	request, _ := ProgressReference(drain)
	if _, err = w.AckProgress(t.Context(), current, request); !errors.Is(err, ErrDisclosureDenied) {
		t.Fatal("retained capability authorized public ACK", err)
	}
}
func TestWCProgressCompositionRejectsConflictingOriginalAndCopiedDriver(t *testing.T) {
	w, d, _ := wcProgressCompositionFixture(t)
	capture, err := d.Capture(t.Context(), 0, 5, 100, "playing")
	if err != nil {
		t.Fatal(err)
	}
	original, _ := ProgressReference(capture)
	if _, err = w.AcceptProgress(t.Context(), d.Disclosure(), capture); err != nil {
		t.Fatal(err)
	}
	for _, edit := range []func(*ProgressReceiptRequest){
		func(q *ProgressReceiptRequest) { q.Receipt.RequestDigest[0] ^= 1 },
		func(q *ProgressReceiptRequest) { q.Kind = "drain_piece" },
	} {
		q := original
		edit(&q)
		out, e := w.ResolveProgress(t.Context(), d.Disclosure(), q)
		if !errors.Is(e, ErrConflict) || !reflect.DeepEqual(out, ProgressResult{}) {
			t.Fatal("conflicting original disclosed a result", out, e)
		}
	}
	foreign := &WCProgressDriver{self: d, writer: w}
	if foreign.current() == nil {
		t.Fatal("copied driver minted authority")
	}
	if _, e := w.ResolveProgress(t.Context(), &ProgressDisclosure{driver: foreign}, original); !errors.Is(e, ErrDisclosureDenied) {
		t.Fatal(e)
	}
	if _, e := w.AckProgress(t.Context(), nil, original); !errors.Is(e, ErrDisclosureDenied) {
		t.Fatal(e)
	}
}

func TestWCProgressReportPolicyMatchesLegacyCompletion(t *testing.T) {
	p := wcProgressPolicy{StartedPercent: 5, PlayedPercent: 95, VideoCompletion: "earliest"}
	for _, c := range []struct {
		kind               int64
		position, duration float64
		state              string
		want               bool
	}{{1, 20, 100, "ended", false}, {1, 95, 100, "playing", true}, {4, 20, 100, "ended", false}, {9, 96, 100, "playing", true}, {1, 0, 0, "ended", true}} {
		got, err := p.completion(c.kind, c.position, c.duration, c.state)
		if err != nil || got != c.want {
			t.Fatal(c, got, err)
		}
	}
	p.VideoCompletion = "credits"
	p.CreditsStart = sql.NullFloat64{Float64: 99, Valid: true}
	if got, err := p.completion(4, 96, 100, "playing"); err != nil || got {
		t.Fatal("episode threshold bypassed credits-only policy", got, err)
	}
}

func TestWCProgressCompositionNativeCurrentTokenRequired(t *testing.T) {
	w, d, _ := wcProgressCompositionFixture(t)
	command, err := d.Capture(t.Context(), 0, 5, 100, "playing")
	if err != nil {
		t.Fatal(err)
	}
	original, _ := ProgressReference(command)
	// A live internal handle alone cannot replace the actual current token row.
	// This models a missing/retired native credential, with caller scope still live.
	d.scope.tokenHash = "missing-native-token"
	if out, e := w.AcceptProgress(t.Context(), d.Disclosure(), command); !errors.Is(e, ErrDisclosureDenied) || !reflect.DeepEqual(out, ProgressResult{}) {
		t.Fatal("native current credential gate missing", out, e)
	}
	if out, e := w.ResolveProgress(t.Context(), d.Disclosure(), original); !errors.Is(e, ErrDisclosureDenied) || !reflect.DeepEqual(out, ProgressResult{}) {
		t.Fatal("receipt disclosure bypassed current token", out, e)
	}
}

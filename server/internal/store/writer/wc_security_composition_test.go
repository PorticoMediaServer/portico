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

// Actual native admin/target births on the owned opening. No raw SQL, fake
// custody/floors, token fallback, fake clock or product constructor.
func wcSecurityCompositionOpening(t *testing.T, actorTier string) (*Writer, *WCProgressDriver, *governor.Authority) {
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
	for _, def := range wcSecurityCommandDefinitions() {
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

	golden := captureEffectiveFixture(t, conn, e.config, defs, "wc_security_composition_test.go", "wc_progress_fixture.go")
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
		partitions[i][governor.MemoryBytes] += 2 << 20
	}
	g, a, err := governor.New(governor.Config{Partitions: partitions, MaxGrantsPerClass: 3, Now: time.Now, WAL: wal})
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
	scope := wcProgressScope{primary: 1, primaryProfile: "profile-one", membershipRole: actorTier, horizon: time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano), expires: time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano), playbackID: "playback-音", profileKey: "viewer-one", accountID: "account-one", profileID: "profile-one", memberBirth: member, profileBirth: profile, libraryKey: "library-one", libraryID: 7, subjectID: 11, serverID: "server", familyID: "wc-family", tokenHash: "wc-token", epoch: 1, tokenGeneration: 1, memberRevision: 1, profileRevision: 1, role: actorTier, memberLibraries: `["library-one"]`}
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
func wcSecurityCompositionFixture(t *testing.T, actorTier, targetTier string) (*Writer, *WCSecurityDriver, string) {
	t.Helper()
	w, actor, _ := wcSecurityCompositionOpening(t, actorTier)
	r := w.effective
	plan := *r.wcProgressPlan
	plan.steps = nil
	plan.wcProgressMemory = commandMemoryBudget{}
	plan.wcSecurityMemory = commandMemoryBudget{64 << 20, 64 << 20}
	for _, d := range wcSecurityCommandDefinitions() {
		plan.steps = append(plan.steps, statementStep{d.ID, Work{16, 4000, 16, 65536, 100 * time.Microsecond}})
	}
	if err := composeWCSecurity(t.Context(), r, plan); err != nil {
		t.Fatal(err)
	}
	target := actor.scope
	target.accountID, target.profileID, target.playbackID, target.familyID, target.tokenHash = "target-account", "target-profile", "target-playback", "target-family", "target-token"
	target.primaryProfile = target.profileID
	target.role, target.membershipRole = targetTier, targetTier
	target.profileKey = wcFixturePersonalOwnerKey("local", target.accountID, target.profileID)
	var err error
	target.memberBirth, err = wcRandomBirth()
	if err != nil {
		t.Fatal(err)
	}
	target.profileBirth, err = wcRandomBirth()
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range []*[16]byte{&target.incarnation, &target.clock} {
		if _, err = rand.Read(b[:]); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := actor.original
	snapshot[2].Text, snapshot[3].Text, snapshot[4].Text = target.accountID, target.profileID, target.playbackID
	snapshot[39].Text, snapshot[40].Text = target.memberBirth, target.profileBirth
	ready := actor.expected
	ready.Key.ID = target.playbackID
	decision := r.governor.Try(ready)
	if decision.Grant == nil {
		t.Fatal(decision.Refusal)
	}
	td, err := newWCProgressDriver(t.Context(), t.Context(), w, target, snapshot, actor.policy, ready, decision.Grant)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if e := td.Close(context.Background()); e != nil {
			t.Error(e)
		}
	})
	if err = initializeWCProgressFixture(t.Context(), td, wcProgressExistingCatalog{target.libraryID, target.subjectID, target.libraryKey, target.subjectPublicID}); err != nil {
		t.Fatal(err)
	}
	expected := actor.expected
	expected.Key = governor.JobKey{Domain: uint16(SecurityAccept), ID: actor.scope.familyID}
	expected.Class = governor.Recovery
	expected.Footprint = []governor.Scope{{Domain: uint16(SecurityAccept)}}
	decision = r.governor.Try(expected)
	if decision.Grant == nil {
		t.Fatal(decision.Refusal)
	}
	driver, err := newWCSecurityDriver(t.Context(), t.Context(), w, actor.scope, expected, decision.Grant)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if e := driver.Close(context.Background()); e != nil {
			t.Error(e)
		}
	})
	return w, driver, target.familyID
}

func TestWCSecurityCompositionOriginalResolveAckAndPermanentScalar(t *testing.T) {
	w, d, family := wcSecurityCompositionFixture(t, "owner", "member")
	c, err := d.PrepareRevoke(t.Context(), family)
	if err != nil {
		t.Fatal(err)
	}
	q, err := SecurityReference(c)
	if err != nil {
		t.Fatal(err)
	}
	out, err := w.AcceptSecurity(t.Context(), d.Disclosure(), c)
	if err != nil || out.Outcome.State != Applied || out.Outcome.Replayed || out.Value.Reason != "admin_revoke" {
		t.Fatal(out, err)
	}
	e := out.Outcome.Evidence
	if e == nil || e.Purpose != SecurityAccept || e.Class != governor.Recovery || e.Durability != Full || e.Policy == nil || e.Policy.Synchronous != 2 || e.Commit != CommitSucceeded {
		t.Fatal("missing actual FULL/Recovery", e)
	}
	before, err := w.CurrentSecurity(t.Context(), d.Disclosure(), family)
	if err != nil {
		t.Fatal(err)
	}
	if before.Revoked != 1 || before.Reason.String != "admin_revoke" || !before.TeardownDue.Valid || !before.LastEvent.Valid {
		t.Fatal(before)
	}
	replay, err := w.ResolveSecurity(t.Context(), d.Disclosure(), q)
	if err != nil || !replay.Outcome.Replayed || replay.Value != out.Value || replay.Digest != out.Digest {
		t.Fatal(replay, err)
	}
	after, err := w.CurrentSecurity(t.Context(), d.Disclosure(), family)
	if err != nil {
		t.Fatal(err)
	}
	before.Evidence, after.Evidence = nil, nil
	if before != after {
		t.Fatal("replay changed effects", before, after)
	}
	floor, err := w.AckSecurity(t.Context(), d.Disclosure(), q)
	if err != nil || floor.AcknowledgedThrough != 1 {
		t.Fatal(floor, err)
	}
	saved, err := w.CurrentSecurity(t.Context(), d.Disclosure(), family)
	if err != nil || saved.Acknowledged != 1 || saved.ControlBytes != after.ControlBytes || saved.LastEvent != after.LastEvent || saved.TeardownDue != after.TeardownDue {
		t.Fatal("ACK cleared scalar/debt or appended hints", saved, err)
	}
	retired, err := w.ResolveSecurity(t.Context(), d.Disclosure(), q)
	if err != nil || retired.Outcome.State != ResultRetired || retired.Value != (SecurityValue{}) {
		t.Fatal(retired, err)
	}
	if _, err = d.PrepareRevoke(t.Context(), family); !errors.Is(err, ErrConflict) {
		t.Fatal("permanent one-shot scalar reused", err)
	}
}
func TestWCSecurityCompositionAdminCannotTargetAdminOrSelf(t *testing.T) {
	w, d, family := wcSecurityCompositionFixture(t, "admin", "admin")
	if _, err := d.PrepareRevoke(t.Context(), family); !errors.Is(err, ErrDisclosureDenied) {
		t.Fatal("admin bridged to admin", err)
	}
	if _, err := w.CurrentSecurity(t.Context(), d.Disclosure(), d.scope.familyID); !errors.Is(err, ErrDisclosureDenied) {
		t.Fatal("admin operation became self-signout", err)
	}
}
func TestWCSecurityCompositionCurrentActorRequired(t *testing.T) {
	w, d, family := wcSecurityCompositionFixture(t, "admin", "member")
	c, err := d.PrepareRevoke(t.Context(), family)
	if err != nil {
		t.Fatal(err)
	}
	q, _ := SecurityReference(c)
	d.scope.tokenHash = "missing-current-token"
	out, err := w.AcceptSecurity(t.Context(), d.Disclosure(), c)
	if !errors.Is(err, ErrDisclosureDenied) || !reflect.DeepEqual(out, SecurityResult{}) {
		t.Fatal("actor token bypass", out, err)
	}
	if out, err = w.ResolveSecurity(t.Context(), d.Disclosure(), q); !errors.Is(err, ErrDisclosureDenied) || !reflect.DeepEqual(out, SecurityResult{}) {
		t.Fatal("historical actor exception", out, err)
	}
}

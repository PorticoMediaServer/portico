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
	return wcSecurityCompositionOpeningClock(t, actorTier, false)
}
func wcSecurityCompositionOpeningClock(t *testing.T, actorTier string, realClock bool) (*Writer, *WCProgressDriver, *governor.Authority) {
	t.Helper()
	w, d, a, _ := wcComponentFixture(t, wcFixtureOptions{kind: "security", actorTier: actorTier, extraMemory: 2 << 20, maxGrants: 3, realClock: realClock})
	return w, d, a
}
func wcSecurityCompositionFixture(t *testing.T, actorTier, targetTier string) (*Writer, *WCSecurityDriver, string) {
	t.Helper()
	w, actor, _ := wcSecurityCompositionOpening(t, actorTier)
	target, err := wcSecurityTargetForFixture(t, w, actor, targetTier, "target")
	if err != nil {
		t.Fatal(err)
	}
	driver := wcSecurityDriverForFixture(t, w, actor)
	return w, driver, target
}

func wcSecurityDriverForFixture(t *testing.T, w *Writer, actor *WCProgressDriver) *WCSecurityDriver {
	t.Helper()
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
	expected := actor.expected
	expected.Key = governor.JobKey{Domain: uint16(SecurityAccept), ID: actor.scope.familyID}
	expected.Class = governor.Recovery
	expected.Footprint = []governor.Scope{{Domain: uint16(SecurityAccept)}}
	decision := r.governor.Try(expected)
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
	return driver
}

func wcSecurityTargetForFixture(t *testing.T, w *Writer, actor *WCProgressDriver, targetTier, name string) (string, error) {
	t.Helper()
	r := w.effective
	target := actor.scope
	target.accountID, target.profileID, target.playbackID, target.familyID, target.tokenHash = name+"-account", name+"-profile", name+"-playback", name+"-family", name+"-token"
	target.primaryProfile = target.profileID
	target.role, target.membershipRole = targetTier, targetTier
	target.profileKey = wcFixturePersonalOwnerKey("local", target.accountID, target.profileID)
	var err error
	target.memberBirth, err = wcRandomBirth()
	if err != nil {
		return "", err
	}
	target.profileBirth, err = wcRandomBirth()
	if err != nil {
		return "", err
	}
	for _, b := range []*[16]byte{&target.incarnation, &target.clock} {
		if _, err = rand.Read(b[:]); err != nil {
			return "", err
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
		return "", err
	}
	t.Cleanup(func() {
		if e := td.Close(context.Background()); e != nil {
			t.Error(e)
		}
	})
	if err = initializeWCProgressFixture(t.Context(), td, wcProgressExistingCatalog{target.libraryID, target.subjectID, target.libraryKey, target.subjectPublicID}); err != nil {
		return "", err
	}
	if err = td.Close(context.Background()); err != nil {
		return "", err
	}
	return target.familyID, nil
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

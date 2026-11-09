package writer

import (
	"errors"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

// The family's descriptors keep the exact IDs and NORMAL durability without
// widening any shared registry list, the closed purpose guard admits only this
// family, the production root seam refuses until the US mint lands, and revoked
// current authority is checked before retained custody is read.
func TestMGCaptureAuthorityPurposeAndResultCodec(t *testing.T) {
	got := mgCaptureDescriptors()
	want := []Descriptor{
		{MGCaptureClaim, "mg.capture.claim", Normal},
		{MGRenewCapture, "mg.capture.renew", Normal},
		{MGCheckpointCapture, "mg.capture.checkpoint", Normal},
	}
	if len(got) != len(want) {
		t.Fatal(got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatal(i, got[i], want[i])
		}
		if _, err := Describe(want[i].ID); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []CommandID{MGCaptureClaim, MGRenewCapture, MGCheckpointCapture} {
		if err := mgCapturePurpose(nil, id); err == nil {
			t.Fatal("nil turn admitted a capture purpose")
		}
		if err := mgCapturePurpose(&commandTurn{}, id); err == nil {
			t.Fatal("empty turn admitted a capture purpose")
		}
	}

	w, _, v := mgCaptureFixture(t)
	fake := mgCaptureFake(v)
	claim := v.claimRequest()
	claimReference := mgCaptureClaimReference(t, mgPersonalIdentity(2, 2), claim)
	turn := mgCaptureTurn(t, w, MGCaptureClaim)
	fresh, err := acceptMGCaptureClaim(t.Context(), turn, fake, v.proof, t.Context(), claimReference, claim)
	if err != nil || fresh.decision != receiptFresh {
		t.Fatal(fresh, err)
	}
	mgPersonalCommit(t, turn)
	ackMGRecording(t, w, v.root, 2)

	// A cancelled/NORMAL-only purpose cannot borrow a FULL transaction frame.
	full, err := w.begin(t.Context(), SecurityAccept, testRecipe(), &testCustody{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = full.rollback(nil) })
	full.descriptor.ID = MGCaptureClaim
	defs := mgCaptureDefinitionsUnion()
	registry, err := stmttest.New(t, defs)
	if err != nil {
		t.Fatal(err)
	}
	var fullSteps []statementStep
	for _, d := range defs {
		fullSteps = append(fullSteps, statementStep{d.ID, Work{8, 1000, 8, 4096, time.Microsecond}})
	}
	fullTurn, err := newCommandTurn(full, registry, fullSteps)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fullTurn.closeRows() })
	if out, err := acceptMGCaptureClaim(t.Context(), fullTurn, fake, v.proof, t.Context(), claimReference, claim); !errors.Is(err, ErrInvalid) || out != (mgCaptureAcceptance{}) {
		t.Fatal("FULL frame borrowed a NORMAL capture purpose", out, err)
	}
	_ = fullTurn.closeRows()
	if _, err := full.rollback(nil); err != nil {
		t.Fatal(err)
	}

	// The production root seam is the only non-test implementation and refuses.
	prodTurn := mgCaptureTurn(t, w, MGCaptureClaim)
	if out, err := (mgPersonalProductionRoots{}).authorizeUSCurrentSession(t.Context(), prodTurn, v.proof, t.Context()); !errors.Is(err, ErrMGRootUnavailable) || out != nil {
		t.Fatal("production seam minted a session", out, err)
	}
	if _, err := mgCaptureAuthorize(t.Context(), prodTurn, MGCaptureClaim, mgPersonalProductionRoots{}, v.proof, t.Context()); !errors.Is(err, ErrMGRootUnavailable) {
		t.Fatal("production root admitted a capture command", err)
	}
	if _, err := prodTurn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}

	// A revoked session is denied on replay BEFORE retained custody is read.
	if _, err := w.db.ExecContext(t.Context(), `UPDATE authorization_session_families SET revoked=1,revoked_reason='explicit_signout',revoked_at='2026-11-09T00:00:00Z' WHERE id=?`, v.proof.witness.issuer.family); err != nil {
		t.Fatal(err)
	}
	turn = mgCaptureTurn(t, w, MGCaptureClaim)
	if got, err := acceptMGCaptureClaim(t.Context(), turn, fake, v.proof, t.Context(), claimReference, claim); !errors.Is(err, ErrDisclosureDenied) || got != (mgCaptureAcceptance{}) {
		t.Fatal("revoked session replayed a claim", got, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `DELETE FROM user_mutation_command_receipts WHERE root_id=?`, v.root); err != nil {
		t.Fatal(err)
	}
	turn = mgCaptureTurn(t, w, MGCaptureClaim)
	if got, err := acceptMGCaptureClaim(t.Context(), turn, fake, v.proof, t.Context(), claimReference, claim); !errors.Is(err, ErrDisclosureDenied) || errors.Is(err, ErrResolveRequired) || got != (mgCaptureAcceptance{}) {
		t.Fatal("revoked session reached retained custody first", got, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}

	// The saved scalar codec is closed and canonical: round-trip equality, then
	// digest and payload tampering are refused.
	encoded := v.claimResult()
	payload, digest, err := mgCaptureResultPayload(mgPersonalIdentity(2, 2), claimReference.RequestDigest, encoded)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeMGCaptureResult(mgPersonalIdentity(2, 2), claimReference.RequestDigest, payload, digest)
	if err != nil || decoded != encoded {
		t.Fatal(decoded, err)
	}
	badDigest := digest
	badDigest[0] ^= 0xff
	if _, err := decodeMGCaptureResult(mgPersonalIdentity(2, 2), claimReference.RequestDigest, payload, badDigest); !errors.Is(err, ErrResolveRequired) {
		t.Fatal("tampered result digest accepted", err)
	}
	tampered := append([]byte(nil), payload...)
	tampered[len(tampered)-1] ^= 0xff
	if _, err := decodeMGCaptureResult(mgPersonalIdentity(2, 2), claimReference.RequestDigest, tampered, digest); !errors.Is(err, ErrResolveRequired) {
		t.Fatal("tampered payload accepted", err)
	}
	otherRequest := claimReference.RequestDigest
	otherRequest[0] ^= 0xff
	if _, err := decodeMGCaptureResult(mgPersonalIdentity(2, 2), otherRequest, payload, digest); !errors.Is(err, ErrResolveRequired) {
		t.Fatal("payload replayed under another request", err)
	}
	invalid := encoded
	invalid.token = ""
	if err := preflightMGCaptureResult(invalid); !errors.Is(err, ErrResolveRequired) {
		t.Fatal("typed result preflight admitted a bare custody tuple", err)
	}

	// A different input under one identity, or a caller digest that disagrees
	// with the typed request, conflicts before any effect.
	w2, _, v2 := mgCaptureFixture(t)
	fake2 := mgCaptureFake(v2)
	claim2 := v2.claimRequest()
	reference2 := mgCaptureClaimReference(t, mgPersonalIdentity(2, 2), claim2)
	turn = mgCaptureTurn(t, w2, MGCaptureClaim)
	if got, err := acceptMGCaptureClaim(t.Context(), turn, fake2, v2.proof, t.Context(), reference2, claim2); err != nil || got.decision != receiptFresh {
		t.Fatal(got, err)
	}
	mgPersonalCommit(t, turn)
	ackMGRecording(t, w2, v2.root, 2)
	changed := claim2
	changed.leaseMS--
	turn = mgCaptureTurn(t, w2, MGCaptureClaim)
	if got, err := acceptMGCaptureClaim(t.Context(), turn, fake2, v2.proof, t.Context(), reference2, changed); !errors.Is(err, ErrConflict) || got != (mgCaptureAcceptance{}) {
		t.Fatal("changed input under one identity", got, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	bad := reference2
	bad.RequestDigest[0] ^= 0xff
	turn = mgCaptureTurn(t, w2, MGCaptureClaim)
	if got, err := acceptMGCaptureClaim(t.Context(), turn, fake2, v2.proof, t.Context(), bad, claim2); !errors.Is(err, ErrConflict) || got != (mgCaptureAcceptance{}) {
		t.Fatal("caller digest not recomputed", got, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}

	// Request constructors refuse incomplete native custody shapes.
	zeroLaunch := v.claimRequest()
	zeroLaunch.launch = mgCaptureLaunch{}
	if _, err := newMGCaptureClaimCommand(mgPersonalIdentity(9, 9), zeroLaunch); !errors.Is(err, ErrInvalid) {
		t.Fatal("zero launch accepted", err)
	}
	badFence := v.claimRequest()
	badFence.sourceFence = "zz"
	if _, err := newMGCaptureClaimCommand(mgPersonalIdentity(9, 9), badFence); !errors.Is(err, ErrInvalid) {
		t.Fatal("bad source fence accepted", err)
	}
	negativeGap := v.checkpointRequest()
	negativeGap.gapCount = -1
	if _, err := newMGCaptureCheckpointCommand(mgPersonalIdentity(9, 9), negativeGap); !errors.Is(err, ErrInvalid) {
		t.Fatal("negative gap accepted", err)
	}
	endOnly := v.checkpointRequest()
	endOnly.capturedStartMS = 0
	if _, err := newMGCaptureCheckpointCommand(mgPersonalIdentity(9, 9), endOnly); !errors.Is(err, ErrInvalid) {
		t.Fatal("end without start accepted", err)
	}
	reversed := v.checkpointRequest()
	reversed.capturedStartMS, reversed.capturedEndMS = reversed.capturedEndMS, reversed.capturedStartMS
	if _, err := newMGCaptureCheckpointCommand(mgPersonalIdentity(9, 9), reversed); !errors.Is(err, ErrInvalid) {
		t.Fatal("reversed progress accepted", err)
	}
	if _, err := newMGCaptureRenewCommand(mgPersonalIdentity(9, 9), v.renewRequest()); err != nil {
		t.Fatal(err)
	}
}

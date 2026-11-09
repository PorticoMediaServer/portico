package writer

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Every accepting capture command carries the same required cells: an identical
// retry replays the original saved scalar once, a durable ACK retires the
// original to a complete zero result, a forged cached stamp and a forged issuer
// are denied with zero output, and a cancelled caller lifetime refuses before
// any custody or effect.
func TestMGCaptureAcceptingCellsForEveryCommand(t *testing.T) {
	type cell struct {
		name    string
		purpose CommandID
		prepare func(t *testing.T, w *executor, v mgCaptureFixtureValues, fake *mgPersonalFakeRoots) (func(context.Context, *commandTurn, *usOwnerAuthorityProof, context.Context) (mgCaptureAcceptance, error), int64)
	}
	cells := []cell{
		{
			name: "claim", purpose: MGCaptureClaim,
			prepare: func(t *testing.T, w *executor, v mgCaptureFixtureValues, fake *mgPersonalFakeRoots) (func(context.Context, *commandTurn, *usOwnerAuthorityProof, context.Context) (mgCaptureAcceptance, error), int64) {
				request := v.claimRequest()
				reference := mgCaptureClaimReference(t, mgPersonalIdentity(2, 2), request)
				return func(ctx context.Context, turn *commandTurn, witness *usOwnerAuthorityProof, lifetime context.Context) (mgCaptureAcceptance, error) {
					return acceptMGCaptureClaim(ctx, turn, fake, witness, lifetime, reference, request)
				}, 2
			},
		},
		{
			name: "renew", purpose: MGRenewCapture,
			prepare: func(t *testing.T, w *executor, v mgCaptureFixtureValues, fake *mgPersonalFakeRoots) (func(context.Context, *commandTurn, *usOwnerAuthorityProof, context.Context) (mgCaptureAcceptance, error), int64) {
				claimMGCapture(t, w, v, fake, mgPersonalIdentity(2, 2))
				request := v.renewRequest()
				reference := mgCaptureRenewReference(t, mgPersonalIdentity(3, 3), request)
				return func(ctx context.Context, turn *commandTurn, witness *usOwnerAuthorityProof, lifetime context.Context) (mgCaptureAcceptance, error) {
					return acceptMGCaptureRenew(ctx, turn, fake, witness, lifetime, reference, request)
				}, 3
			},
		},
		{
			name: "checkpoint", purpose: MGCheckpointCapture,
			prepare: func(t *testing.T, w *executor, v mgCaptureFixtureValues, fake *mgPersonalFakeRoots) (func(context.Context, *commandTurn, *usOwnerAuthorityProof, context.Context) (mgCaptureAcceptance, error), int64) {
				claimMGCapture(t, w, v, fake, mgPersonalIdentity(2, 2))
				renewMGCapture(t, w, v, fake, mgPersonalIdentity(3, 3))
				request := v.checkpointRequest()
				reference := mgCaptureCheckpointReference(t, mgPersonalIdentity(4, 4), request)
				return func(ctx context.Context, turn *commandTurn, witness *usOwnerAuthorityProof, lifetime context.Context) (mgCaptureAcceptance, error) {
					return acceptMGCaptureCheckpoint(ctx, turn, fake, witness, lifetime, reference, request)
				}, 4
			},
		},
	}
	for _, tc := range cells {
		t.Run(tc.name, func(t *testing.T) {
			w, _, v := mgCaptureFixture(t)
			fake := mgCaptureFake(v)
			run, sequence := tc.prepare(t, w, v, fake)

			turn := mgCaptureTurn(t, w, tc.purpose)
			fresh, err := run(t.Context(), turn, v.proof, t.Context())
			if err != nil || fresh.decision != receiptFresh || fresh.digest == (ResultDigest{}) {
				t.Fatal(fresh, err)
			}
			mgPersonalCommit(t, turn)

			turn = mgCaptureTurn(t, w, tc.purpose)
			replay, err := run(t.Context(), turn, v.proof, t.Context())
			if err != nil || replay.decision != receiptReplay || replay.result != fresh.result || replay.digest != fresh.digest {
				t.Fatal(replay, err)
			}
			if _, err := turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}

			ackMGRecording(t, w, v.root, sequence)
			turn = mgCaptureTurn(t, w, tc.purpose)
			retired, err := run(t.Context(), turn, v.proof, t.Context())
			if err != nil || retired.decision != receiptRetired || retired.result != (mgCaptureResult{}) || retired.digest != (ResultDigest{}) {
				t.Fatal(retired, err)
			}
			if _, err := turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}

			forgedStamp := *v.proof
			forgedStamp.tokenExpiry = forgedStamp.tokenExpiry.Add(time.Hour)
			turn = mgCaptureTurn(t, w, tc.purpose)
			if got, err := run(t.Context(), turn, &forgedStamp, t.Context()); !errors.Is(err, ErrDisclosureDenied) || got != (mgCaptureAcceptance{}) {
				t.Fatal("forged cached stamp", got, err)
			}
			if _, err := turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}
			other := v.proof.witness
			other.issuer.account = "another-account"
			forgedIssuer, err := prepareUSOwnerAuthority(other)
			if err != nil {
				t.Fatal(err)
			}
			turn = mgCaptureTurn(t, w, tc.purpose)
			if got, err := run(t.Context(), turn, forgedIssuer, t.Context()); !errors.Is(err, ErrDisclosureDenied) || got != (mgCaptureAcceptance{}) {
				t.Fatal("forged issuer", got, err)
			}
			if _, err := turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}

			cancelled, cancel := context.WithCancel(t.Context())
			cancel()
			turn = mgCaptureTurn(t, w, tc.purpose)
			if got, err := run(cancelled, turn, v.proof, t.Context()); !errors.Is(err, context.Canceled) || got != (mgCaptureAcceptance{}) {
				t.Fatal("cancelled lifetime", got, err)
			}
			if _, err := turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}

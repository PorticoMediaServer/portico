package writer

import (
	"context"
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

func TestWCReclaimUncommittedCustodyLifecycle(t *testing.T) {
	for _, mode := range []string{"close", "cancel", "failed_accept"} {
		t.Run(mode, func(t *testing.T) {
			w, pd, rd := wcReclaimCompositionProgressFixture(t)
			cleanup, err := rd.Prepare(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			old, _ := ReclaimReference(cleanup)
			switch mode {
			case "close":
				if err = rd.Close(t.Context()); err != nil {
					t.Fatal(err)
				}
			case "cancel":
				rd.cancel() // no Close yet: cancellation must also free settled work
			case "failed_accept":
				// A native full-key miss returns ResolveRequired, but never enters
				// COMMIT. It must not be confused with an ambiguous COMMIT reply.
				account := rd.scope.accountID
				rd.scope.accountID += "-missing"
				_, err = w.AcceptReclaim(t.Context(), rd.Disclosure(), cleanup)
				rd.scope.accountID = account
				if !errors.Is(err, ErrResolveRequired) {
					t.Fatal("expected native pre-COMMIT refusal", err)
				}
			}
			capture, err := pd.Capture(t.Context(), 1, 6, 100, "playing")
			if err != nil {
				t.Fatal("uncommitted cleanup stranded progress custody", err)
			}
			ref, _ := ProgressReference(capture)
			if ref.Receipt.Identity.Sequence != old.Receipt.Identity.Sequence {
				t.Fatal("uncommitted cleanup consumed a native sequence", old, ref)
			}
			if err = rd.Close(t.Context()); err != nil {
				t.Fatal(err)
			}
			// Closing the old handle must not release this newer reservation.
			if _, err = w.AcceptProgress(t.Context(), pd.Disclosure(), capture); err != nil {
				t.Fatal("old Close displaced new progress work", err)
			}
			if _, err = w.AckProgress(t.Context(), pd.Disclosure(), ref); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestWCProgressUncommittedCustodyLifecycle(t *testing.T) {
	for _, mode := range []string{"close", "cancel", "failed_accept"} {
		t.Run(mode, func(t *testing.T) {
			w, pd, rd := wcReclaimCompositionProgressFixture(t)
			capture, err := pd.Capture(t.Context(), 1, 6, 100, "playing")
			if err != nil {
				t.Fatal(err)
			}
			old, _ := ProgressReference(capture)
			switch mode {
			case "close":
				if err = pd.Close(t.Context()); err != nil {
					t.Fatal(err)
				}
			case "cancel":
				pd.cancel()
			case "failed_accept":
				token := pd.scope.tokenHash
				pd.scope.tokenHash = "missing-current-token"
				_, err = w.AcceptProgress(t.Context(), pd.Disclosure(), capture)
				pd.scope.tokenHash = token
				if !errors.Is(err, ErrDisclosureDenied) {
					t.Fatal("expected native pre-COMMIT refusal", err)
				}
			}
			cleanup, err := rd.Prepare(t.Context())
			if err != nil {
				t.Fatal("uncommitted progress stranded cleanup custody", err)
			}
			ref, _ := ReclaimReference(cleanup)
			if ref.Receipt.Identity.Sequence != old.Receipt.Identity.Sequence {
				t.Fatal("uncommitted progress consumed a native sequence", old, ref)
			}
			if err = pd.Close(t.Context()); err != nil {
				t.Fatal(err)
			}
			if _, err = w.AcceptReclaim(t.Context(), rd.Disclosure(), cleanup); err != nil {
				t.Fatal("old Close displaced new cleanup work", err)
			}
			if _, err = w.AckReclaim(t.Context(), rd.Disclosure(), ref); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestWCClaimCustodyUnknownCommitSurvivesClose(t *testing.T) {
	for _, after := range []bool{false, true} {
		t.Run(map[bool]string{false: "before_native_commit", true: "after_native_commit"}[after], func(t *testing.T) {
			w, pd, rd := wcReclaimCompositionProgressFixture(t)
			command, err := rd.Prepare(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			reservation := command.(*wcReclaimCommand).custody
			e := w.effective.executor
			if err = e.db.Close(); err != nil {
				t.Fatal(err)
			}
			connector, err := sqliteconn.Connector(w.effective.path, e.config)
			if err != nil {
				t.Fatal(err)
			}
			fault := &faultConnector{Connector: connector, replyError: true, afterCommit: after}
			installFaultPool(e, fault)
			primeEffectiveFixturePool(t, e)
			if _, err = w.AcceptReclaim(t.Context(), rd.Disclosure(), command); !errors.Is(err, ErrResolveRequired) || fault.commits.Load() != 1 {
				t.Fatal("did not reach the actual ambiguous COMMIT", err, fault.commits.Load())
			}
			if err = rd.Close(t.Context()); err != nil {
				t.Fatal(err)
			}
			// The runtime fence also refuses new work. Check the independent
			// claim fence so that it cannot accidentally hide a custody leak.
			if pd.commandCustody.active.Load() != reservation || reservation.state.Load() != wcClaimCommitUnknown {
				t.Fatal("ambiguous COMMIT lost its exact retained reservation")
			}
		})
	}
}

func TestWCClaimCustodyKnownCommitCloseKeepsNativeACKFence(t *testing.T) {
	w, pd, rd := wcReclaimCompositionProgressFixture(t)
	command, err := rd.Prepare(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	q, _ := ReclaimReference(command)
	if _, err = w.AcceptReclaim(t.Context(), rd.Disclosure(), command); err != nil {
		t.Fatal(err)
	}
	if err = rd.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	current, err := w.CurrentProgress(t.Context(), pd.Disclosure())
	if err != nil || current.Last != q.Receipt.Identity.Sequence || current.Acknowledged != current.Last-1 {
		t.Fatal("Close changed the native receipt or floor", current, err)
	}
	if c, e := pd.Capture(t.Context(), 1, 6, 100, "playing"); !errors.Is(e, ErrResolveRequired) || c != nil {
		t.Fatal("Close bypassed the native unACKed command", c, e)
	}
	decision := w.effective.governor.Try(rd.expected)
	if decision.Grant == nil {
		t.Fatal(decision.Refusal)
	}
	recovery, err := newWCReclaimDriver(t.Context(), w, pd, rd.expected, decision.Grant)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = recovery.Close(context.Background()) })
	if out, e := w.ResolveReclaim(t.Context(), recovery.Disclosure(), q); e != nil || !out.Outcome.Replayed {
		t.Fatal("known committed result lost after Close", out, e)
	}
	if _, err = w.AckReclaim(t.Context(), recovery.Disclosure(), q); err != nil {
		t.Fatal(err)
	}
	if _, err = pd.Capture(t.Context(), 1, 6, 100, "playing"); err != nil {
		t.Fatal("known COMMIT left a memory fence after native ACK", err)
	}
}

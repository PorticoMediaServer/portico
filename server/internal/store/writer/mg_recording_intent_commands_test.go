package writer

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

// Every accepting command carries the same required cells: an identical retry
// replays the original saved scalar once, a durable ACK retires the original to
// a complete zero result, a forged issuer is denied and a cancelled caller
// lifetime refuses with zero output.
func TestMGRecordingAcceptingCellsForEveryCommand(t *testing.T) {
	type cell struct {
		name    string
		purpose CommandID
		prepare func(t *testing.T, w *executor, v mgRecordingFixtureValues, fake *mgPersonalFakeRoots) (func(context.Context, *commandTurn, *usOwnerAuthorityProof, context.Context) (mgRecordingAcceptance, error), int64)
	}
	cells := []cell{
		{
			name: "schedule", purpose: MGScheduleRecording,
			prepare: func(t *testing.T, w *executor, v mgRecordingFixtureValues, fake *mgPersonalFakeRoots) (func(context.Context, *commandTurn, *usOwnerAuthorityProof, context.Context) (mgRecordingAcceptance, error), int64) {
				request := v.schedule()
				reference := mgRecordingScheduleReference(t, mgPersonalIdentity(1, 1), request)
				return func(ctx context.Context, turn *commandTurn, witness *usOwnerAuthorityProof, lifetime context.Context) (mgRecordingAcceptance, error) {
					return acceptMGRecordingSchedule(ctx, turn, fake, witness, lifetime, reference, request)
				}, 1
			},
		},
		{
			name: "update", purpose: MGUpdateRecording,
			prepare: func(t *testing.T, w *executor, v mgRecordingFixtureValues, fake *mgPersonalFakeRoots) (func(context.Context, *commandTurn, *usOwnerAuthorityProof, context.Context) (mgRecordingAcceptance, error), int64) {
				saved := scheduleMGRecording(t, w, v, fake, mgPersonalIdentity(1, 1))
				ackMGRecording(t, w, v.root, 1)
				options := v.options()
				options.Priority, options.AfterMS = 7, 0
				request := v.update(saved.result.id, 1, options)
				reference := mgRecordingUpdateReference(t, mgPersonalIdentity(2, 2), request)
				return func(ctx context.Context, turn *commandTurn, witness *usOwnerAuthorityProof, lifetime context.Context) (mgRecordingAcceptance, error) {
					return acceptMGRecordingUpdate(ctx, turn, fake, witness, lifetime, reference, request)
				}, 2
			},
		},
		{
			name: "cancel", purpose: MGCancelRecording,
			prepare: func(t *testing.T, w *executor, v mgRecordingFixtureValues, fake *mgPersonalFakeRoots) (func(context.Context, *commandTurn, *usOwnerAuthorityProof, context.Context) (mgRecordingAcceptance, error), int64) {
				saved := scheduleMGRecording(t, w, v, fake, mgPersonalIdentity(1, 1))
				ackMGRecording(t, w, v.root, 1)
				request := v.cancel(saved.result.id, 1)
				reference := mgRecordingCancelReference(t, mgPersonalIdentity(2, 2), request)
				return func(ctx context.Context, turn *commandTurn, witness *usOwnerAuthorityProof, lifetime context.Context) (mgRecordingAcceptance, error) {
					return acceptMGRecordingCancel(ctx, turn, fake, witness, lifetime, reference, request)
				}, 2
			},
		},
		{
			name: "keep", purpose: MGKeepRecording,
			prepare: func(t *testing.T, w *executor, v mgRecordingFixtureValues, fake *mgPersonalFakeRoots) (func(context.Context, *commandTurn, *usOwnerAuthorityProof, context.Context) (mgRecordingAcceptance, error), int64) {
				saved := scheduleMGRecording(t, w, v, fake, mgPersonalIdentity(1, 1))
				ackMGRecording(t, w, v.root, 1)
				request := v.keep(saved.result.id, 1, true)
				reference := mgRecordingKeepReference(t, mgPersonalIdentity(2, 2), request)
				return func(ctx context.Context, turn *commandTurn, witness *usOwnerAuthorityProof, lifetime context.Context) (mgRecordingAcceptance, error) {
					return acceptMGRecordingKeep(ctx, turn, fake, witness, lifetime, reference, request)
				}, 2
			},
		},
	}
	for _, tc := range cells {
		t.Run(tc.name, func(t *testing.T) {
			w, _, v := mgRecordingFixture(t)
			fake := mgRecordingFake(v)
			run, sequence := tc.prepare(t, w, v, fake)

			turn := mgRecordingTurn(t, w, tc.purpose)
			fresh, err := run(t.Context(), turn, v.proof, t.Context())
			if err != nil || fresh.decision != receiptFresh || fresh.digest == (ResultDigest{}) {
				t.Fatal(fresh, err)
			}
			mgPersonalCommit(t, turn)

			// Identical retry replays the original saved scalar with zero effects.
			turn = mgRecordingTurn(t, w, tc.purpose)
			replay, err := run(t.Context(), turn, v.proof, t.Context())
			if err != nil || replay.decision != receiptReplay || replay.result != fresh.result || replay.digest != fresh.digest {
				t.Fatal(replay, err)
			}
			if _, err := turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}

			// ACK retires it: a complete zero result, never a reconstructed
			// success and never a fresh effect.
			ackMGRecording(t, w, v.root, sequence)
			turn = mgRecordingTurn(t, w, tc.purpose)
			retired, err := run(t.Context(), turn, v.proof, t.Context())
			if err != nil || retired.decision != receiptRetired || retired.result != (mgRecordingIntentResult{}) || retired.digest != (ResultDigest{}) {
				t.Fatal(retired, err)
			}
			if _, err := turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}

			// A forged cached stamp and a forged issuer are denied with zero
			// output before any custody read.
			forgedStamp := *v.proof
			forgedStamp.tokenExpiry = forgedStamp.tokenExpiry.Add(time.Hour)
			turn = mgRecordingTurn(t, w, tc.purpose)
			if got, err := run(t.Context(), turn, &forgedStamp, t.Context()); !errors.Is(err, ErrDisclosureDenied) || got != (mgRecordingAcceptance{}) {
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
			turn = mgRecordingTurn(t, w, tc.purpose)
			if got, err := run(t.Context(), turn, forgedIssuer, t.Context()); !errors.Is(err, ErrDisclosureDenied) || got != (mgRecordingAcceptance{}) {
				t.Fatal("forged issuer", got, err)
			}
			if _, err := turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}

			// A cancelled caller lifetime refuses before any custody or effect.
			cancelled, cancel := context.WithCancel(t.Context())
			cancel()
			turn = mgRecordingTurn(t, w, tc.purpose)
			if got, err := run(cancelled, turn, v.proof, t.Context()); !errors.Is(err, context.Canceled) || got != (mgRecordingAcceptance{}) {
				t.Fatal("cancelled lifetime", got, err)
			}
			if _, err := turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// Current authority and the exact root precede retained custody. A revoked
// session never replays a saved result, a draining root never admits a fresh
// effect, stale owner facts are denied, a FULL frame cannot borrow a NORMAL
// purpose and the production seam refuses until the US mint lands.
func TestMGRecordingAcceptingAuthorityCells(t *testing.T) {
	w, _, v := mgRecordingFixture(t)
	fake := mgRecordingFake(v)
	identity := mgPersonalIdentity(1, 1)
	request := v.schedule()
	reference := mgRecordingScheduleReference(t, identity, request)

	turn := mgRecordingTurn(t, w, MGScheduleRecording)
	fresh, err := acceptMGRecordingSchedule(t.Context(), turn, fake, v.proof, t.Context(), reference, request)
	if err != nil || fresh.decision != receiptFresh {
		t.Fatal(fresh, err)
	}
	mgPersonalCommit(t, turn)

	// A session for another root never admits this reference.
	turn = mgRecordingTurn(t, w, MGScheduleRecording)
	session, err := mgRecordingAuthorize(t.Context(), turn, MGScheduleRecording, fake, v.proof, t.Context())
	if err != nil {
		t.Fatal(err)
	}
	foreign := uoRootReference{root: "zyxwvutsrqponmlkjihgfe", kind: MGScheduleRecording, identity: identity, request: reference.RequestDigest}
	if _, err := mgPersonalRootLookupCommand(t.Context(), turn, session, foreign); !errors.Is(err, ErrInvalid) {
		t.Fatal("foreign root passed the session binding", err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}

	// A revoked session is denied on replay BEFORE retained custody is read,
	// even when the receipt rows are physically gone.
	if _, err := w.db.ExecContext(t.Context(), `UPDATE authorization_session_families SET revoked=1,revoked_reason='explicit_signout',revoked_at=? WHERE id=?`, w.now().UTC().Format("2006-01-02T15:04:05Z"), v.proof.witness.issuer.family); err != nil {
		t.Fatal(err)
	}
	turn = mgRecordingTurn(t, w, MGScheduleRecording)
	got, err := acceptMGRecordingSchedule(t.Context(), turn, fake, v.proof, t.Context(), reference, request)
	if !errors.Is(err, ErrDisclosureDenied) || got != (mgRecordingAcceptance{}) {
		t.Fatal("revoked session replayed the saved result", got, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `DELETE FROM user_mutation_command_receipts WHERE root_id=?`, v.root); err != nil {
		t.Fatal(err)
	}
	turn = mgRecordingTurn(t, w, MGScheduleRecording)
	if got, err := acceptMGRecordingSchedule(t.Context(), turn, fake, v.proof, t.Context(), reference, request); !errors.Is(err, ErrDisclosureDenied) || errors.Is(err, ErrResolveRequired) || got != (mgRecordingAcceptance{}) {
		t.Fatal("revoked session reached retained custody first", got, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}

	// A fresh lookup whose root drains before persistence cannot apply.
	w2, _, v2 := mgRecordingFixture(t)
	fake2 := mgRecordingFake(v2)
	turn = mgRecordingTurn(t, w2, MGScheduleRecording)
	session, err = mgRecordingAuthorize(t.Context(), turn, MGScheduleRecording, fake2, v2.proof, t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ref := uoRootReference{root: session.mgPersonalRootID(), kind: MGScheduleRecording, identity: identity, request: reference.RequestDigest}
	lookup, err := mgPersonalRootLookupCommand(t.Context(), turn, session, ref)
	if err != nil || lookup.decision != receiptFresh {
		t.Fatal(lookup.decision, err)
	}
	if _, err := turn.transaction.conn.ExecContext(t.Context(), `UPDATE user_mutation_roots SET state='draining' WHERE id=?`, v2.root); err != nil {
		t.Fatal(err)
	}
	if _, err := mgPersonalRootPersist(t.Context(), turn, lookup, mgRecordingExpectedResult(t, v2, v2.scope, v2.programme, v2.options(), "scheduled", 1, 0, 1, 1), 1); !errors.Is(err, ErrUORootGone) {
		t.Fatal("draining root persisted a fresh effect", err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	var leaked int64
	if err := w2.db.QueryRowContext(t.Context(), `SELECT count(*) FROM dvr_recordings`).Scan(&leaked); err != nil || leaked != 0 {
		t.Fatal("draining-root effect leaked", leaked, err)
	}

	// Stale owner facts (a member revision advance) are denied before any
	// occurrence effect.
	w3, _, v3 := mgRecordingFixture(t)
	fake3 := mgRecordingFake(v3)
	if _, err := w3.db.ExecContext(t.Context(), `UPDATE direct_memberships SET revision=2 WHERE account_id=?`, v3.owner.account); err != nil {
		t.Fatal(err)
	}
	stale := v3.schedule()
	turn = mgRecordingTurn(t, w3, MGScheduleRecording)
	if got, err := acceptMGRecordingSchedule(t.Context(), turn, fake3, v3.proof, t.Context(), mgRecordingScheduleReference(t, mgPersonalIdentity(1, 1), stale), stale); !errors.Is(err, ErrDisclosureDenied) || got != (mgRecordingAcceptance{}) {
		t.Fatal("stale owner facts were accepted", got, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	if err := w3.db.QueryRowContext(t.Context(), `SELECT count(*) FROM dvr_recordings`).Scan(&leaked); err != nil || leaked != 0 {
		t.Fatal("stale-owner effect leaked", leaked, err)
	}

	// A NORMAL-only purpose cannot borrow a FULL transaction frame.
	w4, _, v4 := mgRecordingFixture(t)
	full, err := w4.begin(t.Context(), SecurityAccept, testRecipe(), &testCustody{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = full.rollback(nil) })
	full.descriptor.ID = MGScheduleRecording
	fullDefs := mgRecordingDefinitionsUnion()
	registry, err := stmttest.New(t, fullDefs)
	if err != nil {
		t.Fatal(err)
	}
	var fullSteps []statementStep
	for _, d := range fullDefs {
		fullSteps = append(fullSteps, statementStep{d.ID, Work{8, 1000, 8, 4096, time.Microsecond}})
	}
	fullTurn, err := newCommandTurn(full, registry, fullSteps)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fullTurn.closeRows() })
	fullRequest := v4.schedule()
	if got, err := acceptMGRecordingSchedule(t.Context(), fullTurn, mgRecordingFake(v4), v4.proof, t.Context(), mgRecordingScheduleReference(t, mgPersonalIdentity(1, 1), fullRequest), fullRequest); !errors.Is(err, ErrInvalid) || got != (mgRecordingAcceptance{}) {
		t.Fatal("FULL frame borrowed a NORMAL recording purpose", got, err)
	}

	// The production seam is the only non-test implementation and refuses.
	w5, _, v5 := mgRecordingFixture(t)
	turn = mgRecordingTurn(t, w5, MGScheduleRecording)
	if out, err := (mgPersonalProductionRoots{}).authorizeUSCurrentSession(t.Context(), turn, v5.proof, t.Context()); !errors.Is(err, ErrMGRootUnavailable) || out != nil {
		t.Fatal("production seam minted a session", out, err)
	}
	if _, err := mgRecordingAuthorize(t.Context(), turn, MGScheduleRecording, mgPersonalProductionRoots{}, v5.proof, t.Context()); !errors.Is(err, ErrMGRootUnavailable) {
		t.Fatal("production root admitted a recording command", err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
}

// A lost COMMIT reply is never success. The durable truth is read through a NEW
// read-only connection, never the quarantined writer pool, and each frame is
// all-or-nothing in both the before-commit and after-commit fault positions.
func TestMGRecordingUnknownCommitFreshReadback(t *testing.T) {
	type unknownCommitCase struct {
		name    string
		purpose CommandID
		prepare func(t *testing.T, w *executor, v mgRecordingFixtureValues, fake *mgPersonalFakeRoots) func(context.Context, *commandTurn, *usOwnerAuthorityProof) (mgRecordingAcceptance, error)
		check   func(t *testing.T, db *sql.DB, v mgRecordingFixtureValues, committed bool)
	}
	cases := []unknownCommitCase{
		{
			name: "schedule", purpose: MGScheduleRecording,
			prepare: func(t *testing.T, w *executor, v mgRecordingFixtureValues, fake *mgPersonalFakeRoots) func(context.Context, *commandTurn, *usOwnerAuthorityProof) (mgRecordingAcceptance, error) {
				request := v.schedule()
				reference := mgRecordingScheduleReference(t, mgPersonalIdentity(1, 1), request)
				return func(ctx context.Context, turn *commandTurn, witness *usOwnerAuthorityProof) (mgRecordingAcceptance, error) {
					return acceptMGRecordingSchedule(ctx, turn, fake, witness, t.Context(), reference, request)
				}
			},
			check: func(t *testing.T, db *sql.DB, v mgRecordingFixtureValues, committed bool) {
				var state string
				var recordings, receipts, sequence int64
				if err := db.QueryRowContext(context.Background(), `SELECT (SELECT count(*) FROM dvr_recordings),(SELECT count(*) FROM user_mutation_command_receipts),(SELECT last_command_sequence FROM user_mutation_command_owners WHERE root_id=?),(SELECT coalesce(min(state),'') FROM dvr_recordings)`, v.root).Scan(&recordings, &receipts, &sequence, &state); err != nil {
					t.Fatal(err)
				}
				if committed {
					if recordings != 1 || receipts != 1 || sequence != 1 || state != "scheduled" {
						t.Fatal("committed schedule is not whole", recordings, receipts, sequence, state)
					}
				} else if recordings != 0 || receipts != 0 || sequence != 0 {
					t.Fatal("uncommitted schedule leaked", recordings, receipts, sequence)
				}
			},
		},
		{
			name: "update", purpose: MGUpdateRecording,
			prepare: func(t *testing.T, w *executor, v mgRecordingFixtureValues, fake *mgPersonalFakeRoots) func(context.Context, *commandTurn, *usOwnerAuthorityProof) (mgRecordingAcceptance, error) {
				saved := scheduleMGRecording(t, w, v, fake, mgPersonalIdentity(1, 1))
				ackMGRecording(t, w, v.root, 1)
				options := v.options()
				options.Priority, options.BeforeMS = 8, 0
				request := v.update(saved.result.id, 1, options)
				reference := mgRecordingUpdateReference(t, mgPersonalIdentity(2, 2), request)
				return func(ctx context.Context, turn *commandTurn, witness *usOwnerAuthorityProof) (mgRecordingAcceptance, error) {
					return acceptMGRecordingUpdate(ctx, turn, fake, witness, t.Context(), reference, request)
				}
			},
			check: func(t *testing.T, db *sql.DB, v mgRecordingFixtureValues, committed bool) {
				var priority, startMS, receipts, sequence int64
				if err := db.QueryRowContext(context.Background(), `SELECT (SELECT priority FROM dvr_recordings),(SELECT start_ms FROM dvr_recordings),(SELECT count(*) FROM user_mutation_command_receipts),(SELECT last_command_sequence FROM user_mutation_command_owners WHERE root_id=?)`, v.root).Scan(&priority, &startMS, &receipts, &sequence); err != nil {
					t.Fatal(err)
				}
				if committed {
					if priority != 8 || receipts != 2 || sequence != 2 {
						t.Fatal("committed update is not whole", priority, receipts, sequence)
					}
				} else if priority != 5 || receipts != 1 || sequence != 1 {
					t.Fatal("uncommitted update leaked", priority, receipts, sequence)
				}
			},
		},
		{
			name: "cancel", purpose: MGCancelRecording,
			prepare: func(t *testing.T, w *executor, v mgRecordingFixtureValues, fake *mgPersonalFakeRoots) func(context.Context, *commandTurn, *usOwnerAuthorityProof) (mgRecordingAcceptance, error) {
				saved := scheduleMGRecording(t, w, v, fake, mgPersonalIdentity(1, 1))
				ackMGRecording(t, w, v.root, 1)
				request := v.cancel(saved.result.id, 1)
				reference := mgRecordingCancelReference(t, mgPersonalIdentity(2, 2), request)
				return func(ctx context.Context, turn *commandTurn, witness *usOwnerAuthorityProof) (mgRecordingAcceptance, error) {
					return acceptMGRecordingCancel(ctx, turn, fake, witness, t.Context(), reference, request)
				}
			},
			check: func(t *testing.T, db *sql.DB, v mgRecordingFixtureValues, committed bool) {
				var state string
				var generation, enabled, receipts, sequence int64
				if err := db.QueryRowContext(context.Background(), `SELECT (SELECT state FROM dvr_recordings),(SELECT cancel_generation FROM dvr_recordings),(SELECT enabled FROM live_reservations),(SELECT count(*) FROM user_mutation_command_receipts),(SELECT last_command_sequence FROM user_mutation_command_owners WHERE root_id=?)`, v.root).Scan(&state, &generation, &enabled, &receipts, &sequence); err != nil {
					t.Fatal(err)
				}
				if committed {
					if state != "cancelled" || generation != 1 || enabled != 0 || receipts != 2 || sequence != 2 {
						t.Fatal("committed cancel is not whole", state, generation, enabled, receipts, sequence)
					}
				} else if state != "scheduled" || generation != 0 || enabled != 1 || receipts != 1 || sequence != 1 {
					t.Fatal("uncommitted cancel leaked", state, generation, enabled, receipts, sequence)
				}
			},
		},
		{
			name: "keep", purpose: MGKeepRecording,
			prepare: func(t *testing.T, w *executor, v mgRecordingFixtureValues, fake *mgPersonalFakeRoots) func(context.Context, *commandTurn, *usOwnerAuthorityProof) (mgRecordingAcceptance, error) {
				saved := scheduleMGRecording(t, w, v, fake, mgPersonalIdentity(1, 1))
				ackMGRecording(t, w, v.root, 1)
				request := v.keep(saved.result.id, 1, true)
				reference := mgRecordingKeepReference(t, mgPersonalIdentity(2, 2), request)
				return func(ctx context.Context, turn *commandTurn, witness *usOwnerAuthorityProof) (mgRecordingAcceptance, error) {
					return acceptMGRecordingKeep(ctx, turn, fake, witness, t.Context(), reference, request)
				}
			},
			check: func(t *testing.T, db *sql.DB, v mgRecordingFixtureValues, committed bool) {
				var keep, receipts, sequence int64
				if err := db.QueryRowContext(context.Background(), `SELECT (SELECT keep FROM dvr_recordings),(SELECT count(*) FROM user_mutation_command_receipts),(SELECT last_command_sequence FROM user_mutation_command_owners WHERE root_id=?)`, v.root).Scan(&keep, &receipts, &sequence); err != nil {
					t.Fatal(err)
				}
				if committed {
					if keep != 1 || receipts != 2 || sequence != 2 {
						t.Fatal("committed keep is not whole", keep, receipts, sequence)
					}
				} else if keep != 0 || receipts != 1 || sequence != 1 {
					t.Fatal("uncommitted keep leaked", keep, receipts, sequence)
				}
			},
		},
	}
	for _, tc := range cases {
		for _, after := range []bool{false, true} {
			t.Run(tc.name+"/"+map[bool]string{false: "before_commit", true: "after_commit"}[after], func(t *testing.T) {
				w, path, v := mgRecordingFixture(t)
				fake := mgRecordingFake(v)
				run := tc.prepare(t, w, v, fake)
				if err := w.db.Close(); err != nil {
					t.Fatal(err)
				}
				connector, err := sqliteconn.Connector(path, w.config, sqliteconn.ExistingFile)
				if err != nil {
					t.Fatal(err)
				}
				installFaultPool(w, &faultConnector{Connector: connector, replyError: true, afterCommit: after})

				turn := mgRecordingTurn(t, w, tc.purpose)
				if _, err := run(t.Context(), turn, v.proof); err != nil {
					t.Fatal("fresh command did not reach COMMIT", err)
				}
				_ = turn.closeRows()
				if _, err := turn.transaction.commit(); !errors.Is(err, ErrResolveRequired) || !errors.Is(err, errCommitReply) {
					t.Fatal("uncertain COMMIT reported success", err)
				}
				if !w.ambiguous || !w.fenced {
					t.Fatal("uncertain writer was not fenced")
				}

				db := mgPersonalReadOnly(t, path)
				tc.check(t, db, v, after)
			})
		}
	}
}

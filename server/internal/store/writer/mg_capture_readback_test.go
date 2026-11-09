package writer

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

// A lost COMMIT reply is never success. The durable truth is read through a NEW
// read-only connection, never the quarantined writer pool, and each capture
// frame is all-or-nothing in both the before-commit and after-commit fault
// positions.
func TestMGCaptureUnknownCommitFreshReadback(t *testing.T) {
	type unknownCommitCase struct {
		name    string
		purpose CommandID
		prepare func(t *testing.T, w *executor, v mgCaptureFixtureValues, fake *mgPersonalFakeRoots) func(context.Context, *commandTurn, *usOwnerAuthorityProof) (mgCaptureAcceptance, error)
		check   func(t *testing.T, db *sql.DB, v mgCaptureFixtureValues, committed bool)
	}
	cases := []unknownCommitCase{
		{
			name: "claim", purpose: MGCaptureClaim,
			prepare: func(t *testing.T, w *executor, v mgCaptureFixtureValues, fake *mgPersonalFakeRoots) func(context.Context, *commandTurn, *usOwnerAuthorityProof) (mgCaptureAcceptance, error) {
				request := v.claimRequest()
				reference := mgCaptureClaimReference(t, mgPersonalIdentity(2, 2), request)
				return func(ctx context.Context, turn *commandTurn, witness *usOwnerAuthorityProof) (mgCaptureAcceptance, error) {
					return acceptMGCaptureClaim(ctx, turn, fake, witness, t.Context(), reference, request)
				}
			},
			check: func(t *testing.T, db *sql.DB, v mgCaptureFixtureValues, committed bool) {
				var state string
				var receipts, sequence, generation, revision, last, launch int64
				if err := db.QueryRowContext(context.Background(), `SELECT (SELECT count(*) FROM user_mutation_command_receipts),(SELECT last_command_sequence FROM user_mutation_command_owners WHERE root_id=?),(SELECT state FROM dvr_recordings),(SELECT claim_generation FROM dvr_recordings),(SELECT revision FROM dvr_recordings),(SELECT last_command_sequence FROM dvr_recordings),(SELECT count(*) FROM dvr_recordings WHERE capture_launch_id IS NOT NULL AND allocation_id=? AND artifact_id=?)`, v.root, v.allocation.id, v.artifact).Scan(&receipts, &sequence, &state, &generation, &revision, &last, &launch); err != nil {
					t.Fatal(err)
				}
				if committed {
					if receipts != 2 || sequence != 2 || state != "preparing" || generation != 1 || revision != 2 || last != 1 || launch != 1 {
						t.Fatal("committed claim is not whole", receipts, sequence, state, generation, revision, last, launch)
					}
				} else if receipts != 1 || sequence != 1 || state != "scheduled" || generation != 0 || revision != 1 || last != 0 || launch != 0 {
					t.Fatal("uncommitted claim leaked", receipts, sequence, state, generation, revision, last, launch)
				}
			},
		},
		{
			name: "renew", purpose: MGRenewCapture,
			prepare: func(t *testing.T, w *executor, v mgCaptureFixtureValues, fake *mgPersonalFakeRoots) func(context.Context, *commandTurn, *usOwnerAuthorityProof) (mgCaptureAcceptance, error) {
				claimMGCapture(t, w, v, fake, mgPersonalIdentity(2, 2))
				request := v.renewRequest()
				reference := mgCaptureRenewReference(t, mgPersonalIdentity(3, 3), request)
				return func(ctx context.Context, turn *commandTurn, witness *usOwnerAuthorityProof) (mgCaptureAcceptance, error) {
					return acceptMGCaptureRenew(ctx, turn, fake, witness, t.Context(), reference, request)
				}
			},
			check: func(t *testing.T, db *sql.DB, v mgCaptureFixtureValues, committed bool) {
				var receipts, sequence, lease, revision, last int64
				if err := db.QueryRowContext(context.Background(), `SELECT (SELECT count(*) FROM user_mutation_command_receipts),(SELECT last_command_sequence FROM user_mutation_command_owners WHERE root_id=?),(SELECT lease_until_ms FROM dvr_recordings),(SELECT revision FROM dvr_recordings),(SELECT last_command_sequence FROM dvr_recordings)`, v.root).Scan(&receipts, &sequence, &lease, &revision, &last); err != nil {
					t.Fatal(err)
				}
				if committed {
					if receipts != 3 || sequence != 3 || lease != v.renewLease() || revision != 3 || last != 2 {
						t.Fatal("committed renewal is not whole", receipts, sequence, lease, revision, last)
					}
				} else if receipts != 2 || sequence != 2 || lease != v.claimLease() || revision != 2 || last != 1 {
					t.Fatal("uncommitted renewal leaked", receipts, sequence, lease, revision, last)
				}
			},
		},
		{
			name: "checkpoint", purpose: MGCheckpointCapture,
			prepare: func(t *testing.T, w *executor, v mgCaptureFixtureValues, fake *mgPersonalFakeRoots) func(context.Context, *commandTurn, *usOwnerAuthorityProof) (mgCaptureAcceptance, error) {
				claimMGCapture(t, w, v, fake, mgPersonalIdentity(2, 2))
				renewMGCapture(t, w, v, fake, mgPersonalIdentity(3, 3))
				request := v.checkpointRequest()
				reference := mgCaptureCheckpointReference(t, mgPersonalIdentity(4, 4), request)
				return func(ctx context.Context, turn *commandTurn, witness *usOwnerAuthorityProof) (mgCaptureAcceptance, error) {
					return acceptMGCaptureCheckpoint(ctx, turn, fake, witness, t.Context(), reference, request)
				}
			},
			check: func(t *testing.T, db *sql.DB, v mgCaptureFixtureValues, committed bool) {
				request := v.checkpointRequest()
				var receipts, sequence, start, end, gaps, revision, last int64
				if err := db.QueryRowContext(context.Background(), `SELECT (SELECT count(*) FROM user_mutation_command_receipts),(SELECT last_command_sequence FROM user_mutation_command_owners WHERE root_id=?),(SELECT captured_start_ms FROM dvr_recordings),(SELECT captured_end_ms FROM dvr_recordings),(SELECT capture_gap_count FROM dvr_recordings),(SELECT revision FROM dvr_recordings),(SELECT last_command_sequence FROM dvr_recordings)`, v.root).Scan(&receipts, &sequence, &start, &end, &gaps, &revision, &last); err != nil {
					t.Fatal(err)
				}
				if committed {
					if receipts != 4 || sequence != 4 || start != request.capturedStartMS || end != request.capturedEndMS || gaps != request.gapCount || revision != 4 || last != 3 {
						t.Fatal("committed checkpoint is not whole", receipts, sequence, start, end, gaps, revision, last)
					}
				} else if receipts != 3 || sequence != 3 || start != 0 || end != 0 || gaps != 0 || revision != 3 || last != 2 {
					t.Fatal("uncommitted checkpoint leaked", receipts, sequence, start, end, gaps, revision, last)
				}
			},
		},
	}
	for _, tc := range cases {
		for _, after := range []bool{false, true} {
			t.Run(tc.name+"/"+map[bool]string{false: "before_commit", true: "after_commit"}[after], func(t *testing.T) {
				w, path, v := mgCaptureFixture(t)
				fake := mgCaptureFake(v)
				run := tc.prepare(t, w, v, fake)
				if err := w.db.Close(); err != nil {
					t.Fatal(err)
				}
				connector, err := sqliteconn.Connector(path, w.config, sqliteconn.ExistingFile)
				if err != nil {
					t.Fatal(err)
				}
				installFaultPool(w, &faultConnector{Connector: connector, replyError: true, afterCommit: after})

				turn := mgCaptureTurn(t, w, tc.purpose)
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

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

func TestMGPersonalPreferenceAcceptReplayAndRetired(t *testing.T) {
	w, _, v := mgPersonalFixture(t)
	fake := mgPersonalFake(t, v)
	identity := mgPersonalIdentity(1, 1)
	request := mgPersonalPreferenceRequest{owner: v.owner, target: v.live, favorite: true}
	reference := mgPersonalPreferenceReference(t, identity, request)

	turn := mgPersonalTurn(t, w, MGSetChannelPreference)
	out, err := acceptMGPersonalPreference(t.Context(), turn, fake, v.proof, t.Context(), reference, request)
	want := mgPersonalPreferenceResult{source: "personal-live", channel: "live-channel", revision: 1, favorite: true}
	if err != nil || out.decision != receiptFresh || out.preference != want || out.digest == (ResultDigest{}) {
		t.Fatal(out, err)
	}
	mgPersonalCommit(t, turn)

	// Identical retry: the original saved scalar is returned with no new row,
	// no receipt and no owner advance.
	turn = mgPersonalTurn(t, w, MGSetChannelPreference)
	replay, err := acceptMGPersonalPreference(t.Context(), turn, fake, v.proof, t.Context(), reference, request)
	if err != nil || replay.decision != receiptReplay || replay.preference != want || replay.digest != out.digest {
		t.Fatal(replay, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}

	// A tampered retained payload is never decoded as the original result: the
	// exact length/digest binding fails closed on replay.
	if _, err := w.db.ExecContext(t.Context(), `UPDATE user_mutation_command_receipts SET result_payload=substr(result_payload,1,length(result_payload)-1) WHERE root_id=?`, v.root); err != nil {
		t.Fatal(err)
	}
	turn = mgPersonalTurn(t, w, MGSetChannelPreference)
	if got, err := acceptMGPersonalPreference(t.Context(), turn, fake, v.proof, t.Context(), reference, request); !errors.Is(err, ErrResolveRequired) || got != (mgPersonalAcceptance{}) {
		t.Fatal("tampered payload replayed", got, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}

	// A different input under the same command identity/digest conflicts before
	// any effect; so does a caller digest that disagrees with the typed request.
	turn = mgPersonalTurn(t, w, MGSetChannelPreference)
	changed := request
	changed.favorite = false
	if got, err := acceptMGPersonalPreference(t.Context(), turn, fake, v.proof, t.Context(), reference, changed); !errors.Is(err, ErrConflict) || got != (mgPersonalAcceptance{}) {
		t.Fatal("changed input under one identity", got, err)
	}
	bad := reference
	bad.RequestDigest[0] ^= 0xff
	if got, err := acceptMGPersonalPreference(t.Context(), turn, fake, v.proof, t.Context(), bad, request); !errors.Is(err, ErrConflict) || got != (mgPersonalAcceptance{}) {
		t.Fatal("caller digest not recomputed", got, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}

	// ACK retires the original: replay is a complete zero result, never a
	// reconstructed success and never a fresh effect.
	if _, err := w.db.ExecContext(t.Context(), `UPDATE user_mutation_command_owners SET acknowledged_command_sequence=1 WHERE root_id=?`, v.root); err != nil {
		t.Fatal(err)
	}
	turn = mgPersonalTurn(t, w, MGSetChannelPreference)
	retired, err := acceptMGPersonalPreference(t.Context(), turn, fake, v.proof, t.Context(), reference, request)
	if err != nil || retired.decision != receiptRetired || retired.preference != (mgPersonalPreferenceResult{}) || retired.digest != (ResultDigest{}) {
		t.Fatal(retired, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}

	var preferences, receipts, sequence, bytes int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM live_channel_preferences),(SELECT count(*) FROM user_mutation_command_receipts),(SELECT last_command_sequence FROM user_mutation_command_owners WHERE root_id=?),(SELECT receipt_variable_bytes FROM user_mutation_command_owners WHERE root_id=?)`, v.root, v.root).Scan(&preferences, &receipts, &sequence, &bytes); err != nil {
		t.Fatal(err)
	}
	if preferences != 1 || receipts != 1 || sequence != 1 || bytes <= 0 {
		t.Fatal(preferences, receipts, sequence, bytes)
	}
}

func TestMGPersonalReminderAcceptReplayAndGuideReplacement(t *testing.T) {
	w, _, v := mgPersonalFixture(t)
	fake := mgPersonalFake(t, v)
	identity := mgPersonalIdentity(1, 1)
	request := v.reminder(true)
	reference := mgPersonalReminderReference(t, identity, request)

	turn := mgPersonalTurn(t, w, MGSetReminder)
	saved, err := acceptMGPersonalReminder(t.Context(), turn, fake, v.proof, t.Context(), reference, request)
	if err != nil || saved.decision != receiptFresh || saved.reminder.libraryGeneration != v.library.generation || saved.reminder.externalGeneration != "" || saved.reminder.start != v.start.Format(time.RFC3339) {
		t.Fatal(saved, err)
	}
	mgPersonalCommit(t, turn)

	// Replace the live source head and revoke nothing else. The retained LC
	// reminder still replays its ORIGINAL saved row and the old LC generation
	// remains pinned.
	if _, err := w.db.ExecContext(t.Context(), `UPDATE live_sources SET revision=8,active_generation='replacement-generation' WHERE id='personal-live'`); err != nil {
		t.Fatal(err)
	}
	turn = mgPersonalTurn(t, w, MGSetReminder)
	replay, err := acceptMGPersonalReminder(t.Context(), turn, fake, v.proof, t.Context(), reference, request)
	if err != nil || replay.decision != receiptReplay || replay.reminder != saved.reminder {
		t.Fatal(replay, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}

	// The same reminder can be deleted after guide replacement; its revision CAS
	// is the only ownership fact, and the LC generation remains pinned until then.
	deleteRequest := mgPersonalDeleteRequest{owner: v.owner, id: saved.reminder.id, expected: 1}
	deleteReference := mgPersonalDeleteReference(t, mgPersonalIdentity(2, 2), deleteRequest)
	if _, err := w.db.ExecContext(t.Context(), `UPDATE user_mutation_command_owners SET acknowledged_command_sequence=1 WHERE root_id=?`, v.root); err != nil {
		t.Fatal(err)
	}
	turn = mgPersonalTurn(t, w, MGDeleteReminder)
	deleted, err := acceptMGPersonalReminderDelete(t.Context(), turn, fake, v.proof, t.Context(), deleteReference, deleteRequest)
	if err != nil || deleted.decision != receiptFresh || deleted.reminder != saved.reminder {
		t.Fatal(deleted, err)
	}
	mgPersonalCommit(t, turn)
	var reminders int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM live_reminders`).Scan(&reminders); err != nil || reminders != 0 {
		t.Fatal(reminders, err)
	}
}

func TestMGPersonalAcceptingAuthorityCells(t *testing.T) {
	// A cached stamp that disagrees with its native witness is forged and never
	// reaches the seam.
	w, _, v := mgPersonalFixture(t)
	fake := mgPersonalFake(t, v)
	request := mgPersonalPreferenceRequest{owner: v.owner, target: v.live, favorite: true}
	reference := mgPersonalPreferenceReference(t, mgPersonalIdentity(1, 1), request)
	forged := *v.proof
	forged.tokenExpiry = forged.tokenExpiry.Add(48 * time.Hour)
	turn := mgPersonalTurn(t, w, MGSetChannelPreference)
	if got, err := acceptMGPersonalPreference(t.Context(), turn, fake, &forged, t.Context(), reference, request); !errors.Is(err, ErrDisclosureDenied) || got != (mgPersonalAcceptance{}) {
		t.Fatal("forged cached stamp", got, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}

	// A self-consistent witness for another issuer still fails the exact frozen
	// root identity: the seam is not a caller-selected account.
	other := v.proof.witness
	other.issuer.account = "another-account"
	forgedIssuer, err := prepareUSOwnerAuthority(other)
	if err != nil {
		t.Fatal(err)
	}
	turn = mgPersonalTurn(t, w, MGSetChannelPreference)
	if got, err := acceptMGPersonalPreference(t.Context(), turn, fake, forgedIssuer, t.Context(), reference, request); !errors.Is(err, ErrDisclosureDenied) || got != (mgPersonalAcceptance{}) {
		t.Fatal("forged issuer", got, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}

	// A cancelled caller lifetime refuses before any custody or effect.
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	turn = mgPersonalTurn(t, w, MGSetChannelPreference)
	if got, err := acceptMGPersonalPreference(cancelled, turn, fake, v.proof, t.Context(), reference, request); !errors.Is(err, context.Canceled) || got != (mgPersonalAcceptance{}) {
		t.Fatal("cancelled lifetime", got, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}

	// A fresh lookup whose root drains before persistence cannot apply.
	turn = mgPersonalTurn(t, w, MGSetChannelPreference)
	session, err := mgPersonalAuthorize(t.Context(), turn, MGSetChannelPreference, fake, v.proof, t.Context())
	if err != nil {
		t.Fatal(err)
	}
	// A session for another root never admits this reference.
	foreign := uoRootReference{root: "zyxwvutsrqponmlkjihgfe", kind: MGSetChannelPreference, identity: reference.Identity, request: reference.RequestDigest}
	if _, err := mgPersonalRootLookupCommand(t.Context(), turn, session, foreign); !errors.Is(err, ErrInvalid) {
		t.Fatal("foreign root passed the session binding", err)
	}
	ref := uoRootReference{root: session.mgPersonalRootID(), kind: MGSetChannelPreference, identity: reference.Identity, request: reference.RequestDigest}
	lookup, err := mgPersonalRootLookupCommand(t.Context(), turn, session, ref)
	if err != nil || lookup.decision != receiptFresh {
		t.Fatal(lookup.decision, err)
	}
	if _, err := turn.transaction.conn.ExecContext(t.Context(), `UPDATE user_mutation_roots SET state='draining' WHERE id=?`, v.root); err != nil {
		t.Fatal(err)
	}
	if _, err := mgPersonalRootPersist(t.Context(), turn, lookup, mgPersonalPreferenceResult{source: "personal-live", channel: "live-channel", revision: 1, favorite: true}, 1); !errors.Is(err, ErrUORootGone) {
		t.Fatal("draining root persisted a fresh effect", err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	var preferences int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM live_channel_preferences`).Scan(&preferences); err != nil || preferences != 0 {
		t.Fatal("draining-root effect leaked", preferences, err)
	}

	// A NORMAL-only purpose cannot borrow a FULL transaction frame. The purpose
	// guard is separately observable from the missing mint.
	w2, _, v2 := mgPersonalFixture(t)
	full, err := w2.begin(t.Context(), SecurityAccept, testRecipe(), &testCustody{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = full.rollback(nil) })
	full.descriptor.ID = MGSetChannelPreference
	fullDefs := mgPersonalDefinitionsUnion()
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
	fullRequest := mgPersonalPreferenceRequest{owner: v2.owner, target: v2.live}
	stale := mgPersonalPreferenceReference(t, mgPersonalIdentity(1, 1), fullRequest)
	if got, err := acceptMGPersonalPreference(t.Context(), fullTurn, mgPersonalFake(t, v2), v2.proof, t.Context(), stale, fullRequest); !errors.Is(err, ErrInvalid) || got != (mgPersonalAcceptance{}) {
		t.Fatal("FULL frame borrowed a NORMAL personal purpose", got, err)
	}
}

func TestMGPersonalReplayReauthorizesAfterRevocation(t *testing.T) {
	w, _, v := mgPersonalFixture(t)
	fake := mgPersonalFake(t, v)
	identity := mgPersonalIdentity(1, 1)
	request := mgPersonalPreferenceRequest{owner: v.owner, target: v.live, hidden: true}
	reference := mgPersonalPreferenceReference(t, identity, request)
	turn := mgPersonalTurn(t, w, MGSetChannelPreference)
	if _, err := acceptMGPersonalPreference(t.Context(), turn, fake, v.proof, t.Context(), reference, request); err != nil {
		t.Fatal(err)
	}
	mgPersonalCommit(t, turn)

	if _, err := w.db.ExecContext(t.Context(), `UPDATE authorization_session_families SET revoked=1,revoked_reason='explicit_signout',revoked_at=? WHERE id=?`, w.now().UTC().Format("2006-01-02T15:04:05Z"), v.proof.witness.issuer.family); err != nil {
		t.Fatal(err)
	}
	turn = mgPersonalTurn(t, w, MGSetChannelPreference)
	got, err := acceptMGPersonalPreference(t.Context(), turn, fake, v.proof, t.Context(), reference, request)
	if !errors.Is(err, ErrDisclosureDenied) || got != (mgPersonalAcceptance{}) {
		t.Fatal("revoked session replayed the saved result", got, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}

	// The current authority check precedes retained custody: with the receipt
	// rows physically gone, a revoked session is still DENIED rather than a
	// ResolveRequired custody miss.
	if _, err := w.db.ExecContext(t.Context(), `DELETE FROM user_mutation_command_receipts WHERE root_id=?`, v.root); err != nil {
		t.Fatal(err)
	}
	turn = mgPersonalTurn(t, w, MGSetChannelPreference)
	if got, err := acceptMGPersonalPreference(t.Context(), turn, fake, v.proof, t.Context(), reference, request); !errors.Is(err, ErrDisclosureDenied) || errors.Is(err, ErrResolveRequired) || got != (mgPersonalAcceptance{}) {
		t.Fatal("revoked session reached retained custody first", got, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
}

func TestMGPersonalProductionRootSeamRefuses(t *testing.T) {
	w, _, v := mgPersonalFixture(t)
	turn := mgPersonalTurn(t, w, MGSetChannelPreference)
	out, err := mgPersonalProductionRoots{}.authorizeUSCurrentSession(t.Context(), turn, v.proof, t.Context())
	if !errors.Is(err, ErrMGRootUnavailable) || out != nil {
		t.Fatal("production seam minted a session", out, err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
}

// A lost COMMIT reply is never success. The durable truth is read through a NEW
// read-only connection, never the quarantined writer pool, and the frame is
// all-or-nothing in both the before-commit and after-commit fault positions.
func TestMGPersonalUnknownCommitFreshReadback(t *testing.T) {
	type unknownCommitCase struct {
		name    string
		purpose CommandID
		prepare func(t *testing.T, w *executor, v mgPersonalFixtureValues) func(context.Context, *commandTurn, *usOwnerAuthorityProof) (mgPersonalAcceptance, error)
		read    func(t *testing.T, db *sql.DB, root string) (rows, receipts, sequence int64)
	}
	cases := []unknownCommitCase{
		{
			name: "channel-preference", purpose: MGSetChannelPreference,
			prepare: func(t *testing.T, w *executor, v mgPersonalFixtureValues) func(context.Context, *commandTurn, *usOwnerAuthorityProof) (mgPersonalAcceptance, error) {
				request := mgPersonalPreferenceRequest{owner: v.owner, target: v.live, favorite: true}
				reference := mgPersonalPreferenceReference(t, mgPersonalIdentity(1, 1), request)
				fake := mgPersonalFake(t, v)
				return func(ctx context.Context, turn *commandTurn, witness *usOwnerAuthorityProof) (mgPersonalAcceptance, error) {
					return acceptMGPersonalPreference(ctx, turn, fake, witness, t.Context(), reference, request)
				}
			},
			read: func(t *testing.T, db *sql.DB, root string) (int64, int64, int64) {
				var rows, receipts, sequence int64
				if err := db.QueryRowContext(context.Background(), `SELECT (SELECT count(*) FROM live_channel_preferences),(SELECT count(*) FROM user_mutation_command_receipts),(SELECT last_command_sequence FROM user_mutation_command_owners WHERE root_id=?)`, root).Scan(&rows, &receipts, &sequence); err != nil {
					t.Fatal(err)
				}
				return rows, receipts, sequence
			},
		},
		{
			name: "set-reminder", purpose: MGSetReminder,
			prepare: func(t *testing.T, w *executor, v mgPersonalFixtureValues) func(context.Context, *commandTurn, *usOwnerAuthorityProof) (mgPersonalAcceptance, error) {
				request := v.reminder(true)
				reference := mgPersonalReminderReference(t, mgPersonalIdentity(1, 1), request)
				fake := mgPersonalFake(t, v)
				return func(ctx context.Context, turn *commandTurn, witness *usOwnerAuthorityProof) (mgPersonalAcceptance, error) {
					return acceptMGPersonalReminder(ctx, turn, fake, witness, t.Context(), reference, request)
				}
			},
			read: func(t *testing.T, db *sql.DB, root string) (int64, int64, int64) {
				var rows, receipts, sequence int64
				if err := db.QueryRowContext(context.Background(), `SELECT (SELECT count(*) FROM live_reminders),(SELECT count(*) FROM user_mutation_command_receipts),(SELECT last_command_sequence FROM user_mutation_command_owners WHERE root_id=?)`, root).Scan(&rows, &receipts, &sequence); err != nil {
					t.Fatal(err)
				}
				return rows, receipts, sequence
			},
		},
		{
			name: "delete-reminder", purpose: MGDeleteReminder,
			prepare: func(t *testing.T, w *executor, v mgPersonalFixtureValues) func(context.Context, *commandTurn, *usOwnerAuthorityProof) (mgPersonalAcceptance, error) {
				request := v.reminder(true)
				if _, err := w.db.ExecContext(t.Context(), `INSERT INTO live_reminders(id,owner_key,kind,source_id,channel_id,generation_id,programme_id,revision,start_utc) VALUES(?,?,?,?,?,?,?,?,?)`,
					request.id(), request.owner.key(), string(request.target.kind), request.target.source, request.target.channel, request.target.generation, request.programme, int64(1), v.start.Format(time.RFC3339)); err != nil {
					t.Fatal(err)
				}
				del := mgPersonalDeleteRequest{owner: v.owner, id: request.id(), expected: 1}
				reference := mgPersonalDeleteReference(t, mgPersonalIdentity(1, 1), del)
				fake := mgPersonalFake(t, v)
				return func(ctx context.Context, turn *commandTurn, witness *usOwnerAuthorityProof) (mgPersonalAcceptance, error) {
					return acceptMGPersonalReminderDelete(ctx, turn, fake, witness, t.Context(), reference, del)
				}
			},
			read: func(t *testing.T, db *sql.DB, root string) (int64, int64, int64) {
				var rows, receipts, sequence int64
				if err := db.QueryRowContext(context.Background(), `SELECT (SELECT count(*) FROM live_reminders),(SELECT count(*) FROM user_mutation_command_receipts),(SELECT last_command_sequence FROM user_mutation_command_owners WHERE root_id=?)`, root).Scan(&rows, &receipts, &sequence); err != nil {
					t.Fatal(err)
				}
				return rows, receipts, sequence
			},
		},
	}
	for _, tc := range cases {
		for _, after := range []bool{false, true} {
			t.Run(tc.name+"/"+map[bool]string{false: "before_commit", true: "after_commit"}[after], func(t *testing.T) {
				w, path, v := mgPersonalFixture(t)
				run := tc.prepare(t, w, v)
				var beforeRows int64
				if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM live_reminders`).Scan(&beforeRows); err != nil {
					t.Fatal(err)
				}
				if err := w.db.Close(); err != nil {
					t.Fatal(err)
				}
				connector, err := sqliteconn.Connector(path, w.config, sqliteconn.ExistingFile)
				if err != nil {
					t.Fatal(err)
				}
				installFaultPool(w, &faultConnector{Connector: connector, replyError: true, afterCommit: after})

				turn := mgPersonalTurn(t, w, tc.purpose)
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
				rows, receipts, sequence := tc.read(t, db, v.root)
				if after {
					if receipts != 1 || sequence != 1 || (tc.name == "delete-reminder" && rows != 0) {
						t.Fatal("committed frame is not whole", rows, receipts, sequence)
					}
				} else if rows != beforeRows || receipts != 0 || sequence != 0 {
					t.Fatal("uncommitted frame leaked", rows, receipts, sequence)
				}
			})
		}
	}
}

// Every accepting command carries the same required cells: an identical retry
// replays once, a durable ACK retires the original, a forged issuer is denied
// and a cancelled caller lifetime refuses with zero output.
func TestMGPersonalAcceptingCellsForEveryCommand(t *testing.T) {
	w, _, v := mgPersonalFixture(t)
	fake := mgPersonalFake(t, v)

	// Seed one retained reminder for the delete command.
	seedRequest := v.reminder(true)
	seedIdentity := mgPersonalIdentity(1, 1)
	seedReference := mgPersonalReminderReference(t, seedIdentity, seedRequest)
	turn := mgPersonalTurn(t, w, MGSetReminder)
	seed, err := acceptMGPersonalReminder(t.Context(), turn, fake, v.proof, t.Context(), seedReference, seedRequest)
	if err != nil || seed.decision != receiptFresh {
		t.Fatal(seed, err)
	}
	mgPersonalCommit(t, turn)
	ackMGPersonal(t, w, v.root, 1)

	preference := mgPersonalPreferenceRequest{owner: v.owner, target: v.library, favorite: true}
	reminder := v.reminder(false)
	deleteRequest := mgPersonalDeleteRequest{owner: v.owner, id: seed.reminder.id, expected: 1}
	cases := []struct {
		name    string
		purpose CommandID
		seq     int64
		op      byte
		run     func(context.Context, *commandTurn, *usOwnerAuthorityProof, context.Context) (mgPersonalAcceptance, error)
		want    mgPersonalAcceptance
	}{
		{
			name: "channel-preference", purpose: MGSetChannelPreference, seq: 2, op: 2,
			run: func(ctx context.Context, turn *commandTurn, witness *usOwnerAuthorityProof, lifetime context.Context) (mgPersonalAcceptance, error) {
				return acceptMGPersonalPreference(ctx, turn, fake, witness, lifetime, mgPersonalPreferenceReference(t, mgPersonalIdentity(2, 2), preference), preference)
			},
			want: mgPersonalAcceptance{decision: receiptFresh, preference: mgPersonalPreferenceResult{source: "library:library-channel", channel: "library-channel", revision: 1, favorite: true}},
		},
		{
			name: "set-reminder", purpose: MGSetReminder, seq: 3, op: 3,
			run: func(ctx context.Context, turn *commandTurn, witness *usOwnerAuthorityProof, lifetime context.Context) (mgPersonalAcceptance, error) {
				return acceptMGPersonalReminder(ctx, turn, fake, witness, lifetime, mgPersonalReminderReference(t, mgPersonalIdentity(3, 3), reminder), reminder)
			},
			want: mgPersonalAcceptance{decision: receiptFresh, reminder: mgPersonalReminderFacts{}.result()},
		},
		{
			name: "delete-reminder", purpose: MGDeleteReminder, seq: 4, op: 4,
			run: func(ctx context.Context, turn *commandTurn, witness *usOwnerAuthorityProof, lifetime context.Context) (mgPersonalAcceptance, error) {
				return acceptMGPersonalReminderDelete(ctx, turn, fake, witness, lifetime, mgPersonalDeleteReference(t, mgPersonalIdentity(4, 4), deleteRequest), deleteRequest)
			},
			want: mgPersonalAcceptance{decision: receiptFresh, reminder: seed.reminder},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			turn := mgPersonalTurn(t, w, tc.purpose)
			fresh, err := tc.run(t.Context(), turn, v.proof, t.Context())
			if err != nil || fresh.decision != receiptFresh || fresh.digest == (ResultDigest{}) {
				t.Fatal(fresh, err)
			}
			if tc.name != "set-reminder" && tc.name != "delete-reminder" && fresh.preference != tc.want.preference {
				t.Fatal(fresh)
			}
			if tc.name == "set-reminder" {
				want := tc.want.reminder
				facts := v.reminder(false)
				want.id, want.owner = fresh.reminder.id, fresh.reminder.owner
				want.kind, want.source, want.channel, want.generation, want.programme = "live-source", facts.target.source, facts.target.channel, facts.target.generation, facts.programme
				want.revision, want.start, want.externalGeneration = 1, fresh.reminder.start, facts.target.generation
				if fresh.reminder != want {
					t.Fatal(fresh.reminder, want)
				}
			}
			if tc.name == "delete-reminder" && fresh.reminder != tc.want.reminder {
				t.Fatal(fresh.reminder, tc.want.reminder)
			}
			mgPersonalCommit(t, turn)

			// Identical retry replays the original saved result with zero effects.
			turn = mgPersonalTurn(t, w, tc.purpose)
			replay, err := tc.run(t.Context(), turn, v.proof, t.Context())
			if err != nil || replay.decision != receiptReplay || replay.digest != fresh.digest {
				t.Fatal(replay, err)
			}
			if _, err := turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}

			// ACK retires it: zero result, never a reconstructed success.
			ackMGPersonal(t, w, v.root, tc.seq)
			turn = mgPersonalTurn(t, w, tc.purpose)
			retired, err := tc.run(t.Context(), turn, v.proof, t.Context())
			if err != nil || retired.decision != receiptRetired || retired.preference != (mgPersonalPreferenceResult{}) || retired.reminder != (mgPersonalReminderResult{}) || retired.digest != (ResultDigest{}) {
				t.Fatal(retired, err)
			}
			if _, err := turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}

			// Forged issuer and cancelled lifetime refuse with zero output. They
			// fail before custody, so they consume no sequence.
			forged := *v.proof
			forged.tokenExpiry = forged.tokenExpiry.Add(time.Hour)
			turn = mgPersonalTurn(t, w, tc.purpose)
			if got, err := tc.run(t.Context(), turn, &forged, t.Context()); !errors.Is(err, ErrDisclosureDenied) || got != (mgPersonalAcceptance{}) {
				t.Fatal("forged issuer", got, err)
			}
			if _, err := turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}
			cancelled, cancel := context.WithCancel(t.Context())
			cancel()
			turn = mgPersonalTurn(t, w, tc.purpose)
			if got, err := tc.run(cancelled, turn, v.proof, t.Context()); !errors.Is(err, context.Canceled) || got != (mgPersonalAcceptance{}) {
				t.Fatal("cancelled lifetime", got, err)
			}
			if _, err := turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func ackMGPersonal(t *testing.T, w *executor, root string, sequence int64) {
	t.Helper()
	if _, err := w.db.ExecContext(t.Context(), `UPDATE user_mutation_command_owners SET acknowledged_command_sequence=? WHERE root_id=? AND last_command_sequence=?`, sequence, root, sequence); err != nil {
		t.Fatal(err)
	}
}

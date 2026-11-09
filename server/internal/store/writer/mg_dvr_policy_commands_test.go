package writer

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

var mgDVRPolicyDefaultsInputFixture = mgDVRDefaultsInput{prePaddingMS: 90001, postPaddingMS: 300999, keepMode: "keep-days", keepDays: 30, conflict: "stop-watching"}

func mgDVRPolicyDefaultsRequestAt(t *testing.T, v mgDVRPolicyCommandValues, expected int64, id CommandIdentity) (mgDVRDefaultsPolicyRequest, ReceiptReference) {
	t.Helper()
	r := mgDVRDefaultsPolicyRequest{expected: expected, input: mgDVRPolicyDefaultsInputFixture}
	return r, mgDVRPolicyDefaultsReference(t, id, r)
}
func mgDVRPolicyStorageRequestAt(t *testing.T, expected int64, policy mgDVRStorageDesired, id CommandIdentity) (mgDVRStoragePolicyRequest, ReceiptReference) {
	t.Helper()
	r := mgDVRStoragePolicyRequest{expected: expected, policy: policy}
	return r, mgDVRPolicyStorageReference(t, id, r)
}
func mgDVRPolicyDefaultsRun(fake *mgPersonalFakeRoots, reference ReceiptReference, request mgDVRDefaultsPolicyRequest) func(context.Context, *commandTurn, *usOwnerAuthorityProof, context.Context) (mgDVRPolicyAcceptance, error) {
	return func(ctx context.Context, turn *commandTurn, witness *usOwnerAuthorityProof, lifetime context.Context) (mgDVRPolicyAcceptance, error) {
		return acceptMGSetDVRDefaults(ctx, turn, fake, witness, lifetime, reference, request)
	}
}
func mgDVRPolicyStorageRun(fake *mgPersonalFakeRoots, reference ReceiptReference, request mgDVRStoragePolicyRequest) func(context.Context, *commandTurn, *usOwnerAuthorityProof, context.Context) (mgDVRPolicyAcceptance, error) {
	return func(ctx context.Context, turn *commandTurn, witness *usOwnerAuthorityProof, lifetime context.Context) (mgDVRPolicyAcceptance, error) {
		return acceptMGSetDVRStoragePolicy(ctx, turn, fake, witness, lifetime, reference, request)
	}
}

func mgDVRPolicyRollback(t *testing.T, turn *commandTurn) {
	t.Helper()
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
}

func mgDVRPolicyDefaultsWant(v mgDVRPolicyCommandValues) mgDVRPolicyResult {
	return mgDVRPolicyResult{
		kind: mgDVRPolicyDefaultsKind, revision: v.defaults.revision + 1,
		beforeSeconds: 90, afterSeconds: 300, retentionDays: 30, episodeLimit: 0, conflict: "stop-watching",
		showTemplate: v.defaults.showTemplate, movieTemplate: v.defaults.movieTemplate,
	}
}
func mgDVRPolicyStorageWant(v mgDVRPolicyCommandValues) mgDVRPolicyResult {
	return mgDVRPolicyResult{
		kind: mgDVRPolicyStorageKind, revision: v.storage.revision + 1,
		retentionDays: 30, episodeLimit: 10, floorBytes: 1024, capBytes: 8192,
	}
}
func mgDVRPolicyStorageFixture() mgDVRStorageDesired {
	return mgDVRStorageDesired{30, 10, 1024, 8192}
}

// The defaults command saves one scalar result under the original public root.
// An identical retry replays the SAVED values even after the live singleton has
// moved, an ACK retires the original, and identical desired values are still an
// explicit next revision rather than a silently skipped receipt.
func TestMGDVRPolicyDefaultsAcceptReplayRetiredAndNoOp(t *testing.T) {
	w, _, v := mgDVRPolicyCommandFixture(t)
	fake := mgDVRPolicyFake(t, v)
	request, reference := mgDVRPolicyDefaultsRequestAt(t, v, v.defaults.revision, mgPersonalIdentity(1, 1))
	want := mgDVRPolicyDefaultsWant(v)

	turn := mgDVRPolicyCommandTurn(t, w, MGSetDVRDefaults)
	out, err := acceptMGSetDVRDefaults(t.Context(), turn, fake, v.proof, t.Context(), reference, request)
	if err != nil || out.decision != receiptFresh || out.policy != want || out.digest == (ResultDigest{}) {
		t.Fatal(out, err)
	}
	mgDVRPolicyCommandCommit(t, turn)

	var revision, before, after, days, episodes int64
	var conflict, show, movie string
	if err := w.db.QueryRowContext(t.Context(), `SELECT revision,before_seconds,after_seconds,retention_days,episode_limit,live_tuner_conflict,show_folder_template,movie_folder_template FROM dvr_defaults WHERE singleton=1`).Scan(&revision, &before, &after, &days, &episodes, &conflict, &show, &movie); err != nil {
		t.Fatal(err)
	}
	if revision != 8 || before != 90 || after != 300 || days != 30 || episodes != 0 || conflict != "stop-watching" || show != v.defaults.showTemplate || movie != v.defaults.movieTemplate {
		t.Fatal("native defaults not applied", revision, before, after, days, episodes, conflict, show, movie)
	}
	var receipts, sequence int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM user_mutation_command_receipts WHERE root_id=?),(SELECT last_command_sequence FROM user_mutation_command_owners WHERE root_id=?)`, v.root, v.root).Scan(&receipts, &sequence); err != nil {
		t.Fatal(err)
	}
	if receipts != 1 || sequence != 1 {
		t.Fatal(receipts, sequence)
	}

	// Move the live singleton behind the receipt. The replay must return the
	// ORIGINAL saved scalar, not today's row.
	if _, err := w.db.ExecContext(t.Context(), `UPDATE dvr_defaults SET revision=20,before_seconds=1,after_seconds=2,retention_days=3,episode_limit=4,live_tuner_conflict='keep-watching' WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	turn = mgDVRPolicyCommandTurn(t, w, MGSetDVRDefaults)
	replay, err := acceptMGSetDVRDefaults(t.Context(), turn, fake, v.proof, t.Context(), reference, request)
	if err != nil || replay.decision != receiptReplay || replay.policy != want || replay.digest != out.digest {
		t.Fatal("replay did not return the saved scalar", replay, err)
	}
	mgDVRPolicyRollback(t, turn)
	var unchanged int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM dvr_defaults WHERE singleton=1 AND revision=20`).Scan(&unchanged); err != nil || unchanged != 1 {
		t.Fatal("replay rewrote the live policy", unchanged, err)
	}

	// Restore the post-commit singleton so the following cells start from the
	// same native revision as the saved result.
	if _, err := w.db.ExecContext(t.Context(), `UPDATE dvr_defaults SET revision=8,before_seconds=90,after_seconds=300,retention_days=30,episode_limit=0,live_tuner_conflict='stop-watching' WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}

	// A different input under the same identity conflicts before any effect.
	turn = mgDVRPolicyCommandTurn(t, w, MGSetDVRDefaults)
	changed := request
	changed.input.keepDays = 31
	if got, err := acceptMGSetDVRDefaults(t.Context(), turn, fake, v.proof, t.Context(), reference, changed); !errors.Is(err, ErrConflict) || got != (mgDVRPolicyAcceptance{}) {
		t.Fatal("changed input under one identity", got, err)
	}
	mgDVRPolicyRollback(t, turn)

	// A caller digest that disagrees with the typed request is a conflicting
	// original, never a fresh admission.
	turn = mgDVRPolicyCommandTurn(t, w, MGSetDVRDefaults)
	bad := reference
	bad.RequestDigest[0] ^= 0xff
	if got, err := acceptMGSetDVRDefaults(t.Context(), turn, fake, v.proof, t.Context(), bad, request); !errors.Is(err, ErrConflict) || got != (mgDVRPolicyAcceptance{}) {
		t.Fatal("caller digest not recomputed", got, err)
	}
	mgDVRPolicyRollback(t, turn)

	// A tampered retained payload never decodes as the original result.
	var payload []byte
	if err := w.db.QueryRowContext(t.Context(), `SELECT result_payload FROM user_mutation_command_receipts WHERE root_id=? AND command_sequence=1`, v.root).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `UPDATE user_mutation_command_receipts SET result_payload=substr(result_payload,1,length(result_payload)-1) WHERE root_id=? AND command_sequence=1`, v.root); err != nil {
		t.Fatal(err)
	}
	turn = mgDVRPolicyCommandTurn(t, w, MGSetDVRDefaults)
	if got, err := acceptMGSetDVRDefaults(t.Context(), turn, fake, v.proof, t.Context(), reference, request); !errors.Is(err, ErrResolveRequired) || got != (mgDVRPolicyAcceptance{}) {
		t.Fatal("tampered payload replayed", got, err)
	}
	mgDVRPolicyRollback(t, turn)
	if _, err := w.db.ExecContext(t.Context(), `UPDATE user_mutation_command_receipts SET result_payload=? WHERE root_id=? AND command_sequence=1`, payload, v.root); err != nil {
		t.Fatal(err)
	}

	// A retained header that does not claim exactly one saved scalar, or that was
	// stored for another request, is never decoded as the original result.
	if _, err := w.db.ExecContext(t.Context(), `UPDATE user_mutation_command_receipts SET result_count=0 WHERE root_id=? AND command_sequence=1`, v.root); err != nil {
		t.Fatal(err)
	}
	turn = mgDVRPolicyCommandTurn(t, w, MGSetDVRDefaults)
	if got, err := acceptMGSetDVRDefaults(t.Context(), turn, fake, v.proof, t.Context(), reference, request); !errors.Is(err, ErrResolveRequired) || got != (mgDVRPolicyAcceptance{}) {
		t.Fatal("wrong result count replayed", got, err)
	}
	mgDVRPolicyRollback(t, turn)
	if _, err := w.db.ExecContext(t.Context(), `UPDATE user_mutation_command_receipts SET result_count=1,request_digest=zeroblob(32) WHERE root_id=? AND command_sequence=1`, v.root); err != nil {
		t.Fatal(err)
	}
	turn = mgDVRPolicyCommandTurn(t, w, MGSetDVRDefaults)
	if got, err := acceptMGSetDVRDefaults(t.Context(), turn, fake, v.proof, t.Context(), reference, request); !errors.Is(err, ErrConflict) || got != (mgDVRPolicyAcceptance{}) {
		t.Fatal("foreign request digest replayed", got, err)
	}
	mgDVRPolicyRollback(t, turn)
	if _, err := w.db.ExecContext(t.Context(), `UPDATE user_mutation_command_receipts SET request_digest=? WHERE root_id=? AND command_sequence=1`, reference.RequestDigest[:], v.root); err != nil {
		t.Fatal(err)
	}

	// ACK retires the original: a complete zero result, never a reconstructed
	// policy and never a fresh effect.
	ackMGDVRPolicy(t, w, v.root, 1)
	turn = mgDVRPolicyCommandTurn(t, w, MGSetDVRDefaults)
	retired, err := acceptMGSetDVRDefaults(t.Context(), turn, fake, v.proof, t.Context(), reference, request)
	if err != nil || retired.decision != receiptRetired || retired.policy != (mgDVRPolicyResult{}) || retired.digest != (ResultDigest{}) {
		t.Fatal(retired, err)
	}
	mgDVRPolicyRollback(t, turn)

	// Identical desired values are still an explicit next revision; the command
	// is not a silent no-op.
	nextRequest, nextReference := mgDVRPolicyDefaultsRequestAt(t, v, 8, mgPersonalIdentity(2, 2))
	turn = mgDVRPolicyCommandTurn(t, w, MGSetDVRDefaults)
	noop, err := acceptMGSetDVRDefaults(t.Context(), turn, fake, v.proof, t.Context(), nextReference, nextRequest)
	noopWant := want
	noopWant.revision = 9
	if err != nil || noop.decision != receiptFresh || noop.policy != noopWant {
		t.Fatal(noop, err)
	}
	mgDVRPolicyCommandCommit(t, turn)

	// A stale expected revision is a conflict and writes nothing.
	ackMGDVRPolicy(t, w, v.root, 2)
	staleRequest, staleReference := mgDVRPolicyDefaultsRequestAt(t, v, 7, mgPersonalIdentity(3, 3))
	turn = mgDVRPolicyCommandTurn(t, w, MGSetDVRDefaults)
	if got, err := acceptMGSetDVRDefaults(t.Context(), turn, fake, v.proof, t.Context(), staleReference, staleRequest); !errors.Is(err, ErrConflict) || got != (mgDVRPolicyAcceptance{}) {
		t.Fatal("stale revision", got, err)
	}
	mgDVRPolicyRollback(t, turn)

	if err := w.db.QueryRowContext(t.Context(), `SELECT (SELECT revision FROM dvr_defaults WHERE singleton=1),(SELECT count(*) FROM user_mutation_command_receipts WHERE root_id=?),(SELECT last_command_sequence FROM user_mutation_command_owners WHERE root_id=?)`, v.root, v.root).Scan(&revision, &receipts, &sequence); err != nil {
		t.Fatal(err)
	}
	if revision != 9 || receipts != 2 || sequence != 2 {
		t.Fatal(revision, receipts, sequence)
	}
}

// The storage command has the same accepting shape over its own singleton.
func TestMGDVRPolicyStorageAcceptReplayRetiredAndStaleRevision(t *testing.T) {
	w, _, v := mgDVRPolicyCommandFixture(t)
	fake := mgDVRPolicyFake(t, v)
	request, reference := mgDVRPolicyStorageRequestAt(t, v.storage.revision, mgDVRPolicyStorageFixture(), mgPersonalIdentity(1, 1))
	want := mgDVRPolicyStorageWant(v)

	turn := mgDVRPolicyCommandTurn(t, w, MGSetDVRStoragePolicy)
	out, err := acceptMGSetDVRStoragePolicy(t.Context(), turn, fake, v.proof, t.Context(), reference, request)
	if err != nil || out.decision != receiptFresh || out.policy != want || out.digest == (ResultDigest{}) {
		t.Fatal(out, err)
	}
	mgDVRPolicyCommandCommit(t, turn)

	var revision, days, episodes, floor, cap int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT revision,retention_days,episode_limit,floor_bytes,cap_bytes FROM dvr_storage_policy WHERE singleton=1`).Scan(&revision, &days, &episodes, &floor, &cap); err != nil {
		t.Fatal(err)
	}
	if revision != 12 || days != 30 || episodes != 10 || floor != 1024 || cap != 8192 {
		t.Fatal(revision, days, episodes, floor, cap)
	}

	// The live singleton moves; the replay still returns the saved result.
	if _, err := w.db.ExecContext(t.Context(), `UPDATE dvr_storage_policy SET revision=30,retention_days=1,episode_limit=2,floor_bytes=3,cap_bytes=4 WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	turn = mgDVRPolicyCommandTurn(t, w, MGSetDVRStoragePolicy)
	replay, err := acceptMGSetDVRStoragePolicy(t.Context(), turn, fake, v.proof, t.Context(), reference, request)
	if err != nil || replay.decision != receiptReplay || replay.policy != want || replay.digest != out.digest {
		t.Fatal(replay, err)
	}
	mgDVRPolicyRollback(t, turn)
	if _, err := w.db.ExecContext(t.Context(), `UPDATE dvr_storage_policy SET revision=12,retention_days=30,episode_limit=10,floor_bytes=1024,cap_bytes=8192 WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}

	ackMGDVRPolicy(t, w, v.root, 1)
	turn = mgDVRPolicyCommandTurn(t, w, MGSetDVRStoragePolicy)
	retired, err := acceptMGSetDVRStoragePolicy(t.Context(), turn, fake, v.proof, t.Context(), reference, request)
	if err != nil || retired.decision != receiptRetired || retired.policy != (mgDVRPolicyResult{}) || retired.digest != (ResultDigest{}) {
		t.Fatal(retired, err)
	}
	mgDVRPolicyRollback(t, turn)

	// A stale revision is a conflict before any effect.
	staleRequest, staleReference := mgDVRPolicyStorageRequestAt(t, 11, mgDVRStorageDesired{7, 1, 1, 1}, mgPersonalIdentity(2, 2))
	turn = mgDVRPolicyCommandTurn(t, w, MGSetDVRStoragePolicy)
	if got, err := acceptMGSetDVRStoragePolicy(t.Context(), turn, fake, v.proof, t.Context(), staleReference, staleRequest); !errors.Is(err, ErrConflict) || got != (mgDVRPolicyAcceptance{}) {
		t.Fatal("stale storage revision", got, err)
	}
	mgDVRPolicyRollback(t, turn)
}

// Required accepting cells for every command: forged cached stamp, forged
// issuer, cancelled caller lifetime and the production seam's typed refusal,
// each with zero output and no effect.
func TestMGDVRPolicyCommandsAuthorityCells(t *testing.T) {
	cases := []struct {
		name    string
		purpose CommandID
		run     func(fake *mgPersonalFakeRoots, reference ReceiptReference) func(context.Context, *commandTurn, mgPersonalRootSeam, *usOwnerAuthorityProof, context.Context) (mgDVRPolicyAcceptance, error)
	}{
		{
			name: "dvr-defaults", purpose: MGSetDVRDefaults,
			run: func(fake *mgPersonalFakeRoots, reference ReceiptReference) func(context.Context, *commandTurn, mgPersonalRootSeam, *usOwnerAuthorityProof, context.Context) (mgDVRPolicyAcceptance, error) {
				return func(ctx context.Context, turn *commandTurn, root mgPersonalRootSeam, witness *usOwnerAuthorityProof, lifetime context.Context) (mgDVRPolicyAcceptance, error) {
					request := mgDVRDefaultsPolicyRequest{expected: 7, input: mgDVRPolicyDefaultsInputFixture}
					return acceptMGSetDVRDefaults(ctx, turn, root, witness, lifetime, reference, request)
				}
			},
		},
		{
			name: "dvr-storage", purpose: MGSetDVRStoragePolicy,
			run: func(fake *mgPersonalFakeRoots, reference ReceiptReference) func(context.Context, *commandTurn, mgPersonalRootSeam, *usOwnerAuthorityProof, context.Context) (mgDVRPolicyAcceptance, error) {
				return func(ctx context.Context, turn *commandTurn, root mgPersonalRootSeam, witness *usOwnerAuthorityProof, lifetime context.Context) (mgDVRPolicyAcceptance, error) {
					request := mgDVRStoragePolicyRequest{expected: 11, policy: mgDVRPolicyStorageFixture()}
					return acceptMGSetDVRStoragePolicy(ctx, turn, root, witness, lifetime, reference, request)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, _, v := mgDVRPolicyCommandFixture(t)
			fake := mgDVRPolicyFake(t, v)
			var reference ReceiptReference
			if tc.purpose == MGSetDVRDefaults {
				request, ref := mgDVRPolicyDefaultsRequestAt(t, v, v.defaults.revision, mgPersonalIdentity(1, 1))
				_ = request
				reference = ref
			} else {
				request, ref := mgDVRPolicyStorageRequestAt(t, v.storage.revision, mgDVRPolicyStorageFixture(), mgPersonalIdentity(1, 1))
				_ = request
				reference = ref
			}
			run := tc.run(fake, reference)

			forged := *v.proof
			forged.tokenExpiry = forged.tokenExpiry.Add(48 * time.Hour)
			turn := mgDVRPolicyCommandTurn(t, w, tc.purpose)
			if got, err := run(t.Context(), turn, fake, &forged, t.Context()); !errors.Is(err, ErrDisclosureDenied) || got != (mgDVRPolicyAcceptance{}) {
				t.Fatal("forged cached stamp", got, err)
			}
			mgDVRPolicyRollback(t, turn)

			other := v.proof.witness
			other.issuer.account = "another-account"
			forgedIssuer, err := prepareUSOwnerAuthority(other)
			if err != nil {
				t.Fatal(err)
			}
			turn = mgDVRPolicyCommandTurn(t, w, tc.purpose)
			if got, err := run(t.Context(), turn, fake, forgedIssuer, t.Context()); !errors.Is(err, ErrDisclosureDenied) || got != (mgDVRPolicyAcceptance{}) {
				t.Fatal("forged issuer", got, err)
			}
			mgDVRPolicyRollback(t, turn)

			cancelled, cancel := context.WithCancel(t.Context())
			cancel()
			turn = mgDVRPolicyCommandTurn(t, w, tc.purpose)
			if got, err := run(cancelled, turn, fake, v.proof, t.Context()); !errors.Is(err, context.Canceled) || got != (mgDVRPolicyAcceptance{}) {
				t.Fatal("cancelled lifetime", got, err)
			}
			mgDVRPolicyRollback(t, turn)

			cancelledLifetime, cancelLifetime := context.WithCancel(t.Context())
			cancelLifetime()
			turn = mgDVRPolicyCommandTurn(t, w, tc.purpose)
			if got, err := run(t.Context(), turn, fake, v.proof, cancelledLifetime); !errors.Is(err, context.Canceled) || got != (mgDVRPolicyAcceptance{}) {
				t.Fatal("cancelled caller lifetime", got, err)
			}
			mgDVRPolicyRollback(t, turn)

			// The production seam is the only non-test implementation and refuses
			// until the mint lands; it never synthesizes a policy result.
			turn = mgDVRPolicyCommandTurn(t, w, tc.purpose)
			if got, err := run(t.Context(), turn, mgPersonalProductionRoots{}, v.proof, t.Context()); !errors.Is(err, ErrMGRootUnavailable) || got != (mgDVRPolicyAcceptance{}) {
				t.Fatal("production seam minted or applied", got, err)
			}
			mgDVRPolicyRollback(t, turn)
		})
	}
}

// Stale current authority refuses a replay even with the retained rows gone,
// and a FULL frame cannot borrow either NORMAL purpose.
func TestMGDVRPolicyCommandsStaleAuthorityAndPurpose(t *testing.T) {
	for _, tc := range []struct {
		name    string
		purpose CommandID
		run     func(t *testing.T, v mgDVRPolicyCommandValues, fake *mgPersonalFakeRoots, seq int64) func(context.Context, *commandTurn, *usOwnerAuthorityProof, context.Context) (mgDVRPolicyAcceptance, error)
	}{
		{
			name: "dvr-defaults", purpose: MGSetDVRDefaults,
			run: func(t *testing.T, v mgDVRPolicyCommandValues, fake *mgPersonalFakeRoots, seq int64) func(context.Context, *commandTurn, *usOwnerAuthorityProof, context.Context) (mgDVRPolicyAcceptance, error) {
				request, reference := mgDVRPolicyDefaultsRequestAt(t, v, v.defaults.revision, mgPersonalIdentity(seq, byte(seq)))
				return mgDVRPolicyDefaultsRun(fake, reference, request)
			},
		},
		{
			name: "dvr-storage", purpose: MGSetDVRStoragePolicy,
			run: func(t *testing.T, v mgDVRPolicyCommandValues, fake *mgPersonalFakeRoots, seq int64) func(context.Context, *commandTurn, *usOwnerAuthorityProof, context.Context) (mgDVRPolicyAcceptance, error) {
				request, reference := mgDVRPolicyStorageRequestAt(t, v.storage.revision, mgDVRPolicyStorageFixture(), mgPersonalIdentity(seq, byte(seq)))
				return mgDVRPolicyStorageRun(fake, reference, request)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, _, v := mgDVRPolicyCommandFixture(t)
			fake := mgDVRPolicyFake(t, v)
			run := tc.run(t, v, fake, 1)
			turn := mgDVRPolicyCommandTurn(t, w, tc.purpose)
			if _, err := run(t.Context(), turn, v.proof, t.Context()); err != nil {
				t.Fatal(err)
			}
			mgDVRPolicyCommandCommit(t, turn)

			if _, err := w.db.ExecContext(t.Context(), `UPDATE authorization_session_families SET revoked=1,revoked_reason='explicit_signout',revoked_at=? WHERE id=?`, w.now().UTC().Format("2006-01-02T15:04:05Z"), v.proof.witness.issuer.family); err != nil {
				t.Fatal(err)
			}
			turn = mgDVRPolicyCommandTurn(t, w, tc.purpose)
			if got, err := run(t.Context(), turn, v.proof, t.Context()); !errors.Is(err, ErrDisclosureDenied) || got != (mgDVRPolicyAcceptance{}) {
				t.Fatal("revoked session replayed the saved result", got, err)
			}
			mgDVRPolicyRollback(t, turn)

			// Current authority precedes retained custody: with the receipt rows
			// physically gone, the revoked session is still DENIED.
			if _, err := w.db.ExecContext(t.Context(), `DELETE FROM user_mutation_command_receipts WHERE root_id=?`, v.root); err != nil {
				t.Fatal(err)
			}
			turn = mgDVRPolicyCommandTurn(t, w, tc.purpose)
			if got, err := run(t.Context(), turn, v.proof, t.Context()); !errors.Is(err, ErrDisclosureDenied) || errors.Is(err, ErrResolveRequired) || got != (mgDVRPolicyAcceptance{}) {
				t.Fatal("revoked session reached retained custody first", got, err)
			}
			mgDVRPolicyRollback(t, turn)
		})
	}

	// A FULL frame cannot borrow a NORMAL policy purpose, separately from the
	// missing mint.
	w, _, v := mgDVRPolicyCommandFixture(t)
	full, err := w.begin(t.Context(), SecurityAccept, testRecipe(), &testCustody{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = full.rollback(nil) })
	full.descriptor.ID = MGSetDVRDefaults
	defs := mgPersonalRootDefinitions()
	defs = append(defs, mgDVRPolicyDefinitions()...)
	registry, err := stmttest.New(t, defs)
	if err != nil {
		t.Fatal(err)
	}
	var steps []statementStep
	for _, d := range defs {
		steps = append(steps, statementStep{d.ID, Work{8, 1000, 8, 4096, time.Microsecond}})
	}
	fullTurn, err := newCommandTurn(full, registry, steps)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fullTurn.closeRows() })
	request, reference := mgDVRPolicyDefaultsRequestAt(t, v, v.defaults.revision, mgPersonalIdentity(1, 1))
	if got, err := acceptMGSetDVRDefaults(t.Context(), fullTurn, mgDVRPolicyFake(t, v), v.proof, t.Context(), reference, request); !errors.Is(err, ErrInvalid) || got != (mgDVRPolicyAcceptance{}) {
		t.Fatal("FULL frame borrowed a NORMAL policy purpose", got, err)
	}
}

// A lost COMMIT reply is never success. The durable truth is read through a NEW
// read-only connection, never the quarantined writer pool, and the frame is
// all-or-nothing in both the before-commit and after-commit fault positions.
func TestMGDVRPolicyCommandsUnknownCommitFreshReadback(t *testing.T) {
	for _, tc := range []struct {
		name    string
		purpose CommandID
		run     func(t *testing.T, v mgDVRPolicyCommandValues, fake *mgPersonalFakeRoots) func(context.Context, *commandTurn, *usOwnerAuthorityProof) (mgDVRPolicyAcceptance, error)
		read    func(t *testing.T, db *sql.DB, root string) (revision, receipts, sequence int64)
	}{
		{
			name: "dvr-defaults", purpose: MGSetDVRDefaults,
			run: func(t *testing.T, v mgDVRPolicyCommandValues, fake *mgPersonalFakeRoots) func(context.Context, *commandTurn, *usOwnerAuthorityProof) (mgDVRPolicyAcceptance, error) {
				request, reference := mgDVRPolicyDefaultsRequestAt(t, v, v.defaults.revision, mgPersonalIdentity(1, 1))
				return func(ctx context.Context, turn *commandTurn, witness *usOwnerAuthorityProof) (mgDVRPolicyAcceptance, error) {
					return acceptMGSetDVRDefaults(ctx, turn, fake, witness, t.Context(), reference, request)
				}
			},
			read: func(t *testing.T, db *sql.DB, root string) (int64, int64, int64) {
				var revision, receipts, sequence int64
				if err := db.QueryRowContext(context.Background(), `SELECT (SELECT revision FROM dvr_defaults WHERE singleton=1),(SELECT count(*) FROM user_mutation_command_receipts WHERE root_id=?),(SELECT last_command_sequence FROM user_mutation_command_owners WHERE root_id=?)`, root, root).Scan(&revision, &receipts, &sequence); err != nil {
					t.Fatal(err)
				}
				return revision, receipts, sequence
			},
		},
		{
			name: "dvr-storage", purpose: MGSetDVRStoragePolicy,
			run: func(t *testing.T, v mgDVRPolicyCommandValues, fake *mgPersonalFakeRoots) func(context.Context, *commandTurn, *usOwnerAuthorityProof) (mgDVRPolicyAcceptance, error) {
				request, reference := mgDVRPolicyStorageRequestAt(t, v.storage.revision, mgDVRPolicyStorageFixture(), mgPersonalIdentity(1, 1))
				return func(ctx context.Context, turn *commandTurn, witness *usOwnerAuthorityProof) (mgDVRPolicyAcceptance, error) {
					return acceptMGSetDVRStoragePolicy(ctx, turn, fake, witness, t.Context(), reference, request)
				}
			},
			read: func(t *testing.T, db *sql.DB, root string) (int64, int64, int64) {
				var revision, receipts, sequence int64
				if err := db.QueryRowContext(context.Background(), `SELECT (SELECT revision FROM dvr_storage_policy WHERE singleton=1),(SELECT count(*) FROM user_mutation_command_receipts WHERE root_id=?),(SELECT last_command_sequence FROM user_mutation_command_owners WHERE root_id=?)`, root, root).Scan(&revision, &receipts, &sequence); err != nil {
					t.Fatal(err)
				}
				return revision, receipts, sequence
			},
		},
	} {
		for _, after := range []bool{false, true} {
			t.Run(tc.name+"/"+map[bool]string{false: "before_commit", true: "after_commit"}[after], func(t *testing.T) {
				w, path, v := mgDVRPolicyCommandFixture(t)
				fake := mgDVRPolicyFake(t, v)
				run := tc.run(t, v, fake)
				beforeRevision, _, _ := tc.read(t, w.db, v.root)
				if err := w.db.Close(); err != nil {
					t.Fatal(err)
				}
				connector, err := sqliteconn.Connector(path, w.config, sqliteconn.ExistingFile)
				if err != nil {
					t.Fatal(err)
				}
				installFaultPool(w, &faultConnector{Connector: connector, replyError: true, afterCommit: after})

				turn := mgDVRPolicyCommandTurn(t, w, tc.purpose)
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

				revision, receipts, sequence := tc.read(t, mgPersonalReadOnly(t, path), v.root)
				if after {
					if revision != beforeRevision+1 || receipts != 1 || sequence != 1 {
						t.Fatal("committed frame is not whole", revision, receipts, sequence)
					}
				} else if revision != beforeRevision || receipts != 0 || sequence != 0 {
					t.Fatal("uncommitted frame leaked", revision, receipts, sequence)
				}
			})
		}
	}
}

// The actual RETURNING/client readback value equality is load-bearing: a
// corrupted post-update SELECT must refuse the whole command and leave no
// receipt or owner advance behind.
func TestMGDVRPolicyCommandsValueReadbackMutantRequiresRollback(t *testing.T) {
	for _, tc := range []struct {
		name  string
		table string
	}{
		{name: "dvr-defaults", table: "dvr_defaults"},
		{name: "dvr-storage", table: "dvr_storage_policy"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			observed := false
			w, _, v := mgDVRPolicyCommandFixture(t, func(c driver.Conn) driver.Conn {
				return &mgDVRPolicyMismatchConnection{writerDriverConn: c.(writerDriverConn), table: tc.table, observed: &observed}
			})
			fake := mgDVRPolicyFake(t, v)
			var turn *commandTurn
			if tc.table == "dvr_defaults" {
				request, reference := mgDVRPolicyDefaultsRequestAt(t, v, v.defaults.revision, mgPersonalIdentity(1, 1))
				turn = mgDVRPolicyCommandTurn(t, w, MGSetDVRDefaults)
				if got, err := acceptMGSetDVRDefaults(t.Context(), turn, fake, v.proof, t.Context(), reference, request); !errors.Is(err, ErrConflict) || got != (mgDVRPolicyAcceptance{}) {
					t.Fatal("value-mismatched defaults released", got, err)
				}
			} else {
				request, reference := mgDVRPolicyStorageRequestAt(t, v.storage.revision, mgDVRPolicyStorageFixture(), mgPersonalIdentity(1, 1))
				turn = mgDVRPolicyCommandTurn(t, w, MGSetDVRStoragePolicy)
				if got, err := acceptMGSetDVRStoragePolicy(t.Context(), turn, fake, v.proof, t.Context(), reference, request); !errors.Is(err, ErrConflict) || got != (mgDVRPolicyAcceptance{}) {
					t.Fatal("value-mismatched storage released", got, err)
				}
			}
			if !observed {
				t.Fatal("control missed the independent value readback")
			}
			mgDVRPolicyRollback(t, turn)
			var receipts, sequence int64
			if err := w.db.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM user_mutation_command_receipts WHERE root_id=?),(SELECT last_command_sequence FROM user_mutation_command_owners WHERE root_id=?)`, v.root, v.root).Scan(&receipts, &sequence); err != nil {
				t.Fatal(err)
			}
			if receipts != 0 || sequence != 0 {
				t.Fatal("value-mismatched frame persisted custody", receipts, sequence)
			}
			if tc.table == "dvr_defaults" {
				var revision int64
				if err := w.db.QueryRowContext(t.Context(), `SELECT revision FROM dvr_defaults WHERE singleton IN(1)`).Scan(&revision); err != nil || revision != v.defaults.revision {
					t.Fatal("defaults survived rollback", revision, err)
				}
			} else {
				var revision int64
				if err := w.db.QueryRowContext(t.Context(), `SELECT revision FROM dvr_storage_policy WHERE singleton IN(1)`).Scan(&revision); err != nil || revision != v.storage.revision {
					t.Fatal("storage survived rollback", revision, err)
				}
			}
		})
	}
}

func TestMGDVRPolicyCommandResultCodecRoundTripAndBounds(t *testing.T) {
	id := mgPersonalIdentity(3, 9)
	var request RequestDigest
	request[0] = 7
	defaults := mgDVRPolicyResult{
		kind: mgDVRPolicyDefaultsKind, revision: 4, beforeSeconds: 90, afterSeconds: 300,
		retentionDays: 30, episodeLimit: 0, conflict: "keep-watching",
		showTemplate: "{series}/{title}", movieTemplate: "{title} ({year})",
	}
	payload, digest, err := mgDVRPolicyResultPayload(id, request, defaults)
	if err != nil || len(payload) == 0 {
		t.Fatal(payload, digest, err)
	}
	if got, err := decodeMGDVRPolicyResult(id, request, mgDVRPolicyDefaultsKind, payload, digest); err != nil || got != defaults {
		t.Fatal(got, err)
	}
	// The variant tag is part of the codec: a storage label never decodes the
	// defaults payload, and a cross-kind result is refused before any receipt.
	if _, err := decodeMGDVRPolicyResult(id, request, mgDVRPolicyStorageKind, payload, digest); !errors.Is(err, ErrResolveRequired) {
		t.Fatal("cross-kind payload decoded", err)
	}
	if _, err := decodeMGDVRPolicyResult(id, request, "caller-kind", payload, digest); !errors.Is(err, ErrResolveRequired) {
		t.Fatal("caller kind decoded", err)
	}
	bad := append([]byte(nil), payload...)
	bad[len(bad)-1] ^= 1
	if _, err := decodeMGDVRPolicyResult(id, request, mgDVRPolicyDefaultsKind, bad, digest); !errors.Is(err, ErrResolveRequired) {
		t.Fatal("tampered payload decoded", err)
	}
	wrongDigest := digest
	wrongDigest[0] ^= 1
	if _, err := decodeMGDVRPolicyResult(id, request, mgDVRPolicyDefaultsKind, payload, wrongDigest); !errors.Is(err, ErrResolveRequired) {
		t.Fatal("wrong digest decoded", err)
	}
	other := id
	other.Sequence++
	if _, err := decodeMGDVRPolicyResult(other, request, mgDVRPolicyDefaultsKind, payload, digest); !errors.Is(err, ErrResolveRequired) {
		t.Fatal("foreign identity decoded", err)
	}
	// Complete variant bounds: storage fields are zero in a defaults result and
	// defaults fields are zero in a storage result.
	crossed := defaults
	crossed.capBytes = 1
	if err := preflightMGDVRPolicyResult(crossed); !errors.Is(err, ErrInvalid) {
		t.Fatal("defaults result carried storage bytes", err)
	}
	crossed = mgDVRPolicyResult{kind: mgDVRPolicyStorageKind, revision: 1, conflict: "keep-watching"}
	if err := preflightMGDVRPolicyResult(crossed); !errors.Is(err, ErrInvalid) {
		t.Fatal("storage result carried defaults fields", err)
	}
	if err := preflightMGDVRPolicyResult(mgDVRPolicyResult{kind: "caller-kind", revision: 1}); !errors.Is(err, ErrInvalid) {
		t.Fatal("unknown result kind accepted", err)
	}
	if err := preflightMGDVRPolicyResult(mgDVRPolicyResult{kind: mgDVRPolicyDefaultsKind, revision: 0}); !errors.Is(err, ErrInvalid) {
		t.Fatal("zero revision accepted", err)
	}
}

// Request binding, public input bounds and the closed purpose are separate
// guards from authority and custody.
func TestMGDVRPolicyCommandDigestInputAndPurposeBinding(t *testing.T) {
	w, _, v := mgDVRPolicyCommandFixture(t)
	id := mgPersonalIdentity(1, 1)
	if _, err := (mgDVRDefaultsPolicyRequest{expected: 0, input: mgDVRPolicyDefaultsInputFixture}).digest(id); !errors.Is(err, ErrInvalid) {
		t.Fatal("absence-as-create admitted", err)
	}
	if _, err := (mgDVRDefaultsPolicyRequest{expected: 7, input: mgDVRDefaultsInput{keepMode: "unknown"}}).digest(id); !errors.Is(err, ErrInvalid) {
		t.Fatal("unsupported keep mode admitted", err)
	}
	if _, err := (mgDVRStoragePolicyRequest{expected: 11, policy: mgDVRStorageDesired{episodeLimit: 100001}}).digest(id); !errors.Is(err, ErrInvalid) {
		t.Fatal("out-of-bounds storage policy admitted", err)
	}
	if _, err := (mgDVRStoragePolicyRequest{expected: 11, policy: mgDVRPolicyStorageFixture()}).digest(CommandIdentity{}); !errors.Is(err, ErrInvalid) {
		t.Fatal("zero identity admitted", err)
	}
	// A NORMAL frame carrying the other policy purpose is a closed-purpose
	// refusal, not a shared scalar permission.
	turn := mgDVRPolicyCommandTurn(t, w, MGSetDVRStoragePolicy)
	request, reference := mgDVRPolicyDefaultsRequestAt(t, v, v.defaults.revision, id)
	if got, err := acceptMGSetDVRDefaults(t.Context(), turn, mgDVRPolicyFake(t, v), v.proof, t.Context(), reference, request); !errors.Is(err, ErrInvalid) || got != (mgDVRPolicyAcceptance{}) {
		t.Fatal("storage frame borrowed the defaults purpose", got, err)
	}
	mgDVRPolicyRollback(t, turn)
}

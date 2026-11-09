package writer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

func usPasswordTestIdentity() (CommandIdentity, RequestDigest) {
	return CommandIdentity{Incarnation: [16]byte{1}, OperationID: [16]byte{2}, Sequence: 1}, RequestDigest{3}
}

func usPasswordSavedFixture(c *usPasswordCommand) usPasswordSavedResult {
	target := c.request.target
	return usPasswordSavedResult{kind: usLocalPasswordReplace, ownerID: c.owner, account: target.account,
		memberIncarnation: target.memberIncarnation, profile: target.profile, profileLifetime: target.profileLifetime,
		memberRevision: target.memberRevision, profileRevision: target.profileRevision,
		previousEpoch: target.epoch, resultEpoch: target.epoch + 1, appliedMS: 1000}
}

// The closed result codec: every field participates exactly once and both hash
// images stay out of the digest domain.
func TestUSPasswordResultExactDigest(t *testing.T) {
	v := usPasswordSavedResult{kind: usLocalPasswordReplace, ownerID: usPasswordOwnerID, account: usPasswordAccountID,
		memberIncarnation: strings.Repeat("a", 32), profile: usPasswordProfileID, profileLifetime: strings.Repeat("b", 32),
		memberRevision: usMaxRevision, profileRevision: usMaxRevision, previousEpoch: usMaxRevision - 1,
		resultEpoch: usMaxRevision, appliedMS: 1}
	id, r := usPasswordTestIdentity()
	d, err := usPasswordResultDigest(id, r, v)
	if err != nil {
		t.Fatal(err)
	}
	for i, mutate := range []func(*usPasswordSavedResult){
		func(v *usPasswordSavedResult) { v.kind = usAPICredentialIssue },
		func(v *usPasswordSavedResult) { v.ownerID += "x" },
		func(v *usPasswordSavedResult) { v.account += "x" },
		func(v *usPasswordSavedResult) { v.memberIncarnation = strings.Repeat("c", 32) },
		func(v *usPasswordSavedResult) { v.profile += "x" },
		func(v *usPasswordSavedResult) { v.profileLifetime = strings.Repeat("c", 32) },
		func(v *usPasswordSavedResult) { v.memberRevision-- },
		func(v *usPasswordSavedResult) { v.profileRevision-- },
		func(v *usPasswordSavedResult) { v.previousEpoch--; v.resultEpoch-- },
		func(v *usPasswordSavedResult) { v.appliedMS++ },
	} {
		x := v
		mutate(&x)
		got, e := usPasswordResultDigest(id, r, x)
		if e == nil && got == d {
			t.Fatal(i, "mutation kept the digest")
		}
	}
	for _, invalid := range []usPasswordSavedResult{
		{kind: usLocalPasswordReplace, ownerID: "..", account: usPasswordAccountID, memberIncarnation: strings.Repeat("a", 32), profile: usPasswordProfileID, profileLifetime: strings.Repeat("b", 32), memberRevision: 1, profileRevision: 1, previousEpoch: 1, resultEpoch: 2},
		{kind: usLocalPasswordReplace, ownerID: usPasswordOwnerID, account: usPasswordAccountID, memberIncarnation: strings.Repeat("a", 32), profile: usPasswordProfileID, profileLifetime: strings.Repeat("b", 32), memberRevision: 0, profileRevision: 1, previousEpoch: 1, resultEpoch: 2},
		{kind: usLocalPasswordReplace, ownerID: usPasswordOwnerID, account: usPasswordAccountID, memberIncarnation: strings.Repeat("a", 32), profile: usPasswordProfileID, profileLifetime: strings.Repeat("b", 32), memberRevision: 1, profileRevision: 1, previousEpoch: 1, resultEpoch: 3},
		{kind: usLocalPasswordReplace, ownerID: usPasswordOwnerID, account: usPasswordAccountID, memberIncarnation: strings.Repeat("a", 32), profile: usPasswordProfileID, profileLifetime: strings.Repeat("b", 32), memberRevision: 1, profileRevision: 1, previousEpoch: 0, resultEpoch: 1},
	} {
		if err := validateUSPasswordResult(invalid); !errors.Is(err, ErrResolveRequired) {
			t.Fatal("invalid result accepted", invalid.previousEpoch, invalid.resultEpoch, err)
		}
	}
	var zero usPasswordSavedResult
	if cloneUSPasswordResult(v) != v || cloneUSPasswordResult(zero) != zero {
		t.Fatal("clone")
	}
	for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%d", "%100.2s"} {
		if out := fmt.Sprintf(format, v); strings.Contains(out, usPasswordAccountID) || strings.Contains(out, usPasswordProfileID) {
			t.Fatal("result redaction", format, out)
		}
	}
	if n, err := usPasswordResultVariableBytes(v); err != nil || n <= 0 {
		t.Fatal(n, err)
	}
}

// Original, replay of the same identity in the CURRENT (post-epoch) session,
// then the floor-only ACK.
func TestUSPasswordOriginalReplayAndACK(t *testing.T) {
	w, proof, command := usPasswordFixture(t)
	r := usPasswordRegistry(t, w)
	turn, a, _ := usPasswordTurn(t, w, r, proof, 0)
	original, _, err := runUSPasswordDispatch(t.Context(), turn, a, usPasswordInput(t, command))
	if err != nil {
		t.Fatal(err)
	}
	if original.Outcome.State != Applied || original.Outcome.Replayed || original.Value.resultEpoch != 2 {
		t.Fatal("original", original)
	}
	// A void public response: the closed result carries no image field at all.
	if original.Digest == (ResultDigest{}) || original.ChargedBytes <= 0 {
		t.Fatal("original accounting", original)
	}
	// Independent oracle: the SQL-generated receipt debt equals the charge.
	headerBytes := usPasswordCount(t, w, `SELECT variable_bytes FROM security_command_receipts WHERE owner_id=?`, usPasswordOwnerID)
	childBytes := usPasswordCount(t, w, `SELECT variable_bytes FROM security_password_results WHERE owner_id=?`, usPasswordOwnerID)
	if headerBytes+childBytes != original.ChargedBytes {
		t.Fatal("charge oracle", headerBytes, childBytes, original.ChargedBytes)
	}

	// The replacement advanced accounts.epoch, which is the US-01 stamp's own
	// witness: the CURRENT session (epoch 2) is the honest replay caller.
	current := usPasswordCurrentProof(t, w, proof)
	turn, a, _ = usPasswordTurn(t, w, r, current, 0)
	replay, floor, err := runUSPasswordDispatch(t.Context(), turn, a, &usPasswordInvocation{request: usPasswordInput(t, command).request})
	if err != nil {
		t.Fatal(err)
	}
	if replay.Outcome.State != Applied || !replay.Outcome.Replayed || !reflect.DeepEqual(replay.Value, original.Value) || replay.Digest != original.Digest {
		t.Fatal("replay was not the saved original", replay)
	}
	if floor.AcknowledgedThrough != 0 || replay.ChargedBytes != 0 || replay.OwnerCount != 1 {
		t.Fatal("replay accounting", replay, floor)
	}
	if n := usPasswordCount(t, w, `SELECT count(*) FROM security_command_receipts`); n != 1 {
		t.Fatal("replay built a receipt", n)
	}
	if epoch, _, _ := usPasswordAccount(t, w, usPasswordAccountID); epoch != 2 {
		t.Fatal("replay moved the source", epoch)
	}

	// ACK advances only the floor: no refund, no deletion, no source effect.
	retained := usPasswordCount(t, w, `SELECT retained_variable_bytes FROM security_command_owners WHERE id=?`, usPasswordOwnerID)
	turn, a, _ = usPasswordTurn(t, w, r, current, 0)
	ack, floor, err := runUSPasswordDispatch(t.Context(), turn, a, &usPasswordInvocation{request: usPasswordInput(t, command).request, ack: true})
	if err != nil {
		t.Fatal(err)
	}
	if floor.AcknowledgedThrough != 1 || !reflect.DeepEqual(ack, usPasswordDispatchResult{}) {
		t.Fatal("ack", ack, floor)
	}
	if usPasswordCount(t, w, `SELECT acknowledged_sequence FROM security_command_owners WHERE id=?`, usPasswordOwnerID) != 1 ||
		usPasswordCount(t, w, `SELECT retained_variable_bytes FROM security_command_owners WHERE id=?`, usPasswordOwnerID) != retained ||
		usPasswordCount(t, w, `SELECT count(*) FROM security_command_receipts`) != 1 {
		t.Fatal("ack refunded or deleted retained history")
	}
}

// Replay reads ONLY the retained receipt: later source drift and a different
// current password image are irrelevant to the ORIGINAL result.
func TestUSPasswordReplayAfterSourceDrift(t *testing.T) {
	w, proof, command := usPasswordFixture(t)
	r := usPasswordRegistry(t, w)
	turn, a, _ := usPasswordTurn(t, w, r, proof, 0)
	original, _, err := runUSPasswordDispatch(t.Context(), turn, a, usPasswordInput(t, command))
	if err != nil {
		t.Fatal(err)
	}
	wc008NativeExec(t, w, `UPDATE accounts SET password_hash=? WHERE id=?`, []byte("$2b$10$driftedimage..........................................."), usPasswordAccountID)
	wc008NativeExec(t, w, `UPDATE direct_profiles SET name='Renamed',revision=revision+1 WHERE id=?`, usPasswordProfileID)
	wc008NativeExec(t, w, `UPDATE direct_memberships SET revision=revision+1 WHERE account_id=?`, usPasswordAccountID)
	current := usPasswordCurrentProof(t, w, proof)
	turn, a, _ = usPasswordTurn(t, w, r, current, 0)
	replay, _, err := runUSPasswordDispatch(t.Context(), turn, a, &usPasswordInvocation{request: usPasswordInput(t, command).request})
	if err != nil {
		t.Fatal(err)
	}
	if replay.Outcome.State != Applied || !replay.Outcome.Replayed || !reflect.DeepEqual(replay.Value, original.Value) {
		t.Fatal("source drift changed the saved original", replay)
	}
	if epoch, hash, _ := usPasswordAccount(t, w, usPasswordAccountID); epoch != 2 || string(hash) != "$2b$10$driftedimage..........................................." {
		t.Fatal("replay rewrote source", epoch)
	}
}

// usPasswordFlipClock expires the stamped authorization at a pinned clock
// read, mirroring US-07's late-denial test. It is installed only AFTER the turn
// is minted, so the entry authorization and the turn's start still see the
// original time.
type usPasswordFlipClock struct {
	base   time.Time
	flipAt int
	calls  int
}

func (c *usPasswordFlipClock) now() time.Time {
	c.calls++
	if c.flipAt > 0 && c.calls >= c.flipAt {
		return c.base.Add(2 * time.Hour)
	}
	return c.base
}

func usPasswordFlipClockOn(w *executor, flipAt int) *usPasswordFlipClock {
	c := &usPasswordFlipClock{base: w.now(), flipAt: flipAt}
	w.now = c.now
	return c
}

// A denial that arrives on the deferred (pre-CAS) recheck REPLACES the body
// outcome. The entry recheck must PASS first, so the virtual clock expires the
// stamp on the LAST clock read of a successful body; the pinned read count
// fails the test if either deferred authority check is removed.
func TestUSPasswordDeferredDenialReplacesOutcome(t *testing.T) {
	t.Run("custody", func(t *testing.T) {
		w, proof, command := usPasswordFixture(t)
		r := usPasswordRegistry(t, w)
		turn, a, _ := usPasswordTurn(t, w, r, proof, 0)
		q, err := usPasswordReceiptForCommand(command)
		if err != nil {
			t.Fatal(err)
		}
		clock := usPasswordFlipClockOn(w, 0)
		custody, err := lookupUSPasswordCustody(t.Context(), turn, a, q)
		if err != nil || custody.decision != receiptFresh || !custody.owner.exists {
			t.Fatal("control did not reach the body outcome", custody, err)
		}
		if clock.calls == 0 {
			t.Fatal("no authority clock reads")
		}
		calls := clock.calls
		// The control only read source; release the frame before the replay of
		// the same identical setup on the same database.
		_, _ = turn.transaction.rollback(nil)
		turn, a, _ = usPasswordTurn(t, w, r, proof, 0)
		usPasswordFlipClockOn(w, calls)
		custody, err = lookupUSPasswordCustody(t.Context(), turn, a, q)
		// The denial class must be the ONLY class: never joined to the body's
		// benign conflict classes, and the body outcome must be zeroed.
		if !errors.Is(err, ErrDisclosureDenied) || errors.Is(err, ErrResolveRequired) || errors.Is(err, ErrConflict) || !reflect.DeepEqual(custody, usPasswordCustody{}) {
			t.Fatal("deferred custody denial joined or leaked", custody, err)
		}
	})
	t.Run("store", func(t *testing.T) {
		w, proof, command := usPasswordFixture(t)
		r := usPasswordRegistry(t, w)
		turn, a, _ := usPasswordTurn(t, w, r, proof, 0)
		q, err := usPasswordReceiptForCommand(command)
		if err != nil {
			t.Fatal(err)
		}
		v := usPasswordSavedFixture(command)
		digest, err := usPasswordResultDigest(command.identity, command.digest, v)
		if err != nil {
			t.Fatal(err)
		}
		charge, err := usPasswordResultVariableBytes(v)
		if err != nil {
			t.Fatal(err)
		}
		clock := usPasswordFlipClockOn(w, 0)
		charged, err := storeUSPasswordResult(t.Context(), turn, a, q, v, digest)
		if err != nil || charged != charge || charged <= 0 {
			t.Fatal("control did not reach the body outcome", charged, err)
		}
		calls := clock.calls
		_, _ = turn.transaction.rollback(nil)

		turn, a, _ = usPasswordTurn(t, w, r, proof, 0)
		usPasswordFlipClockOn(w, calls)
		charged, err = storeUSPasswordResult(t.Context(), turn, a, q, v, digest)
		if !errors.Is(err, ErrDisclosureDenied) || errors.Is(err, ErrResolveRequired) || errors.Is(err, ErrConflict) || charged != 0 {
			t.Fatal("deferred store denial joined or kept the body charge", charged, err)
		}
		// The zeroed output is not a missing body: the deferred denial replaced a
		// REAL in-frame charge. Read that prefix through the SAME frame, because
		// the expired virtual clock quarantines the executor pool at terminal.
		if n := usPasswordFrameCount(t, turn, `SELECT count(*) FROM security_command_receipts`); n != 1 {
			t.Fatal("body prefix missing receipts", n)
		}
		if n := usPasswordFrameCount(t, turn, `SELECT count(*) FROM security_password_results`); n != 1 {
			t.Fatal("body prefix missing the typed child", n)
		}
		if n := usPasswordFrameCount(t, turn, `SELECT retained_count FROM security_command_owners WHERE id=?`, usPasswordOwnerID); n != 1 {
			t.Fatal("body prefix missing owner debt", n)
		}
		var epoch int64
		var hash []byte
		var mustChange bool
		if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT epoch,password_hash,must_change FROM accounts WHERE id=?`, usPasswordAccountID).Scan(&epoch, &hash, &mustChange); err != nil {
			t.Fatal(err)
		}
		if epoch != 1 || string(hash) != usPasswordPreviousHash || mustChange {
			t.Fatal("denied store moved the source", epoch, mustChange)
		}
		_, _ = turn.transaction.rollback(err)
	})
}

// usPasswordFrameCount reads an oracle count through the OPEN frame's own
// connection. It is used only where the executor pool is not a legal reader
// (an expired virtual clock quarantines the pool at terminal).
func usPasswordFrameCount(t *testing.T, turn *commandTurn, q string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := turn.transaction.conn.QueryRowContext(t.Context(), q, args...).Scan(&n); err != nil {
		t.Fatal(q, err)
	}
	return n
}

// The 520 release proof reads the frame's post-effect state and fails closed on
// ANY deviation. Each witness is mutated separately inside the frame.
func TestUSPasswordPostStateVerificationFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name string
		sql  string
		args []any
	}{
		{"epoch", `UPDATE accounts SET epoch=epoch+1 WHERE id=?`, []any{usPasswordAccountID}},
		{"profile", `UPDATE accounts SET profile_id='other-profile' WHERE id=?`, []any{usPasswordAccountID}},
		{"must_change", `UPDATE accounts SET must_change=1 WHERE id=?`, []any{usPasswordAccountID}},
		{"hash", `UPDATE accounts SET password_hash=? WHERE id=?`, []any{[]byte(usPasswordPreviousHash), usPasswordAccountID}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, proof, command := usPasswordFixture(t)
			r := usPasswordRegistry(t, w)
			turn, _, _ := usPasswordTurn(t, w, r, proof, 0)
			if _, err := applyUSPasswordReplace(t.Context(), turn, command); err != nil {
				t.Fatal(err)
			}
			v := usPasswordSavedFixture(command)
			if err := verifyUSPasswordPostState(t.Context(), turn, command, v); err != nil {
				t.Fatalf("control post-state was refused: %v", err)
			}
			if _, err := turn.transaction.conn.ExecContext(t.Context(), tc.sql, tc.args...); err != nil {
				t.Fatal(err)
			}
			if err := verifyUSPasswordPostState(t.Context(), turn, command, v); !errors.Is(err, ErrResolveRequired) {
				t.Fatalf("tampered post-state was accepted: %v", err)
			}
		})
	}
}

// After a committed 520 the OLD-epoch session no longer authorizes the next
// command (US-01's binding path, its 'epoch' case), while a session issued after
// the scalar advance replays the ORIGINAL void result.
func TestUSPasswordStaleEpochProofDeniedThenNextEpochReplay(t *testing.T) {
	w, proof, command := usPasswordFixture(t)
	r := usPasswordRegistry(t, w)
	turn, a, _ := usPasswordTurn(t, w, r, proof, 0)
	original, _, err := runUSPasswordDispatch(t.Context(), turn, a, usPasswordInput(t, command))
	if err != nil || original.Outcome.State != Applied || original.Outcome.Replayed {
		t.Fatal("original", original, err)
	}
	// The very session that authorized the original is stale at the next
	// command: accounts.epoch moved to 2 while the stamp binds 1.
	turn, b := usOwnerAuthorityTurn(t, w, r)
	if got, err := authorizeUSOwnerPolicy(t.Context(), turn, proof, t.Context(), b); got != nil || !errors.Is(err, ErrDisclosureDenied) {
		t.Fatal("old-epoch session survived the password epoch advance", got, err)
	}
	// Release the denied frame before re-authenticating on the same database.
	_, _ = turn.transaction.rollback(nil)
	// A re-authenticated next-epoch session reads the saved original.
	current := usPasswordCurrentProof(t, w, proof)
	turn, a, _ = usPasswordTurn(t, w, r, current, 0)
	replay, _, err := runUSPasswordDispatch(t.Context(), turn, a, &usPasswordInvocation{request: usPasswordInput(t, command).request})
	if err != nil {
		t.Fatal(err)
	}
	if replay.Outcome.State != Applied || !replay.Outcome.Replayed || !reflect.DeepEqual(replay.Value, original.Value) || replay.Digest != original.Digest {
		t.Fatal("next-epoch replay did not return the saved original", replay)
	}
}

// usPasswordForeignWitness builds a COMPLETE second account whose current hash
// is the caller's "previous" witness. That is the shape a caller-supplied
// witness attack would use, so the own-account barrier is the only thing that
// can refuse it.
type usPasswordForeignWitness struct{ account, member, profile, lifetime string }

func usPasswordForeignAccount(t *testing.T, w *executor) usPasswordForeignWitness {
	t.Helper()
	f := usPasswordForeignWitness{
		account:  "foreign-account",
		member:   strings.Repeat("f", 32),
		profile:  "foreign-profile",
		lifetime: strings.Repeat("e", 32),
	}
	wc008NativeExec(t, w, `INSERT INTO accounts(id,username,password_hash,profile_id) VALUES(?,?,?,?)`,
		f.account, f.account, []byte(usPasswordPreviousHash), f.profile)
	// A second OWNER row is impossible (direct_one_owner is unique); the target
	// read and CAS do not depend on the role, so an ordinary member is the same
	// foreign-witness shape.
	wc008NativeExec(t, w, `INSERT INTO direct_memberships(account_id,incarnation,role) VALUES(?,?,'member')`, f.account, f.member)
	wc008NativeExec(t, w, `INSERT INTO direct_profiles(id,account_id,name,is_primary,lifetime) VALUES(?,?,'Foreign',1,?)`,
		f.profile, f.account, f.lifetime)
	return f
}

// The target account/profile/member-incarnation/lifetime equality is the ONLY
// barrier against replacing another account's password: neither the source
// target read nor the scalar CAS ever sees the issuer. Each case substitutes
// one foreign witness; deleting the barrier must fail this test (the coherent
// foreign-account case would then replace the foreign password).
func TestUSPasswordForeignTargetDenied(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*usPasswordReplaceRequest, usPasswordForeignWitness)
	}{
		{"foreign_account", func(q *usPasswordReplaceRequest, f usPasswordForeignWitness) {
			q.target.account, q.target.username, q.target.profileName = f.account, f.account, "Foreign"
			q.target.memberIncarnation, q.target.profile, q.target.profileLifetime = f.member, f.profile, f.lifetime
		}},
		{"foreign_profile", func(q *usPasswordReplaceRequest, f usPasswordForeignWitness) { q.target.profile = f.profile }},
		{"foreign_member_incarnation", func(q *usPasswordReplaceRequest, f usPasswordForeignWitness) { q.target.memberIncarnation = f.member }},
		{"foreign_profile_lifetime", func(q *usPasswordReplaceRequest, f usPasswordForeignWitness) { q.target.profileLifetime = f.lifetime }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, proof, command := usPasswordFixture(t)
			foreign := usPasswordForeignAccount(t, w)
			q := command.request
			tc.mutate(&q, foreign)
			target, err := prepareUSPasswordCommand(command.identity, usPasswordOwnerID, q)
			if err != nil {
				t.Fatal(err)
			}
			r := usPasswordRegistry(t, w)
			turn, a, _ := usPasswordTurn(t, w, r, proof, 0)
			out, _, err := runUSPasswordDispatch(t.Context(), turn, a, usPasswordInput(t, target))
			if !errors.Is(err, ErrDisclosureDenied) || errors.Is(err, ErrResolveRequired) || errors.Is(err, ErrConflict) || !reflect.DeepEqual(out, usPasswordDispatchResult{}) {
				t.Fatal("foreign target was not refused as a disclosure denial", out, err)
			}
			if n := usPasswordCount(t, w, `SELECT count(*) FROM security_command_receipts`); n != 0 {
				t.Fatal("foreign target wrote a receipt", n)
			}
			if epoch, hash, _ := usPasswordAccount(t, w, usPasswordAccountID); epoch != 1 || string(hash) != usPasswordPreviousHash {
				t.Fatal("foreign target moved the issuer source", epoch)
			}
			if epoch, hash, _ := usPasswordAccount(t, w, foreign.account); epoch != 1 || string(hash) != usPasswordPreviousHash {
				t.Fatal("foreign target moved the foreign source", epoch)
			}
		})
	}
}

// The scalar CAS carries its own epoch predicate: usPasswordTargetRead must not
// be the only thing between a stale witness and a source write.
func TestUSPasswordReplaceCASRequiresStampedEpoch(t *testing.T) {
	w, proof, command := usPasswordFixture(t)
	r := usPasswordRegistry(t, w)
	turn, _, _ := usPasswordTurn(t, w, r, proof, 0)
	cas := func(epoch int64) error {
		t.Helper()
		row, err := usPasswordQuery(t.Context(), turn, usPasswordReplaceWrite, command.request.nextHash, usPasswordAccountID, epoch, command.request.previousHash, usPasswordProfileID)
		if err != nil {
			t.Fatal(err)
		}
		var account, profile string
		var next int64
		var mustChange bool
		var hash []byte
		return row.Scan(&account, &profile, &next, &mustChange, &hash)
	}
	if err := cas(2); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("stale epoch CAS affected a row: %v", err)
	}
	var epoch int64
	var hash []byte
	var mustChange bool
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT epoch,password_hash,must_change FROM accounts WHERE id=?`, usPasswordAccountID).Scan(&epoch, &hash, &mustChange); err != nil {
		t.Fatal(err)
	}
	if epoch != 1 || string(hash) != string(command.request.previousHash) || mustChange {
		t.Fatal("stale epoch CAS moved the source", epoch, mustChange)
	}
	// Positive control: the stamped epoch DOES write, so the zero above is the
	// predicate and not a broken binding.
	if err := cas(1); err != nil {
		t.Fatal(err)
	}
}

// Receipt failure after a nonempty prefix rolls the WHOLE replacement back.
func TestUSPasswordReceiptFailureRollsBack(t *testing.T) {
	for _, tc := range []struct {
		name   string
		remove func(*commandTurn)
	}{
		{"owner_advance", func(turn *commandTurn) { delete(turn.steps, usPasswordOwnerAdvance) }},
		{"typed_child", func(turn *commandTurn) { delete(turn.steps, usPasswordChildInsert) }},
		{"receipt_header", func(turn *commandTurn) { delete(turn.steps, usPasswordReceiptInsert) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, proof, command := usPasswordFixture(t)
			r := usPasswordRegistry(t, w)
			turn, a, _ := usPasswordTurn(t, w, r, proof, 0)
			tc.remove(turn)
			if out, _, err := runUSPasswordDispatch(t.Context(), turn, a, usPasswordInput(t, command)); err == nil || !reflect.DeepEqual(out, usPasswordDispatchResult{}) {
				t.Fatal("partial prefix escaped", out, err)
			}
			if epoch, hash, _ := usPasswordAccount(t, w, usPasswordAccountID); epoch != 1 || string(hash) != usPasswordPreviousHash {
				t.Fatal("source replacement survived the rollback", epoch)
			}
			if usPasswordCount(t, w, `SELECT count(*) FROM security_command_receipts`) != 0 ||
				usPasswordCount(t, w, `SELECT count(*) FROM security_password_results`) != 0 ||
				usPasswordCount(t, w, `SELECT retained_count FROM security_command_owners WHERE id=?`, usPasswordOwnerID) != 0 {
				t.Fatal("receipt/owner debt survived the rollback")
			}
		})
	}
}

// An uncertain COMMIT is never Applied: the caller only learns to resolve.
func TestUSPasswordUnknownCommitNeverApplied(t *testing.T) {
	for _, after := range []bool{false, true} {
		t.Run(map[bool]string{false: "before", true: "after"}[after], func(t *testing.T) {
			w, proof, command := usPasswordFixture(t)
			r := usPasswordRegistry(t, w)
			var path string
			if err := w.db.QueryRowContext(t.Context(), `SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&path); err != nil {
				t.Fatal(err)
			}
			connector, err := sqliteconn.Connector(path, w.config, sqliteconn.ExistingFile)
			if err != nil {
				t.Fatal(err)
			}
			fault := &faultConnector{Connector: connector, replyError: true, afterCommit: after}
			native := false
			fault.afterNativeCommit = func() { native = true }
			installFaultPool(w, fault)
			turn, a, _ := usPasswordTurn(t, w, r, proof, 0)
			out, floor, err := runUSPasswordDispatch(t.Context(), turn, a, usPasswordInput(t, command))
			if !errors.Is(err, ErrResolveRequired) || out.Outcome.State == Applied || !reflect.DeepEqual(out, usPasswordDispatchResult{}) || floor.AcknowledgedThrough != 0 {
				t.Fatal("uncertain commit exposed success", out, floor, err)
			}
			if fault.commits.Load() != 1 || native != after {
				t.Fatal("physical COMMIT disposition changed", fault.commits.Load(), native, after)
			}
			if _, err := w.begin(t.Context(), usLocalPasswordReplace, testRecipe(), &testCustody{}); !errors.Is(err, ErrResolveRequired) {
				t.Fatal("fenced writer admitted another turn", err)
			}
		})
	}
}

// A caller cancellation landing only AFTER the physical COMMIT must withdraw the
// publication: a void response with zero output. The COMMIT is durable, so a
// retry from a re-authenticated (post-epoch) session resolves the ORIGINAL
// through the receipt without a second source effect or receipt. Deleting the
// deferred final publication in runUSPasswordDispatch makes this test publish
// the committed result.
func TestUSPasswordCanceledAfterCommitPublishesNothing(t *testing.T) {
	w, proof, command := usPasswordFixture(t)
	r := usPasswordRegistry(t, w)
	inv := usPasswordInput(t, command)
	turn, a, _ := usPasswordTurn(t, w, r, proof, 0)
	caller, cancel := context.WithCancel(t.Context())
	defer cancel()
	// Cancel from the first trusted clock sample AFTER COMMIT is attempted:
	// physically the COMMIT has returned, and no pre-terminal context check
	// remains. Only the deferred final publication can still clear the result.
	originalNow := w.now
	cancelled := false
	w.now = func() time.Time {
		if !cancelled && turn.transaction.commitAttempted && !turn.transaction.done {
			cancelled = true
			cancel()
		}
		return originalNow()
	}
	out, floor, err := runUSPasswordDispatch(caller, turn, a, inv)
	w.now = originalNow
	if !cancelled || !errors.Is(err, context.Canceled) || !reflect.DeepEqual(out, usPasswordDispatchResult{}) || floor.AcknowledgedThrough != 0 {
		t.Fatal("post-commit cancellation published a result", out, floor, err)
	}
	epoch, hash, _ := usPasswordAccount(t, w, usPasswordAccountID)
	if epoch != 2 || string(hash) != usPasswordNextHash || usPasswordCount(t, w, `SELECT count(*) FROM security_command_receipts`) != 1 {
		t.Fatal("original source/receipt state", epoch, string(hash))
	}
	// Retry from the honest post-replacement session: the receipt returns the
	// committed ORIGINAL, and the source is not replaced a second time.
	current := usPasswordCurrentProof(t, w, proof)
	before := usPasswordCount(t, w, `SELECT total_changes()`)
	turn, a, _ = usPasswordTurn(t, w, r, current, 0)
	retry, retryFloor, err := runUSPasswordDispatch(t.Context(), turn, a, &usPasswordInvocation{request: inv.request})
	if err != nil || retry.Outcome.State != Applied || !retry.Outcome.Replayed || retryFloor.AcknowledgedThrough != 0 {
		t.Fatal("retry did not resolve the original", retry, err)
	}
	if retry.Value.resultEpoch != 2 || retry.Value.previousEpoch != 1 || retry.Digest == (ResultDigest{}) {
		t.Fatal("retry lost the original value", retry.Value)
	}
	epoch, hash, _ = usPasswordAccount(t, w, usPasswordAccountID)
	if epoch != 2 || string(hash) != usPasswordNextHash ||
		usPasswordCount(t, w, `SELECT count(*) FROM security_command_receipts`) != 1 ||
		usPasswordCount(t, w, `SELECT total_changes()`) != before {
		t.Fatal("retry re-applied the source", epoch, string(hash))
	}
}

// A canceled preparation denies before any read and writes nothing.
func TestUSPasswordCanceledEntryDenies(t *testing.T) {
	w, proof, command := usPasswordFixture(t)
	r := usPasswordRegistry(t, w)
	turn, a, cancel := usPasswordTurn(t, w, r, proof, 0)
	cancel()
	if out, _, err := runUSPasswordDispatch(t.Context(), turn, a, usPasswordInput(t, command)); !errors.Is(err, ErrDisclosureDenied) || !reflect.DeepEqual(out, usPasswordDispatchResult{}) {
		t.Fatal("canceled preparation was accepted", out, err)
	}
	if epoch, hash, _ := usPasswordAccount(t, w, usPasswordAccountID); epoch != 1 || string(hash) != usPasswordPreviousHash {
		t.Fatal("canceled preparation wrote source")
	}
	if usPasswordCount(t, w, `SELECT count(*) FROM security_command_receipts`) != 0 {
		t.Fatal("canceled preparation wrote a receipt")
	}
}

// The retained owner byte bound and count bound each refuse BEFORE any effect.
func TestUSPasswordQuotaByteAndCountRefuse(t *testing.T) {
	for _, tc := range []struct {
		name  string
		count int64
		bytes int64
	}{
		{"byte_bound", 1, usPasswordOwnerReceiptBytes - 1},
		{"count_bound", usPasswordOwnerReceipts, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, proof, command := usPasswordFixture(t)
			wc008NativeExec(t, w, `UPDATE security_command_owners SET last_sequence=1,acknowledged_sequence=1,retained_count=?,retained_variable_bytes=? WHERE id=?`, tc.count, tc.bytes, usPasswordOwnerID)
			// Sequence 2 is the next legitimate original.
			identity := command.identity
			identity.Sequence, identity.OperationID = 2, [16]byte{4}
			next, err := prepareUSPasswordCommand(identity, usPasswordOwnerID, command.request)
			if err != nil {
				t.Fatal(err)
			}
			r := usPasswordRegistry(t, w)
			turn, a, _ := usPasswordTurn(t, w, r, proof, 0)
			out, _, err := runUSPasswordDispatch(t.Context(), turn, a, usPasswordInput(t, next))
			var refusal *Refusal
			if !errors.As(err, &refusal) || refusal.Code != RefusalCapacity || !reflect.DeepEqual(out, usPasswordDispatchResult{}) {
				t.Fatal("quota", out, err)
			}
			if epoch, hash, _ := usPasswordAccount(t, w, usPasswordAccountID); epoch != 1 || string(hash) != usPasswordPreviousHash {
				t.Fatal("capacity refusal wrote a source effect", epoch)
			}
			if usPasswordCount(t, w, `SELECT count(*) FROM security_command_receipts`) != 0 {
				t.Fatal("capacity refusal wrote a receipt")
			}
		})
	}
}

// A retired owner cannot be resurrected by a write or an ACK; its original
// stays readable.
func TestUSPasswordRetiredOwner(t *testing.T) {
	w, proof, command := usPasswordFixture(t)
	r := usPasswordRegistry(t, w)
	turn, a, _ := usPasswordTurn(t, w, r, proof, 0)
	original, _, err := runUSPasswordDispatch(t.Context(), turn, a, usPasswordInput(t, command))
	if err != nil {
		t.Fatal(err)
	}
	wc008NativeExec(t, w, `UPDATE security_command_owners SET retired_ms=1 WHERE id=?`, usPasswordOwnerID)
	current := usPasswordCurrentProof(t, w, proof)
	inv := &usPasswordInvocation{request: usPasswordInput(t, command).request}
	turn, a, _ = usPasswordTurn(t, w, r, current, 0)
	replay, _, err := runUSPasswordDispatch(t.Context(), turn, a, inv)
	if err != nil || replay.Outcome.State != Applied || !replay.Outcome.Replayed || !reflect.DeepEqual(replay.Value, original.Value) {
		t.Fatal("retired owner replay", replay, err)
	}
	turn, a, _ = usPasswordTurn(t, w, r, current, 0)
	if out, _, err := runUSPasswordDispatch(t.Context(), turn, a, &usPasswordInvocation{request: inv.request, ack: true}); !errors.Is(err, ErrOwnerUnavailable) || !reflect.DeepEqual(out, usPasswordDispatchResult{}) {
		t.Fatal("retired owner ACK", out, err)
	}
	identity := command.identity
	identity.Sequence, identity.OperationID = 2, [16]byte{4}
	next, err := prepareUSPasswordCommand(identity, usPasswordOwnerID, command.request)
	if err != nil {
		t.Fatal(err)
	}
	turn, a, _ = usPasswordTurn(t, w, r, current, 0)
	if out, _, err := runUSPasswordDispatch(t.Context(), turn, a, usPasswordInput(t, next)); !errors.Is(err, ErrOwnerUnavailable) || !reflect.DeepEqual(out, usPasswordDispatchResult{}) {
		t.Fatal("retired owner accepted a fresh write", out, err)
	}
	if epoch, _, _ := usPasswordAccount(t, w, usPasswordAccountID); epoch != 2 {
		t.Fatal("retired owner was mutated", epoch)
	}
}

// The dispatcher funds its own authority rechecks out of the parent's grant.
func TestUSPasswordRecheckFunding(t *testing.T) {
	w, proof, command := usPasswordFixture(t)
	r := usPasswordRegistry(t, w)
	for _, memory := range []uint64{uint64(usPasswordDispatchRechecks*usPasswordRecheckBytes) - 1, uint64(usPasswordDispatchRechecks * usPasswordRecheckBytes)} {
		turn, a, _ := usPasswordTurn(t, w, r, proof, memory)
		out, _, err := runUSPasswordDispatch(t.Context(), turn, a, usPasswordInput(t, command))
		var refusal *Refusal
		if !errors.As(err, &refusal) || refusal.Code != RefusalUnqualified || !reflect.DeepEqual(out, usPasswordDispatchResult{}) {
			t.Fatal("unqualified funding", memory, out, err)
		}
	}
	if epoch, _, _ := usPasswordAccount(t, w, usPasswordAccountID); epoch != 1 {
		t.Fatal("unfunded dispatch wrote a source effect", epoch)
	}
	if usPasswordCount(t, w, `SELECT count(*) FROM security_command_receipts`) != 0 {
		t.Fatal("unfunded dispatch wrote a receipt")
	}
	if usPasswordDispatchRechecks != 9 {
		t.Fatal("declared recheck count changed without a funding review")
	}
}

// Neither hash image may appear in any output or in any database row except the
// accounts.password_hash column that is the replacement target itself.
func TestUSPasswordNoSecretLeak(t *testing.T) {
	sentinels := []string{"SENTINELPREV", "SENTINELNEXT"}
	w, proof, command := usPasswordFixture(t)
	r := usPasswordRegistry(t, w)
	turn, a, _ := usPasswordTurn(t, w, r, proof, 0)
	out, _, err := runUSPasswordDispatch(t.Context(), turn, a, usPasswordInput(t, command))
	if err != nil {
		t.Fatal(err)
	}
	// Outputs: the frozen preparation, the closed results and every error value.
	for _, value := range []any{command, command.request, out, out.Value, usPasswordCompletion{out, AckResult{}}} {
		for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x"} {
			text := fmt.Sprintf(format, value)
			for _, sentinel := range sentinels {
				if strings.Contains(text, sentinel) {
					t.Fatal("output leaked password material", format, sentinel)
				}
			}
		}
	}
	// Failing compositions: the ACTUAL error strings must carry no image
	// either. The successful run above returns nil, so scanning that `err` was
	// dead code.
	for _, tc := range []struct {
		name string
		run  func(*testing.T) error
	}{
		{"foreign_target", func(t *testing.T) error {
			w, proof, command := usPasswordFixture(t)
			q := command.request
			q.target.profileLifetime = strings.Repeat("d", 32)
			foreign, err := prepareUSPasswordCommand(command.identity, usPasswordOwnerID, q)
			if err != nil {
				t.Fatal(err)
			}
			r := usPasswordRegistry(t, w)
			turn, a, _ := usPasswordTurn(t, w, r, proof, 0)
			_, _, err = runUSPasswordDispatch(t.Context(), turn, a, usPasswordInput(t, foreign))
			return err
		}},
		{"stale_epoch", func(t *testing.T) error {
			w, proof, command := usPasswordFixture(t)
			stale := *command
			stale.request.target.epoch = 2
			stale.digest, _ = canonicalRequestDigest(stale.kind, 1, stale.identity, stale.request)
			r := usPasswordRegistry(t, w)
			turn, a, _ := usPasswordTurn(t, w, r, proof, 0)
			_, _, err := runUSPasswordDispatch(t.Context(), turn, a, usPasswordInput(t, &stale))
			return err
		}},
		{"receipt_failure", func(t *testing.T) error {
			w, proof, command := usPasswordFixture(t)
			r := usPasswordRegistry(t, w)
			turn, a, _ := usPasswordTurn(t, w, r, proof, 0)
			delete(turn.steps, usPasswordOwnerAdvance)
			_, _, err := runUSPasswordDispatch(t.Context(), turn, a, usPasswordInput(t, command))
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.run(t)
			if err == nil {
				t.Fatal("expected a failing composition")
			}
			for _, format := range []string{"%v", "%+v", "%s", "%q"} {
				text := fmt.Sprintf(format, err)
				for _, sentinel := range sentinels {
					if strings.Contains(text, sentinel) {
						t.Fatal("error leaked password material", tc.name, format, sentinel)
					}
				}
			}
		})
	}
	// Database rows: every table, every column, EXCEPT accounts.password_hash.
	usPasswordScanForSentinels(t, w, sentinels...)
	if epoch, hash, _ := usPasswordAccount(t, w, usPasswordAccountID); epoch != 2 || string(hash) != usPasswordNextHash {
		t.Fatal("the target hash column no longer holds exactly the new image", epoch)
	}
}

// usPasswordCurrentProof is the honest post-replacement caller: the command
// advanced accounts.epoch, so a retry resumes from a session issued after that
// scalar change. This fixture act is a re-authentication, not a grant.
func usPasswordCurrentProof(t *testing.T, w *executor, base *usOwnerAuthorityProof) *usOwnerAuthorityProof {
	t.Helper()
	witness := base.witness
	witness.issuer.epoch++
	proof, err := prepareUSOwnerAuthority(witness)
	if err != nil {
		t.Fatal(err)
	}
	wc008NativeExec(t, w, `UPDATE authorization_session_families SET epoch=? WHERE id=?`, witness.issuer.epoch, witness.issuer.family)
	return proof
}

func usPasswordScanForSentinels(t *testing.T, w *executor, sentinels ...string) {
	t.Helper()
	tables, err := w.db.QueryContext(t.Context(), `SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for tables.Next() {
		var name string
		if err = tables.Scan(&name); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	if err = tables.Err(); err != nil {
		t.Fatal(err)
	}
	if err = tables.Close(); err != nil {
		t.Fatal(err)
	}
	if len(names) == 0 {
		t.Fatal("empty table inventory")
	}
	for _, table := range names {
		columns, err := w.db.QueryContext(t.Context(), `SELECT name FROM pragma_table_info('`+strings.ReplaceAll(table, "'", "''")+`')`)
		if err != nil {
			t.Fatal(table, err)
		}
		var kept []string
		for columns.Next() {
			var column string
			if err = columns.Scan(&column); err != nil {
				t.Fatal(table, err)
			}
			// The ONE excluded column: the replacement target itself.
			if table == "accounts" && column == "password_hash" {
				continue
			}
			kept = append(kept, column)
		}
		if err = columns.Err(); err != nil {
			t.Fatal(table, err)
		}
		if err = columns.Close(); err != nil {
			t.Fatal(table, err)
		}
		if len(kept) == 0 {
			continue
		}
		quoted := make([]string, len(kept))
		for i, column := range kept {
			quoted[i] = `"` + column + `"`
		}
		rows, err := w.db.QueryContext(t.Context(), `SELECT `+strings.Join(quoted, ",")+` FROM "`+table+`"`)
		if err != nil {
			t.Fatal(table, err)
		}
		values := make([]any, len(kept))
		ptrs := make([]any, len(kept))
		for i := range values {
			ptrs[i] = &values[i]
		}
		for rows.Next() {
			if err = rows.Scan(ptrs...); err != nil {
				t.Fatal(table, err)
			}
			for i, value := range values {
				var text string
				switch v := value.(type) {
				case nil:
					continue
				case []byte:
					text = string(v)
				case string:
					text = v
				default:
					text = fmt.Sprint(v)
				}
				for _, sentinel := range sentinels {
					if strings.Contains(text, sentinel) {
						t.Fatalf("password material leaked into %s.%s", table, kept[i])
					}
				}
			}
		}
		if err = rows.Err(); err != nil {
			t.Fatal(table, err)
		}
		if err = rows.Close(); err != nil {
			t.Fatal(table, err)
		}
	}
}

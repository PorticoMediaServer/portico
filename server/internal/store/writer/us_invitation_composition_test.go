package writer

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// usInvitationTestNow is anchored in the real future so the frame's absolute
// safety deadline stays valid; the fake clock still fixes every domain instant.
var usInvitationTestNow = time.Now().Add(24 * time.Hour).Truncate(time.Second)

func usInvitationTestMS() int64 { return usInvitationTestNow.UnixMilli() }

func usInvitationTestDefinitions() []stmt.Definition {
	defs := append([]stmt.Definition(nil), usInvitationDefinitions()...)
	defs = append(defs, usInvitationSourceDefinitions()...)
	defs = append(defs, usInvitationReceiptDefinitions()...)
	return defs
}

func usInvitationTestExecutor(t *testing.T) (*executor, *testInvitationManager) {
	t.Helper()
	w := wc008NativeExecutor(t)
	w.now = func() time.Time { return usInvitationTestNow }
	wc008NativeExec(t, w, `INSERT INTO identity_state(singleton,sign_in_kind,revision) VALUES(1,'local',1)`)
	wc008NativeExec(t, w, `INSERT INTO libraries(id,name,kind,root) VALUES('movies','Movies','movies','/movies')`)
	wc008NativeExec(t, w, `INSERT INTO libraries(id,name,kind,root) VALUES('music','Music','music','/music')`)
	inc := make([]byte, 16)
	inc[0] = 1
	wc008NativeExec(t, w, `INSERT INTO security_command_owners(id,command_incarnation,ingress,server_id,issuer_account_id,issuer_member_incarnation,issuer_profile_id,issuer_profile_lifetime,issuer_installation_incarnation,original_family_id,created_ms)
VALUES('security-owner',?,'authenticated','server','us-owner',?,?,?,?,?,0)`,
		inc, strings.Repeat("a", 32), "issuer-profile", strings.Repeat("b", 32), strings.Repeat("c", 32), "issuer-family")
	return w, usTestInvitationManager("us-owner", "member", "admin")
}

func usInvitationTestSeedInvitation(t *testing.T, w *executor, role, state string, revision, expiry int64) {
	t.Helper()
	wc008NativeExec(t, w, `INSERT INTO access_invitations(id,notes,code_hash,role,allowed_libraries,state,created_ms,expires_ms,created_by,revision,bound_username,max_streams_mode,max_streams,allow_remote,access_tokens,max_content_rating)
VALUES('us-invite',NULL,?,'`+role+`','["movies"]','`+state+`',?,?,'us-owner',?,'','inherit',0,1,1,'')`,
		usInvitationFixtureHash, usInvitationTestMS()-500, expiry, revision)
}

func usInvitationTestCreateCommand(t *testing.T, id string, role string, libraries []string) (*usInvitationCommand, string) {
	t.Helper()
	key := bytes.Repeat([]byte{7}, 32)
	code, hash, err := usInvitationLegacyLink(key, id)
	if err != nil {
		t.Fatal(err)
	}
	i, scope := usInvitationPreparationFixture()
	p, err := prepareUSInvitationCreate(i, scope, id, hash, usInvitationCreateInput{role: role, libraries: libraries})
	if err != nil {
		t.Fatal(err)
	}
	protected, err := prepareUSInvitationCode(p, "invitation-key-id", key, bytes.Repeat([]byte{9}, 12), code)
	if err != nil {
		t.Fatal(err)
	}
	signIn, err := prepareUSInvitationSignInKind("local", 1)
	if err != nil {
		t.Fatal(err)
	}
	keyID := protected.keyID
	cmd := &usInvitationCommand{kind: usInvitationCreate, ownerID: scope.ownerID, actor: scope.issuer.account,
		identity: i, prepared: p, signIn: signIn,
		code: usInvitationResultCode{keyID: &keyID, nonce: append([]byte(nil), protected.nonce...), body: append([]byte(nil), protected.body...)}}
	cmd.digest, err = usInvitationCommandDigest(cmd)
	if err != nil {
		t.Fatal(err)
	}
	return cmd, code
}

func usInvitationTestEditCommand(t *testing.T, revision int64, role string, libraries []string, expiresMS int64) *usInvitationCommand {
	t.Helper()
	i, scope := usInvitationPreparationFixture()
	p, err := prepareUSInvitationEdit(i, scope, "us-invite", usInvitationEditInput{
		expectedRevision: revision, role: role, libraries: libraries,
		expiresAt: time.UnixMilli(expiresMS).UTC().Format(time.RFC3339)}, usInvitationTestMS())
	if err != nil {
		t.Fatal(err)
	}
	signIn, err := prepareUSInvitationSignInKind("local", 1)
	if err != nil {
		t.Fatal(err)
	}
	cmd := &usInvitationCommand{kind: usInvitationEdit, ownerID: scope.ownerID, actor: scope.issuer.account, identity: i, prepared: p, signIn: signIn}
	cmd.digest, err = usInvitationCommandDigest(cmd)
	if err != nil {
		t.Fatal(err)
	}
	return cmd
}

func usInvitationTestRevokeCommand(t *testing.T, revision int64) *usInvitationCommand {
	t.Helper()
	i, scope := usInvitationPreparationFixture()
	p, err := prepareUSInvitationRevoke(i, scope, "us-invite", revision)
	if err != nil {
		t.Fatal(err)
	}
	signIn, err := prepareUSInvitationSignInKind("local", 1)
	if err != nil {
		t.Fatal(err)
	}
	cmd := &usInvitationCommand{kind: usInvitationRevoke, ownerID: scope.ownerID, actor: scope.issuer.account, identity: i, prepared: p, signIn: signIn}
	cmd.digest, err = usInvitationCommandDigest(cmd)
	if err != nil {
		t.Fatal(err)
	}
	return cmd
}

func usInvitationTestInvocation(t *testing.T, c *usInvitationCommand) *usInvitationInvocation {
	t.Helper()
	q, err := usInvitationReceiptForCommand(c)
	if err != nil {
		t.Fatal(err)
	}
	return &usInvitationInvocation{request: q, input: c}
}

func usInvitationTestCount(t *testing.T, w *executor, q string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := w.db.QueryRowContext(t.Context(), q, args...).Scan(&n); err != nil {
		t.Fatal(q, err)
	}
	return n
}

// TestUSInvitationComposition proves the physical original/replay/ACK shape,
// source-drift replay, corrupt-child refusal and full rollback for 550-552.
func TestUSInvitationComposition(t *testing.T) {
	t.Run("create_original_replay_ack", func(t *testing.T) {
		w, m := usInvitationTestExecutor(t)
		cmd, code := usInvitationTestCreateCommand(t, "us-created", "member", []string{"movies", "music"})
		inv := usInvitationTestInvocation(t, cmd)
		turn := wc008NativeTurn(t, w, usInvitationCreate, usInvitationTestDefinitions())
		out, floor, err := runUSInvitationDispatch(t.Context(), turn, m, inv)
		if err != nil || out.Outcome.State != Applied || out.Outcome.Replayed || floor.AcknowledgedThrough != 0 {
			t.Fatal("fresh create", out, floor, err)
		}
		if out.Code.keyID == nil || len(out.Code.body) != usInvitationLegacyCodeLen+16 || out.Value.code.present() {
			t.Fatal("first response code projection", out.Code.keyID, len(out.Code.body), out.Value.code)
		}
		if out.DTO.ID != "us-created" || out.DTO.Role != "member" || out.DTO.State != "pending" || out.DTO.Revision != 1 ||
			!reflect.DeepEqual(out.DTO.AllowedLibraries, []string{"movies", "music"}) || out.DTO.Code != "" ||
			out.DTO.CreatedAt != usInvitationTestNow.UTC().Format(time.RFC3339) || out.DTO.ExpiresAt != time.UnixMilli(usInvitationTestMS()+168*usInvitationHourMS).UTC().Format(time.RFC3339) {
			t.Fatal("complete create DTO", out.DTO)
		}
		var storedHash, storedRole, storedState, storedLibraries, storedBy, storedBound string
		var revision, expires int64
		if err := w.db.QueryRowContext(t.Context(), `SELECT code_hash,role,state,allowed_libraries,created_by,revision,expires_ms,bound_username FROM access_invitations WHERE id='us-created'`).
			Scan(&storedHash, &storedRole, &storedState, &storedLibraries, &storedBy, &revision, &expires, &storedBound); err != nil {
			t.Fatal(err)
		}
		if storedHash == "" || storedRole != "member" || storedState != "pending" || storedLibraries != `["movies","music"]` ||
			storedBy != "us-owner" || revision != 1 || expires != usInvitationTestMS()+168*usInvitationHourMS || storedBound != "" {
			t.Fatal("stored create source", storedHash, storedRole, storedState, storedLibraries, storedBy, revision, expires)
		}
		// Independent legacy oracle: the stored code_hash is the digest of the
		// exact HMAC code legacy redeem recomputes from the id.
		legacy, legacyHash, err := usInvitationLegacyLink(bytes.Repeat([]byte{7}, 32), "us-created")
		if err != nil || legacy != code || legacyHash != storedHash {
			t.Fatal("legacy code derivation", legacy == code, legacyHash == storedHash, err)
		}
		if usInvitationTestCount(t, w, `SELECT count(*) FROM security_command_receipts`) != 1 ||
			usInvitationTestCount(t, w, `SELECT count(*) FROM security_invitation_results`) != 1 ||
			usInvitationTestCount(t, w, `SELECT count(*) FROM security_result_libraries WHERE cohort='invitation'`) != 2 ||
			usInvitationTestCount(t, w, `SELECT retained_count FROM security_command_owners WHERE id='security-owner'`) != 1 {
			t.Fatal("retained create cohort")
		}

		// Source drift: replay reads ONLY the retained original and returns it
		// WITHOUT the sealed Code.
		wc008NativeExec(t, w, `UPDATE access_invitations SET notes='drifted',revision=revision+1 WHERE id='us-created'`)
		before := usInvitationTestCount(t, w, `SELECT total_changes()`)
		replayTurn := wc008NativeTurn(t, w, usInvitationCreate, usInvitationTestDefinitions())
		replay, replayFloor, err := runUSInvitationDispatch(t.Context(), replayTurn, m, &usInvitationInvocation{request: inv.request})
		if err != nil || replay.Outcome.State != Applied || !replay.Outcome.Replayed || replayFloor.AcknowledgedThrough != 0 {
			t.Fatal("replay", replay, replayFloor, err)
		}
		if replay.Code.present() || replay.DTO.Code != "" || replay.Value.code.present() || replay.Digest == (ResultDigest{}) {
			t.Fatal("replay disclosed the code")
		}
		if replay.DTO.Notes != nil || replay.DTO.Revision != 1 || !reflect.DeepEqual(replay.DTO.AllowedLibraries, []string{"movies", "music"}) {
			t.Fatal("replay drifted to current source", replay.DTO)
		}
		if usInvitationTestCount(t, w, `SELECT total_changes()`) != before || usInvitationTestCount(t, w, `SELECT count(*) FROM security_command_receipts`) != 1 {
			t.Fatal("replay mutated retained state")
		}

		// Corpus corruption: a changed child must be a resolve-required refusal.
		wc008NativeExec(t, w, `UPDATE security_result_libraries SET library_id='drifted' WHERE cohort='invitation' AND ordinal=1`)
		corruptTurn := wc008NativeTurn(t, w, usInvitationCreate, usInvitationTestDefinitions())
		corrupt, corruptFloor, err := runUSInvitationDispatch(t.Context(), corruptTurn, m, &usInvitationInvocation{request: inv.request})
		if !errors.Is(err, ErrResolveRequired) || !reflect.DeepEqual(corrupt, usInvitationDispatchResult{}) || corruptFloor.AcknowledgedThrough != 0 {
			t.Fatal("corrupt child accepted", corrupt, corruptFloor, err)
		}
		wc008NativeExec(t, w, `UPDATE security_result_libraries SET library_id='music' WHERE cohort='invitation' AND ordinal=1`)

		ackTurn := wc008NativeTurn(t, w, usInvitationCreate, usInvitationTestDefinitions())
		ack, ackFloor, err := runUSInvitationDispatch(t.Context(), ackTurn, m, &usInvitationInvocation{request: inv.request, ack: true})
		if err != nil || ackFloor.AcknowledgedThrough != 1 || !reflect.DeepEqual(ack, usInvitationDispatchResult{}) {
			t.Fatal("ACK", ack, ackFloor, err)
		}
		if usInvitationTestCount(t, w, `SELECT count(*) FROM security_command_receipts`) != 1 {
			t.Fatal("ACK deleted retained bytes")
		}
	})

	t.Run("edit_original", func(t *testing.T) {
		w, m := usInvitationTestExecutor(t)
		usInvitationTestSeedInvitation(t, w, "member", "pending", 1, usInvitationTestMS()+1000)
		cmd := usInvitationTestEditCommand(t, 1, "member", []string{"music"}, usInvitationTestMS()+2000)
		turn := wc008NativeTurn(t, w, usInvitationEdit, usInvitationTestDefinitions())
		out, _, err := runUSInvitationDispatch(t.Context(), turn, m, usInvitationTestInvocation(t, cmd))
		if err != nil || out.Outcome.State != Applied || out.Code.present() || out.DTO.Code != "" {
			t.Fatal("fresh edit", out, err)
		}
		if out.DTO.Revision != 2 || out.DTO.State != "pending" || !reflect.DeepEqual(out.DTO.AllowedLibraries, []string{"music"}) ||
			out.DTO.ExpiresAt != time.UnixMilli(usInvitationTestMS()+2000).UTC().Format(time.RFC3339) {
			t.Fatal("edit DTO", out.DTO)
		}
		var role, state, libraries string
		var revision, expires int64
		if err := w.db.QueryRowContext(t.Context(), `SELECT role,state,allowed_libraries,revision,expires_ms FROM access_invitations WHERE id='us-invite'`).
			Scan(&role, &state, &libraries, &revision, &expires); err != nil || role != "member" || state != "pending" ||
			libraries != `["music"]` || revision != 2 || expires != usInvitationTestMS()+2000 {
			t.Fatal("stored edit", role, state, libraries, revision, expires, err)
		}
		if usInvitationTestCount(t, w, `SELECT count(*) FROM security_command_receipts WHERE command_kind=551 AND result_key_id IS NULL`) != 1 {
			t.Fatal("edit receipt retained a code")
		}
		// Drift the live row; replay must still return the saved original.
		wc008NativeExec(t, w, `UPDATE access_invitations SET role='admin',allowed_libraries='["movies"]' WHERE id='us-invite'`)
		replayTurn := wc008NativeTurn(t, w, usInvitationEdit, usInvitationTestDefinitions())
		replay, _, err := runUSInvitationDispatch(t.Context(), replayTurn, m, &usInvitationInvocation{request: usInvitationTestInvocation(t, cmd).request})
		if err != nil || !replay.Outcome.Replayed || replay.DTO.Role != "member" ||
			!reflect.DeepEqual(replay.DTO.AllowedLibraries, []string{"music"}) || replay.DTO.Revision != 2 {
			t.Fatal("edit replay drift", replay, err)
		}
	})

	t.Run("revoke_original", func(t *testing.T) {
		w, m := usInvitationTestExecutor(t)
		usInvitationTestSeedInvitation(t, w, "member", "pending", 1, usInvitationTestMS()+1000)
		cmd := usInvitationTestRevokeCommand(t, 1)
		turn := wc008NativeTurn(t, w, usInvitationRevoke, usInvitationTestDefinitions())
		out, _, err := runUSInvitationDispatch(t.Context(), turn, m, usInvitationTestInvocation(t, cmd))
		if err != nil || out.Outcome.State != Applied || out.DTO.State != "revoked" || out.DTO.Revision != 2 || out.Code.present() {
			t.Fatal("fresh revoke", out, err)
		}
		var state string
		var revision int64
		if err := w.db.QueryRowContext(t.Context(), `SELECT state,revision FROM access_invitations WHERE id='us-invite'`).Scan(&state, &revision); err != nil || state != "revoked" || revision != 2 {
			t.Fatal("stored revoke", state, revision, err)
		}
	})

	t.Run("create_library_rollback", func(t *testing.T) {
		w, m := usInvitationTestExecutor(t)
		cmd, _ := usInvitationTestCreateCommand(t, "us-missing-lib", "member", []string{"missing"})
		turn := wc008NativeTurn(t, w, usInvitationCreate, usInvitationTestDefinitions())
		out, floor, err := runUSInvitationDispatch(t.Context(), turn, m, usInvitationTestInvocation(t, cmd))
		if !errors.Is(err, ErrInvitationLibrary) || !reflect.DeepEqual(out, usInvitationDispatchResult{}) || floor.AcknowledgedThrough != 0 {
			t.Fatal("missing library accepted", out, floor, err)
		}
		if usInvitationTestCount(t, w, `SELECT count(*) FROM access_invitations WHERE id='us-missing-lib'`) != 0 ||
			usInvitationTestCount(t, w, `SELECT count(*) FROM security_command_receipts`) != 0 ||
			usInvitationTestCount(t, w, `SELECT retained_count FROM security_command_owners WHERE id='security-owner'`) != 0 {
			t.Fatal("refused create left a prefix")
		}
	})
}

// TestUSInvitationCanceledAfterCommit proves the deferred final publication
// withdraws a physically committed result when the CALLER is canceled after
// COMMIT was attempted. A fresh-context retry resolves the ORIGINAL.
func TestUSInvitationCanceledAfterCommit(t *testing.T) {
	w, m := usInvitationTestExecutor(t)
	cmd, _ := usInvitationTestCreateCommand(t, "us-created", "member", []string{"movies"})
	inv := usInvitationTestInvocation(t, cmd)
	turn := wc008NativeTurn(t, w, usInvitationCreate, usInvitationTestDefinitions())
	caller, cancel := context.WithCancel(t.Context())
	defer cancel()
	originalNow := w.now
	cancelled := false
	w.now = func() time.Time {
		if !cancelled && turn.transaction.commitAttempted && !turn.transaction.done {
			cancelled = true
			cancel()
		}
		return originalNow()
	}
	out, floor, err := runUSInvitationDispatch(caller, turn, m, inv)
	w.now = originalNow
	if !cancelled || !errors.Is(err, context.Canceled) || !reflect.DeepEqual(out, usInvitationDispatchResult{}) || floor.AcknowledgedThrough != 0 {
		t.Fatal("post-commit cancellation published a result", out, floor, err)
	}
	if usInvitationTestCount(t, w, `SELECT count(*) FROM access_invitations WHERE id='us-created'`) != 1 ||
		usInvitationTestCount(t, w, `SELECT count(*) FROM security_command_receipts`) != 1 {
		t.Fatal("committed original missing")
	}
	retryTurn := wc008NativeTurn(t, w, usInvitationCreate, usInvitationTestDefinitions())
	retry, _, err := runUSInvitationDispatch(t.Context(), retryTurn, m, &usInvitationInvocation{request: inv.request})
	if err != nil || retry.Outcome.State != Applied || !retry.Outcome.Replayed || retry.Code.present() || retry.DTO.ID != "us-created" {
		t.Fatal("retry did not resolve the original", retry, err)
	}
}

// TestUSInvitationUnknownCommitNeverApplied drives both physical dispositions of
// an uncertain COMMIT: the caller only learns to resolve the ORIGINAL.
func TestUSInvitationUnknownCommitNeverApplied(t *testing.T) {
	for _, after := range []bool{false, true} {
		t.Run(map[bool]string{false: "before", true: "after"}[after], func(t *testing.T) {
			w, path, m := usInvitationTestExecutorPath(t)
			connector, err := sqliteconn.Connector(path, w.config, sqliteconn.ExistingFile)
			if err != nil {
				t.Fatal(err)
			}
			fault := &faultConnector{Connector: connector, replyError: true, afterCommit: after}
			native := false
			fault.afterNativeCommit = func() { native = true }
			installFaultPool(w, fault)
			cmd, _ := usInvitationTestCreateCommand(t, "us-created", "member", []string{"movies"})
			turn := wc008NativeTurn(t, w, usInvitationCreate, usInvitationTestDefinitions())
			out, floor, err := runUSInvitationDispatch(t.Context(), turn, m, usInvitationTestInvocation(t, cmd))
			if !errors.Is(err, ErrResolveRequired) || out.Outcome.State == Applied || !reflect.DeepEqual(out, usInvitationDispatchResult{}) || floor.AcknowledgedThrough != 0 {
				t.Fatal("uncertain commit exposed success", out, floor, err)
			}
			if !w.ambiguous || !w.fenced || fault.commits.Load() != 1 || native != after {
				t.Fatal("uncertain writer disposition", w.ambiguous, w.fenced, fault.commits.Load(), native, after)
			}
		})
	}
}

func usInvitationTestExecutorPath(t *testing.T) (*executor, string, *testInvitationManager) {
	t.Helper()
	w := wc008NativeExecutor(t)
	path := ""
	if err := w.db.QueryRowContext(t.Context(), `SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&path); err != nil {
		t.Fatal(err)
	}
	w.now = func() time.Time { return usInvitationTestNow }
	wc008NativeExec(t, w, `INSERT INTO identity_state(singleton,sign_in_kind,revision) VALUES(1,'local',1)`)
	wc008NativeExec(t, w, `INSERT INTO libraries(id,name,kind,root) VALUES('movies','Movies','movies','/movies')`)
	inc := make([]byte, 16)
	inc[0] = 1
	wc008NativeExec(t, w, `INSERT INTO security_command_owners(id,command_incarnation,ingress,server_id,issuer_account_id,issuer_member_incarnation,issuer_profile_id,issuer_profile_lifetime,issuer_installation_incarnation,original_family_id,created_ms)
VALUES('security-owner',?,'authenticated','server','us-owner',?,?,?,?,?,0)`,
		inc, strings.Repeat("a", 32), "issuer-profile", strings.Repeat("b", 32), strings.Repeat("c", 32), "issuer-family")
	return w, path, usTestInvitationManager("us-owner", "member", "admin")
}

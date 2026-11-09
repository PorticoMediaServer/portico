package writer

import (
	"errors"
	"testing"
)

func mpSessionLogicalFixture(t *testing.T) *executor {
	t.Helper()
	w := mpSessionStopFixture(t)
	wc008NativeExec(t, w, `INSERT INTO playback_v1_sessions(id,device_id,account_id,profile_id,authority,start_key,start_digest,kind,role,state,request,revision,generation,media_session_id,media_generation,lease_expires_ms,created_ms,updated_ms) VALUES('logical','device','account','profile','local','key','digest','vod','player','playing','',1,1,'retire-media',2,100,1,1)`)
	return w
}
func mpSessionStopInputFixture() mpSessionStopInput {
	return mpSessionStopInput{SessionID: "logical", MediaSessionID: "retire-media", ExecutionID: "execution", AccountID: "account", ProfileID: "profile", RetireRequestID: "stop", LogicalRevision: 1, LogicalGeneration: 1, PhysicalRevision: 1, PhysicalGeneration: 2}
}

// A synthetic SAME-turn authority is limited to body/rollback controls. Actual
// native actor and supervisor authorization are separate prefix tests; these
// fixtures neither open a purpose nor qualify an accepting receipt driver.
func mpSessionStopBodyAuthority(t *commandTurn, id CommandID) *mpSessionStopAuthorization {
	a := &mpSessionStopAuthorization{turn: t, life: t.transaction.ctx, account: "account", profile: "profile", purpose: id, nowMS: 101}
	a.self = a
	return a
}
func TestMPSessionStopIntentNoReleaseAndNoop(t *testing.T) {
	for _, id := range []CommandID{715, 716, 718} {
		t.Run(string(rune(id)), func(t *testing.T) {
			w := mpSessionLogicalFixture(t)
			q := mpSessionStopInputFixture()
			turn := wc008NativeTurn(t, w, id, mpSessionStopDefinitions())
			a := mpSessionStopBodyAuthority(turn, id)
			got, err := applyMPSessionStop(t.Context(), turn, a, q)
			if err != nil || !got.Changed || !got.PhysicalPending || got.LogicalRevision != 2 || got.PhysicalRevision != 2 || got.RetireRequestID != "stop" {
				t.Fatal(got, err)
			}
			if _, err = turn.transaction.commit(); err != nil {
				t.Fatal(err)
			}
			var retired, exited, cleanup, grant, rev int64
			var state string
			if err = w.db.QueryRow(`SELECT mp_retired_generation,mp_exited_generation,mp_cleanup_complete,mp_grant_retired,mp_revision,state FROM playback_sessions`).Scan(&retired, &exited, &cleanup, &grant, &rev, &state); err != nil || retired != 2 || exited != 0 || cleanup != 0 || grant != 1 || rev != 2 || state != "stopped" {
				t.Fatal(retired, exited, cleanup, grant, rev, state, err)
			}
			q.LogicalRevision = 2
			q.PhysicalRevision = 2
			turn = wc008NativeTurn(t, w, id, mpSessionStopDefinitions())
			a = mpSessionStopBodyAuthority(turn, id)
			noop, err := applyMPSessionStop(t.Context(), turn, a, q)
			if err != nil || noop.Changed || !noop.PhysicalPending || noop.EndedMS != got.EndedMS {
				t.Fatal(noop, err)
			}
		})
	}
}
func TestMPSessionStopExactCASAndPrefixRollback(t *testing.T) {
	for _, cell := range []string{"replace", "account", "not-due", "older-retired", "older-exited", "half-retired", "copy", "rollback"} {
		t.Run(cell, func(t *testing.T) {
			w := mpSessionLogicalFixture(t)
			q := mpSessionStopInputFixture()
			id := CommandID(715)
			switch cell {
			case "replace":
				q.LogicalGeneration = 2
			case "account":
				q.AccountID = "other"
			case "not-due":
				id = 716
				wc008NativeExec(t, w, `UPDATE playback_v1_sessions SET lease_expires_ms=102`)
			case "older-retired":
				wc008NativeExec(t, w, `UPDATE playback_sessions SET mp_retired_generation=1,mp_retire_request_id='older'`)
			case "older-exited":
				wc008NativeExec(t, w, `UPDATE playback_sessions SET mp_retired_generation=1,mp_retire_request_id='older',mp_exited_generation=1,mp_exit_request_id='older',mp_exit_supervisor_incarnation=?,mp_exited_ms=1`, make([]byte, 16))
			case "half-retired":
				wc008NativeExec(t, w, `UPDATE playback_sessions SET mp_retired_generation=2,mp_retire_request_id='older'`)
			}
			turn := wc008NativeTurn(t, w, id, mpSessionStopDefinitions())
			a := mpSessionStopBodyAuthority(turn, id)
			if cell == "copy" {
				copy := *a
				a = &copy
			}
			got, err := applyMPSessionStop(t.Context(), turn, a, q)
			if cell == "rollback" {
				if err != nil || !got.Changed {
					t.Fatal(got, err)
				}
			} else if err == nil || got != (mpSessionStopValue{}) {
				t.Fatal("bad stop accepted", got, err)
			}
			_, _ = turn.transaction.rollback(nil)
			var ended, revision int64
			if err = w.db.QueryRow(`SELECT ended_ms,revision FROM playback_v1_sessions WHERE id='logical'`).Scan(&ended, &revision); err != nil || ended != 0 || revision != 1 {
				t.Fatal("prefix leaked", ended, revision, err)
			}
		})
	}
}
func TestMPSessionStopSelectionAndRevokedAuthority(t *testing.T) {
	w := mpSessionLogicalFixture(t)
	turn := wc008NativeTurn(t, w, 718, mpSessionStopDefinitions())
	a := mpSessionStopBodyAuthority(turn, 718)
	id, err := nextMPSessionAccountStop(t.Context(), turn, a, "")
	if err != nil || id != "logical" {
		t.Fatal(id, err)
	}
	if _, err = nextMPSessionAccountStop(t.Context(), turn, a, id); err == nil {
		t.Fatal("account cursor not bounded")
	}
	_, _ = turn.transaction.rollback(nil)
	turn = wc008NativeTurn(t, w, 716, mpSessionStopDefinitions())
	a = mpSessionStopBodyAuthority(turn, 716)
	id, err = nextMPSessionExpiry(t.Context(), turn, a)
	if err != nil || id != "logical" {
		t.Fatal(id, err)
	}
	a.self = nil
	if got, err := applyMPSessionStop(t.Context(), turn, a, mpSessionStopInputFixture()); !errors.Is(err, ErrDisclosureDenied) || got != (mpSessionStopValue{}) {
		t.Fatal(got, err)
	}
}

func TestMPSessionStopNativeCurrentActorAndExpiryOwner(t *testing.T) {
	w, pd, _ := wcProgressCompositionFixture(t)
	scope := pd.scope
	if err := pd.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	e := w.effective.executor
	turn := wc008NativeTurn(t, e, 715, mpSessionStopDefinitions())
	if a, err := authorizeMPSessionStop(t.Context(), turn, scope, t.Context()); err != nil || a == nil || a.account != scope.accountID || a.profile != scope.profileID {
		t.Fatal(a, err)
	}
	_, _ = turn.transaction.rollback(nil)
	wc008NativeExec(t, e, `UPDATE authorization_session_families SET revoked=1 WHERE id=?`, scope.familyID)
	turn = wc008NativeTurn(t, e, 715, mpSessionStopDefinitions())
	if a, err := authorizeMPSessionStop(t.Context(), turn, scope, t.Context()); a != nil || !errors.Is(err, ErrDisclosureDenied) {
		t.Fatal(a, err)
	}
	_, _ = turn.transaction.rollback(nil)
	// Revoking the original issuer does not revoke the server's physical cleanup
	// owner. Its own current MP05 lifetime and native server binding still fence it.
	wc008NativeExec(t, e, `INSERT INTO configuration(key,value) VALUES('id',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, scope.serverID)
	s, err := newMPPhysicalSupervisor(w, t.Context(), scope.serverID)
	if err != nil {
		t.Fatal(err)
	}
	turn = wc008NativeTurn(t, e, 716, mpSessionStopDefinitions())
	if a, err := authorizeMPSessionExpiry(t.Context(), turn, s); err != nil || a == nil {
		t.Fatal(a, err)
	}
	s.revoke()
	if a, err := authorizeMPSessionExpiry(t.Context(), turn, s); a != nil || !errors.Is(err, ErrDisclosureDenied) {
		t.Fatal(a, err)
	}
}

func TestMPSessionAccountStopOrdinaryViewerHiddenProfile(t *testing.T) {
	w, pd, _ := wcProgressCompositionFixture(t)
	scope := pd.scope
	if scope.role != "member" {
		t.Fatal("fixture is not an ordinary viewer")
	}
	if err := pd.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	e := w.effective.executor
	wc008NativeExec(t, e, `INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id) VALUES(900,'account-stop-asset','hidden.mkv',100,1,'matroska','h264','aac',1920,1080,1,?)`, scope.libraryID)
	wc008NativeExec(t, e, `INSERT INTO playback_sessions(id,session_hash,account_id,profile_id,item_id,asset_id,generation,state,grant_hash,grant_token,expires_at,request_id,duration,mp_execution_id) VALUES('retire-media','hash',?,'hidden-profile',?,'account-stop-asset',2,'playing','account-stop-grant','opaque','2099-01-01T00:00:00Z','source-request',1,'execution')`, scope.accountID, scope.subjectID)
	wc008NativeExec(t, e, `INSERT INTO playback_v1_sessions(id,device_id,account_id,profile_id,authority,start_key,start_digest,kind,role,state,request,revision,generation,media_session_id,media_generation,lease_expires_ms,created_ms,updated_ms) VALUES('logical','hidden-device',?,'hidden-profile','local','key','digest','vod','player','playing','',1,1,'retire-media',2,100,1,1)`, scope.accountID)
	turn := wc008NativeTurn(t, e, 718, mpSessionStopDefinitions())
	a, err := authorizeMPSessionStop(t.Context(), turn, scope, t.Context())
	if err != nil {
		t.Fatal(err)
	}
	q := mpSessionStopInputFixture()
	q.AccountID = scope.accountID
	q.ProfileID = "hidden-profile"
	got, err := applyMPSessionStop(t.Context(), turn, a, q)
	if err != nil || !got.Changed || !got.PhysicalPending {
		t.Fatal(got, err)
	}
	_, _ = turn.transaction.rollback(nil)
	// The same viewer cannot stop a foreign account, even if the caller supplies
	// the exact session/generation tuple; no hidden-title proof is consulted.
	turn = wc008NativeTurn(t, e, 718, mpSessionStopDefinitions())
	a, err = authorizeMPSessionStop(t.Context(), turn, scope, t.Context())
	if err != nil {
		t.Fatal(err)
	}
	q.AccountID = "foreign"
	if got, err = applyMPSessionStop(t.Context(), turn, a, q); got != (mpSessionStopValue{}) || !errors.Is(err, ErrDisclosureDenied) {
		t.Fatal(got, err)
	}
}

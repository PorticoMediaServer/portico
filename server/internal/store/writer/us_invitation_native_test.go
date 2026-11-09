package writer

import (
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const usInvitationSeed = `INSERT INTO access_invitations(id,code_hash,role,allowed_libraries,created_ms,expires_ms,created_by,max_streams_mode,max_streams,allow_remote,access_tokens,max_content_rating,bound_username)
VALUES('us-invite','bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb','member','["library"]',500,2000,'us-owner','limit',3,0,0,'R','recipient');`

// Native source effects/SEARCH Capture only. No issuer authentication, receipt
// replay, admission, FULL COMMIT or performance qualification is claimed.
func TestUSInvitationNative(t *testing.T) {
	w, _ := testCanonicalExecutor(t)
	if _, err := w.db.ExecContext(t.Context(), usInvitationSeed); err != nil {
		t.Fatal(err)
	}
	registry := nativeInventoryFixtureRegistry(t, w, usInvitationDefinitions(), "us_invitation_statements.go", "us_invitation_native_test.go")
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	i, scope := usInvitationPreparationFixture()
	create, err := prepareUSInvitationCreate(i, scope, "us-created", usInvitationFixtureHash, usInvitationCreateInput{})
	if err != nil {
		t.Fatal(err)
	}
	assertUSInvitationEffect(t, conn, registry, create, 1000, true, 1, "pending", 1000+168*usInvitationHourMS)
	var hash, storedLibraries, role, bound string
	if err := conn.QueryRowContext(t.Context(), "SELECT code_hash,allowed_libraries,role,bound_username FROM access_invitations WHERE id='us-created'").Scan(&hash, &storedLibraries, &role, &bound); err != nil || hash != usInvitationFixtureHash || storedLibraries != "[]" || role != "member" || bound != "" {
		t.Fatal("hash-only storage or defaults", hash, storedLibraries, role, bound, err)
	}

	edit := &usInvitationPrepared{kind: usInvitationEdit, id: "us-invite", expectedRevision: 1, role: "admin", librariesJSON: "[]", expiresMS: 3000}
	assertUSInvitationEffect(t, conn, registry, edit, 1000, true, 2, "pending", 3000)
	var mode, rating, boundAfter string
	var streams, remote, tokens int
	if err := conn.QueryRowContext(t.Context(), "SELECT max_streams_mode,max_streams,allow_remote,access_tokens,max_content_rating,bound_username FROM access_invitations WHERE id='us-invite'").Scan(&mode, &streams, &remote, &tokens, &rating, &boundAfter); err != nil || mode != "limit" || streams != 3 || remote != 0 || tokens != 0 || rating != "R" || boundAfter != "recipient" {
		t.Fatal("omitted access/bound recipient changed", mode, streams, remote, tokens, rating, boundAfter, err)
	}
	// Plain-SQL oracle: the stored row, not the prepared bindings, must carry
	// exactly the prepared edit facts.
	var storedRole, editState, editLibraries string
	var storedRevision, editExpiry int64
	if err := conn.QueryRowContext(t.Context(), "SELECT role,allowed_libraries,state,revision,expires_ms FROM access_invitations WHERE id='us-invite'").Scan(&storedRole, &editLibraries, &editState, &storedRevision, &editExpiry); err != nil || storedRole != "admin" || editLibraries != "[]" || editState != "pending" || storedRevision != 2 || editExpiry != 3000 {
		t.Fatal("stored edit facts", storedRole, editLibraries, editState, storedRevision, editExpiry, err)
	}
	// Same expected revision cannot apply twice. Receipt replay belongs to root.
	assertUSInvitationEffect(t, conn, registry, edit, 1000, false, 0, "", 0)
	edit.expectedRevision, edit.replaceAccess = 2, true
	edit.access = usInvitationAccess{mode: "unlimited", allowRemote: true, accessTokens: true}
	assertUSInvitationEffect(t, conn, registry, edit, 1000, true, 3, "pending", 3000)
	if err := conn.QueryRowContext(t.Context(), "SELECT max_streams_mode,max_streams,allow_remote,access_tokens,max_content_rating FROM access_invitations WHERE id='us-invite'").Scan(&mode, &streams, &remote, &tokens, &rating); err != nil || mode != "unlimited" || streams != 0 || remote != 1 || tokens != 1 || rating != "" {
		t.Fatal("replacement access not stored", mode, streams, remote, tokens, rating, err)
	}
	revoke, err := prepareUSInvitationRevoke(i, scope, "us-invite", 3)
	if err != nil {
		t.Fatal(err)
	}
	// Expiration does not prevent an administrator explicitly revoking a still
	// pending record. A distinct operation at the new revoked revision may
	// revoke again; the original expected-revision CAS still cannot replay.
	assertUSInvitationEffect(t, conn, registry, revoke, 4000, true, 4, "revoked", 3000)
	if err := conn.QueryRowContext(t.Context(), "SELECT state,revision,expires_ms FROM access_invitations WHERE id='us-invite'").Scan(&editState, &storedRevision, &editExpiry); err != nil || editState != "revoked" || storedRevision != 4 || editExpiry != 3000 {
		t.Fatal("stored revoke facts", editState, storedRevision, editExpiry, err)
	}
	revoke.expectedRevision = 4
	assertUSInvitationEffect(t, conn, registry, revoke, 4000, true, 5, "revoked", 3000)
}

// Expired, stale and already-decided records are denied by the point CAS. No
// guard writes a row, so callers see one exact no-op rather than a prefix.
func TestUSInvitationNativeGuards(t *testing.T) {
	w, _ := testCanonicalExecutor(t)
	if _, err := w.db.ExecContext(t.Context(), usInvitationSeed); err != nil {
		t.Fatal(err)
	}
	registry := nativeInventoryFixtureRegistry(t, w, usInvitationDefinitions(), "us_invitation_statements.go", "us_invitation_native_test.go")
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for _, tc := range []struct {
		name, state           string
		revision, now, expiry int64
		replace               bool
	}{
		{"stale", "pending", 2, 1000, 3000, false}, {"expired", "pending", 1, 2000, 3000, false},
		{"accepted", "accepted", 1, 1000, 3000, true}, {"revoked", "revoked", 1, 1000, 3000, false},
		{"new_expired", "pending", 1, 1000, 1000, true}, {"new_too_far", "pending", 1, 1000, 1001 + usInvitationMaxSpanMS, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := conn.ExecContext(t.Context(), "UPDATE access_invitations SET state=?,revision=1,expires_ms=2000 WHERE id='us-invite'", tc.state); err != nil {
				t.Fatal(err)
			}
			p := &usInvitationPrepared{kind: usInvitationEdit, id: "us-invite", expectedRevision: tc.revision, role: "member", librariesJSON: "[]", expiresMS: tc.expiry, replaceAccess: tc.replace, access: usInvitationAccess{mode: "inherit"}}
			assertUSInvitationEffect(t, conn, registry, p, tc.now, false, 0, "", 0)
		})
	}
	if _, err := conn.ExecContext(t.Context(), "UPDATE access_invitations SET state='accepted',revision=1,expires_ms=2000 WHERE id='us-invite'"); err != nil {
		t.Fatal(err)
	}
	assertUSInvitationEffect(t, conn, registry, &usInvitationPrepared{kind: usInvitationRevoke, id: "us-invite", expectedRevision: 1}, 1000, false, 0, "", 0)
}

func assertUSInvitationEffect(t *testing.T, conn *sql.Conn, registry *stmt.Registry, p *usInvitationPrepared, now int64, changed bool, wantRev int64, wantState string, wantExpiry int64) {
	t.Helper()
	id, args, err := p.effectBindings(now)
	if err != nil {
		t.Fatal(err)
	}
	statement, err := registry.Lookup(id)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := statement.Bind(args...)
	if err != nil {
		t.Fatal(err)
	}
	var before, after int64
	if err := conn.QueryRowContext(t.Context(), "SELECT total_changes()").Scan(&before); err != nil {
		t.Fatal(err)
	}
	var gotID, state string
	var revision, expires int64
	err = bound.QueryRowContext(t.Context(), conn).Scan(&gotID, &revision, &state, &expires)
	if changed {
		if err != nil || gotID != p.id || revision != wantRev || state != wantState || expires != wantExpiry {
			t.Fatal("effect result", gotID, revision, state, expires, err)
		}
	} else if err != sql.ErrNoRows {
		t.Fatal("guard failed", id, err)
	}
	if err := conn.QueryRowContext(t.Context(), "SELECT total_changes()").Scan(&after); err != nil {
		t.Fatal(err)
	}
	wantChanges := int64(0)
	if changed {
		wantChanges = 1
	}
	if after-before != wantChanges {
		t.Fatal("source effect count", after-before, wantChanges)
	}
}

// Three executable mutants establish that the real negative controls detect
// weakened revision, pending-state and strict expiry predicates.
func TestUSInvitationGuardMutants(t *testing.T) {
	w, _ := testCanonicalExecutor(t)
	if _, err := w.db.ExecContext(t.Context(), usInvitationSeed); err != nil {
		t.Fatal(err)
	}
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for _, tc := range []struct {
		name, from, to, state string
		revision, now         int64
	}{
		{"revision", "revision=?", "revision>=?", "pending", 1, 1000},
		{"state", "state='pending'", "state IN('pending','accepted')", "accepted", 2, 1000},
		{"expiry", "expires_ms>?", "expires_ms>=?", "pending", 2, 2000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := conn.ExecContext(t.Context(), "UPDATE access_invitations SET state=?,revision=2,expires_ms=2000 WHERE id='us-invite'", tc.state); err != nil {
				t.Fatal(err)
			}
			def := usInvitationEffectDefinitions()[1]
			def.SQL = strings.Replace(def.SQL, tc.from, tc.to, 1)
			registry := nativeFixtureRegistry(t, conn, w.config, []stmt.Definition{def}, "us_invitation_native_test.go")
			p := &usInvitationPrepared{kind: usInvitationEdit, id: "us-invite", expectedRevision: tc.revision, role: "member", librariesJSON: "[]", expiresMS: 3000}
			assertUSInvitationEffect(t, conn, registry, p, tc.now, true, 3, tc.state, 3000)
		})
	}
}

// The prepared sign-in-kind revision is current-frame evidence. Kind or
// revision drift is a disclosure denial, never a stale-witness success.
func TestUSInvitationKindDrift(t *testing.T) {
	w := wc008NativeExecutor(t)
	wc008NativeExec(t, w, `INSERT INTO identity_state(singleton,sign_in_kind,revision) VALUES(1,'local',1)`)
	defs := usInvitationDefinitions()
	want, err := prepareUSInvitationSignInKind("local", 1)
	if err != nil {
		t.Fatal(err)
	}
	turn := wc008NativeTurn(t, w, usAPICredentialIssue, defs)
	if err := checkUSInvitationFreshKind(t.Context(), turn, want); err != nil {
		t.Fatal("current kind denied", err)
	}
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	// Kind and revision each independently invalidate the captured witness.
	wc008NativeExec(t, w, `UPDATE identity_state SET sign_in_kind='portico',revision=2`)
	turn = wc008NativeTurn(t, w, usAPICredentialIssue, defs)
	if err := checkUSInvitationFreshKind(t.Context(), turn, want); !errors.Is(err, ErrDisclosureDenied) {
		t.Fatal("drifted kind accepted", err)
	}
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	wc008NativeExec(t, w, `UPDATE identity_state SET sign_in_kind='local',revision=3`)
	next, err := prepareUSInvitationSignInKind("local", 3)
	if err != nil {
		t.Fatal(err)
	}
	turn = wc008NativeTurn(t, w, usAPICredentialIssue, defs)
	if err := checkUSInvitationFreshKind(t.Context(), turn, next); err != nil {
		t.Fatal("current revision denied", err)
	}
	if err := checkUSInvitationFreshKind(t.Context(), turn, usInvitationSignInKind{kind: "local", revision: 1}); !errors.Is(err, ErrDisclosureDenied) {
		t.Fatal("stale revision accepted", err)
	}
	if err := checkUSInvitationFreshKind(t.Context(), turn, usInvitationSignInKind{}); !errors.Is(err, ErrInvalid) {
		t.Fatal("malformed witness accepted", err)
	}
}

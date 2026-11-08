package writer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func usFamilyBirthFixture(t *testing.T) (*executor, usFamilyBirthInput) {
	t.Helper()
	w := wc008NativeExecutor(t)
	in := usFamilyBirthInput{family: "new-family", server: "server", account: "account", profile: "profile", member: strings.Repeat("a", 32), lifetime: strings.Repeat("b", 32), device: "device", installationID: "installation-synthetic-000000000001", installationIncarnation: strings.Repeat("c", 32), lineage: "lineage", horizon: w.now().Add(24 * time.Hour).UTC().Truncate(time.Second).Format(time.RFC3339), epoch: 1, authorityGeneration: 1, incarnation: [16]byte{1}}
	h := sha256.Sum256([]byte(in.installationID))
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO configuration(key,value) VALUES('id','server')`, nil},
		{`INSERT INTO accounts(id,username,password_hash,profile_id) VALUES('account','account',X'','profile')`, nil},
		{`INSERT INTO direct_memberships(account_id,incarnation,role) VALUES('account',?,'member')`, []any{in.member}},
		{`INSERT INTO direct_profiles(id,account_id,lifetime,name,is_primary) VALUES('profile','account',?,'Profile',1)`, []any{in.lifetime}},
		{`INSERT INTO identity_state(singleton,sign_in_kind) VALUES(1,'local')`, nil},
		{`INSERT INTO identity_installations(installation_hash,incarnation) VALUES(?,?)`, []any{hex.EncodeToString(h[:]), in.installationIncarnation}},
		{`INSERT INTO identity_devices(id,account_id,installation_id,name,platform,app,app_version,first_seen,last_seen,retired_ms) VALUES('device','account',?,'Device','test','test','1','','',0)`, []any{in.installationID}},
		{`INSERT INTO identity_pin_lineages(id,account_id,origin_device_id,created_at) VALUES('lineage','account','device','')`, nil},
	} {
		wc008NativeExec(t, w, q.sql, q.args...)
	}
	for n, scope := range []string{"device", "profile_pin", "profile_remember", "profile_lineage"} {
		profile, lifetime, device, lineage := in.profile, in.lifetime, "", ""
		if scope == "device" {
			profile, lifetime, device = "", "", in.device
		}
		if scope == "profile_lineage" {
			lineage = in.lineage
		}
		wc008NativeExec(t, w, `INSERT INTO identity_security_fences(id,scope_kind,account_id,member_incarnation,profile_id,profile_lifetime,device_id,lineage_id,updated_ms) VALUES(?,?,'account',?,?,?,?,?,0)`, n+1, scope, in.member, profile, lifetime, device, lineage)
	}
	return w, in
}
func usFamilyBirthCredentialRow(c *usCredentialIssuePrepared) (string, []any) {
	return `INSERT INTO api_credentials(id,kind,account_id,profile_id,name,scopes,secret_hash,hint,family_id,device_id,created_ms,expires_ms) VALUES(?,?,?,?,?,?,?,?,?,?,1,?)`, []any{c.id, c.kind, c.target.account, c.target.profile, c.name, c.scopes, c.secretHash, c.hint, c.family, c.device, c.expiresMS}
}
func usFamilyBirthCredentialFixture(t *testing.T) (*executor, usFamilyBirthInput, *usCredentialIssuePrepared) {
	t.Helper()
	w, in := usFamilyBirthFixture(t)
	in.device = "credential-device"
	in.installationID = ""
	in.installationIncarnation = ""
	target := usCredentialTarget{account: in.account, memberIncarnation: in.member, profile: in.profile, profileLifetime: in.lifetime, username: "account", profileName: "Profile", epoch: in.epoch, memberRevision: 1, profileRevision: 1}
	c, err := prepareUSCredentialIssue(usCredentialIssueRequest{target: target, id: strings.Repeat("c", 22), kind: "key", name: "Key", scopes: []string{"read"}, secretHash: usCredentialFixtureHash("birth-key"), hint: "abcdef", family: in.family, device: in.device}, w.now().UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	q, args := usFamilyBirthCredentialRow(c)
	wc008NativeExec(t, w, q, args...)
	wc008NativeExec(t, w, `UPDATE identity_security_fences SET device_id='credential-device' WHERE scope_kind='device'`)
	// Credential devices have a synthetic installation name, not an interactive
	// identity_installations record. The maker's origin lineage is inherited.
	wc008NativeExec(t, w, `DELETE FROM identity_installations`)
	return w, in, c
}
func usFamilyBirthTurn(t *testing.T, w *executor, credential bool) *commandTurn {
	t.Helper()
	turn := wc008NativeTurn(t, w, usAPICredentialIssue, usFamilyBirthDefinitions())
	if !credential {
		turn.transaction.descriptor.ID = 510
	} // unqualified, unregistered
	return turn
}
func TestUSFamilyBirthKindsAndCurrentFences(t *testing.T) {
	for _, credential := range []bool{false, true} {
		t.Run(fmt.Sprint(credential), func(t *testing.T) {
			var w *executor
			var in usFamilyBirthInput
			var c *usCredentialIssuePrepared
			var p *usFamilyBirthPrepared
			var err error
			if credential {
				w, in, c = usFamilyBirthCredentialFixture(t)
				p, err = prepareUSCredentialFamilyBirth(in, c)
			} else {
				w, in = usFamilyBirthFixture(t)
				p, err = prepareUSOrdinaryFamilyBirth(in)
			}
			if err != nil {
				t.Fatal(err)
			}
			wc008NativeExec(t, w, `UPDATE identity_security_fences SET generation=7`)
			turn := usFamilyBirthTurn(t, w, credential)
			out, err := applyUSFamilyBirth(t.Context(), turn, p, credential, c)
			if err != nil || out.controlBytes <= 0 {
				t.Fatal(out, err)
			}
			if _, err = turn.transaction.commit(); err != nil {
				t.Fatal(err)
			}
			var kind, count, gate int
			if err = w.db.QueryRowContext(t.Context(), `SELECT security_kind,pin_gate FROM authorization_session_families WHERE id='new-family'`).Scan(&kind, &gate); err != nil {
				t.Fatal(err)
			}
			want := 0
			if credential {
				want = 1
			}
			if kind != want || gate != 0 {
				t.Fatal(kind, gate)
			}
			if err = w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM authorization_family_security_fences WHERE family_id='new-family' AND confirmed_generation=7`).Scan(&count); err != nil || count != 4 {
				t.Fatal(count, err)
			}
			if credential {
				var platform, installation string
				var retired, remember int
				if err = w.db.QueryRowContext(t.Context(), `SELECT platform,installation_id,retired_ms,remember_account FROM identity_devices WHERE id=?`, c.device).Scan(&platform, &installation, &retired, &remember); err != nil || platform != "api" || installation != "api-credential-"+c.id || retired != 0 || remember != 0 {
					t.Fatal(platform, installation, retired, remember, err)
				}
				wc008NativeExec(t, w, `DELETE FROM api_credentials`)
				if err = w.db.QueryRowContext(t.Context(), `SELECT security_kind FROM authorization_session_families WHERE id='new-family'`).Scan(&kind); err != nil || kind != 1 {
					t.Fatal(kind, err)
				}
			}
		})
	}
}
func TestUSFamilyBirthCurrentSourceDenials(t *testing.T) {
	for name, q := range map[string]string{
		"device_retired":       `UPDATE identity_devices SET retired_ms=1`,
		"installation_retired": `UPDATE identity_installations SET retired=1`,
		"member_disabled":      `UPDATE direct_memberships SET disabled=1`,
		"member_incarnation":   `UPDATE direct_memberships SET incarnation='dddddddddddddddddddddddddddddddd'`,
		"profile_lifetime":     `UPDATE direct_profiles SET lifetime='dddddddddddddddddddddddddddddddd'`,
		"epoch":                `UPDATE accounts SET epoch=2`, "authority": `UPDATE identity_state SET authority_generation=2`,
		"server":        `UPDATE configuration SET value='other' WHERE key='id'`,
		"fence_denied":  `UPDATE identity_security_fences SET denied=1 WHERE scope_kind='device'`,
		"missing_fence": `DELETE FROM identity_security_fences WHERE scope_kind='profile_pin'`,
		"must_change":   `UPDATE accounts SET must_change=1`, "profile_deleted": `UPDATE direct_profiles SET deleted=1`,
		"waiting_move":      `INSERT INTO identity_move_waiting(account_id,move_id,state,created_ms) VALUES('account','move','waiting',0)`,
		"lineage_account":   `INSERT INTO accounts(id,username,password_hash,profile_id) VALUES('other','other',X'','other-profile'); UPDATE identity_pin_lineages SET account_id='other'`,
		"lineage_origin":    `UPDATE identity_pin_lineages SET origin_device_id='other-device'`,
		"credential_marker": `INSERT INTO api_credentials(id,kind,account_id,profile_id,name,scopes,secret_hash,hint,family_id,device_id,created_ms) VALUES('marker','key','account','profile','Key','read','hash','hint','new-family','device',1)`,
	} {
		t.Run(name, func(t *testing.T) {
			w, in := usFamilyBirthFixture(t)
			wc008NativeExec(t, w, q)
			p, err := prepareUSOrdinaryFamilyBirth(in)
			if err != nil {
				t.Fatal(err)
			}
			turn := usFamilyBirthTurn(t, w, false)
			out, err := applyUSOrdinaryFamilyBirth(t.Context(), turn, p)
			if out != (usFamilyBirthEffect{}) || !errors.Is(err, ErrDisclosureDenied) {
				t.Fatal(out, err)
			}
		})
	}
}
func TestUSFamilyBirthCredentialBinding(t *testing.T) {
	for _, mode := range []string{"family", "device", "account", "member", "profile", "lifetime", "epoch"} {
		t.Run(mode, func(t *testing.T) {
			w, in, c := usFamilyBirthCredentialFixture(t)
			p, err := prepareUSCredentialFamilyBirth(in, c)
			if err != nil {
				t.Fatal(err)
			}
			other := *c
			switch mode {
			case "family":
				other.family = "wrong-family"
			case "device":
				other.device = "wrong-device"
			case "account":
				other.target.account = "wrong-account"
			case "member":
				other.target.memberIncarnation = strings.Repeat("d", 32)
			case "profile":
				other.target.profile = "wrong-profile"
			case "lifetime":
				other.target.profileLifetime = strings.Repeat("d", 32)
			case "epoch":
				other.target.epoch++
			}
			if got, e := prepareUSCredentialFamilyBirth(in, &other); got != nil || !errors.Is(e, ErrInvalid) {
				t.Fatal(got, e)
			}
			turn := usFamilyBirthTurn(t, w, true)
			if out, e := applyUSCredentialFamilyBirth(t.Context(), turn, p, &other); out != (usFamilyBirthEffect{}) || !errors.Is(e, ErrInvalid) {
				t.Fatal(out, e)
			}
		})
	}
}
func TestUSFamilyBirthCredentialMarkerAndFreshDevice(t *testing.T) {
	for _, mode := range []string{"missing_marker", "marker_family", "marker_device", "maker_device", "lineage_account", "ordinary_prepared"} {
		t.Run(mode, func(t *testing.T) {
			w, in, c := usFamilyBirthCredentialFixture(t)
			switch mode {
			case "missing_marker":
				wc008NativeExec(t, w, `DELETE FROM api_credentials`)
			case "marker_family":
				wc008NativeExec(t, w, `UPDATE api_credentials SET family_id='wrong-family'`)
			case "marker_device":
				wc008NativeExec(t, w, `UPDATE api_credentials SET device_id='wrong-device'`)
			case "maker_device":
				in.device = "device"
				c.device = "device"
				wc008NativeExec(t, w, `UPDATE api_credentials SET device_id='device'; UPDATE identity_security_fences SET device_id='device' WHERE scope_kind='device'`)
			case "lineage_account":
				wc008NativeExec(t, w, `INSERT INTO accounts(id,username,password_hash,profile_id) VALUES('other','other',X'','other-profile'); UPDATE identity_pin_lineages SET account_id='other'`)
			}
			p, err := prepareUSCredentialFamilyBirth(in, c)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "ordinary_prepared" {
				ordinary := in
				ordinary.installationID = "installation-synthetic-000000000001"
				ordinary.installationIncarnation = strings.Repeat("c", 32)
				p, err = prepareUSOrdinaryFamilyBirth(ordinary)
				if err != nil {
					t.Fatal(err)
				}
			}
			turn := usFamilyBirthTurn(t, w, true)
			if out, e := applyUSCredentialFamilyBirth(t.Context(), turn, p, c); e == nil || out != (usFamilyBirthEffect{}) {
				t.Fatal(out, e)
			}
		})
	}
}
func TestUSFamilyBirthRollbackAndPINGate(t *testing.T) {
	for _, credential := range []bool{false, true} {
		t.Run(fmt.Sprint(credential), func(t *testing.T) {
			var w *executor
			var in usFamilyBirthInput
			var c *usCredentialIssuePrepared
			var p *usFamilyBirthPrepared
			var err error
			if credential {
				w, in, c = usFamilyBirthCredentialFixture(t)
				p, err = prepareUSCredentialFamilyBirth(in, c)
			} else {
				w, in = usFamilyBirthFixture(t)
				p, err = prepareUSOrdinaryFamilyBirth(in)
			}
			if err != nil {
				t.Fatal(err)
			}
			wc008NativeExec(t, w, `UPDATE direct_profiles SET pin_hash=X'01'`)
			turn := usFamilyBirthTurn(t, w, credential)
			if out, e := applyUSFamilyBirth(t.Context(), turn, p, credential, c); e != nil || out.pinGate != 1 {
				t.Fatal(out, e)
			}
			if _, err = turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}
			turn = usFamilyBirthTurn(t, w, credential)
			delete(turn.steps, usFamilyBirthWitness)
			if out, e := applyUSFamilyBirth(t.Context(), turn, p, credential, c); e == nil || out != (usFamilyBirthEffect{}) {
				t.Fatal(out, e)
			}
			// Even if the parent ignores the effect error, COMMIT must fail closed.
			if _, e := turn.transaction.commit(); e == nil {
				t.Fatal("ignored error committed a partial family")
			}
			var count int
			if err = w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM authorization_session_families`).Scan(&count); err != nil || count != 0 {
				t.Fatal(count, err)
			}
			if credential {
				if err = w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM identity_devices WHERE id='credential-device'`).Scan(&count); err != nil || count != 0 {
					t.Fatal("API device survived rollback", count, err)
				}
			}
		})
	}
}
func TestUSFamilyBirthCredentialDevicePrefixRollback(t *testing.T) {
	w, in, c := usFamilyBirthCredentialFixture(t)
	p, err := prepareUSCredentialFamilyBirth(in, c)
	if err != nil {
		t.Fatal(err)
	}
	wc008NativeExec(t, w, `DELETE FROM api_credentials`)
	turn := usFamilyBirthTurn(t, w, true)
	q, args := usFamilyBirthCredentialRow(c)
	if _, err = turn.transaction.conn.ExecContext(t.Context(), q, args...); err != nil {
		t.Fatal(err)
	}
	delete(turn.steps, usFamilyBirthCredential) // device inserted; family step fails
	if out, e := applyUSCredentialFamilyBirth(t.Context(), turn, p, c); e == nil || out != (usFamilyBirthEffect{}) {
		t.Fatal(out, e)
	}
	if _, e := turn.transaction.commit(); e == nil {
		t.Fatal("ignored device-prefix error committed")
	}
	for _, q := range []string{`SELECT count(*) FROM api_credentials`, `SELECT count(*) FROM identity_devices WHERE id='credential-device'`, `SELECT count(*) FROM authorization_session_families`} {
		var count int
		if e := w.db.QueryRowContext(t.Context(), q).Scan(&count); e != nil || count != 0 {
			t.Fatal(q, count, e)
		}
	}
}
func TestUSFamilyBirthCanonicalPreparation(t *testing.T) {
	w, in := usFamilyBirthFixture(t)
	_ = w
	for _, bad := range []string{"2027-01-01T00:00:00.1Z", "2027-01-01T00:00:00+14:00"} {
		copy := in
		copy.horizon = bad
		if p, e := prepareUSOrdinaryFamilyBirth(copy); p != nil || !errors.Is(e, ErrInvalid) {
			t.Fatal(p, e)
		}
	}
	in.installationID = "short"
	if p, e := prepareUSOrdinaryFamilyBirth(in); p != nil || !errors.Is(e, ErrInvalid) {
		t.Fatal(p, e)
	}
}
func TestUSFamilyBirthClosedPurposeAndCancellation(t *testing.T) {
	w, in := usFamilyBirthFixture(t)
	p, err := prepareUSOrdinaryFamilyBirth(in)
	if err != nil {
		t.Fatal(err)
	}
	turn := usFamilyBirthTurn(t, w, false)
	turn.transaction.descriptor.ID = USMemberLibraries
	if out, e := applyUSOrdinaryFamilyBirth(t.Context(), turn, p); e == nil || out != (usFamilyBirthEffect{}) {
		t.Fatal(out, e)
	}
	turn.transaction.descriptor.ID = 510
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if out, e := applyUSOrdinaryFamilyBirth(canceled, turn, p); e == nil || out != (usFamilyBirthEffect{}) {
		t.Fatal(out, e)
	}
}
func TestUSFamilyBirthNativePrepareAndRequiredKind(t *testing.T) {
	w, in := usFamilyBirthFixture(t)
	wc008NativePrepare(t, w, usFamilyBirthDefinitions())
	for _, kind := range []string{"omitted", "NULL", "2"} {
		q := `INSERT INTO authorization_session_families(id,server_id,account_id,profile_id,authority,role,epoch,authorization_horizon,current_generation,command_member_incarnation,command_profile_lifetime,command_incarnation`
		values := `) VALUES('bad','server','account','profile','local','member',1,'horizon',1,?,?,?)`
		if kind != "omitted" {
			q += `,security_kind`
			values = `) VALUES('bad','server','account','profile','local','member',1,'horizon',1,?,?,?,` + kind + `)`
		}
		if _, e := w.db.ExecContext(t.Context(), q+values, in.member, in.lifetime, in.incarnation[:]); e == nil {
			t.Fatal("accepted", kind)
		}
	}
}
func TestUSFamilyBirthFullFrameGuards(t *testing.T) {
	w, in, c := usFamilyBirthCredentialFixture(t)
	p, err := prepareUSCredentialFamilyBirth(in, c)
	if err != nil {
		t.Fatal(err)
	}
	turn := usFamilyBirthTurn(t, w, true)
	tx := turn.transaction
	for _, which := range []string{"not_begun", "done", "policy", "durability", "synchronous"} {
		begun, done, verified, durability, synchronous := tx.begun, tx.done, tx.policyVerified, tx.descriptor.Durability, tx.settings.Synchronous
		switch which {
		case "not_begun":
			tx.begun = false
		case "done":
			tx.done = true
		case "policy":
			tx.policyVerified = false
		case "durability":
			tx.descriptor.Durability = Normal
		case "synchronous":
			tx.settings.Synchronous = 1
		}
		out, e := applyUSCredentialFamilyBirth(t.Context(), turn, p, c)
		tx.begun, tx.done, tx.policyVerified, tx.descriptor.Durability, tx.settings.Synchronous = begun, done, verified, durability, synchronous
		if out != (usFamilyBirthEffect{}) || !errors.Is(e, ErrInvalid) {
			t.Fatal(which, out, e)
		}
	}
}

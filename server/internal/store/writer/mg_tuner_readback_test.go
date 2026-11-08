package writer

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

// The US owner authority population plus the immutable MG source/channel head,
// built through a decorated driver. A cell can then observe or perturb only the
// exact readback boundary; this fixture still asserts no authority of its own.
func mgTunerAuthorityExecutor(t *testing.T, decorate func(driver.Conn) driver.Conn, kind mgActorKind) (*executor, *mgActorProof) {
	t.Helper()
	w, _, _ := testExecutor(t, decorate)
	now := w.now().UTC().Truncate(time.Second)
	_, request := usPolicyFixture()
	witness := usOwnerAuthorityWitness{issuer: request.issuer, device: "issuer-device", lineage: "issuer-lineage", installationID: "issuer-installation-synthetic-000001", tokenExpiry: now.Add(10 * time.Minute).Format(time.RFC3339), horizon: now.Add(time.Hour).Format(time.RFC3339)}
	proof, err := prepareUSOwnerAuthority(witness)
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO configuration(key,value) VALUES('id','server') ON CONFLICT(key) DO UPDATE SET value=excluded.value`, nil},
		{`INSERT INTO accounts(id,username,password_hash,profile_id) VALUES('issuer','issuer',X'','issuer-profile')`, nil},
		{`INSERT INTO direct_memberships(account_id,incarnation,role) VALUES('issuer',?,'owner')`, []any{witness.issuer.memberIncarnation}},
		{`INSERT INTO direct_profiles(id,account_id,name,is_primary,lifetime) VALUES('issuer-profile','issuer','Issuer',1,?)`, []any{witness.issuer.profileLifetime}},
		{`INSERT INTO identity_installations(installation_hash,incarnation) VALUES(?,?)`, []any{proof.installationHash, witness.issuer.installation}},
		{`INSERT INTO identity_devices(id,account_id,installation_id,name,platform,app,app_version,first_seen,last_seen) VALUES('issuer-device','issuer',?,'Issuer','test','test','1','','')`, []any{witness.installationID}},
		{`INSERT INTO identity_pin_lineages(id,account_id,origin_device_id,created_at) VALUES('issuer-lineage','issuer','issuer-device','')`, nil},
		{`INSERT INTO identity_state(singleton,sign_in_kind,authority_generation) VALUES(1,'local',1) ON CONFLICT(singleton) DO UPDATE SET sign_in_kind='local',authority_generation=1`, nil},
		{`INSERT INTO authorization_session_families(security_kind,id,server_id,account_id,profile_id,authority,role,epoch,authorization_horizon,current_generation,device_id,pin_lineage_id,command_member_incarnation,command_profile_lifetime,command_incarnation) VALUES(0,'issuer-family','server','issuer','issuer-profile','local','owner',1,?,1,'issuer-device','issuer-lineage',?,?,?)`, []any{witness.horizon, witness.issuer.memberIncarnation, witness.issuer.profileLifetime, []byte{9, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}}},
		{`INSERT INTO authorization_family_tokens(token_hash,family_id,generation,expires_at) VALUES(?,'issuer-family',1,?)`, []any{witness.issuer.tokenDigest, witness.tokenExpiry}},
		{`INSERT INTO identity_security_fences(id,scope_kind,account_id,member_incarnation,device_id,updated_ms) VALUES(1,'device','issuer',?,'issuer-device',0)`, []any{witness.issuer.memberIncarnation}},
		{`INSERT INTO authorization_family_security_fences(family_id,scope_kind,fence_id,confirmed_generation) VALUES('issuer-family','device',1,1)`, nil},
		{mgSourceFixtureSQL, nil},
		{mgTunerFixtureSQL, nil},
	} {
		if _, err = w.db.ExecContext(t.Context(), query.sql, query.args...); err != nil {
			t.Fatal(query.sql, err)
		}
	}
	switch kind {
	case mgActorViewer:
		if _, err := w.db.ExecContext(t.Context(), `UPDATE authorization_session_families SET role='member'; UPDATE live_source_identities SET settings_viewer_access='server-members'`); err != nil {
			t.Fatal(err)
		}
	case mgActorAdminViewer:
		if _, err := w.db.ExecContext(t.Context(), `UPDATE authorization_session_families SET role='admin'; UPDATE direct_memberships SET role='admin'; UPDATE live_source_identities SET settings_viewer_access='server-members'`); err != nil {
			t.Fatal(err)
		}
	}
	return w, &mgActorProof{native: *proof, kind: kind}
}

const mgTunerReadSQLMarker = "FROM live_allocations INDEXED BY sqlite_autoindex_live_allocations_1 WHERE id=?"

// Revokes the current family from the same connection immediately before the
// Nth exact allocation readback, proving the release still rechecks authority.
type mgTunerRevokeConnection struct {
	writerDriverConn
	at    int
	reads int
	armed bool
}

func (c *mgTunerRevokeConnection) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	if c.armed && strings.Contains(q, mgTunerReadSQLMarker) {
		c.reads++
		if c.reads == c.at {
			if _, err := c.writerDriverConn.ExecContext(ctx, `UPDATE authorization_session_families SET revoked=1 WHERE id='issuer-family'`, nil); err != nil {
				return nil, err
			}
		}
	}
	return c.writerDriverConn.QueryContext(ctx, q, args)
}

// Returns one already-native row with a single value perturbed, so an
// error-only fault cannot stand in for the value-equality guards. The write and
// readback selectors can be armed after the acquire phase commits.
type mgTunerMismatchConnection struct {
	writerDriverConn
	write  string // "insert", "update" or ""
	readAt int    // 1-based exact point readback to perturb, 0 for none
	reads  int
	hit    *bool
}

func (c *mgTunerMismatchConnection) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	rows, err := c.writerDriverConn.QueryContext(ctx, q, args)
	if err != nil {
		return rows, err
	}
	corrupt := c.write == "insert" && strings.HasPrefix(q, "INSERT INTO live_allocations(") ||
		c.write == "update" && strings.HasPrefix(q, "UPDATE live_allocations INDEXED BY sqlite_autoindex_live_allocations_1 SET lease_until_ms=")
	if strings.Contains(q, mgTunerReadSQLMarker) {
		c.reads++
		corrupt = corrupt || c.readAt != 0 && c.reads == c.readAt
	}
	if corrupt {
		return &mgTunerMismatchRows{Rows: rows, hit: c.hit}, nil
	}
	return rows, nil
}

type mgTunerMismatchRows struct {
	driver.Rows
	hit *bool
}

func (r *mgTunerMismatchRows) Next(values []driver.Value) error {
	if err := r.Rows.Next(values); err != nil {
		return err
	}
	// lease_until_ms is the one field a live allocation may advance.
	value, ok := values[11].(int64)
	if !ok {
		return fmt.Errorf("native lease has type %T", values[11])
	}
	values[11] = value + 1
	*r.hit = true
	return nil
}

func TestMGTunerNativeRevokeBeforeRelease(t *testing.T) {
	for _, cell := range []string{"acquire", "renew"} {
		t.Run(cell, func(t *testing.T) {
			revoke := &mgTunerRevokeConnection{at: 1, armed: cell == "acquire"}
			e, p := mgTunerAuthorityExecutor(t, func(c driver.Conn) driver.Conn {
				revoke.writerDriverConn = c.(writerDriverConn)
				return revoke
			}, mgActorOwner)
			r := mgTunerRequestAt(t, e)
			if cell == "acquire" {
				_, _, a := mgTunerFixtureWith(t, e, p)
				if out, err := acquireMGTuner(t.Context(), a, r); !errors.Is(err, ErrDisclosureDenied) || out != (mgTunerAllocation{}) {
					t.Fatal("revoked acquire released a result", out, err)
				}
				if !revoke.revoked() {
					t.Fatal("control missed the readback boundary")
				}
				return
			}
			turn, a := mgTunerSession(t, e, p, MGAcquireAllocation)
			if got, err := acquireMGTuner(t.Context(), a, r); err != nil || got != r.allocation {
				t.Fatal(got, err)
			}
			mgTunerCommit(t, turn)
			revoke.armed = true
			renewTurn, renew := mgTunerSession(t, e, p, MGRenewAllocation)
			before := r.allocation
			r.allocation.lease += 1000
			if out, err := renewMGTuner(t.Context(), renew, before, r); !errors.Is(err, ErrDisclosureDenied) || out != (mgTunerAllocation{}) {
				t.Fatal("revoked renew released a result", out, err)
			}
			_ = renewTurn
			if !revoke.revoked() {
				t.Fatal("control missed the readback boundary")
			}
		})
	}
}

func (c *mgTunerRevokeConnection) revoked() bool { return c.reads >= c.at }

func TestMGTunerNativeReadbackValueMismatch(t *testing.T) {
	for _, cell := range []string{"insert-returning", "point-read", "renew-returning", "renew-point-read"} {
		t.Run(cell, func(t *testing.T) {
			hit := false
			mismatch := &mgTunerMismatchConnection{hit: &hit}
			switch cell {
			case "insert-returning":
				mismatch.write = "insert"
			case "point-read":
				mismatch.readAt = 1
			}
			e, p := mgTunerAuthorityExecutor(t, func(c driver.Conn) driver.Conn {
				mismatch.writerDriverConn = c.(writerDriverConn)
				return mismatch
			}, mgActorOwner)
			r := mgTunerRequestAt(t, e)
			if cell != "renew-returning" && cell != "renew-point-read" {
				turn, a := mgTunerSession(t, e, p, MGAcquireAllocation)
				if out, err := acquireMGTuner(t.Context(), a, r); !errors.Is(err, ErrConflict) || out != (mgTunerAllocation{}) {
					t.Fatal("mismatched native value released", out, err)
				}
				if !hit {
					t.Fatal("control missed the readback")
				}
				if err := turn.closeRows(); err != nil {
					t.Fatal(err)
				}
				if _, err := turn.transaction.rollback(nil); err != nil {
					t.Fatal(err)
				}
				var n int64
				if err := e.db.QueryRowContext(t.Context(), `SELECT count(*) FROM live_allocations`).Scan(&n); err != nil || n != 0 {
					t.Fatal("mismatched allocation escaped rollback", n, err)
				}
				return
			}
			// Arm the corruption only for the renewal so the acquire commits honestly.
			turn, a := mgTunerSession(t, e, p, MGAcquireAllocation)
			if got, err := acquireMGTuner(t.Context(), a, r); err != nil || got != r.allocation {
				t.Fatal(got, err)
			}
			mgTunerCommit(t, turn)
			if cell == "renew-returning" {
				mismatch.write = "update"
			} else {
				mismatch.reads, mismatch.readAt = 0, 1
			}
			renewTurn, renew := mgTunerSession(t, e, p, MGRenewAllocation)
			before := r.allocation
			r.allocation.lease += 1000
			if out, err := renewMGTuner(t.Context(), renew, before, r); !errors.Is(err, ErrConflict) || out != (mgTunerAllocation{}) {
				t.Fatal("mismatched renewal released", out, err)
			}
			if !hit {
				t.Fatal("control missed the renewal readback")
			}
			if err := renewTurn.closeRows(); err != nil {
				t.Fatal(err)
			}
			if _, err := renewTurn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}
			var lease int64
			if err := e.db.QueryRowContext(t.Context(), `SELECT lease_until_ms FROM live_allocations WHERE id='alloc'`).Scan(&lease); err != nil || lease != before.lease {
				t.Fatal("mismatched lease escaped rollback", lease, err)
			}
		})
	}
}

func TestMGTunerNativeSourceChannelFences(t *testing.T) {
	for _, cell := range []string{"disabled", "removed", "generation", "channel"} {
		t.Run(cell, func(t *testing.T) {
			_, turn, _ := mgTunerFixture(t, MGAcquireAllocation, mgActorOwner)
			if cell == "disabled" {
				if _, err := turn.transaction.conn.ExecContext(t.Context(), `UPDATE live_sources SET state='disabled' WHERE id='source'`); err != nil {
					t.Fatal(err)
				}
			}
			if cell == "removed" {
				if _, err := turn.transaction.conn.ExecContext(t.Context(), `UPDATE live_source_identities SET has_live_source_removals=1,removal_request_id='gone',removal_revision=8,removal_removed_at='now' WHERE id='source'`); err != nil {
					t.Fatal(err)
				}
			}
			guide := "old"
			if cell == "generation" {
				guide = "new"
			}
			channel := "channel"
			if cell == "channel" {
				channel = "elsewhere"
			}
			if _, err := mgTunerSourceChannel(t.Context(), turn, guide, "source", channel); !errors.Is(err, ErrConflict) {
				t.Fatal("source/channel fence released", err)
			}
		})
	}
}

func TestMGTunerNativePurposeAndDurability(t *testing.T) {
	e, turn, a := mgTunerFixture(t, MGAcquireAllocation, mgActorOwner)
	r := mgTunerRequestAt(t, e)
	turn.transaction.descriptor.Durability = Full
	if out, err := acquireMGTuner(t.Context(), a, r); !errors.Is(err, ErrInvalid) || out != (mgTunerAllocation{}) {
		t.Fatal("mislabeled durability", out, err)
	}
	turn.transaction.descriptor.Durability = Normal
	turn.transaction.settings.Synchronous = 0
	if out, err := acquireMGTuner(t.Context(), a, r); !errors.Is(err, ErrInvalid) || out != (mgTunerAllocation{}) {
		t.Fatal("wrong native synchronous", out, err)
	}
	turn.transaction.settings.Synchronous = int(sqliteconn.Normal)
	if _, err := renewMGTuner(t.Context(), a, mgTunerAllocation{}, r); !errors.Is(err, ErrInvalid) {
		t.Fatal("acquire frame renewed", err)
	}
	if _, err := mgTunerTime(t.Context(), turn); err != nil {
		t.Fatal("native clock", err)
	}
	turn.transaction.descriptor.ID = MGRenewAllocation
	if _, err := mgTunerTime(t.Context(), turn); err != nil {
		t.Fatal("renew native clock", err)
	}
}

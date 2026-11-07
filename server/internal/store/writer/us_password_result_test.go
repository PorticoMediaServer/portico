package writer

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

func usResultTestIdentity() (CommandIdentity, RequestDigest) {
	return CommandIdentity{Incarnation: [16]byte{1}, OperationID: [16]byte{2}, Sequence: 1}, RequestDigest{3}
}
func usPasswordResultFixture() usPasswordSavedResult {
	return usPasswordSavedResult{owner: "owner", account: "account", memberIncarnation: strings.Repeat("a", 32), profile: "profile", profileLifetime: strings.Repeat("b", 32), memberRevision: math.MaxInt64, profileRevision: math.MaxInt64, previousEpoch: math.MaxInt64 - 1, resultEpoch: math.MaxInt64, appliedMS: 1}
}
func TestUSPasswordExactResult(t *testing.T) {
	v := usPasswordResultFixture()
	id, r := usResultTestIdentity()
	d, err := usPasswordResultDigest(id, r, v)
	if err != nil {
		t.Fatal(err)
	}
	for i, m := range []func(*usPasswordSavedResult){func(v *usPasswordSavedResult) { v.owner += "x" }, func(v *usPasswordSavedResult) { v.account += "x" }, func(v *usPasswordSavedResult) { v.memberIncarnation = strings.Repeat("c", 32) }, func(v *usPasswordSavedResult) { v.profile += "x" }, func(v *usPasswordSavedResult) { v.profileLifetime = strings.Repeat("c", 32) }, func(v *usPasswordSavedResult) { v.memberRevision-- }, func(v *usPasswordSavedResult) { v.profileRevision-- }, func(v *usPasswordSavedResult) { v.previousEpoch--; v.resultEpoch-- }, func(v *usPasswordSavedResult) { v.appliedMS++ }} {
		x := v
		m(&x)
		got, e := usPasswordResultDigest(id, r, x)
		if e != nil || got == d {
			t.Fatal(i, e)
		}
	}
	x := v
	x.resultEpoch--
	if validateUSPasswordResult(x) == nil {
		t.Fatal("epoch")
	}
	if cloneUSPasswordResult(v) != v {
		t.Fatal("clone")
	}
	for _, f := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%d", "%100.2s"} {
		if strings.Contains(fmt.Sprintf(f, v), "account") {
			t.Fatal("redaction", f)
		}
	}
}
func TestUSPasswordNativeStorage(t *testing.T) {
	w := usSchemaDraftFixture(t)
	c, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err = c.ExecContext(t.Context(), "PRAGMA foreign_keys=OFF"); err != nil {
		t.Fatal(err)
	}
	defer c.ExecContext(t.Context(), "PRAGMA foreign_keys=ON")
	v := usPasswordResultFixture()
	var header, child int64
	err = c.QueryRowContext(t.Context(), `INSERT INTO security_command_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,result_version,result_codec,applied_ms,result_digest,result_count) VALUES(?,zeroblob(16),1,zeroblob(16),zeroblob(32),520,1,'password_replace_v1',0,zeroblob(32),0) RETURNING variable_bytes`, v.owner).Scan(&header)
	if err != nil {
		t.Fatal(err)
	}
	err = c.QueryRowContext(t.Context(), `INSERT INTO security_password_results(owner_id,command_incarnation,command_sequence,account_id,member_incarnation,profile_id,profile_lifetime,member_revision,profile_revision,previous_epoch,result_epoch) VALUES(?,zeroblob(16),1,?,?,?,?,?,?,?,?) RETURNING variable_bytes`, v.owner, v.account, v.memberIncarnation, v.profile, v.profileLifetime, v.memberRevision, v.profileRevision, v.previousEpoch, v.resultEpoch).Scan(&child)
	if err != nil {
		t.Fatal(err)
	}
	got, err := usPasswordResultVariableBytes(v)
	if err != nil || got != child+header {
		t.Fatal(got, child, err)
	}
}

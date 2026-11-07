package writer

import (
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func TestUSInvitationShortReceiptStatementsCapture(t *testing.T) {
	w := usSchemaDraftFixture(t)
	r := nativeInventoryFixtureRegistry(t, w, usInvitationShortReceiptDefinitions(), "us_invitation_short_receipt_statements.go", "us_invitation_short_receipt_statements_test.go")
	if len(r.Manifest()) != 6 {
		t.Fatal("incomplete inventory")
	}
	for _, d := range usInvitationShortReceiptDefinitions() {
		table := "security_command_receipts"
		if d.ID == usInvitationShortReceiptResultOctets || d.ID == usInvitationShortReceiptResult || d.ID == usInvitationShortReceiptResultInsert {
			table = "security_invitation_short_results"
		}
		if d.Kind == stmt.Read {
			if !strings.Contains(d.SQL, " FROM "+table+" WHERE owner_id=? AND command_incarnation=? AND command_sequence=?") || len(d.Params) != 3 || !reflect.DeepEqual(d.Plan.PrimaryKeys, []string{table}) {
				t.Fatal("read must use exact primary key", d.ID)
			}
		} else if !strings.HasPrefix(d.SQL, "INSERT INTO "+table+"(") {
			t.Fatal("unexpected write object", d.ID)
		}
		if strings.Count(d.SQL, table) != 1 || strings.Contains(d.SQL, ";") || strings.Contains(d.SQL, " JOIN ") || strings.Contains(d.SQL, "SELECT") && d.Kind == stmt.Write {
			t.Fatal("unexpected additional objects", d.ID)
		}
		for n, p := range d.Params {
			want := d.ID == usInvitationShortReceiptInsert && n >= 11
			if p.Nullable != want {
				t.Fatalf("%s parameter %s nullable=%v", d.ID, p.Name, p.Nullable)
			}
		}
	}
}

// Native storage and projections only; composition and current authority are separate.
func TestUSInvitationShortReceiptStatementsStorage(t *testing.T) {
	w := usSchemaDraftFixture(t)
	defs := map[stmt.ID]stmt.Definition{}
	for _, d := range usInvitationShortReceiptDefinitions() {
		defs[d.ID] = d
	}
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	inc := make([]byte, 16)
	_, err = conn.ExecContext(t.Context(), `INSERT INTO security_command_owners(id,command_incarnation,ingress,server_id,issuer_account_id,issuer_member_incarnation,issuer_profile_id,issuer_profile_lifetime,issuer_installation_incarnation,original_family_id,created_ms) VALUES('security-owner',?,'authenticated','server','issuer','aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','issuer-profile','bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb','cccccccccccccccccccccccccccccccc','family',0)`, inc)
	if err != nil {
		t.Fatal(err)
	}
	args := func(id stmt.ID) []any { return append([]any(nil), defs[id].Cohorts[0].Bindings...) }
	reject := func(id stmt.ID, a []any) {
		t.Helper()
		if _, e := conn.ExecContext(t.Context(), defs[id].SQL, a...); e == nil {
			t.Fatalf("%s accepted invalid row", id)
		}
	}
	insert := func(id stmt.ID, a []any) int64 {
		t.Helper()
		var n int64
		if e := conn.QueryRowContext(t.Context(), defs[id].SQL, a...).Scan(&n); e != nil {
			t.Fatal(e)
		}
		return n
	}
	h := args(usInvitationShortReceiptInsert)
	for _, change := range []func([]any){
		func(a []any) { a[5] = int64(552) }, func(a []any) { a[7] = "invitation_revoke_v1" }, func(a []any) { a[6] = int64(2) }, func(a []any) { a[10] = int64(1) },
		func(a []any) { a[11] = "key" }, func(a []any) { a[12] = make([]byte, 12) }, func(a []any) { a[13] = make([]byte, 16) },
	} {
		a := append([]any(nil), h...)
		change(a)
		reject(usInvitationShortReceiptInsert, a)
	}
	// Every nonempty generic secret group is forbidden for this typed purpose.
	for mask := 1; mask < 8; mask++ {
		a := append([]any(nil), h...)
		values := []any{"payload-key", make([]byte, 12), make([]byte, 35)}
		for n, v := range values {
			if mask&(1<<n) != 0 {
				a[11+n] = v
			}
		}
		reject(usInvitationShortReceiptInsert, a)
	}
	var hb, count int64
	if err = conn.QueryRowContext(t.Context(), defs[usInvitationShortReceiptInsert].SQL, h...).Scan(&hb, &count); err != nil {
		t.Fatal(err)
	}
	if hb != int64(len("security-owner")+96+len("invitation_short_issue_v1")) || count != 0 {
		t.Fatal("header bytes/count", hb, count)
	}
	d := args(usInvitationShortReceiptResultInsert)
	d[4] = "invité"
	for n := range d {
		bad := append([]any(nil), d...)
		bad[n] = nil
		reject(usInvitationShortReceiptResultInsert, bad)
	}
	for _, tc := range []struct {
		n int
		v any
	}{
		{3, int64(552)}, {2, int64(9)}, {0, "wrong"}, {1, make([]byte, 15)},
		{20, make([]byte, 11)}, {20, make([]byte, 13)}, {21, make([]byte, 34)}, {21, make([]byte, 36)}, {22, make([]byte, 31)}, {22, make([]byte, 33)},
		{19, ""}, {19, []byte("key")}, {6, strings.Repeat("A", 64)}, {10, make([]byte, 31)},
		{14, int64(0)}, {16, int64(601001)},
	} {
		bad := append([]any(nil), d...)
		bad[tc.n] = tc.v
		reject(usInvitationShortReceiptResultInsert, bad)
	}
	// A valid other-purpose header cannot own this child.
	other := append([]any(nil), h...)
	other[2] = int64(2)
	other[3] = []byte(strings.Repeat("x", 16))
	other[5] = int64(552)
	other[7] = "invitation_revoke_v1"
	var ob, oc int64
	if err = conn.QueryRowContext(t.Context(), defs[usInvitationShortReceiptInsert].SQL, other...).Scan(&ob, &oc); err != nil {
		t.Fatal(err)
	}
	wrong := append([]any(nil), d...)
	wrong[2] = int64(2)
	reject(usInvitationShortReceiptResultInsert, wrong)
	db := insert(usInvitationShortReceiptResultInsert, d)
	want := 0
	for _, v := range d {
		switch x := v.(type) {
		case string:
			want += len(x)
		case []byte:
			want += len(x)
		}
	}
	if db != int64(want) {
		t.Fatal("actual generated child bytes", db, want)
	}
	key := []any{"security-owner", inc, int64(1)}
	for _, tc := range []struct {
		id   stmt.ID
		want int64
	}{{usInvitationShortReceiptOctets, hb}, {usInvitationShortReceiptResultOctets, db}} {
		var got int64
		if err = conn.QueryRowContext(t.Context(), defs[tc.id].SQL, key...).Scan(&got); err != nil || got != tc.want {
			t.Fatal(got, err)
		}
	}
	for _, tc := range []struct {
		id   stmt.ID
		want []any
	}{{usInvitationShortReceiptHeader, []any{h[5], h[6], h[7], h[8], h[9], h[10], nil, nil, nil, hb}}, {usInvitationShortReceiptResult, append(append([]any(nil), d[4:]...), db)}} {
		rows, e := conn.QueryContext(t.Context(), defs[tc.id].SQL, key...)
		if e != nil {
			t.Fatal(e)
		}
		if !rows.Next() {
			t.Fatal("missing typed row")
		}
		got := make([]any, len(tc.want))
		ptr := make([]any, len(got))
		for n := range got {
			ptr[n] = &got[n]
		}
		if e = rows.Scan(ptr...); e != nil {
			t.Fatal(e)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatal("typed projection", got, tc.want)
		}
		if rows.Next() {
			t.Fatal("nonpoint read")
		}
		rows.Close()
	}
	for _, id := range []stmt.ID{usInvitationShortReceiptOctets, usInvitationShortReceiptHeader, usInvitationShortReceiptResultOctets, usInvitationShortReceiptResult} {
		for _, a := range [][]any{{"wrong", inc, int64(1)}, {"security-owner", make([]byte, 15), int64(1)}, {"security-owner", inc, int64(9)}} {
			rows, e := conn.QueryContext(t.Context(), defs[id].SQL, a...)
			if e != nil {
				t.Fatal(e)
			}
			found := rows.Next()
			e = rows.Err()
			rows.Close()
			if found || e != nil {
				t.Fatal("PK binding isolation", id, e)
			}
		}
	}
	reject(usInvitationShortReceiptInsert, h)
	reject(usInvitationShortReceiptResultInsert, d)
	if _, err = conn.ExecContext(t.Context(), `DELETE FROM security_command_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`, key...); err == nil {
		t.Fatal("parent FK allowed deletion")
	}
}

package writer

import (
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func TestUSRestrictionsReceiptStatementsCapture(t *testing.T) {
	w := usSchemaDraftFixture(t)
	r := nativeInventoryFixtureRegistry(t, w, usRestrictionsReceiptDefinitions(), "us_restrictions_receipt_statements.go", "us_restrictions_receipt_statements_test.go")
	if len(r.Manifest()) != 9 {
		t.Fatal("incomplete inventory")
	}
	for _, d := range usRestrictionsReceiptDefinitions() {
		for n, p := range d.Params {
			want := d.ID == usRestrictionsReceiptInsert && n >= 11
			if p.Nullable != want {
				t.Fatalf("%s parameter %s nullable=%v", d.ID, p.Name, p.Nullable)
			}
		}
	}
}

// Native storage and projections only; composition and current authority are separate.
func TestUSRestrictionsReceiptStatementsStorage(t *testing.T) {
	w := usSchemaDraftFixture(t)
	defs := map[stmt.ID]stmt.Definition{}
	for _, d := range usRestrictionsReceiptDefinitions() {
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
	h := args(usRestrictionsReceiptInsert)
	for _, change := range []func([]any){
		func(a []any) { a[7] = "member_libraries_v1" }, func(a []any) { a[6] = int64(2) }, func(a []any) { a[10] = int64(65) },
		func(a []any) { a[11] = "key" }, func(a []any) { a[12] = make([]byte, 12) }, func(a []any) { a[13] = make([]byte, 16) },
	} {
		a := append([]any(nil), h...)
		change(a)
		reject(usRestrictionsReceiptInsert, a)
	}
	var hb, count int64
	if err = conn.QueryRowContext(t.Context(), defs[usRestrictionsReceiptInsert].SQL, h...).Scan(&hb, &count); err != nil {
		t.Fatal(err)
	}
	if hb != int64(len("security-owner")+96+len("profile_restrictions_v1")) || count != 1 {
		t.Fatal("header bytes/count", hb, count)
	}
	d := args(usRestrictionsReceiptResultInsert)
	d[9] = "é"
	d[10] = "界"
	for n := 3; n < len(d); n++ {
		a := append([]any(nil), d...)
		a[n] = nil
		reject(usRestrictionsReceiptResultInsert, a)
	}
	for _, n := range []int{12, 13, 14, 15, 16} {
		a := append([]any(nil), d...)
		a[n] = int64(2)
		reject(usRestrictionsReceiptResultInsert, a)
	}
	for _, tc := range []struct {
		n int
		v any
	}{{3, int64(540)}, {8, int64(1)}, {11, int64(-2)}, {4, []byte("target")}, {5, "short"}, {7, "short"}} {
		a := append([]any(nil), d...)
		a[tc.n] = tc.v
		reject(usRestrictionsReceiptResultInsert, a)
	}
	// A valid purpose-540 parent cannot own a purpose-542 typed child.
	_, err = conn.ExecContext(t.Context(), `INSERT INTO security_command_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,result_version,result_codec,applied_ms,result_digest,result_count,target_account_id,target_member_incarnation,result_revision,policy_inherits,result_changed) VALUES(?,?,2,?, ?,540,1,'member_libraries_v1',1000,?,0,'target',?,2,0,1)`, h[0], inc, []byte(strings.Repeat("x", 16)), h[4], h[9], d[5])
	if err != nil {
		t.Fatal(err)
	}
	wrong := append([]any(nil), d...)
	wrong[2] = int64(2)
	reject(usRestrictionsReceiptResultInsert, wrong)
	db := insert(usRestrictionsReceiptResultInsert, d)
	want := len("security-owner") + 16
	for _, n := range []int{4, 5, 6, 7, 9, 10} {
		want += len(d[n].(string))
	}
	if db != int64(want) {
		t.Fatal("typed bytes", db, want)
	}
	l := args(usRestrictionsReceiptLabelInsert)
	for _, tc := range []struct {
		n int
		v any
	}{{3, int64(-1)}, {3, int64(64)}, {4, ""}, {4, strings.Repeat("界", 121)}, {4, nil}, {4, []byte("label")}} {
		a := append([]any(nil), l...)
		a[tc.n] = tc.v
		reject(usRestrictionsReceiptLabelInsert, a)
	}
	repaired := strings.Repeat("\ufffd", 120)
	l[4] = repaired
	lb := insert(usRestrictionsReceiptLabelInsert, l)
	l[3] = int64(1)
	insert(usRestrictionsReceiptLabelInsert, l)
	if lb != int64(len("security-owner")+16+360) {
		t.Fatal("label bytes", lb)
	}
	key := []any{"security-owner", inc, int64(1)}
	for _, tc := range []struct {
		id   stmt.ID
		want int64
	}{{usRestrictionsReceiptOctets, hb}, {usRestrictionsReceiptResultOctets, db}} {
		var got int64
		if err = conn.QueryRowContext(t.Context(), defs[tc.id].SQL, key...).Scan(&got); err != nil || got != tc.want {
			t.Fatal(got, err)
		}
	}
	for _, tc := range []struct {
		id   stmt.ID
		want []any
	}{{usRestrictionsReceiptHeader, []any{h[5], h[6], h[7], h[8], h[9], h[10], nil, nil, nil, hb}}, {usRestrictionsReceiptResult, append(append([]any(nil), d[4:]...), db)}} {
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
	for _, id := range []stmt.ID{usRestrictionsReceiptOctets, usRestrictionsReceiptHeader, usRestrictionsReceiptResultOctets, usRestrictionsReceiptResult, usRestrictionsReceiptLabelOctets, usRestrictionsReceiptLabels} {
		keys := [][]any{key, {"wrong", inc, int64(1)}, {"security-owner", make([]byte, 15), int64(1)}, {"security-owner", inc, int64(9)}}
		for k, a := range keys {
			if id == usRestrictionsReceiptLabelOctets || id == usRestrictionsReceiptLabels {
				a = append(append([]any(nil), a...), int64(-1), int64(64))
			}
			rows, e := conn.QueryContext(t.Context(), defs[id].SQL, a...)
			if e != nil {
				t.Fatal(e)
			}
			n := 0
			for rows.Next() {
				n++
			}
			e = rows.Err()
			rows.Close()
			want := 0
			if k == 0 {
				want = 1
				if id == usRestrictionsReceiptLabelOctets || id == usRestrictionsReceiptLabels {
					want = 2
				}
			}
			if e != nil || n != want {
				t.Fatal("binding isolation", id, n, e)
			}
		}
	}
	for _, id := range []stmt.ID{usRestrictionsReceiptLabelOctets, usRestrictionsReceiptLabels} {
		for _, bounds := range [][2]int64{{-1, 64}, {0, 64}, {-1, 1}, {1, 64}} {
			rows, e := conn.QueryContext(t.Context(), defs[id].SQL, append(append([]any(nil), key...), bounds[0], bounds[1])...)
			if e != nil {
				t.Fatal(e)
			}
			n := 0
			for rows.Next() {
				var ordinal int64
				var value any
				if e = rows.Scan(&ordinal, &value); e != nil {
					t.Fatal(e)
				}
				if ordinal <= bounds[0] || ordinal >= bounds[1] {
					t.Fatal("bounds")
				}
				if id == usRestrictionsReceiptLabels && value != repaired {
					t.Fatal("repaired label")
				}
				if id == usRestrictionsReceiptLabelOctets && value != lb {
					t.Fatal("label preflight")
				}
				n++
			}
			e = rows.Err()
			rows.Close()
			want := 0
			for ordinal := int64(0); ordinal < 2; ordinal++ {
				if ordinal > bounds[0] && ordinal < bounds[1] {
					want++
				}
			}
			if e != nil || n != want {
				t.Fatal("label positions", n, e)
			}
		}
	}
	for _, tc := range []struct {
		id stmt.ID
		a  []any
	}{{usRestrictionsReceiptInsert, h}, {usRestrictionsReceiptResultInsert, d}, {usRestrictionsReceiptLabelInsert, l}} {
		reject(tc.id, tc.a)
		if tc.id != usRestrictionsReceiptInsert {
			a := append([]any(nil), tc.a...)
			a[2] = int64(9)
			reject(tc.id, a)
		}
	}
}

package writer

import (
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"testing"
)

func TestUSInvitationReceiptStatementsCapture(t *testing.T) {
	w := usSchemaDraftFixture(t)
	r := nativeInventoryFixtureRegistry(t, w, usInvitationReceiptDefinitions(), "us_invitation_receipt_statements.go", "us_invitation_receipt_statements_test.go")
	if len(r.Manifest()) != 9 {
		t.Fatal("incomplete inventory")
	}
}

// Native storage only; no authority, accepting composition or crypto.
func TestUSInvitationReceiptStatementsStorage(t *testing.T) {
	for _, kind := range []int64{550, 551, 552} {
		t.Run(fmt.Sprint(kind), func(t *testing.T) {
			w := usSchemaDraftFixture(t)
			defs := map[stmt.ID]stmt.Definition{}
			for _, d := range usInvitationReceiptDefinitions() {
				defs[d.ID] = d
			}
			conn, e := w.db.Conn(t.Context())
			if e != nil {
				t.Fatal(e)
			}
			defer conn.Close()
			inc := make([]byte, 16)
			_, e = conn.ExecContext(t.Context(), `INSERT INTO security_command_owners(id,command_incarnation,ingress,server_id,issuer_account_id,issuer_member_incarnation,issuer_profile_id,issuer_profile_lifetime,issuer_installation_incarnation,original_family_id,created_ms) VALUES('security-owner',?,'authenticated','server','issuer','aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','issuer-profile','bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb','cccccccccccccccccccccccccccccccc','family',0)`, inc)
			if e != nil {
				t.Fatal(e)
			}
			h := append([]any(nil), defs[usInvitationReceiptInsert].Cohorts[0].Bindings...)
			h[5] = kind
			h[7] = map[int64]string{550: "invitation_create_v1", 551: "invitation_edit_v1", 552: "invitation_revoke_v1"}[kind]
			if kind != 550 {
				h[11], h[12], h[13] = nil, nil, nil
			}

			// Isolated purpose/secret-slot CHECK failures precede the valid row.
			for _, mutate := range []func([]any){
				func(a []any) { a[7] = "pin_set_v1" },
				func(a []any) { a[10] = int64(257) },
				func(a []any) {
					if kind == 550 {
						a[12] = make([]byte, 11)
					} else {
						a[11] = "forbidden"
					}
				},
				func(a []any) {
					if kind == 550 {
						a[13] = make([]byte, 15)
					} else {
						a[13] = make([]byte, 59)
					}
				},
			} {
				bad := append([]any(nil), h...)
				mutate(bad)
				if _, err := conn.ExecContext(t.Context(), defs[usInvitationReceiptInsert].SQL, bad...); err == nil {
					t.Fatal("purpose/byte control accepted")
				}
			}
			var hb, count int64
			if e = conn.QueryRowContext(t.Context(), defs[usInvitationReceiptInsert].SQL, h...).Scan(&hb, &count); e != nil {
				t.Fatal(e)
			}
			want := int64(len("security-owner") + 96 + len(h[7].(string)))
			if kind == 550 {
				want += 3 + 12 + 59
			}
			if hb != want || count != 1 {
				t.Fatal("header bytes/count", hb, count, want)
			}
			d := append([]any(nil), defs[usInvitationReceiptResultInsert].Cohorts[0].Bindings...)
			d[5] = "é"
			if kind != 550 {
				d[12] = int64(2)
			}
			if kind == 552 {
				d[7] = "revoked"
			}
			var db, lb int64
			if e = conn.QueryRowContext(t.Context(), defs[usInvitationReceiptResultInsert].SQL, d...).Scan(&db); e != nil {
				t.Fatal(e)
			}
			expected := len("security-owner") + 16
			for _, n := range []int{3, 4, 5, 6, 7, 11, 13, 14, 18, 19} {
				if d[n] != nil {
					expected += len(d[n].(string))
				}
			}
			if db != int64(expected) {
				t.Fatal("DTO bytes", db, expected)
			}
			l := append([]any(nil), defs[usInvitationReceiptLibraryInsert].Cohorts[0].Bindings...)
			if e = conn.QueryRowContext(t.Context(), defs[usInvitationReceiptLibraryInsert].SQL, l...).Scan(&lb); e != nil {
				t.Fatal(e)
			}
			if lb != int64(len("security-owner")+16+len("invitation")+len("movies")) {
				t.Fatal("library bytes", lb)
			}
			for _, tc := range []struct {
				id   stmt.ID
				want int64
			}{{usInvitationReceiptOctets, hb}, {usInvitationReceiptResultOctets, db}} {
				var got int64
				if e = conn.QueryRowContext(t.Context(), defs[tc.id].SQL, "security-owner", inc, int64(1)).Scan(&got); e != nil || got != tc.want {
					t.Fatal("numeric preflight", got, e)
				}
				for _, key := range [][]any{{"wrong", inc, int64(1)}, {"security-owner", make([]byte, 15), int64(1)}, {"security-owner", inc, int64(2)}} {
					rows, err := conn.QueryContext(t.Context(), defs[tc.id].SQL, key...)
					if err != nil {
						t.Fatal(err)
					}
					if rows.Next() {
						t.Fatal("wrong binding leaked")
					}
					rows.Close()
				}
			}

			// An adjacent policy cohort must never enter invitation replay.
			other := append([]any(nil), l...)
			other[3] = "policy"
			if _, err := conn.ExecContext(t.Context(), defs[usInvitationReceiptLibraryInsert].SQL, other...); err != nil {
				t.Fatal(err)
			}
			// Exercise typed header and DTO projections without interpretation.
			for _, id := range []stmt.ID{usInvitationReceiptHeader, usInvitationReceiptResult} {
				rows, err := conn.QueryContext(t.Context(), defs[id].SQL, "security-owner", inc, int64(1))
				if err != nil {
					t.Fatal(err)
				}
				cols, err := rows.Columns()
				if err != nil {
					t.Fatal(err)
				}
				if !rows.Next() {
					t.Fatal("missing typed row")
				}
				values := make([]any, len(cols))
				for n := range values {
					values[n] = new(any)
				}
				if err = rows.Scan(values...); err != nil {
					t.Fatal(err)
				}
				if rows.Next() {
					t.Fatal("nonpoint typed read")
				}
				rows.Close()
			}
			for _, id := range []stmt.ID{usInvitationReceiptLibraryOctets, usInvitationReceiptLibraries} {
				for _, after := range []int64{-1, 0} {
					rows, err := conn.QueryContext(t.Context(), defs[id].SQL, "security-owner", inc, int64(1), after, int64(256))
					if err != nil {
						t.Fatal(err)
					}
					n := 0
					for rows.Next() {
						n++
					}
					err = rows.Err()
					rows.Close()
					if err != nil || n != int(-after) {
						t.Fatal("keyset", n, err)
					}
				}
			}
			for _, tc := range []struct {
				id   stmt.ID
				args []any
			}{{usInvitationReceiptInsert, h}, {usInvitationReceiptResultInsert, d}, {usInvitationReceiptLibraryInsert, l}} {
				if _, err := conn.ExecContext(t.Context(), defs[tc.id].SQL, tc.args...); err == nil {
					t.Fatal("replacement accepted", tc.id)
				}
				args := append([]any(nil), tc.args...)
				args[2] = int64(9)
				if tc.id != usInvitationReceiptInsert {
					if _, err := conn.ExecContext(t.Context(), defs[tc.id].SQL, args...); err == nil {
						t.Fatal("orphan accepted")
					}
				}
			}
		})
	}
}

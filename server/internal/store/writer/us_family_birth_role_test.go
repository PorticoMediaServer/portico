package writer

import (
	"errors"
	"fmt"
	"testing"
)

func TestUSFamilyBirthEffectiveProfileRole(t *testing.T) {
	for _, credential := range []bool{false, true} {
		for _, role := range []string{"owner", "admin", "member"} {
			for _, primary := range []int{0, 1} {
				t.Run(fmt.Sprint(credential, "/", role, "/", primary), func(t *testing.T) {
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
					wc008NativeExec(t, w, `UPDATE direct_memberships SET role=?`, role)
					wc008NativeExec(t, w, `UPDATE direct_profiles SET is_primary=?`, primary)
					turn := usFamilyBirthTurn(t, w, credential)
					if _, err = applyUSFamilyBirth(t.Context(), turn, p, credential, c); err != nil {
						t.Fatal(err)
					}
					if _, err = turn.transaction.commit(); err != nil {
						t.Fatal(err)
					}
					var actual string
					if err = w.db.QueryRowContext(t.Context(), `SELECT role FROM authorization_session_families WHERE id=?`, in.family).Scan(&actual); err != nil {
						t.Fatal(err)
					}
					want := role
					if primary == 0 {
						want = "member"
					}
					if actual != want {
						t.Fatalf("role=%s want=%s", actual, want)
					}
				})
			}
		}
	}
}

func TestUSFamilyBirthPrimaryPointerMustMatch(t *testing.T) {
	w, in := usFamilyBirthFixture(t)
	wc008NativeExec(t, w, `UPDATE accounts SET profile_id='other-profile'`)
	p, err := prepareUSOrdinaryFamilyBirth(in)
	if err != nil {
		t.Fatal(err)
	}
	turn := usFamilyBirthTurn(t, w, false)
	if _, err = applyUSFamilyBirth(t.Context(), turn, p, false, nil); !errors.Is(err, ErrConflict) {
		t.Fatal("inconsistent primary admitted", err)
	}
	if _, err = turn.transaction.commit(); err == nil {
		t.Fatal("ignored denial committed")
	}
	if n := usFamilyRoleCount(t, w); n != 0 {
		t.Fatal("partial family", n)
	}
}
func usFamilyRoleCount(t *testing.T, w *executor) int {
	t.Helper()
	var n int
	if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM authorization_session_families`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

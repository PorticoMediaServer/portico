package identity

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
)

// O-169: setup's Remote access and Remote security reach the settings inside
// setup's own transaction, with the right defaults for each sign-in kind.
func TestSetupRemoteChoices(t *testing.T) {
	for _, c := range []struct {
		name                     string
		q                        SetupOptions
		access, security, reject string
	}{
		{"Portico Account, defaults", SetupOptions{Name: "Home"}, "on", "enhanced", ""},
		{"local, defaults", SetupOptions{Name: "Home", AuthMode: "local", Username: "owner", Password: "Local-password1!"}, "off", "enhanced", ""},
		{"Portico Account, private and standard", SetupOptions{Name: "Home", RemoteAccess: "private", RemoteSecurity: "standard"}, "private", "standard", ""},
		{"local, on", SetupOptions{Name: "Home", AuthMode: "local", Username: "owner", Password: "Local-password1!", RemoteAccess: "on"}, "on", "enhanced", ""},
		{"unknown access", SetupOptions{Name: "Home", RemoteAccess: "sometimes"}, "", "", "input"},
		{"unknown security", SetupOptions{Name: "Home", RemoteSecurity: "loose"}, "", "", "input"},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, db, _ := familyFixture(t)
			ctx := context.Background()
			if _, e := db.Exec(`DELETE FROM accounts`); e != nil {
				t.Fatal(e)
			}
			token, e := os.ReadFile(s.setupPath)
			if e != nil {
				t.Fatal(e)
			}
			var gotAccess, gotSecurity string
			var inTx bool
			s.SetupRemoteTx = func(ctx context.Context, tx *sql.Tx, access, security string) error {
				gotAccess, gotSecurity = access, security
				// Setup's own transaction: the name it is writing is already there.
				var name string
				inTx = tx.QueryRowContext(ctx, `SELECT value FROM configuration WHERE key='name'`).Scan(&name) == nil && name == "Home"
				return nil
			}
			q := c.q
			q.RequestID, q.SetupToken = "33333333-3333-4333-8333-333333333333", string(token)
			_, e = s.SetupWithOptions(ctx, q, false)
			if c.reject != "" {
				if !errors.Is(e, ErrDirectInput) || gotAccess != "" {
					t.Fatalf("accepted %+v: %v", q, e)
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			if gotAccess != c.access || gotSecurity != c.security || !inTx {
				t.Fatalf("got %q/%q in setup's transaction=%v, want %q/%q", gotAccess, gotSecurity, inTx, c.access, c.security)
			}
		})
	}
}

// A failing write fails setup: a server is never set up without its choices.
func TestSetupRemoteChoicesFailureFailsSetup(t *testing.T) {
	s, db, _ := familyFixture(t)
	ctx := context.Background()
	if _, e := db.Exec(`DELETE FROM accounts`); e != nil {
		t.Fatal(e)
	}
	token, _ := os.ReadFile(s.setupPath)
	boom := errors.New("boom")
	s.SetupRemoteTx = func(context.Context, *sql.Tx, string, string) error { return boom }
	if _, e := s.SetupWithOptions(ctx, SetupOptions{RequestID: "33333333-3333-4333-8333-333333333333", SetupToken: string(token), Name: "Home"}, false); !errors.Is(e, boom) {
		t.Fatalf("setup went on without its remote choices: %v", e)
	}
	var n int
	if e := db.QueryRow(`SELECT count(*) FROM onboarding_state_v1`).Scan(&n); e != nil || n != 0 {
		t.Fatalf("setup state committed: %d %v", n, e)
	}
}

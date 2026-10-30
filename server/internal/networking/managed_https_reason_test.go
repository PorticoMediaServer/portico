package networking

import (
	"errors"
	"strings"
	"testing"
)

// A return waiting on managed HTTPS says what it is waiting for: every no-origin exit
// names a value-free reason, and the owner return route carries it as its step. (Setup
// waits on no route since O-163.)
func TestManagedHTTPSAbsenceNamesItsReason(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, c := range []struct{ name, sql, reason string }{
		{"serving", "", ""},
		{"managed route off", `UPDATE identity_managed_route SET enabled=0`, "sign-in kind not portico or managed route off"},
		{"remote access off", `UPDATE networking_remote_settings SET enabled=0`, "remote access off or remote state stale"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f, h, ctx, _ := managedHTTPSFixture(t)
			if c.sql != "" {
				if _, err := f.db.Exec(c.sql); err != nil {
					t.Fatal(err)
				}
			}
			v, err := f.store.InstalledIntent(ctx)
			if err != nil {
				t.Fatal(err)
			}
			tx, err := f.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			origin, reason, err := h.managedHTTPSTx(ctx, tx)
			if err != nil || reason != c.reason || (c.reason == "") != (origin != "") {
				t.Fatal("reason", origin, reason, err)
			}
			_, _, err = h.ownerReturnRouteTx(ctx, tx, v)
			if c.reason == "" {
				if err != nil {
					t.Fatal("serving route refused", err)
				}
				return
			}
			s := summarizeFailure(ctx, err)
			if !errors.Is(err, ErrUnavailable) || s.Step != "owner return route: "+c.reason || strings.Contains(s.Step, "http") {
				t.Fatal("the route's absence is not named", s.Step, err)
			}
		})
	}
}

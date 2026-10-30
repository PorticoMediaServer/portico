package networking

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/claimweb"
)

// A real fixture claim is installed and acknowledged, but no certificate or
// serving-state row is invented. Signed capture checks belong to the identity
// tests; this callback trace pins the ordering at the installed transition.
func TestWebInstalledWakesManagersBeforeMissingServingState(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, mode := range []string{"missing serving state", "not ready", "terminal refusal"} {
		t.Run(mode, func(t *testing.T) {
			f, certificates, parent := certificateFixture(t)
			h := &ClaimHandler{store: f.store, runner: certificates.runner, certificates: certificates,
				remote: &RemoteManager{wake: make(chan struct{}, 1)}, recoveryWake: make(chan struct{}, 1)}
			var calls int
			h.setupReturnResolver = func(ctx context.Context, resume, operation string) error {
				calls++
				if resume != "original-private-note" || operation != f.binding.OperationID {
					t.Fatal("capture changed")
				}
				if len(certificates.wake) != 1 || len(h.remote.wake) != 1 || len(h.recoveryWake) != 1 {
					t.Fatal("binding ran before ordinary manager wakes")
				}
				if mode == "not ready" {
					return ErrUnavailable
				}
				if mode == "terminal refusal" {
					return ErrStale
				}
				tx, err := f.db.BeginTx(ctx, nil)
				if err != nil {
					return err
				}
				defer tx.Rollback()
				_, err = certificates.servingSnapshotTx(ctx, tx)
				if !errors.Is(err, sql.ErrNoRows) {
					t.Fatalf("expected genuinely absent serving state, got %v", err)
				}
				return err
			}
			err := h.runner.Do(parent, func(ctx context.Context) error {
				// Initialize only real configuration, without creating scope material.
				g, err := f.store.tx(ctx)
				if err != nil {
					return err
				}
				id, err := readIdentity(ctx, g.Tx())
				if err == nil {
					lease, e := claimAuthority(ctx)
					if e != nil {
						err = e
					} else {
						_, err = readCertificateConfigTx(ctx, g.Tx(), id, lease.Incarnation())
					}
				}
				if err == nil {
					err = commitClaimDatabaseTx(ctx, g)
				}
				g.Rollback()
				if err != nil {
					return err
				}
				v, err := f.store.InstalledIntent(ctx)
				if err != nil {
					return err
				}
				if v.Purpose != "setup" {
					t.Fatal("fixture is not a genuine installed setup")
				}
				return h.afterWebClaimInstalled(ctx, v, localWebClaim{SetupStart: &claimweb.Signed{}}, "original-private-note")
			})
			if mode == "not ready" && err != nil || mode == "missing serving state" && !errors.Is(err, sql.ErrNoRows) || mode == "terminal refusal" && !errors.Is(err, ErrStale) {
				t.Fatal("binding diagnostic changed", err)
			}
			if calls != 1 || len(certificates.wake) != 1 || len(h.remote.wake) != 1 || len(h.recoveryWake) != 1 {
				t.Fatal("installed work was not woken")
			}
			for _, table := range []string{"networking_certificate_state", "networking_certificates"} {
				var count int
				if err := f.db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count != 0 {
					t.Fatal("wake fabricated certificate state", table, err)
				}
			}
		})
	}
}

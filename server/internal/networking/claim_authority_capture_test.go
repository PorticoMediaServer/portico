package networking

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestClaimAuthorityCaptureTerminalWithoutBearer(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f, v, ctx := installedAuthorityFixture(t)
	proof, verify := authorityFixture(t, v, "revoked", 1)
	c, _ := NewClaimAuthorityConsumer(f.store, verify, func(ctx context.Context, tx *sql.Tx, v Intent, _ string) error {
		_, e := tx.ExecContext(ctx, `DELETE FROM networking_claim_credentials WHERE operation_id=?`, v.OperationID)
		return e
	})
	if e := c.Apply(ctx, v, proof); e != nil {
		t.Fatal(e)
	}
	capture, e := f.store.CaptureClaimAuthority(ctx)
	if e != nil || capture.Intent.OperationID != v.OperationID || capture.KeyIncarnation == "" {
		t.Fatal("public authority capture unavailable", e)
	}
	if _, e = f.store.InstalledCredential(ctx, v); e == nil {
		t.Fatal("terminal capture granted bearer")
	}
	if e = f.store.CurrentClaimAuthority(ctx, capture); e != nil {
		t.Fatal(e)
	}
	wrong := capture
	wrong.KeyIncarnation = "another"
	if e = f.store.CurrentClaimAuthority(ctx, wrong); !errors.Is(e, ErrStale) {
		t.Fatal("wrong key incarnation accepted")
	}
	if _, e = f.store.CaptureClaimAuthority(context.Background()); !errors.Is(e, ErrUnavailable) {
		t.Fatal("ungated key-proof capture allowed")
	}
	if _, _, e = f.store.BeginCancel(ctx, v.Binding, f.cancel(t, v)); e != nil {
		t.Fatal(e)
	}
	if e = f.store.CurrentClaimAuthority(ctx, capture); !errors.Is(e, ErrStale) {
		t.Fatal("late capture survived cancel")
	}
}

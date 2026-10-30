package networking

import (
	"context"
	"errors"
	"testing"
)

func TestClaimCurrentInstalledCaptureAndCancellation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := fixtureCurrentSQL(t)
	ctx := fixtureClaimContext(t)
	if _, e := f.store.InstalledIntent(ctx); !errors.Is(e, ErrStale) {
		t.Fatal("fresh identity appeared installed")
	}
	v := f.retrieving(t)
	result := resultFor(t, v)
	defer result.Credential.Clear()
	installed, e := f.store.Install(ctx, v, result)
	if e != nil {
		t.Fatal(e)
	}
	got, e := f.store.InstalledIntent(ctx)
	if e != nil || got.OperationID != installed.OperationID || got.Revision != installed.Revision || got.ClaimGeneration != installed.ClaimGeneration || got.CredentialGeneration != installed.CredentialGeneration {
		t.Fatal("incorrect installed capture")
	}
	if _, e = f.store.InstalledIntent(context.Background()); !errors.Is(e, ErrUnavailable) {
		t.Fatal("ungated installed capture allowed")
	}
	if _, _, e = f.store.BeginCancel(ctx, got.Binding, f.cancel(t, got)); e != nil {
		t.Fatal(e)
	}
	if _, e = f.store.InstalledIntent(ctx); !errors.Is(e, ErrStale) {
		t.Fatal("cancelled claim remained installed")
	}
}

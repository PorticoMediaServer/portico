package writer

import (
	"bytes"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
	"unsafe"
)

func scanLifecycleCreateInput() (CommandIdentity, inventoryCreateRequest) {
	i, _, v := scanLifecycleResultFixture(false)
	o := v.Original
	return i, inventoryCreateRequest{
		Scope: inventoryScope{runID: o.RunID, libraryKey: o.LibraryKey, libraryID: o.LibraryID},
		Source: inventorySource{id: o.SourceID, incarnation: o.SourceIncarnation, rootIdentity: o.RootIdentity,
			resolvedRoot: bytes.Clone(o.ResolvedRoot), generation: o.SourceGeneration, followSymlinks: o.FollowSymlinks},
		PolicyRevision: o.PolicyRevision, ScanMode: o.ScanMode, RootIdentity: v.RootIdentity, RootRevision: v.RootRevision,
	}
}

func scanLifecycleClaimInput() (CommandIdentity, inventoryClaimRequest) {
	i, _, v := scanLifecycleResultFixture(true)
	return i, inventoryClaimRequest{
		Original: v.Original,
		Run: inventoryRun{revision: 3, policyRevision: v.Original.PolicyRevision,
			leaseOwner: 23, leaseGeneration: 1, leaseExpiresMS: 1, lastSequence: 3, acknowledgedSequence: 3,
			phase: "inventory", scanMode: v.Original.ScanMode},
		Root: scanRootWitness{Path: ".", Identity: v.RootIdentity, Revision: v.RootRevision, Cursor: v.RootCursor,
			State: "pending", Pages: v.RootPages, Skipped: v.RootSkipped},
		Discovered: v.Discovered, Processed: v.Processed, JobStatus: "running", Attempts: 2,
		JobCreatedAt: "2000-01-01T00:00:00.000000000Z", JobErrorText: "captured old error",
		NextAttemptMS: scanOptionalMS{Present: true, Value: 0},
	}
}

func requireScanLifecycleOwned(t *testing.T, original, owned reflect.Value) {
	t.Helper()
	switch original.Kind() {
	case reflect.Struct:
		for n := range original.NumField() {
			requireScanLifecycleOwned(t, original.Field(n), owned.Field(n))
		}
	case reflect.String:
		a, b := original.String(), owned.String()
		if a != b || a != "" && unsafe.StringData(a) == unsafe.StringData(b) {
			t.Fatal("retained caller string backing", original.Type())
		}
	case reflect.Slice:
		if original.Type().Elem().Kind() != reflect.Uint8 || !bytes.Equal(original.Bytes(), owned.Bytes()) ||
			original.Len() > 0 && original.Pointer() == owned.Pointer() {
			t.Fatal("retained caller root backing")
		}
	}
}

func TestScanLifecyclePreparationFreezesCompleteOriginal(t *testing.T) {
	i, in := scanLifecycleCreateInput()
	create, err := newInventoryCreateCommand(i, in)
	if err != nil || create == nil || !create.valid {
		t.Fatal(create, err)
	}
	requireScanLifecycleOwned(t, reflect.ValueOf(in), reflect.ValueOf(create.request))
	want, err := canonicalRequestDigest(InventoryCreate, 1, i, in)
	if err != nil || want != create.digest {
		t.Fatal("partial create digest", err)
	}
	in.Source.resolvedRoot[0] = '!'
	if bytes.Equal(in.Source.resolvedRoot, create.request.Source.resolvedRoot) {
		t.Fatal("caller changed frozen root")
	}
	i, claimInput := scanLifecycleClaimInput()
	claim, err := newInventoryClaimCommand(i, claimInput)
	if err != nil || claim == nil || !claim.valid {
		t.Fatal(claim, err)
	}
	requireScanLifecycleOwned(t, reflect.ValueOf(claimInput), reflect.ValueOf(claim.request))
	want, err = canonicalRequestDigest(InventoryClaim, 1, i, claimInput)
	if err != nil || want != claim.digest {
		t.Fatal("partial claim digest", err)
	}
	changed := claimInput
	changed.Root.Cursor += ":next"
	next, err := newInventoryClaimCommand(i, changed)
	if err != nil || next.digest == claim.digest {
		t.Fatal("root progress omitted from original", err)
	}
	changed = claimInput
	changed.Original.CreateRequestDigest[0]++
	next, err = newInventoryClaimCommand(i, changed)
	if err != nil || next.digest == claim.digest {
		t.Fatal("creation reference omitted from original", err)
	}
}

func TestScanLifecyclePreparationKeepsFreshChecksAfterHistory(t *testing.T) {
	i, in := scanLifecycleClaimInput()
	// Expired lease, maximal native revision/counters, and a future due time
	// remain representable original values. Only the fresh protocol checks them.
	in.Run.revision, in.Run.lastSequence = math.MaxInt64, math.MaxInt64
	in.Run.acknowledgedSequence = math.MaxInt64
	in.Run.leaseGeneration = math.MaxInt64
	in.NextAttemptMS.Value = math.MaxInt64
	cmd, err := newInventoryClaimCommand(i, in)
	if err != nil || cmd == nil {
		t.Fatal("preparation performed fresh clock/overflow checks", err)
	}
	in.Run.leaseOwner, in.Run.leaseGeneration, in.Run.leaseExpiresMS = 0, 0, 0
	if cmd, err := newInventoryClaimCommand(i, in); err != nil || cmd == nil {
		t.Fatal("unclaimed lease is not a live-lease requirement", err)
	}
}

func TestScanLifecyclePreparationRefusesMalformedAndOversizedInputs(t *testing.T) {
	for _, mutate := range []func(*inventoryClaimRequest){
		func(v *inventoryClaimRequest) { v.Run.leaseGeneration = 0 },
		func(v *inventoryClaimRequest) { v.NextAttemptMS = scanOptionalMS{Value: 1} },
		func(v *inventoryClaimRequest) { v.Root.Path = "child" },
		func(v *inventoryClaimRequest) { v.Root.Revision = "\xff" },
		func(v *inventoryClaimRequest) { v.Original.ResolvedRoot = []byte{'/', 0} },
		func(v *inventoryClaimRequest) { v.Root.Cursor = strings.Repeat("x", maxCanonicalRequestBytes) },
	} {
		i, in := scanLifecycleClaimInput()
		mutate(&in)
		if cmd, err := newInventoryClaimCommand(i, in); cmd != nil || !errors.Is(err, ErrInvalid) {
			t.Fatal("malformed claim became a command", cmd, err)
		}
	}
	i, in := scanLifecycleCreateInput()
	in.Source.resolvedRoot = []byte("\xff")
	if cmd, err := newInventoryCreateCommand(i, in); cmd != nil || !errors.Is(err, ErrInvalid) {
		t.Fatal("lossy original-root transport", cmd, err)
	}
}

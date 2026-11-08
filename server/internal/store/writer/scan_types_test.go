package writer

import (
	"bytes"
	"context"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
	"unsafe"
)

func inventoryRequestFixture() (inventoryAcceptRequest, CommandIdentity) {
	identity := CommandIdentity{Sequence: 19}
	for i := range identity.Incarnation {
		identity.Incarnation[i], identity.OperationID[i] = byte(i+1), byte(i+33)
	}
	return inventoryAcceptRequest{
		variant: inventoryOrdinaryBatch,
		scope:   inventoryScope{runID: "scan-日本", libraryID: 9, libraryKey: "library-α"},
		source: inventorySource{id: "source-one", incarnation: "incarnation:007", rootIdentity: "device/inode:42",
			resolvedRoot: []byte("/media"), generation: 23, followSymlinks: false},
		run: inventoryRun{revision: 41, policyRevision: 7, leaseOwner: 13, leaseGeneration: 17,
			leaseExpiresMS: 1, lastSequence: 18, acknowledgedSequence: 18, phase: "inventory", scanMode: "first-import"},
		directory: inventoryDirectory{path: ".", identity: "directory:11", revision: "directory-revision:013", cursorKind: inventoryLinuxCursor,
			expectedCursor: "", nextCursor: "opaque:00042", pages: 5, skipped: 3, rawNames: 1},
		files: []inventoryFile{{sourceKey: "source-key:001", relativePath: "Arrival.mkv", assetPath: "/media/Arrival.mkv",
			objectIdentity: "file-native:31", revision: "opaque:019", changeToken: "change:29", size: 42, modifiedNS: -11,
			title: "Arrival", sortTitle: "Arrival", sortKey: "arrival", sortHead: "a", sortKeyKeepArticles: "arrival",
			sortHeadKeepArticles: "a", year: 2016, metadataLanguage: "en-US"}},
	}, identity
}

func inventoryTestClone(in inventoryAcceptRequest) inventoryAcceptRequest {
	in.source.resolvedRoot = bytes.Clone(in.source.resolvedRoot)
	in.files = append([]inventoryFile(nil), in.files...)
	return in
}

// Test-only access to private native fields; production has no reflection
// setters, unsafe ownership shortcuts or alternate request encoder.
func inventoryTestWritable(v reflect.Value) reflect.Value {
	return reflect.NewAt(v.Type(), unsafe.Pointer(v.UnsafeAddr())).Elem()
}

func inventoryTestLeaves(v reflect.Value, path []int, visit func([]int, reflect.Value)) {
	switch v.Kind() {
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			inventoryTestLeaves(v.Field(i), append(append([]int(nil), path...), i), visit)
		}
	case reflect.Array, reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			inventoryTestLeaves(v.Index(i), append(append([]int(nil), path...), i), visit)
		}
	default:
		visit(path, v)
	}
}

func inventoryTestAt(v reflect.Value, path []int) reflect.Value {
	for _, index := range path {
		if v.Kind() == reflect.Struct {
			v = v.Field(index)
		} else {
			v = v.Index(index)
		}
	}
	return inventoryTestWritable(v)
}

func TestInventoryAcceptRequestCoversEveryField(t *testing.T) {
	request, identity := inventoryRequestFixture()
	prepared, err := newInventoryAcceptCommand(identity, request)
	if err != nil {
		t.Fatal(err)
	}
	want, err := canonicalRequestDigest(InventoryAccept, inventoryAcceptRequestVersion, identity, request)
	if err != nil || prepared.digest != want {
		t.Fatal("constructor did not hash the complete original request", err)
	}
	count := 0
	inventoryTestLeaves(reflect.ValueOf(&request).Elem(), nil, func(path []int, _ reflect.Value) {
		count++
		changed := inventoryTestClone(request)
		leaf := inventoryTestAt(reflect.ValueOf(&changed).Elem(), path)
		switch leaf.Kind() {
		case reflect.Bool:
			leaf.SetBool(!leaf.Bool())
		case reflect.String:
			leaf.SetString(leaf.String() + "+")
		case reflect.Int64:
			leaf.SetInt(leaf.Int() + 1)
		case reflect.Uint8:
			leaf.SetUint(leaf.Uint() ^ 1)
		default:
			t.Fatalf("uncovered request type %s", leaf.Type())
		}
		digest, err := canonicalRequestDigest(InventoryAccept, inventoryAcceptRequestVersion, identity, changed)
		if err != nil || digest == prepared.digest {
			t.Fatalf("request leaf omitted at %v: %v", path, err)
		}
		candidate, err := newInventoryAcceptCommand(identity, changed)
		if err == nil && candidate.digest != digest {
			t.Fatalf("constructor changed or partially hashed leaf at %v", path)
		}
	})
	if count < 50 {
		t.Fatal("sparse request mutation coverage", count)
	}
	for i := 0; i < 33; i++ {
		changed := identity
		switch {
		case i < 16:
			changed.Incarnation[i] ^= 0x80
		case i < 32:
			changed.OperationID[i-16] ^= 0x80
		default:
			changed.Sequence++
		}
		digest, err := canonicalRequestDigest(InventoryAccept, inventoryAcceptRequestVersion, changed, request)
		if err != nil || digest == prepared.digest {
			t.Fatal("original identity byte/sequence omitted", i, err)
		}
	}
	t.Logf("complete request leaves=%d; original identity mutations=33", count)
}

func TestInventoryAcceptRequestOwnsBuffers(t *testing.T) {
	request, identity := inventoryRequestFixture()
	before := inventoryTestClone(request)
	prepared, err := newInventoryAcceptCommand(identity, request)
	if err != nil || !reflect.DeepEqual(request, before) {
		t.Fatal("constructor mutated input", err)
	}
	inventoryTestLeaves(reflect.ValueOf(&request).Elem(), nil, func(path []int, leaf reflect.Value) {
		if leaf.Kind() == reflect.String && leaf.String() != "" {
			owned := inventoryTestAt(reflect.ValueOf(&prepared.request).Elem(), path).String()
			if owned != leaf.String() || unsafe.StringData(owned) == unsafe.StringData(leaf.String()) {
				t.Fatalf("request retains caller string buffer at %v", path)
			}
		}
	})
	request.source.resolvedRoot[0] = '!'
	request.files[0].title, request.files[0].revision = "changed", "different"
	if !reflect.DeepEqual(prepared.request, before) {
		t.Fatal("prepared command aliases caller slice/root")
	}
	digest, err := canonicalRequestDigest(InventoryAccept, inventoryAcceptRequestVersion, prepared.identity, prepared.request)
	if err != nil || digest != prepared.digest {
		t.Fatal("queued immutable request drifted", err)
	}
	ref, err := prepared.receiptRequest()
	if err != nil || ref.RunID != before.scope.runID || ref.LibraryKey != before.scope.libraryKey || ref.LibraryID != before.scope.libraryID ||
		ref.Receipt != (ReceiptReference{Identity: identity, RequestDigest: prepared.digest}) {
		t.Fatal("wrong original historical receipt scope", err)
	}
	for _, zero := range []*inventoryAcceptCommand{nil, {}} {
		if got, err := zero.receiptRequest(); !errors.Is(err, ErrInvalid) || got != (ScanReceiptRequest{}) {
			t.Fatal("unprepared command yielded a receipt reference", err)
		}
	}
}

func TestInventoryAcceptRequestRefusesInvalidOrUnqualifiedShape(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*inventoryAcceptRequest)
		want error
	}{
		{"nil_batch", func(r *inventoryAcceptRequest) { r.files = nil }, ErrInvalid},
		{"empty_batch", func(r *inventoryAcceptRequest) { r.files = []inventoryFile{} }, ErrInvalid},
		{"oversize_batch", func(r *inventoryAcceptRequest) { r.files = make([]inventoryFile, maxInventoryAcceptFiles+1) }, ErrCap},
		{"unknown_variant", func(r *inventoryAcceptRequest) { r.variant = "cue" }, ErrInvalid},
		{"invalid_utf8", func(r *inventoryAcceptRequest) { r.files[0].title = "\xff" }, ErrInvalid},
		{"empty_title", func(r *inventoryAcceptRequest) { r.files[0].title = " \t" }, ErrInvalid},
		{"title_cap", func(r *inventoryAcceptRequest) { r.files[0].title = strings.Repeat("é", MaxTitleCharacters+1) }, ErrCap},
		{"negative_size", func(r *inventoryAcceptRequest) { r.files[0].size = -1 }, ErrInvalid},
		{"directory_not_ordinary_file", func(r *inventoryAcceptRequest) { r.files[0].directory = true }, ErrInvalid},
		{"unadmitted_cursor_kind", func(r *inventoryAcceptRequest) { r.directory.cursorKind = "windows_find_stream_v1" }, ErrInvalid},
		{"bad_parent", func(r *inventoryAcceptRequest) { r.directory.path = "other" }, ErrInvalid},
		{"traversal", func(r *inventoryAcceptRequest) { r.files[0].relativePath = "../Arrival.mkv" }, ErrInvalid},
		{"nul_path", func(r *inventoryAcceptRequest) { r.files[0].assetPath += "\x00" }, ErrInvalid},
		{"nul_root", func(r *inventoryAcceptRequest) { r.source.resolvedRoot = []byte("/media\x00") }, ErrInvalid},
		{"relative_root", func(r *inventoryAcceptRequest) { r.source.resolvedRoot = []byte("media") }, ErrInvalid},
		{"raw_names_filtered", func(r *inventoryAcceptRequest) { r.directory.rawNames++ }, ErrInvalid},
		{"unstable", func(r *inventoryAcceptRequest) { r.directory.unstable = true }, ErrInvalid},
		{"unacked_predecessor", func(r *inventoryAcceptRequest) { r.run.acknowledgedSequence-- }, ErrInvalid},
		{"wrong_original_sequence", func(r *inventoryAcceptRequest) { r.run.lastSequence++ }, ErrInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request, identity := inventoryRequestFixture()
			tc.edit(&request)
			if got, err := newInventoryAcceptCommand(identity, request); got != nil || !errors.Is(err, tc.want) {
				t.Fatalf("partial/unqualified command: %v, %v", got != nil, err)
			}
		})
	}
}

func TestInventoryAcceptRequestDefersFreshNativeChecks(t *testing.T) {
	request, identity := inventoryRequestFixture()
	// These original values are representable, even when fresh execution would
	// fail expiry/overflow/progress. Historical resolution must come first.
	identity.Sequence = math.MaxInt64
	request.run.lastSequence, request.run.acknowledgedSequence = identity.Sequence-1, identity.Sequence-1
	request.run.revision, request.source.generation = math.MaxInt64, math.MaxInt64
	request.directory.pages, request.directory.skipped = math.MaxInt64, math.MaxInt64
	request.directory.nextCursor = request.directory.expectedCursor
	request.files[0].modifiedNS = math.MinInt64
	request.scope.runID += "\x00native"
	request.source.resolvedRoot = []byte{'/', 0xff} // byte transport remains a separate proof
	prepared, err := newInventoryAcceptCommand(identity, request)
	if err != nil || prepared.request.directory.nextCursor != "" || !bytes.Equal(prepared.request.source.resolvedRoot, request.source.resolvedRoot) {
		t.Fatal("constructor invented current eligibility/normalization", err)
	}
}

func TestInventoryAcceptRequestPreflightsEveryTextPayload(t *testing.T) {
	request, identity := inventoryRequestFixture()
	large := strings.Repeat("x", maxCanonicalRequestBytes)
	inventoryTestLeaves(reflect.ValueOf(&request).Elem(), nil, func(path []int, leaf reflect.Value) {
		if leaf.Kind() != reflect.String {
			return
		}
		changed := inventoryTestClone(request)
		inventoryTestAt(reflect.ValueOf(&changed).Elem(), path).SetString(large)
		if got, err := newInventoryAcceptCommand(identity, changed); got != nil || !errors.Is(err, ErrCap) {
			t.Fatalf("field length not preflighted at %v: %v", path, err)
		}
	})
	request.source.resolvedRoot = bytes.Repeat([]byte{'/'}, maxCanonicalRequestBytes+1)
	if got, err := newInventoryAcceptCommand(identity, request); got != nil || !errors.Is(err, ErrCap) {
		t.Fatal("native root length not preflighted", err)
	}
}

func TestInventoryAcceptRequestLateOverBudgetDoesNotCopyPrefix(t *testing.T) {
	request, identity := inventoryRequestFixture()
	request.scope.runID = "\xff" // size refusal must precede even early UTF-8 validation
	request.source.resolvedRoot = bytes.Repeat([]byte{'/'}, maxCanonicalRequestBytes/2)
	request.files[0].metadataLanguage = strings.Repeat("x", maxCanonicalRequestBytes/2)
	result := testing.Benchmark(func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if got, err := newInventoryAcceptCommand(identity, request); got != nil || !errors.Is(err, ErrCap) {
				b.Fatal("late payload did not refuse whole command", err)
			}
		}
	})
	if result.AllocedBytesPerOp() > 8<<10 {
		t.Fatal("oversized preparation copied/hashed a large prefix", result.AllocedBytesPerOp())
	}
}

func TestInventoryAcceptRequestCannotExecuteFacade(t *testing.T) {
	request, identity := inventoryRequestFixture()
	prepared, err := newInventoryAcceptCommand(identity, request)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []*Writer{nil, {}} {
		result, err := w.AcceptInventory(context.Background(), nil, prepared)
		if !errors.Is(err, ErrNotImplemented) || !reflect.DeepEqual(result, InventoryResult{}) {
			t.Fatal("domain request bypassed contract stub", result, err)
		}
	}
}

type inventoryWrappedCommand struct{ InventoryAcceptCommand }
type inventoryPanicMarker struct{}

func (*inventoryPanicMarker) isInventoryAcceptCommand() { panic("marker must not be invoked") }

func TestInventoryAcceptRequestReferenceRejectsWrappers(t *testing.T) {
	request, identity := inventoryRequestFixture()
	prepared, err := newInventoryAcceptCommand(identity, request)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := prepared.receiptRequest()
	if got, err := inventoryAcceptReference(prepared); err != nil || got != want {
		t.Fatal("reference is not the saved original identity/digest", err)
	}
	for _, input := range []InventoryAcceptCommand{nil, (*inventoryAcceptCommand)(nil), &inventoryAcceptCommand{},
		&inventoryWrappedCommand{prepared}, &inventoryWrappedCommand{}, &inventoryPanicMarker{}} {
		if got, err := inventoryAcceptReference(input); !errors.Is(err, ErrInvalid) || got != (ScanReceiptRequest{}) {
			t.Fatalf("non-concrete command yielded reference: %T %v", input, err)
		}
	}
}

func TestInventoryAcceptRequestQueuedHashIndependentOfCaller(t *testing.T) {
	request, identity := inventoryRequestFixture()
	prepared, err := newInventoryAcceptCommand(identity, request)
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() {
		for i := 0; i < 100; i++ {
			digest, err := canonicalRequestDigest(InventoryAccept, inventoryAcceptRequestVersion, identity, prepared.request)
			if err != nil {
				finished <- err
				return
			}
			if digest != prepared.digest {
				finished <- errors.New("queued command changed after constructor returned")
				return
			}
		}
		finished <- nil
	}()
	for i := 0; i < 100; i++ {
		request.source.resolvedRoot[0] ^= 1
		request.files[0].title = strings.Repeat("x", i+1)
		request.files[0].revision = strings.Repeat("y", i+1)
	}
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
}

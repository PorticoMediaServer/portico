package writer

import (
	"bytes"
	"errors"
	"reflect"
	"testing"
)

func inventoryPublicInput(t *testing.T) (InventoryAcceptInput, CommandIdentity) {
	t.Helper()
	r, id := inventoryRequestFixture()
	f := r.files[0]
	return InventoryAcceptInput{
		Scope:     InventoryScopeInput{r.scope.runID, r.scope.libraryKey, r.scope.libraryID},
		Source:    InventorySourceInput{r.source.id, r.source.incarnation, r.source.rootIdentity, bytes.Clone(r.source.resolvedRoot), r.source.generation, r.source.followSymlinks},
		Run:       InventoryRunInput{r.run.revision, r.run.policyRevision, r.run.leaseOwner, r.run.leaseGeneration, r.run.leaseExpiresMS, r.run.lastSequence, r.run.acknowledgedSequence, r.run.phase, r.run.scanMode},
		Directory: InventoryDirectoryInput{[]byte(r.directory.path), r.directory.identity, r.directory.revision, r.directory.expectedCursor, r.directory.nextCursor, r.directory.cursorKind, r.directory.pages, r.directory.skipped, r.directory.rawNames, r.directory.complete, r.directory.unstable},
		Files: []InventoryFilenameInput{{RelativePath: []byte(f.relativePath), AssetPath: []byte(f.assetPath), SourceKey: f.sourceKey, ObjectIdentity: f.objectIdentity, Revision: f.revision, ChangeToken: f.changeToken, Directory: f.directory, Size: f.size, ModifiedNS: f.modifiedNS,
			Title: f.title, SortTitle: f.sortTitle, SortKey: f.sortKey, SortHead: f.sortHead, SortKeyKeepArticles: f.sortKeyKeepArticles, SortHeadKeepArticles: f.sortHeadKeepArticles, Year: f.year, Part: f.part, MetadataLanguage: f.metadataLanguage}},
	}, id
}

func TestInventoryPublicPreparationMatchesPrivateAndFreezes(t *testing.T) {
	in, id := inventoryPublicInput(t)
	value, err := NewInventoryAcceptCommand(id, in)
	if err != nil {
		t.Fatal(err)
	}
	c := value.(*inventoryAcceptCommand)
	r, _ := inventoryRequestFixture()
	want, err := newInventoryAcceptCommand(id, r)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c, want) {
		t.Fatal("public projection changed original private request/wire")
	}
	ref, err := InventoryAcceptReference(value)
	if err != nil || ref.Receipt.Identity != id || ref.Receipt.RequestDigest != want.digest {
		t.Fatal(ref, err)
	}
	in.Source.ResolvedRoot[0] = 'X'
	in.Source.ID = "mutated"
	in.Files[0].Title = "mutated"
	in.Files[0].Revision = "mutated"
	in.Directory.Path[0] = 'X'
	in.Files[0].RelativePath[0] = 'X'
	in.Files[0].AssetPath[0] = 'X'
	if !reflect.DeepEqual(c, want) {
		t.Fatal("public input remained mutable")
	}
	ref.Receipt.Identity.Sequence++
	got, err := InventoryAcceptReference(value)
	if err != nil || got.Receipt.Identity != id {
		t.Fatal("reference mutated command", got, err)
	}
}

func TestInventoryPublicPreparationNativePathBytes(t *testing.T) {
	in, id := inventoryPublicInput(t)
	in.Files[0].RelativePath = []byte("\u96ea\ufffd.mkv")
	in.Files[0].AssetPath = append([]byte("/media/"), in.Files[0].RelativePath...)
	c, err := NewInventoryAcceptCommand(id, in)
	if err != nil {
		t.Fatal(err)
	}
	p := c.(*inventoryAcceptCommand)
	if !bytes.Equal([]byte(p.request.files[0].relativePath), in.Files[0].RelativePath) || !bytes.Equal([]byte(p.request.files[0].assetPath), in.Files[0].AssetPath) {
		t.Fatal("valid native UTF-8 changed")
	}
	// A literal U+FFFD is carried exactly; invalid native bytes never become it.
	in.Source.ResolvedRoot = []byte{'/', 0xff}
	c, err = NewInventoryAcceptCommand(id, in)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(c.(*inventoryAcceptCommand).request.source.resolvedRoot, in.Source.ResolvedRoot) {
		t.Fatal("BLOB root was replaced")
	}
	in.Source.ResolvedRoot = []byte{'/', 0, 0xff}
	if c, err = NewInventoryAcceptCommand(id, in); c != nil || !errors.Is(err, ErrInvalid) || errors.Is(err, ErrFilenameUnrepresentable) {
		t.Fatal("NUL root accepted or misclassified", c, err)
	}
}

func TestInventoryPublicPreparationUnrepresentableCause(t *testing.T) {
	inventoryPublicPathCause(t, []byte{'a', 0xff, 'b'}, ErrFilenameUnrepresentable)
	inventoryPublicPathCause(t, []byte{'a', 0xfe, 'b'}, ErrFilenameUnrepresentable)
}

func TestInventoryPublicPreparationNULCause(t *testing.T) {
	inventoryPublicPathCause(t, []byte{'a', 0, 'b'}, ErrInvalid)
	inventoryPublicPathCause(t, []byte{'a', 0, 0xff, 'b'}, ErrInvalid)
	// An unsafe root must not be hidden by an unrepresentable leaf.
	in, id := inventoryPublicInput(t)
	in.Source.ResolvedRoot = []byte{'/', 0, 'x'}
	in.Files[0].RelativePath = []byte{'a', 0xff}
	if c, err := NewInventoryAcceptCommand(id, in); c != nil || !errors.Is(err, ErrInvalid) || errors.Is(err, ErrFilenameUnrepresentable) {
		t.Fatal("NUL root misclassified as filename refusal", c, err)
	}
}

func inventoryPublicPathCause(t *testing.T, raw []byte, want error) {
	t.Helper()
	for _, field := range []string{"directory", "relative", "asset"} {
		t.Run(field, func(t *testing.T) {
			in, id := inventoryPublicInput(t)
			switch field {
			case "directory":
				in.Directory.Path = raw
			case "relative":
				in.Files[0].RelativePath = raw
			case "asset":
				in.Files[0].AssetPath = raw
			}
			before := bytes.Clone(raw)
			c, err := NewInventoryAcceptCommand(id, in)
			if c != nil || !errors.Is(err, want) {
				t.Fatalf("native %s %x produced command/error %v/%v", field, raw, c, err)
			}
			other := ErrInvalid
			if want == ErrInvalid {
				other = ErrFilenameUnrepresentable
			}
			if errors.Is(err, other) {
				t.Fatal("native causes collapsed", err)
			}
			if !bytes.Equal(raw, before) {
				t.Fatal("native refusal mutated input")
			}
		})
	}
}

func TestInventoryPublicPreparationEveryFieldBinds(t *testing.T) {
	base, id := inventoryPublicInput(t)
	c, err := NewInventoryAcceptCommand(id, base)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := InventoryAcceptReference(c)
	leaves := 0
	inventoryTestLeaves(reflect.ValueOf(&base).Elem(), nil, func(path []int, _ reflect.Value) {
		leaves++
		in, _ := inventoryPublicInput(t)
		field := inventoryTestAt(reflect.ValueOf(&in).Elem(), path)
		switch field.Kind() {
		case reflect.String:
			field.SetString(field.String() + "changed")
		case reflect.Int64:
			field.SetInt(field.Int() + 1)
		case reflect.Uint8:
			field.SetUint(field.Uint() ^ 1)
		case reflect.Bool:
			field.SetBool(!field.Bool())
		default:
			t.Fatal(field.Kind())
		}
		value, err := NewInventoryAcceptCommand(id, in)
		if err != nil {
			if value != nil {
				t.Fatal("error returned a command")
			}
			return
		}
		got, err := InventoryAcceptReference(value)
		if err != nil || got.Receipt.RequestDigest == want.Receipt.RequestDigest {
			t.Fatal("omitted public field", path, err)
		}
	})
	if leaves < 40 {
		t.Fatal("incomplete public input walk", leaves)
	}
}

type inventoryPublicWrapper struct{ InventoryAcceptCommand }

func TestInventoryPublicPreparationClosedAndBounded(t *testing.T) {
	for _, n := range []int{0, 2} {
		in, id := inventoryPublicInput(t)
		in.Files = make([]InventoryFilenameInput, n)
		if c, err := NewInventoryAcceptCommand(id, in); err == nil || c != nil {
			t.Fatal("unsupported batch", n, c, err)
		}
	}
	in, id := inventoryPublicInput(t)
	in.Source.ResolvedRoot = make([]byte, maxCanonicalRequestBytes)
	if c, err := NewInventoryAcceptCommand(id, in); err == nil || c != nil {
		t.Fatal("oversized native root", c, err)
	}
	in, id = inventoryPublicInput(t)
	c, err := NewInventoryAcceptCommand(id, in)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []InventoryAcceptCommand{nil, (*inventoryAcceptCommand)(nil), inventoryPublicWrapper{c}} {
		if ref, err := InventoryAcceptReference(bad); err == nil || ref != (ScanReceiptRequest{}) {
			t.Fatal("nonconcrete command reference", ref, err)
		}
	}
}

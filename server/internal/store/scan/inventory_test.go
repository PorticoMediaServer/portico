package scan

import (
	"errors"
	"strconv"
	"strings"
	"testing"
)

func observation() FileObservation {
	return FileObservation{
		Witness: FileWitness{
			Source:         SourceWitness{LibraryID: 1, SourceID: "source", Incarnation: "000abc", Revision: "rev:09"},
			RelativePath:   "Arrival (2016)/Arrival.2016.1080p.mkv",
			ObjectIdentity: "device:inode", Revision: "object:007", Size: 42, ModifiedNS: -1,
		},
		SourceKey: "existing:key:0007",
	}
}

func TestInventoryFilenamePreparation(t *testing.T) {
	f := observation()
	files := []FileObservation{f}
	b, err := PrepareInventory(f.Witness.Source, "movie", files, InventoryBudget{Files: 1, Bytes: 4096})
	if err != nil {
		t.Fatal(err)
	}
	got, ok := b.File(0)
	if !ok || got.Title != "Arrival" || got.Year != 2016 || got.FileObservation != f || b.Len() != 1 {
		t.Fatalf("filename candidate = %+v, count %d", got, b.Len())
	}
	// Queue input remains sealed even if the caller reuses its inventory buffer.
	files[0].SourceKey = "overwritten"
	files[0].Witness.Size = 1234
	got.Title = "overwritten"
	got, _ = b.File(0)
	if got.SourceKey != f.SourceKey || got.Witness != f.Witness || got.Title != "Arrival" {
		t.Fatal("prepared input aliases mutable caller state")
	}
	for _, i := range []int{-1, 1} {
		if _, ok := b.File(i); ok {
			t.Fatalf("out of range File(%d) succeeded", i)
		}
	}
	// The exact payload boundary includes the generated title; failure cannot
	// return a prefix that the caller could mistake for a complete batch.
	for _, n := range []int64{b.Bytes() - 1, b.Bytes()} {
		out, e := PrepareInventory(f.Witness.Source, "movie", []FileObservation{f}, InventoryBudget{Files: 1, Bytes: n})
		if n < b.Bytes() {
			if !errors.Is(e, ErrCapacity) || out.Len() != 0 {
				t.Fatalf("under-budget result = %d, %v", out.Len(), e)
			}
		} else if e != nil || out.Bytes() != n {
			t.Fatalf("exact budget = %d, %v", out.Bytes(), e)
		}
	}
}

func TestInventoryLibraryIsolation(t *testing.T) {
	a := observation()
	b := a
	b.Witness.Source.LibraryID++
	budget := InventoryBudget{Files: 2, Bytes: 8192}
	for _, f := range []FileObservation{a, b} {
		out, err := PrepareInventory(f.Witness.Source, "movie", []FileObservation{f}, budget)
		got, _ := out.File(0)
		if err != nil || got.Witness.Source.LibraryID != f.Witness.Source.LibraryID || got.SourceKey != f.SourceKey {
			t.Fatalf("overlapping library input = %+v, %v", got, err)
		}
	}
	if _, err := PrepareInventory(a.Witness.Source, "movie", []FileObservation{a, b}, budget); !errors.Is(err, ErrInvalid) {
		t.Fatalf("cross-library batch accepted: %v", err)
	}
	// Separate paths may share a physical inode. Preparation never folds their
	// library-scoped identities into one global physical-file identity.
	b = a
	b.Witness.RelativePath = "Another.mkv"
	b.SourceKey = "another:key"
	if out, err := PrepareInventory(a.Witness.Source, "movie", []FileObservation{a, b}, budget); err != nil || out.Len() != 2 {
		t.Fatalf("distinct path observations = %d, %v", out.Len(), err)
	}
}

func TestInventoryRejectsInvalidOrOverBudgetWholeBatch(t *testing.T) {
	for i, p := range []string{"", ".", "../a.mkv", "/a.mkv", "a/../b.mkv", "a//b.mkv", "a\\b.mkv", "a\x00.mkv", strings.Repeat("x/", 128) + "a.mkv", strings.Repeat("a", 16385)} {
		t.Run("path_"+strconv.Itoa(i), func(t *testing.T) {
			f := observation()
			f.Witness.RelativePath = p
			out, err := PrepareInventory(f.Witness.Source, "movie", []FileObservation{f}, InventoryBudget{Files: 2, Bytes: 65536})
			if !errors.Is(err, ErrInvalid) || out.Len() != 0 {
				t.Fatalf("invalid path accepted: %v", err)
			}
		})
	}
	a := observation()
	for _, change := range []struct {
		name string
		edit func(*FileObservation)
	}{
		{"negative size", func(f *FileObservation) { f.Witness.Size = -1 }},
		{"missing object revision", func(f *FileObservation) { f.Witness.Revision = "" }},
		{"missing identity key", func(f *FileObservation) { f.SourceKey = "" }},
		{"stale source", func(f *FileObservation) { f.Witness.Source.Revision += ":changed" }},
		{"duplicate path", func(f *FileObservation) { f.SourceKey += ":other" }},
		{"duplicate key", func(f *FileObservation) { f.Witness.RelativePath = "Another.mkv" }},
	} {
		t.Run(change.name, func(t *testing.T) {
			b := a
			change.edit(&b)
			out, err := PrepareInventory(a.Witness.Source, "movie", []FileObservation{a, b}, InventoryBudget{Files: 2, Bytes: 8192})
			if !errors.Is(err, ErrInvalid) || out.Len() != 0 {
				t.Fatalf("invalid tail returned a prefix: %d, %v", out.Len(), err)
			}
		})
	}
	for _, budget := range []InventoryBudget{{}, {Files: 1}, {Bytes: 4096}, {Files: -1, Bytes: 4096}} {
		if _, err := PrepareInventory(a.Witness.Source, "movie", []FileObservation{a}, budget); !errors.Is(err, ErrCapacity) {
			t.Fatalf("invalid budget accepted: %v", err)
		}
	}
	if _, err := PrepareInventory(a.Witness.Source, "movie", []FileObservation{a, a}, InventoryBudget{Files: 1, Bytes: 8192}); !errors.Is(err, ErrCapacity) {
		t.Fatalf("raw count was not bounded: %v", err)
	}
}

func TestInventoryLocalNamingKinds(t *testing.T) {
	for _, kind := range []string{"movie", "tv", "anime", "music", "audiobook", "book", "comic", "magazine", "newspaper"} {
		f := observation()
		f.Witness.Size = 0
		f.Witness.ObjectIdentity = "" // adapter without stable inode support
		out, err := PrepareInventory(f.Witness.Source, kind, []FileObservation{f}, InventoryBudget{Files: 1, Bytes: 4096})
		if err != nil || out.Len() != 1 {
			t.Fatalf("%s: %d, %v", kind, out.Len(), err)
		}
	}
	f := observation()
	for _, source := range []SourceWitness{{}, {LibraryID: 1}, {LibraryID: 1, SourceID: "s", Incarnation: "i"}} {
		if _, err := PrepareInventory(source, "movie", []FileObservation{f}, InventoryBudget{Files: 1, Bytes: 4096}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("incomplete source accepted: %v", err)
		}
	}
	if _, err := PrepareInventory(f.Witness.Source, "unknown", []FileObservation{f}, InventoryBudget{Files: 1, Bytes: 4096}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown kind accepted: %v", err)
	}
}

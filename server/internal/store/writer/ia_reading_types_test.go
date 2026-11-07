package writer

import (
	"crypto/sha256"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/naming"
	"reflect"
	"strings"
	"testing"
)

func iaReadingFixture() (iaReadingInventoryRequest, CommandIdentity) {
	old, id := inventoryRequestFixture()
	old.files[0].relativePath = "Book.epub"
	old.files[0].assetPath = "/media/Book.epub"
	return iaReadingInventoryRequest{kind: "book", format: "epub", scope: old.scope, source: old.source, run: old.run, directory: old.directory, files: old.files,
		naming: naming.Reading{Titles: []naming.Evidence[string]{{Value: "One", Source: naming.FileName, Path: "Book.epub"}, {Value: "Two", Source: naming.ParentFolder, Path: "."}}, Episodes: []naming.Evidence[[]int]{{Value: []int{1, 2}, Source: naming.FileName}}},
		values: compactcatalog.ReadingValues{Title: "Book", Author: &compactcatalog.ReadingValue{Value: "Author", Source: compactcatalog.ReadingFolder}, SeriesNumber: &compactcatalog.ReadingNumber{Value: 2, Source: compactcatalog.ReadingFolder}}}, id
}
func TestIAReadingPreparationFamiliesAndFreeze(t *testing.T) {
	for _, family := range []string{"ebook", "comic", "magazine", "newspaper"} {
		t.Run(family, func(t *testing.T) {
			r, id := iaReadingFixture()
			if family == "comic" {
				r.kind = "comic"
				r.format = "cbz"
				r.files[0].relativePath = "Book.cbz"
				r.files[0].assetPath = "/media/Book.cbz"
			}
			if family == "magazine" || family == "newspaper" {
				r.values.PublicationType = &compactcatalog.ReadingValue{Value: family, Source: compactcatalog.ReadingFileMetadata}
			}
			c, err := newIAReadingInventoryCommand(id, r)
			if err != nil {
				t.Fatal(err)
			}
			r.source.resolvedRoot[1] = 'X'
			r.files[0].revision = "changed"
			r.naming.Titles[1].Value = "changed"
			r.naming.Episodes[0].Value[1] = 99
			r.values.Author.Value = "changed"
			r.values.SeriesNumber.Value = 99
			if c.request.naming.Titles[1].Value != "Two" || c.request.naming.Episodes[0].Value[1] != 2 || c.request.values.Author.Value != "Author" || c.request.values.SeriesNumber.Value != 2 || c.request.values.IssueDate != nil {
				t.Fatal("lost originals")
			}
			if err := c.checkOriginal(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestIAReadingPreparationRefusals(t *testing.T) {
	cases := []func(*iaReadingInventoryRequest){
		func(r *iaReadingInventoryRequest) { r.files = nil },
		func(r *iaReadingInventoryRequest) { r.naming.Titles = make([]naming.Evidence[string], 65) },
		func(r *iaReadingInventoryRequest) { r.naming.Episodes[0].Value = make([]int, 9) },
		func(r *iaReadingInventoryRequest) { r.values.Author.Value = "\xff" },
		func(r *iaReadingInventoryRequest) { r.values.Author.Source = "x\x00" },
		func(r *iaReadingInventoryRequest) { r.source.resolvedRoot = []byte("/\xff") },
		func(r *iaReadingInventoryRequest) { r.source.id = "" },
		func(r *iaReadingInventoryRequest) { r.source.rootIdentity = "" },
		func(r *iaReadingInventoryRequest) { r.run.leaseGeneration = 0 },
		func(r *iaReadingInventoryRequest) { r.run.lastSequence++ },
		func(r *iaReadingInventoryRequest) { r.directory.cursorKind = "other" },
		func(r *iaReadingInventoryRequest) { r.directory.path = "other" },
		func(r *iaReadingInventoryRequest) { r.files[0].assetPath = "/other/Book.epub" },
		func(r *iaReadingInventoryRequest) { r.files[0].relativePath = "a/../Book.epub" },
		func(r *iaReadingInventoryRequest) { r.format = "pdf" },
		func(r *iaReadingInventoryRequest) { r.kind = "magazine" },
		func(r *iaReadingInventoryRequest) {
			r.values.PublicationType = &compactcatalog.ReadingValue{Value: "serial"}
		},
	}
	for i, m := range cases {
		r, id := iaReadingFixture()
		m(&r)
		if _, err := newIAReadingInventoryCommand(id, r); err == nil {
			t.Fatalf("accepted %d", i)
		}
	}
}
func TestIAReadingPreparationDrift(t *testing.T) {
	cases := []func(*iaReadingInventoryRequest){
		func(r *iaReadingInventoryRequest) { r.values.Author.Value += "x" },
		func(r *iaReadingInventoryRequest) { r.values.Author.Source = compactcatalog.ReadingFileName },
		func(r *iaReadingInventoryRequest) { r.values.SeriesNumber.Value++ },
		func(r *iaReadingInventoryRequest) { r.source.generation++ },
		func(r *iaReadingInventoryRequest) { r.run.policyRevision++ },
		func(r *iaReadingInventoryRequest) { r.directory.nextCursor += "x" },
		func(r *iaReadingInventoryRequest) { r.files[0].changeToken += "x" },
		func(r *iaReadingInventoryRequest) { r.naming.Titles[1].Confidence = .7 },
		func(r *iaReadingInventoryRequest) {
			r.values.IssueDate = &compactcatalog.ReadingValue{Value: "2026-11", Source: compactcatalog.ReadingFolder}
		},
	}
	for i, m := range cases {
		r, id := iaReadingFixture()
		c, err := newIAReadingInventoryCommand(id, r)
		if err != nil {
			t.Fatal(err)
		}
		m(&c.request)
		if !errors.Is(c.checkOriginal(), ErrConflict) {
			t.Fatalf("unbound %d", i)
		}
	}
}
func TestIAReadingPreparationEnvelope(t *testing.T) {
	r, id := iaReadingFixture()
	var size uint64
	iaAudioWireSize(reflect.ValueOf(r), &size)
	e := requestEncoder{h: sha256.New(), path: make(map[uintptr]bool)}
	e.value(reflect.ValueOf(r), 0)
	if e.err != nil || size != e.written {
		t.Fatalf("wire size %d != %d: %v", size, e.written, e.err)
	}
	// Fill precisely to the complete envelope ceiling; framing counts too.
	overhead := uint64(len(canonicalRequestCodec) + 4 + 16 + 8 + 16)
	r.naming.Diagnostics = []string{""}
	size = 0
	iaAudioWireSize(reflect.ValueOf(r), &size)
	r.naming.Diagnostics[0] = strings.Repeat("x", int(maxCanonicalRequestBytes-size-overhead))
	if _, err := newIAReadingInventoryCommand(id, r); err != nil {
		t.Fatal(err)
	}
	r.naming.Diagnostics[0] += "x"
	if _, err := newIAReadingInventoryCommand(id, r); !errors.Is(err, ErrCap) {
		t.Fatalf("complete envelope: %v", err)
	}
	// Independent aggregate byte control with all collections below their caps.
	r, id = iaReadingFixture()
	r.naming.Titles[0].Value = strings.Repeat("x", 17<<20)
	r.naming.Diagnostics = []string{strings.Repeat("x", 16<<20)}
	if _, err := newIAReadingInventoryCommand(id, r); !errors.Is(err, ErrCap) {
		t.Fatalf("aggregate: %v", err)
	}
	// Count refusal wins even with oversized, malformed text, before text work.
	r.naming.Episodes[0].Value = make([]int, 9)
	r.naming.Titles[0].Value = "\xff" + r.naming.Titles[0].Value
	if _, err := newIAReadingInventoryCommand(id, r); err == nil || errors.Is(err, ErrCap) {
		t.Fatalf("count preflight: %v", err)
	}
}

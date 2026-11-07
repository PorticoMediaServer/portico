package writer

import (
	"crypto/sha256"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/naming"
)

func iaAudioFixture() (iaAudioInventoryRequest, CommandIdentity) {
	old, id := inventoryRequestFixture()
	return iaAudioInventoryRequest{kind: "audiobook", scope: old.scope, source: old.source, run: old.run, directory: old.directory, files: old.files,
		naming: naming.Read("Author/Series/Book 2 - Title/01.m4b", "audiobook"),
		book:   naming.AudiobookFolder{Title: "Title", Author: "Author", Series: "Series", Sequence: "2", Year: "2001", ASIN: "B0015T963C", Narrators: []string{"First", "Second"}}}, id
}
func TestIAAudioPreparationBounds(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*iaAudioInventoryRequest)
	}{
		{"files", func(r *iaAudioInventoryRequest) { r.files = append(r.files, r.files[0]) }},
		{"artists", func(r *iaAudioInventoryRequest) { r.naming.Artists = make([]naming.Evidence[string], 65) }},
		{"nested", func(r *iaAudioInventoryRequest) {
			r.naming.Episodes = []naming.Evidence[[]int]{{Value: make([]int, 65)}}
		}},
		{"cue", func(r *iaAudioInventoryRequest) { r.cue = &iaAudioCue{children: make([]iaAudioCueChild, 17)} }},
		{"aggregate", func(r *iaAudioInventoryRequest) {
			r.source.resolvedRoot = []byte("/" + strings.Repeat("r", 17<<20))
			r.book.Author = strings.Repeat("a", 16<<20)
		}},
		{"framing", func(r *iaAudioInventoryRequest) { r.book.Title = strings.Repeat("a", maxCanonicalRequestBytes-2048) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, id := iaAudioFixture()
			tc.change(&r)
			if _, err := newIAAudioInventoryCommand(id, r); err == nil {
				t.Fatal("accepted oversized request")
			}
		})
	}
}
func TestIAAudioPreparationOriginalBinding(t *testing.T) {
	mutations := []struct {
		name   string
		change func(*iaAudioInventoryRequest)
	}{
		{"root", func(r *iaAudioInventoryRequest) { r.source.resolvedRoot[1] ^= 1 }},
		{"incarnation", func(r *iaAudioInventoryRequest) { r.source.incarnation += "x" }},
		{"generation", func(r *iaAudioInventoryRequest) { r.source.generation++ }},
		{"root identity", func(r *iaAudioInventoryRequest) { r.source.rootIdentity += "x" }},
		{"run policy", func(r *iaAudioInventoryRequest) { r.run.policyRevision++ }},
		{"run revision", func(r *iaAudioInventoryRequest) { r.run.revision++ }},
		{"lease", func(r *iaAudioInventoryRequest) { r.run.leaseGeneration++ }},
		{"lease owner", func(r *iaAudioInventoryRequest) { r.run.leaseOwner++ }},
		{"lease expiry", func(r *iaAudioInventoryRequest) { r.run.leaseExpiresMS++ }},
		{"last", func(r *iaAudioInventoryRequest) { r.run.lastSequence++ }},
		{"ack", func(r *iaAudioInventoryRequest) { r.run.acknowledgedSequence++ }},
		{"expected", func(r *iaAudioInventoryRequest) { r.directory.expectedCursor += "x" }},
		{"next", func(r *iaAudioInventoryRequest) { r.directory.nextCursor += "x" }},
		{"file revision", func(r *iaAudioInventoryRequest) { r.files[0].revision += "x" }},
		{"file part", func(r *iaAudioInventoryRequest) { r.files[0].part++ }},
	}
	for _, tc := range mutations {
		t.Run(tc.name, func(t *testing.T) {
			r, id := iaAudioFixture()
			c, err := newIAAudioInventoryCommand(id, r)
			if err != nil {
				t.Fatal(err)
			}
			tc.change(&c.request)
			if !errors.Is(c.checkOriginal(), ErrConflict) {
				t.Fatal("original mutation unbound")
			}
		})
	}
}
func TestIAAudioPreparationFrozenEvidence(t *testing.T) {
	r, id := iaAudioFixture()
	r.naming.Artists = []naming.Evidence[string]{{Value: "one", Source: naming.FileName}, {Value: "two", Source: naming.ParentFolder}}
	r.naming.Episodes = []naming.Evidence[[]int]{{Value: []int{1, 2}, Source: naming.FileName}}
	c, err := newIAAudioInventoryCommand(id, r)
	if err != nil {
		t.Fatal(err)
	}
	r.source.resolvedRoot[1] = 'X'
	r.files[0].changeToken = "changed"
	r.book.Narrators[1] = "changed"
	r.naming.Artists[1].Value = "changed"
	r.naming.Episodes[0].Value[1] = 99
	if err := c.checkOriginal(); err != nil {
		t.Fatal(err)
	}
	if c.request.book.Narrators[1] != "Second" || c.request.naming.Artists[1].Value != "two" || c.request.naming.Episodes[0].Value[1] != 2 {
		t.Fatal("lost alternative evidence")
	}
}
func TestIAAudioPreparationBookFields(t *testing.T) {
	for _, field := range []string{"title", "author", "series", "sequence", "year", "asin", "narrator"} {
		t.Run(field, func(t *testing.T) {
			r, id := iaAudioFixture()
			a, err := newIAAudioInventoryCommand(id, r)
			if err != nil {
				t.Fatal(err)
			}
			switch field {
			case "title":
				r.book.Title += "x"
			case "author":
				r.book.Author += "x"
			case "series":
				r.book.Series += "x"
			case "sequence":
				r.book.Sequence += "x"
			case "year":
				r.book.Year += "x"
			case "asin":
				r.book.ASIN += "x"
			case "narrator":
				r.book.Narrators[1] += "x"
			}
			b, err := newIAAudioInventoryCommand(id, r)
			if err != nil {
				t.Fatal(err)
			}
			if a.digest == b.digest {
				t.Fatal("field omitted")
			}
		})
	}
	r, id := iaAudioFixture()
	r.kind = "music"
	if _, err := newIAAudioInventoryCommand(id, r); err == nil {
		t.Fatal("ambiguous music/book accepted")
	}
	r.book = naming.AudiobookFolder{}
	if _, err := newIAAudioInventoryCommand(id, r); err != nil {
		t.Fatal(err)
	}
	r.kind = "book"
	if _, err := newIAAudioInventoryCommand(id, r); err == nil {
		t.Fatal("open variant")
	}
}
func iaAudioCueFixture() (iaAudioInventoryRequest, CommandIdentity) {
	r, id := iaAudioFixture()
	f := r.files[0]
	f.relativePath = "album.cue"
	f.objectIdentity = "cue-object"
	f.revision = "cue-rev"
	f.assetPath = "/media/album.cue"
	end := 3.0
	r.cue = &iaAudioCue{complete: true, file: f, children: []iaAudioCueChild{{ordinal: 1, part: 7, start: 0, end: &end}, {ordinal: 2, part: 3, start: 3}}}
	return r, id
}
func TestIAAudioPreparationCueCohort(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*iaAudioInventoryRequest)
		bad    bool
	}{
		{"cue revision", func(r *iaAudioInventoryRequest) { r.cue.file.revision += "x" }, false},
		{"child part", func(r *iaAudioInventoryRequest) { r.cue.children[1].part++ }, false},
		{"child start", func(r *iaAudioInventoryRequest) { r.cue.children[1].start++ }, false},
		{"order", func(r *iaAudioInventoryRequest) {
			r.cue.children[0], r.cue.children[1] = r.cue.children[1], r.cue.children[0]
		}, true},
		{"tail nan", func(r *iaAudioInventoryRequest) { r.cue.children[1].start = math.NaN() }, true},
		{"tail end", func(r *iaAudioInventoryRequest) { v := 2.0; r.cue.children[1].end = &v }, true},
		{"overlap", func(r *iaAudioInventoryRequest) { r.cue.children[1].start = 2 }, true},
		{"missing boundary", func(r *iaAudioInventoryRequest) { r.cue.children[0].end = nil }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, id := iaAudioCueFixture()
			a, err := newIAAudioInventoryCommand(id, r)
			if err != nil {
				t.Fatal(err)
			}
			tc.change(&r)
			b, err := newIAAudioInventoryCommand(id, r)
			if tc.bad {
				if err == nil {
					t.Fatal("invalid cohort accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if a.digest == b.digest {
				t.Fatal("cue evidence omitted")
			}
		})
	}
	r, id := iaAudioCueFixture()
	c, err := newIAAudioInventoryCommand(id, r)
	if err != nil {
		t.Fatal(err)
	}
	*r.cue.children[0].end = 999
	r.cue.children[1].title = "mutated"
	r.cue.file.revision = "mutated"
	if err := c.checkOriginal(); err != nil {
		t.Fatal(err)
	}
}

func TestIAAudioPreparationEnvelope(t *testing.T) {
	r, id := iaAudioCueFixture()
	r.naming.Artists = make([]naming.Evidence[string], 64)
	for i := range r.naming.Artists {
		r.naming.Artists[i] = naming.Evidence[string]{Value: "artist", Source: naming.FileName, Path: "album/track.flac"}
	}
	r.cue.children = make([]iaAudioCueChild, 16)
	for i := range r.cue.children {
		end := float64(i + 1)
		r.cue.children[i] = iaAudioCueChild{ordinal: int64(i + 1), part: int64(16 - i), start: float64(i), end: &end}
	}
	if _, err := newIAAudioInventoryCommand(id, r); err != nil {
		t.Fatal(err)
	}
	var size uint64
	iaAudioWireSize(reflect.ValueOf(r), &size)
	encoder := requestEncoder{h: sha256.New(), path: make(map[uintptr]bool)}
	encoder.value(reflect.ValueOf(r), 0)
	if encoder.err != nil || encoder.written != size {
		t.Fatalf("preflight envelope %d, encoder %d: %v", size, encoder.written, encoder.err)
	}
	r.cue.complete = false
	if _, err := newIAAudioInventoryCommand(id, r); err == nil {
		t.Fatal("incomplete cohort accepted")
	}
}

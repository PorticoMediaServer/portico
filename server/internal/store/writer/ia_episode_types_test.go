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

func iaEpisodeFixture() (iaEpisodeInventoryRequest, CommandIdentity) {
	old, id := inventoryRequestFixture()
	end := 60.0
	return iaEpisodeInventoryRequest{scope: old.scope, source: old.source, run: old.run, directory: old.directory, files: old.files, complete: true,
		naming:   naming.Reading{Titles: []naming.Evidence[string]{{Value: "Show", Source: naming.ParentFolder, Path: "Show"}}, Seasons: []naming.Evidence[int]{{Value: 2, Source: naming.FileName}}, Episodes: []naming.Evidence[[]int]{{Value: []int{3, 4}, Source: naming.FileName}, {Value: []int{8, 9}, Source: naming.ParentFolder}}, Parts: []naming.Evidence[int]{{Value: 7, Source: naming.FileName}}},
		episodes: []iaEpisodeLink{{season: 2, episode: 3, part: 7, start: 0, end: &end}, {season: 2, episode: 4, part: 9, start: 60}}}, id
}
func TestIAEpisodePreparationFrozenCohort(t *testing.T) {
	r, id := iaEpisodeFixture()
	c, err := newIAEpisodeInventoryCommand(id, r)
	if err != nil {
		t.Fatal(err)
	}
	r.source.resolvedRoot[1] = 'X'
	r.files[0].revision = "changed"
	r.naming.Episodes[1].Value[1] = 99
	r.naming.Titles[0].Value = "changed"
	*r.episodes[0].end = 999
	r.episodes[1].part = 100
	if err := c.checkOriginal(); err != nil {
		t.Fatal(err)
	}
	if c.request.episodes[1].episode != 4 || c.request.episodes[1].part != 9 || c.request.episodes[1].end != nil || *c.request.episodes[0].end != 60 || c.request.naming.Episodes[1].Value[1] != 9 {
		t.Fatal("lost independent facts or alternatives")
	}
}
func TestIAEpisodePreparationRefusals(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*iaEpisodeInventoryRequest)
	}{
		{"incomplete", func(r *iaEpisodeInventoryRequest) { r.complete = false }},
		{"empty", func(r *iaEpisodeInventoryRequest) { r.episodes = nil }},
		{"over8", func(r *iaEpisodeInventoryRequest) { r.episodes = make([]iaEpisodeLink, 9) }},
		{"alternativeOver8", func(r *iaEpisodeInventoryRequest) { r.naming.Episodes[1].Value = make([]int, 9) }},
		{"evidenceCount", func(r *iaEpisodeInventoryRequest) { r.naming.Titles = make([]naming.Evidence[string], 65) }},
		{"fileCount", func(r *iaEpisodeInventoryRequest) { r.files = append(r.files, r.files[0]) }},
		{"utf8", func(r *iaEpisodeInventoryRequest) { r.naming.Titles[0].Value = "\xff" }},
		{"sourceUTF8", func(r *iaEpisodeInventoryRequest) { r.naming.Episodes[1].Source = "\xff" }},
		{"nul", func(r *iaEpisodeInventoryRequest) { r.naming.Episodes[1].Path = "x\x00" }},
		{"rootUTF8", func(r *iaEpisodeInventoryRequest) { r.source.resolvedRoot = []byte("/\xff") }},
		{"nativeRoot", func(r *iaEpisodeInventoryRequest) { r.source.resolvedRoot = []byte(`C:\media`) }},
		{"nativeFile", func(r *iaEpisodeInventoryRequest) { r.files[0].relativePath = `Show\e.mkv` }},
		{"rootMismatch", func(r *iaEpisodeInventoryRequest) { r.source.resolvedRoot = []byte("/different") }},
		{"directory", func(r *iaEpisodeInventoryRequest) { r.directory.path = "other" }},
		{"cursor", func(r *iaEpisodeInventoryRequest) { r.directory.cursorKind = "other" }},
		{"lease", func(r *iaEpisodeInventoryRequest) { r.run.leaseGeneration = 0 }},
		{"sequence", func(r *iaEpisodeInventoryRequest) { r.run.lastSequence++ }},
		{"duplicate", func(r *iaEpisodeInventoryRequest) { r.episodes[1] = r.episodes[0] }},
		{"range", func(r *iaEpisodeInventoryRequest) { v := 0.0; r.episodes[0].end = &v }},
		{"nan", func(r *iaEpisodeInventoryRequest) { r.episodes[1].start = math.NaN() }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, id := iaEpisodeFixture()
			tc.mutate(&r)
			if _, err := newIAEpisodeInventoryCommand(id, r); err == nil {
				t.Fatal("accepted malformed original")
			}
		})
	}
}
func TestIAEpisodePreparationOriginalBinding(t *testing.T) {
	mutations := []func(*iaEpisodeInventoryRequest){
		func(r *iaEpisodeInventoryRequest) { r.source.rootIdentity += "x" }, func(r *iaEpisodeInventoryRequest) { r.source.generation++ }, func(r *iaEpisodeInventoryRequest) { r.source.incarnation += "x" }, func(r *iaEpisodeInventoryRequest) { r.source.resolvedRoot[1] = 'X' },
		func(r *iaEpisodeInventoryRequest) { r.run.revision++ }, func(r *iaEpisodeInventoryRequest) { r.run.policyRevision++ }, func(r *iaEpisodeInventoryRequest) { r.run.leaseOwner++ }, func(r *iaEpisodeInventoryRequest) { r.run.leaseGeneration++ }, func(r *iaEpisodeInventoryRequest) { r.run.leaseExpiresMS++ }, func(r *iaEpisodeInventoryRequest) { r.directory.expectedCursor += "x" }, func(r *iaEpisodeInventoryRequest) { r.directory.nextCursor += "x" }, func(r *iaEpisodeInventoryRequest) { r.directory.identity += "x" }, func(r *iaEpisodeInventoryRequest) { r.directory.revision += "x" }, func(r *iaEpisodeInventoryRequest) { r.files[0].revision += "x" },
		func(r *iaEpisodeInventoryRequest) { r.episodes[1].season++ }, func(r *iaEpisodeInventoryRequest) { r.episodes[1].episode++ }, func(r *iaEpisodeInventoryRequest) { r.episodes[1].part++ }, func(r *iaEpisodeInventoryRequest) { v := 120.0; r.episodes[1].end = &v }, func(r *iaEpisodeInventoryRequest) { *r.episodes[0].end++ }, func(r *iaEpisodeInventoryRequest) { r.naming.Episodes[1].Value[1]++ }, func(r *iaEpisodeInventoryRequest) { r.naming.Episodes[1].Confidence = .5 }, func(r *iaEpisodeInventoryRequest) { r.scope.libraryID++ },
	}
	for i, m := range mutations {
		r, id := iaEpisodeFixture()
		c, err := newIAEpisodeInventoryCommand(id, r)
		if err != nil {
			t.Fatal(err)
		}
		m(&c.request)
		if !errors.Is(c.checkOriginal(), ErrConflict) {
			t.Fatalf("drift %d unbound", i)
		}
	}
}
func TestIAEpisodePreparationEnvelope(t *testing.T) {
	r, id := iaEpisodeFixture()
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
	if _, err := newIAEpisodeInventoryCommand(id, r); err != nil {
		t.Fatal(err)
	}
	r.naming.Diagnostics[0] += "x"
	if _, err := newIAEpisodeInventoryCommand(id, r); !errors.Is(err, ErrCap) {
		t.Fatalf("complete envelope: %v", err)
	}
	// Independent aggregate byte control with all collections below their caps.
	r, id = iaEpisodeFixture()
	r.naming.Titles[0].Value = strings.Repeat("x", 17<<20)
	r.naming.Diagnostics = []string{strings.Repeat("x", 16<<20)}
	if _, err := newIAEpisodeInventoryCommand(id, r); !errors.Is(err, ErrCap) {
		t.Fatalf("aggregate: %v", err)
	}
	// Count refusal wins even with oversized, malformed text, before text work.
	r.episodes = make([]iaEpisodeLink, 9)
	r.naming.Titles[0].Value = "\xff" + r.naming.Titles[0].Value
	if _, err := newIAEpisodeInventoryCommand(id, r); err == nil || errors.Is(err, ErrCap) {
		t.Fatalf("count preflight: %v", err)
	}
}

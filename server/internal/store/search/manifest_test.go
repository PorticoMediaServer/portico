package search_test

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"os"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/search"
)

type vectorSeed struct {
	Recipe      string                 `json:"recipe_version"`
	Subject     search.ManifestSubject `json:"subject_kind"`
	ID          int64                  `json:"subject_id"`
	Library     *int64                 `json:"library_id"`
	Kind        *int64                 `json:"catalog_kind"`
	Bank        int64                  `json:"search_generation_id"`
	BankKey     string                 `json:"bank_key"`
	Engine      string                 `json:"engine_version"`
	Unicode     string                 `json:"unicode_fingerprint"`
	Input       int64                  `json:"input_generation"`
	Incarnation string                 `json:"source_incarnation_hex"`
	Revision    int64                  `json:"source_revision"`
	Count       int64                  `json:"expected_fragment_count"`
	Bytes       int64                  `json:"expected_prepared_bytes"`
}

func (v vectorSeed) input(t *testing.T) search.ManifestInput {
	t.Helper()
	x := search.ManifestInput{Subject: v.Subject, SubjectID: v.ID, SearchGenerationID: v.Bank, BankKey: v.BankKey, EngineVersion: v.Engine, UnicodeFingerprint: v.Unicode, Recipe: v.Recipe, InputGeneration: v.Input, SourceRevision: v.Revision}
	if v.Library != nil {
		x.LibraryID = *v.Library
	}
	if v.Kind != nil {
		x.Kind = *v.Kind
	}
	b, e := hex.DecodeString(v.Incarnation)
	if e != nil || len(b) != 16 {
		t.Fatal("bad vector incarnation")
	}
	copy(x.SourceIncarnation[:], b)
	return x
}

type vectorRow struct {
	Field      uint16 `json:"field"`
	Ordinal    int64  `json:"ordinal"`
	Revision   string `json:"source_revision"`
	Prefix     string `json:"prefix_tokens"`
	Lexical    string `json:"lexical_tokens"`
	Normalized string `json:"normalized"`
}

func (r vectorRow) row() search.ManifestRow {
	return search.ManifestRow{Field: r.Field, Ordinal: r.Ordinal, Revision: r.Revision, Prefix: r.Prefix, Lexical: r.Lexical, Normalized: r.Normalized}
}

type vectorCase struct {
	Name   string      `json:"name"`
	Seed   *vectorSeed `json:"seed"`
	Rows   []vectorRow `json:"rows"`
	Chain0 string      `json:"chain0_hex"`
	Final  string      `json:"final_chain_hex"`
	Order  bool        `json:"canonical_order"`
	Steps  []struct {
		Leaf  string `json:"leaf_hex"`
		Chain string `json:"chain_hex"`
		Count int64  `json:"verified_count"`
		Bytes int64  `json:"verified_prepared_bytes"`
	} `json:"steps"`
}

func hexHash(x [32]byte) string { return hex.EncodeToString(x[:]) }

func TestManifestIndependentVectorsAndRestart(t *testing.T) {
	var v struct {
		Algorithm string       `json:"algorithm"`
		Positive  []vectorCase `json:"positive"`
		Negative  []vectorCase `json:"negative"`
		Baseline  string       `json:"negative_baseline"`
	}
	b, e := os.ReadFile("testdata/manifest-v1.json")
	if e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(b, &v); e != nil {
		t.Fatal(e)
	}
	if v.Algorithm != search.ManifestAlgorithm || len(v.Positive) != 6 || len(v.Negative) != 9 {
		t.Fatal("wrong vector cohort")
	}
	var baseline vectorSeed
	for _, c := range v.Positive {
		if c.Name == v.Baseline {
			baseline = *c.Seed
		}
	}
	for index, c := range append(v.Positive, v.Negative...) {
		t.Run(c.Name, func(t *testing.T) {
			s := c.Seed
			if s == nil {
				s = &baseline
			}
			in := s.input(t)
			seed, e := search.ManifestSeed(in, s.Count, s.Bytes)
			if e != nil || hexHash(seed) != c.Chain0 {
				t.Fatalf("seed %x %v", seed, e)
			}
			cp := search.ManifestCheckpoint{Chain: seed}
			rawChain := seed
			var rows []search.ManifestRow
			orderErr := false
			for i, r := range c.Rows {
				row := r.row()
				rows = append(rows, row)
				leaf, e := search.ManifestLeaf(row)
				if e != nil || hexHash(leaf) != c.Steps[i].Leaf {
					t.Fatalf("leaf %d %x %v", i, leaf, e)
				}
				rawChain = search.ManifestStep(rawChain, leaf)
				if hexHash(rawChain) != c.Steps[i].Chain {
					t.Fatalf("step %d", i)
				}
				if !orderErr {
					next, e := search.AdvanceManifest(cp, row)
					if e != nil {
						orderErr = true
					} else {
						cp = next
						if cp.Count != c.Steps[i].Count || cp.PreparedBytes != c.Steps[i].Bytes {
							t.Fatal("count/byte checkpoint")
						}
					}
				}
				// Restoring ordinary fields into a new value is the whole resume
				// operation. No encoder-specific hash state exists to serialize.
				cp = search.ManifestCheckpoint{Chain: cp.Chain, Count: cp.Count, PreparedBytes: cp.PreparedBytes, AfterField: cp.AfterField, AfterOrdinal: cp.AfterOrdinal}
			}
			if hexHash(rawChain) != c.Final || orderErr == c.Order {
				t.Fatal("final trace or canonical ordering")
			}
			m, e := search.PrepareManifest(in, rows)
			if c.Order {
				// Missing/extra controls retain the ORIGINAL expected envelope
				// in cp. Preparing a different full stream computes a new seed.
				if e != nil || (m.Seed() == seed) != m.Matches(cp) {
					t.Fatalf("prepared manifest %v", e)
				}
				restored, e := search.RestoreManifest(search.ManifestAlgorithm, m.Seed(), m.Digest(), m.Count(), m.Bytes())
				if e != nil || restored.Matches(cp) != m.Matches(cp) {
					t.Fatal("persisted expected image cannot resume without a whole-image load")
				}
			} else if !errors.Is(e, search.ErrManifest) {
				t.Fatal("bad order accepted")
			}
			if index >= len(v.Positive) {
				if v.Positive[2].Final == c.Final {
					t.Fatal("negative collision")
				}
				baselineSeed, _ := search.ManifestSeed(baseline.input(t), baseline.Count, baseline.Bytes)
				baseRows := []search.ManifestRow{}
				for _, r := range v.Positive[2].Rows {
					baseRows = append(baseRows, r.row())
				}
				base, _ := search.PrepareManifest(baseline.input(t), baseRows)
				if baselineSeed != base.Seed() || base.Matches(cp) {
					t.Fatal("mutated/missing/extra manifest matched original")
				}
			}
		})
	}
}

func TestManifestRejectsInvalidCheckpointAndOverflow(t *testing.T) {
	if (search.Manifest{}).Matches(search.ManifestCheckpoint{}) {
		t.Fatal("zero manifest is not a seal")
	}
	if _, e := search.RestoreManifest("old-whole-stream", [32]byte{}, [32]byte{}, 0, 0); !errors.Is(e, search.ErrManifest) {
		t.Fatal("old algorithm relabeled")
	}
	if _, e := search.RestoreManifest(search.ManifestAlgorithm, [32]byte{}, [32]byte{1}, 0, 0); !errors.Is(e, search.ErrManifest) {
		t.Fatal("zero-row chain differs from seed")
	}
	row := search.ManifestRow{Field: 1, Ordinal: 0, Revision: "09", Normalized: "東京"}
	for _, cp := range []search.ManifestCheckpoint{
		{Count: -1}, {PreparedBytes: -1}, {Count: 1}, {Count: 1, AfterField: 1, AfterOrdinal: 1}, {Count: 0, AfterField: 1},
		{Count: math.MaxInt64, AfterField: 1}, {Count: 1, PreparedBytes: math.MaxInt64, AfterField: 1, AfterOrdinal: 0},
	} {
		next, e := search.AdvanceManifest(cp, row)
		if !errors.Is(e, search.ErrManifest) || next != (search.ManifestCheckpoint{}) {
			t.Fatal("invalid checkpoint advanced")
		}
	}
	for _, r := range []search.ManifestRow{{Field: 0}, {Field: 65}, {Field: 1, Ordinal: -1}, {Field: 1, Ordinal: 1}} {
		_, e := search.AdvanceManifest(search.ManifestCheckpoint{}, r)
		if !errors.Is(e, search.ErrManifest) {
			t.Fatal("invalid first row")
		}
	}
	input := search.ManifestInput{Subject: search.ManifestPerson, SearchGenerationID: 1, InputGeneration: 1, SourceRevision: 1}
	for _, edit := range []func(*search.ManifestInput){func(x *search.ManifestInput) { x.Subject = 0 }, func(x *search.ManifestInput) { x.LibraryID = 1 }, func(x *search.ManifestInput) { x.InputGeneration = 0 }} {
		bad := input
		edit(&bad)
		if _, e := search.ManifestSeed(bad, 0, 0); !errors.Is(e, search.ErrManifest) {
			t.Fatal("invalid seed")
		}
	}
	if _, e := search.ManifestSeed(input, -1, 0); !errors.Is(e, search.ErrManifest) {
		t.Fatal("negative count")
	}
	if _, e := search.ManifestSeed(input, 0, -1); !errors.Is(e, search.ErrManifest) {
		t.Fatal("negative bytes")
	}
}

func TestManifestPreparedFieldsAndMutation(t *testing.T) {
	p, e := search.PrepareFragments(strings.Repeat("a b 東京 ", 400), search.DefaultFragmentBudget())
	if e != nil {
		t.Fatal(e)
	}
	var rows []search.ManifestRow
	for i := 0; i < p.Len(); i++ {
		rows = append(rows, p.At(i).ManifestRow(1, int64(i), "native:\x00revision"))
	}
	in := search.ManifestInput{Subject: search.ManifestEntity, SubjectID: 42, LibraryID: 5, Kind: 1, SearchGenerationID: 1, InputGeneration: 7, SourceRevision: 9, Recipe: search.RecipeVersion}
	m, e := search.PrepareManifest(in, rows)
	if e != nil {
		t.Fatal(e)
	}
	cp := search.ManifestCheckpoint{Chain: m.Seed()}
	for _, r := range rows {
		cp, e = search.AdvanceManifest(cp, r)
		if e != nil {
			t.Fatal(e)
		}
	}
	if !m.Matches(cp) || m.Count() != int64(p.Len()) || m.Bytes() != cp.PreparedBytes {
		t.Fatal("prepared field seal")
	}
	for _, mutate := range []func(*search.ManifestCheckpoint){func(x *search.ManifestCheckpoint) { x.Chain[0] ^= 1 }, func(x *search.ManifestCheckpoint) { x.Count++ }, func(x *search.ManifestCheckpoint) { x.PreparedBytes++ }} {
		bad := cp
		mutate(&bad)
		if m.Matches(bad) {
			t.Fatal("changed checkpoint accepted")
		}
	}
}

package searchmanifest_test

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"testing"

	manifest "github.com/PorticoMediaServer/portico/server/internal/store/internal/searchmanifest"
)

type vectorSeed struct {
	Recipe      string           `json:"recipe_version"`
	Subject     manifest.Subject `json:"subject_kind"`
	ID          int64            `json:"subject_id"`
	Library     *int64           `json:"library_id"`
	Kind        *int64           `json:"catalog_kind"`
	Bank        int64            `json:"search_generation_id"`
	BankKey     string           `json:"bank_key"`
	Engine      string           `json:"engine_version"`
	Unicode     string           `json:"unicode_fingerprint"`
	Input       int64            `json:"input_generation"`
	Incarnation string           `json:"source_incarnation_hex"`
	Revision    int64            `json:"source_revision"`
	Count       int64            `json:"expected_fragment_count"`
	Bytes       int64            `json:"expected_prepared_bytes"`
}

func (v vectorSeed) input(t *testing.T) manifest.Input {
	t.Helper()
	x := manifest.Input{Subject: v.Subject, SubjectID: v.ID, SearchGenerationID: v.Bank, BankKey: v.BankKey, EngineVersion: v.Engine, UnicodeFingerprint: v.Unicode, Recipe: v.Recipe, InputGeneration: v.Input, SourceRevision: v.Revision}
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

func (r vectorRow) row() manifest.Row {
	return manifest.Row{Field: r.Field, Ordinal: r.Ordinal, Revision: r.Revision, Prefix: r.Prefix, Lexical: r.Lexical, Normalized: r.Normalized}
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
	b, e := os.ReadFile("../../search/testdata/manifest-v1.json")
	if e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(b, &v); e != nil {
		t.Fatal(e)
	}
	if v.Algorithm != manifest.Algorithm || len(v.Positive) != 6 || len(v.Negative) != 9 {
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
			seed, e := manifest.Seed(in, s.Count, s.Bytes)
			if e != nil || hexHash(seed) != c.Chain0 {
				t.Fatalf("seed %x %v", seed, e)
			}
			cp := manifest.Checkpoint{Chain: seed}
			rawChain := seed
			var rows []manifest.Row
			orderErr := false
			for i, r := range c.Rows {
				row := r.row()
				rows = append(rows, row)
				leaf, e := manifest.Leaf(row)
				if e != nil || hexHash(leaf) != c.Steps[i].Leaf {
					t.Fatalf("leaf %d %x %v", i, leaf, e)
				}
				rawChain = manifest.Step(rawChain, leaf)
				if hexHash(rawChain) != c.Steps[i].Chain {
					t.Fatalf("step %d", i)
				}
				if !orderErr {
					next, e := manifest.Advance(cp, row)
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
				cp = manifest.Checkpoint{Chain: cp.Chain, Count: cp.Count, PreparedBytes: cp.PreparedBytes, AfterField: cp.AfterField, AfterOrdinal: cp.AfterOrdinal}
			}
			if hexHash(rawChain) != c.Final || orderErr == c.Order {
				t.Fatal("final trace or canonical ordering")
			}
			m, e := manifest.Prepare(in, rows)
			if c.Order {
				// Missing/extra controls retain the ORIGINAL expected envelope
				// in cp. Preparing a different full stream computes a new seed.
				if e != nil || (m.Seed() == seed) != m.Matches(cp) {
					t.Fatalf("prepared manifest %v", e)
				}
				restored, e := manifest.Restore(manifest.Algorithm, m.Seed(), m.Digest(), m.Count(), m.Bytes())
				if e != nil || restored.Matches(cp) != m.Matches(cp) {
					t.Fatal("persisted expected image cannot resume without a whole-image load")
				}
			} else if !errors.Is(e, manifest.ErrManifest) {
				t.Fatal("bad order accepted")
			}
			if index >= len(v.Positive) {
				if v.Positive[2].Final == c.Final {
					t.Fatal("negative collision")
				}
				baselineSeed, _ := manifest.Seed(baseline.input(t), baseline.Count, baseline.Bytes)
				baseRows := []manifest.Row{}
				for _, r := range v.Positive[2].Rows {
					baseRows = append(baseRows, r.row())
				}
				base, _ := manifest.Prepare(baseline.input(t), baseRows)
				if baselineSeed != base.Seed() || base.Matches(cp) {
					t.Fatal("mutated/missing/extra manifest matched original")
				}
			}
		})
	}
}

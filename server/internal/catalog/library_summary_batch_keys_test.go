package catalog

import (
	"context"
	"fmt"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/telemetry"
)

func batchSummaryKeysInSnapshot(t *testing.T, s *Service, viewer Viewer, libraries []Library) (map[string]summaryKey, map[string]telemetry.SummaryCacheEvent) {
	t.Helper()
	var keys map[string]summaryKey
	var reasons map[string]telemetry.SummaryCacheEvent
	if err := dbwork.WithReadSnapshot(context.Background(), s.db, func(ctx context.Context) error {
		var err error
		keys, reasons, err = s.WithContext(ctx).librarySummaryBatchKeys(ctx, viewer, libraries)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return keys, reasons
}

// sameSummaryKeyShape compares the static, policy-derived fields that every
// reason must preserve. Fact-derived fields are only compared when the reason
// is empty; bypassed keys may carry extra raw facts for the caller.
func sameSummaryKeyShape(a, b summaryKey) bool {
	return a.DB == b.DB && a.Namespace == b.Namespace && a.Library == b.Library && a.Kind == b.Kind && a.Policy == b.Policy && a.Algorithm == b.Algorithm && a.Cap == b.Cap && a.Kinds == b.Kinds
}

// batchSummaryKeysParity compares the batch against the current per-library
// function inside the same snapshot. Empty reasons must produce identical keys;
// a bypassed key may additionally carry raw generation binding.
func batchSummaryKeysParity(t *testing.T, s *Service, viewer Viewer, libraries []Library) (map[string]summaryKey, map[string]telemetry.SummaryCacheEvent) {
	t.Helper()
	var keys map[string]summaryKey
	var reasons map[string]telemetry.SummaryCacheEvent
	if err := dbwork.WithReadSnapshot(context.Background(), s.db, func(ctx context.Context) error {
		reader := s.WithContext(ctx)
		var err error
		keys, reasons, err = reader.librarySummaryBatchKeys(ctx, viewer, libraries)
		if err != nil {
			return err
		}
		if len(keys) != len(libraries) {
			return fmt.Errorf("batch returned %d keys for %d libraries", len(keys), len(libraries))
		}
		for _, library := range libraries {
			legacyKey, legacyReason := reader.librarySummaryKey(ctx, viewer, library)
			if got := reasons[library.ID]; got != legacyReason {
				return fmt.Errorf("%s: batch reason %q, legacy %q", library.ID, got, legacyReason)
			}
			key := keys[library.ID]
			if legacyReason == "" {
				if key != legacyKey {
					return fmt.Errorf("%s: batch key %+v, legacy %+v", library.ID, key, legacyKey)
				}
				continue
			}
			if !sameSummaryKeyShape(key, legacyKey) {
				return fmt.Errorf("%s: batch shape %+v, legacy %+v", library.ID, key, legacyKey)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return keys, reasons
}

func TestLibrarySummaryBatchKeysMatchLegacy(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	t.Run("unrestricted_ready", func(t *testing.T) {
		c, s, viewer, _ := readingBrowseFixture(t, "book")
		c.Library("films", "Films", "movie", t.TempDir())
		c.Library("audio", "Audio", "audiobook", t.TempDir())
		c.Drain()
		viewer.Libraries = []string{"reading", "other", "films", "audio"}
		libraries := []Library{{ID: "reading", Kind: "book"}, {ID: "other", Kind: "book"}, {ID: "films", Kind: "movie"}, {ID: "audio", Kind: "audiobook"}}
		keys, reasons := batchSummaryKeysParity(t, s, viewer, libraries)
		for _, library := range libraries {
			if reasons[library.ID] != "" {
				t.Fatalf("%s: unrestricted key bypassed: %s", library.ID, reasons[library.ID])
			}
		}
		if keys["reading"].Reading.Ready < 1 || keys["reading"].Reading.Input != keys["reading"].Reading.Ready {
			t.Fatalf("ready Reading key lost its generation: %+v", keys["reading"].Reading)
		}
		if keys["reading"].Reading.Library == keys["other"].Reading.Library {
			t.Fatal("two Reading libraries share one binding")
		}
		if keys["reading"].Reading.Namespace == keys["other"].Reading.Namespace {
			t.Fatal("two Reading libraries share one generation namespace")
		}
	})

	t.Run("restricted_conservative_bypasses", func(t *testing.T) {
		c, s, viewer, _ := readingBrowseFixture(t, "book")
		c.Library("films", "Films", "movie", t.TempDir())
		c.Library("audio", "Audio", "audiobook", t.TempDir())
		c.Drain()
		viewer.Restrictions = identity.ContentRestrictions{BlockUnrated: true}
		viewer.Libraries = []string{"reading", "other", "films", "audio"}
		libraries := []Library{{ID: "reading", Kind: "book"}, {ID: "other", Kind: "book"}, {ID: "films", Kind: "movie"}, {ID: "audio", Kind: "audiobook"}}
		c.Exec(`INSERT INTO catalog_derivations(domain,version,rebuilding,active_slot,cleaning,first_build) VALUES(18,1,0,0,0,0),(19,1,0,0,0,0) ON CONFLICT(domain) DO UPDATE SET rebuilding=0,cleaning=0,first_build=0`)
		keys, reasons := batchSummaryKeysParity(t, s, viewer, libraries)
		for _, library := range libraries {
			if library.Kind == "audiobook" {
				if reasons[library.ID] != telemetry.SummaryBypassRestrictedMembership {
					t.Fatalf("%s: audiobook reason %q", library.ID, reasons[library.ID])
				}
				continue
			}
			if reasons[library.ID] != "" {
				t.Fatalf("%s: restricted key bypassed: %s", library.ID, reasons[library.ID])
			}
		}
		if keys["films"].PolicyEpoch < 1 {
			t.Fatal("restricted key lost the policy epoch")
		}
		if keys["audio"].Handle == 0 || keys["audio"].Handle != c.Handle("audio") {
			t.Fatalf("bypassed audiobook lost its handle: %d", keys["audio"].Handle)
		}
		if !keys["films"].Domains[compactcatalog.DomainBrowseRows].Present || !keys["films"].Domains[compactcatalog.DomainBrowseEdges].Present {
			t.Fatalf("restricted key lost its domains: %+v", keys["films"].Domains)
		}
		if keys["films"].Reading != (summaryReading{}) {
			t.Fatalf("movie key carries a Reading binding: %+v", keys["films"].Reading)
		}

		cases := []struct {
			name, setup, cleanup string
			want                 telemetry.SummaryCacheEvent
		}{
			{"dirty", `INSERT INTO catalog_dirty(domain,entity_id,revision) VALUES(19,999999,1)`, `DELETE FROM catalog_dirty WHERE domain=19 AND entity_id=999999`, telemetry.SummaryBypassDirty},
			{"failure", `INSERT INTO catalog_derivation_failures(domain,entity_id,reason,failed_ms) VALUES(19,999999,'test',1)`, `DELETE FROM catalog_derivation_failures WHERE domain=19 AND entity_id=999999`, telemetry.SummaryBypassFailure},
			{"retaining", `UPDATE catalog_generations SET retaining=1`, `UPDATE catalog_generations SET retaining=0`, telemetry.SummaryBypassRetaining},
			{"rebuild", `UPDATE catalog_derivations SET rebuilding=1 WHERE domain=18`, `UPDATE catalog_derivations SET rebuilding=0 WHERE domain=18`, telemetry.SummaryBypassRebuild},
			{"domain_order_last_wins", `UPDATE catalog_derivations SET rebuilding=1 WHERE domain=18; UPDATE catalog_derivations SET cleaning=1 WHERE domain=19`, `UPDATE catalog_derivations SET rebuilding=0,cleaning=0,first_build=0 WHERE domain IN(18,19)`, telemetry.SummaryBypassCleanup},
			{"policy_epoch", `UPDATE catalog_facet_source_state SET policy_epoch=0 WHERE singleton=1`, `UPDATE catalog_facet_source_state SET policy_epoch=1 WHERE singleton=1`, telemetry.SummaryBypassIdentity},
			{"classification_identity", `DELETE FROM catalog_summary_classification_revision`, `INSERT INTO catalog_summary_classification_revision(singleton,revision) VALUES(1,1)`, telemetry.SummaryBypassIdentity},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				c.Exec(tc.setup)
				_, got := batchSummaryKeysParity(t, s, viewer, libraries)
				for _, library := range libraries {
					if library.Kind == "audiobook" {
						if got[library.ID] != telemetry.SummaryBypassRestrictedMembership {
							t.Fatalf("%s: audiobook reason %q", library.ID, got[library.ID])
						}
						continue
					}
					if got[library.ID] != tc.want {
						t.Fatalf("%s: reason %q, want %q", library.ID, got[library.ID], tc.want)
					}
				}
				c.Exec(tc.cleanup)
				_, reset := batchSummaryKeysParity(t, s, viewer, libraries)
				for _, library := range libraries {
					if library.Kind == "audiobook" {
						continue
					}
					if reset[library.ID] != "" {
						t.Fatalf("%s: cleanup left reason %q", library.ID, reset[library.ID])
					}
				}
			})
		}

		// One library's identity failure must not move the other libraries.
		c.Exec(`UPDATE library_revisions SET revision=0 WHERE library_id='films'`)
		_, got := batchSummaryKeysParity(t, s, viewer, libraries)
		if got["films"] != telemetry.SummaryBypassIdentity || got["reading"] != "" || got["other"] != "" {
			t.Fatalf("identity failure spread across libraries: %+v", got)
		}
		c.Exec(`UPDATE library_revisions SET revision=1 WHERE library_id='films'`)
	})

	t.Run("pending_reading_raw_window", func(t *testing.T) {
		c, s, viewer, _ := readingBrowseFixture(t, "book")
		viewer.Libraries = []string{"reading"}
		libraries := []Library{{ID: "reading", Kind: "book"}}
		var namespace, input, ready int64
		if err := c.DB.QueryRow(`SELECT a.namespace,a.input_generation,a.ready_generation FROM reading_attribution_state a JOIN catalog_libraries c ON c.id=a.library_id WHERE c.library_id='reading'`).Scan(&namespace, &input, &ready); err != nil {
			t.Fatal(err)
		}
		keys, reasons := batchSummaryKeysParity(t, s, viewer, libraries)
		if reasons["reading"] != telemetry.SummaryBypassReading {
			t.Fatalf("cold Reading reason %q", reasons["reading"])
		}
		key := keys["reading"]
		if key.Reading.Library == 0 || key.Reading.Namespace != namespace || key.Reading.Input != input || key.Reading.Ready != ready {
			t.Fatalf("cold Reading raw binding %+v, want library=%d namespace=%d input=%d ready=%d", key.Reading, key.Handle, namespace, input, ready)
		}
		if key.Reading.Ready != 0 {
			t.Fatalf("cold Reading projection unexpectedly ready: %+v", key.Reading)
		}

		c.Drain()
		keys, reasons = batchSummaryKeysParity(t, s, viewer, libraries)
		if reasons["reading"] != "" {
			t.Fatalf("ready Reading reason %q", reasons["reading"])
		}
		ready = keys["reading"].Reading.Ready
		if ready < 1 || keys["reading"].Reading.Input != ready {
			t.Fatalf("ready Reading generation %+v", keys["reading"].Reading)
		}

		s.ConfigureReadingMobi(true)
		keys, reasons = batchSummaryKeysParity(t, s, viewer, libraries)
		if reasons["reading"] != "" || !keys["reading"].Reading.Mobi {
			t.Fatalf("Mobi capability missing from the ready key: %+v %q", keys["reading"].Reading, reasons["reading"])
		}

		c.Exec(`UPDATE reading_attribution_state SET input_generation=input_generation+1,phase='scan' WHERE library_id=(SELECT id FROM catalog_libraries WHERE library_id='reading')`)
		if err := c.DB.QueryRow(`SELECT a.namespace,a.input_generation,a.ready_generation FROM reading_attribution_state a JOIN catalog_libraries c ON c.id=a.library_id WHERE c.library_id='reading'`).Scan(&namespace, &input, &ready); err != nil {
			t.Fatal(err)
		}
		keys, reasons = batchSummaryKeysParity(t, s, viewer, libraries)
		if reasons["reading"] != telemetry.SummaryBypassReading {
			t.Fatalf("advancing Reading reason %q", reasons["reading"])
		}
		key = keys["reading"]
		if key.Reading.Library == 0 || key.Reading.Namespace != namespace || key.Reading.Input != input || key.Reading.Ready != ready {
			t.Fatalf("advancing Reading raw binding %+v, want library=%d namespace=%d input=%d ready=%d", key.Reading, key.Handle, namespace, input, ready)
		}
		if key.Reading.Ready < 1 || key.Reading.Input != key.Reading.Ready+1 {
			t.Fatalf("advancing Reading did not keep the previous ready generation: %+v", key.Reading)
		}
	})

	t.Run("isolated_bindings_and_no_cache_mutation", func(t *testing.T) {
		c, s, viewer, _ := readingBrowseFixture(t, "book")
		c.Library("films", "Films", "movie", t.TempDir())
		c.Drain()
		viewer.Libraries = []string{"reading", "other", "films"}
		libraries := []Library{{ID: "reading", Kind: "book"}, {ID: "other", Kind: "book"}, {ID: "films", Kind: "movie"}}
		entries, flights := len(s.state.summaries.entries), len(s.state.summaries.flights)
		before := telemetry.LibrarySummaryCacheCounters()
		keys, reasons := batchSummaryKeysInSnapshot(t, s, viewer, libraries)
		after := telemetry.LibrarySummaryCacheCounters()
		for _, library := range libraries {
			if reasons[library.ID] != "" {
				t.Fatalf("%s: reason %q", library.ID, reasons[library.ID])
			}
		}
		if len(s.state.summaries.entries) != entries || len(s.state.summaries.flights) != flights {
			t.Fatal("batch inserted a summary cache entry or flight")
		}
		for event, value := range after {
			if before[event] != value {
				t.Fatalf("batch counted cache event %s", event)
			}
		}
		for _, library := range libraries {
			key := keys[library.ID]
			handle := c.Handle(library.ID)
			if key.Library != library.ID || key.Handle != handle {
				t.Fatalf("%s: key bound to %q/%d", library.ID, key.Library, key.Handle)
			}
			if isReadingLibrary(library.Kind) && key.Reading.Library != handle {
				t.Fatalf("%s: Reading bound to %d, want %d", library.ID, key.Reading.Library, handle)
			}
		}
		reversed := []Library{libraries[2], libraries[1], libraries[0]}
		again, _ := batchSummaryKeysInSnapshot(t, s, viewer, reversed)
		for _, library := range libraries {
			if again[library.ID] != keys[library.ID] {
				t.Fatalf("%s: key depends on input order", library.ID)
			}
		}
	})
}

func TestLibrarySummaryBatchKeysStatementCountIsFlat(t *testing.T) {
	c := catalogtest.Open(t)
	kinds := []string{"movie", "tv", "anime", "music", "audiobook", "book", "comic", "book", "book", "book", "book"}
	libraries := make([]Library, 0, len(kinds))
	viewer := Viewer{Restrictions: identity.ContentRestrictions{BlockUnrated: true}}
	for i, kind := range kinds {
		id := fmt.Sprintf("batch-%d", i)
		c.Library(id, id, kind, t.TempDir())
		libraries = append(libraries, Library{ID: id, Kind: kind})
		viewer.Libraries = append(viewer.Libraries, id)
	}
	s := New(c.DB)
	// Measure the full restricted path: metadata, shared policy facts, domains.
	c.Exec(`DELETE FROM catalog_dirty; DELETE FROM catalog_derivation_failures`)
	count := func(libs []Library) int {
		t.Helper()
		ctx, trace := dbwork.TraceQueryPlans(context.Background())
		var statements int
		if err := dbwork.WithReadSnapshot(ctx, c.DB, func(ctx context.Context) error {
			start := len(trace())
			_, _, err := s.WithContext(ctx).librarySummaryBatchKeys(ctx, viewer, libs)
			statements = len(trace()) - start
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return statements
	}
	two, eleven := count(libraries[:2]), count(libraries)
	if two != eleven {
		t.Fatalf("statement count follows library count: %d libraries=%d, %d libraries=%d", 2, two, len(libraries), eleven)
	}
	if two > 4 || eleven > 4 || two < 1 {
		t.Fatalf("statement count outside the fixed budget: 2=%d, 11=%d", two, eleven)
	}

	// No snapshot still answers every library conservatively without SQL.
	ctx, trace := dbwork.TraceQueryPlans(context.Background())
	_, reasons, err := s.WithContext(ctx).librarySummaryBatchKeys(ctx, viewer, libraries[:2])
	if err != nil {
		t.Fatal(err)
	}
	if len(trace()) != 0 {
		t.Fatalf("snapshotless batch executed %d statements", len(trace()))
	}
	for _, library := range libraries[:2] {
		if reasons[library.ID] != telemetry.SummaryBypassSnapshot {
			t.Fatalf("%s: snapshotless reason %q", library.ID, reasons[library.ID])
		}
	}

	viewer.Restrictions = identity.ContentRestrictions{}
	if got := count(libraries); got != 1 {
		t.Fatalf("unrestricted statement count %d, want 1", got)
	}
}

package writer

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchpolicy"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func searchInputTestChange() *searchInputChange {
	return &searchInputChange{AcceptedAtMS: 1000, NewEntities: []newOrdinaryEntitySource{{
		SubjectID: 1, LibraryID: 1, Kind: 1, MetadataIncarnation: [16]byte{9}, EntityRevision: 1,
	}}}
}

func searchInputTestTurn(t *testing.T, w *executor, registry *stmt.Registry, custody *testCustody) *commandTurn {
	t.Helper()
	tx, err := w.begin(t.Context(), InventoryAccept, testRecipe(), custody)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = tx.rollback(nil) })
	var steps []statementStep
	for _, entry := range registry.Manifest() {
		// Synthetic component forecasts and virtual clock; no production
		// Inventory recipe or physical latency/WAL qualification is asserted.
		steps = append(steps, statementStep{entry.ID, Work{4, 100, 4, 4096, time.Millisecond}})
	}
	turn, err := newCommandTurn(tx, registry, steps)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = turn.closeRows() })
	return turn
}

func searchInputAssertRolledBack(t *testing.T, w *executor) {
	t.Helper()
	var intact bool
	if err := w.db.QueryRowContext(t.Context(), `SELECT
(SELECT title='A' FROM catalog_entities WHERE id=1)
AND (SELECT count(*)=0 FROM search_publish_jobs)
AND (SELECT pending_subjects=0 FROM search_generations WHERE id=1)
AND (SELECT revision=1 AND input_sequence=0 AND active_generation_id=1 AND building_generation_id IS NULL FROM search_revision)`,
	).Scan(&intact); err != nil || !intact {
		t.Fatalf("source/search prefix escaped rollback: %v", err)
	}
}

func TestSearchInputHelperGenerationRevisionChangesBetweenReadAndCAS(t *testing.T) {
	w, registry := searchInputFixture(t)
	custody := &testCustody{}
	turn := searchInputTestTurn(t, w, registry, custody)
	step := 0
	custody.checkHook = func() {
		step++
		if step == 7 { // AFTER bank read, immediately BEFORE pending CAS.
			if _, err := turn.transaction.conn.ExecContext(turn.transaction.ctx,
				`UPDATE search_generations SET job_revision=?,job_lease_owner=7,job_lease_generation=9,
job_lease_expires_ms=2000,phase='tail',after_entity_id=31 WHERE id=1`, int64(math.MaxInt64)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := applySearchInputIntent(t.Context(), turn, searchInputTestChange()); err != nil {
		t.Fatal("unrelated generation driver change rejected source:", err)
	}
	custody.checkHook = nil
	if err := turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	var exact bool
	if err := w.db.QueryRowContext(t.Context(), `SELECT job_revision=? AND job_lease_owner=7
AND job_lease_generation=9 AND job_lease_expires_ms=2000 AND phase='tail' AND after_entity_id=31
AND pending_subjects=1 FROM search_generations WHERE id=1`, int64(math.MaxInt64)).Scan(&exact); err != nil || !exact || step != 8 {
		t.Fatalf("lost independent driver/accounting effects, statements=%d: %v", step, err)
	}
}

func TestSearchInputHelperWitnessFailuresRollbackWholeCohort(t *testing.T) {
	w, registry := searchInputFixture(t)
	// Serial subcases roll back completely and reuse this adopted fixture.
	// Avoid repeating the entire canonical bootstrap under the race detector.
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO search_generations(
 id,bank_key,recipe_version,engine_version,unicode_fingerprint,state,created_ms)
 VALUES(2,'new_raw_target','old_recipe','old_engine',?,'building',1000)`, searchInputExampleFingerprint); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name string
		at   int
		sql  string
	}{
		{"source revision", 3, `UPDATE catalog_entities SET revision=2 WHERE id=1`},
		{"source incarnation", 3, `UPDATE catalog_entities SET metadata_incarnation=zeroblob(16) WHERE id=1`},
		{"pending", 7, `UPDATE search_generations SET pending_subjects=1 WHERE id=1`},
		{"bank key", 7, `UPDATE search_generations SET bank_key='changed' WHERE id=1`},
		{"state", 7, `UPDATE search_generations SET state='retired' WHERE id=1`},
		// Recipe drift before INSERT is also rejected by the native FK;
		// after INSERT that same FK forbids changing the referenced tuple.
		{"stored recipe", 6, `UPDATE search_generations SET recipe_version='changed' WHERE id=1`},
		{"stored engine", 7, `UPDATE search_generations SET engine_version='changed' WHERE id=1`},
		{"stored fingerprint", 7, `UPDATE search_generations SET unicode_fingerprint='changed' WHERE id=1`},
		{"serving revision", 8, `UPDATE search_revision SET revision=2 WHERE singleton=1`},
		{"heads", 8, `UPDATE search_revision SET active_generation_id=NULL WHERE singleton=1`},
		{"building head", 8, `UPDATE search_revision SET building_generation_id=2 WHERE singleton=1`},
		{"clock", 8, `UPDATE search_revision SET input_sequence=1 WHERE singleton=1`},
		{"retained subject", 4, `UPDATE catalog_subjects SET kind=2 WHERE id=1`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			custody := &testCustody{}
			turn := searchInputTestTurn(t, w, registry, custody)
			if tt.name == "retained subject" {
				// Model an inconsistent captured parent only inside this
				// rollback-bound fault fixture. Production never changes FK mode.
				if _, err := turn.transaction.conn.ExecContext(turn.transaction.ctx, `PRAGMA defer_foreign_keys=ON`); err != nil {
					t.Fatal(err)
				}
			}
			// Model an earlier caller source effect in the SAME frame. The
			// actual Inventory source/result/receipt integration is SC-owned.
			if _, err := turn.transaction.conn.ExecContext(turn.transaction.ctx, `UPDATE catalog_entities SET title='proposed' WHERE id=1`); err != nil {
				t.Fatal(err)
			}
			n := 0
			custody.checkHook = func() {
				n++
				if n == tt.at {
					if _, err := turn.transaction.conn.ExecContext(turn.transaction.ctx, tt.sql); err != nil {
						t.Fatal(err)
					}
				}
			}
			err := applySearchInputIntent(t.Context(), turn, searchInputTestChange())
			if err == nil || tt.name != "stored recipe" && !errors.Is(err, errSearchInputConflict) {
				t.Fatalf("stale %s did not reject: %v", tt.name, err)
			}
			custody.checkHook = nil
			if closeErr := turn.closeRows(); closeErr != nil {
				t.Fatal(closeErr)
			}
			if _, got := turn.transaction.rollback(err); !errors.Is(got, err) {
				t.Fatal(got)
			}
			searchInputAssertRolledBack(t, w)
		})
	}
}

func TestSearchInputHelperNoOpAndClosedSteps(t *testing.T) {
	w, registry := searchInputFixture(t)
	custody := &testCustody{}
	turn := searchInputTestTurn(t, w, registry, custody)
	checks, remaining := custody.checks, turn.transaction.quantum.Remaining()
	if err := applySearchInputIntent(t.Context(), turn, &searchInputChange{}); err != nil {
		t.Fatal(err)
	}
	if custody.checks != checks || turn.transaction.quantum.Remaining() != remaining {
		t.Fatal("no-op consumed a statement or changed clock")
	}
	delete(turn.steps, searchInputPending)
	if err := applySearchInputIntent(t.Context(), turn, searchInputTestChange()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("missing compiled step admitted: %v", err)
	}
	if custody.checks != checks {
		t.Fatal("missing compiled step admitted a prefix")
	}
	_, _ = turn.transaction.rollback(nil)
	searchInputAssertRolledBack(t, w)
}

func TestSearchInputHelperDualRawTargets(t *testing.T) {
	w, registry := searchInputFixture(t)
	// This target is RAW data only. Its key is never an SQL identifier and
	// creates no native bank, eligibility, dispatcher or generation authority.
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO search_generations(
id,bank_key,recipe_version,engine_version,unicode_fingerprint,state,created_ms,pending_subjects)
VALUES(2,'raw_only_fixture','old_recipe','old_engine',?,'building',1000,7)`, searchInputExampleFingerprint); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `UPDATE search_revision SET building_generation_id=2 WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	var schemaBefore, schemaAfter int64
	if err := w.db.QueryRowContext(t.Context(), `PRAGMA schema_version`).Scan(&schemaBefore); err != nil {
		t.Fatal(err)
	}
	custody := &testCustody{}
	turn := searchInputTestTurn(t, w, registry, custody)
	checks := custody.checks
	if err := applySearchInputIntent(t.Context(), turn, searchInputTestChange()); err != nil {
		t.Fatal(err)
	}
	if custody.checks-checks != 11 {
		t.Fatalf("dual RAW target statement count: %d", custody.checks-checks)
	}
	if err := turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
	var exact bool
	if err := w.db.QueryRowContext(t.Context(), `SELECT
(SELECT count(*)=2 FROM search_publish_jobs WHERE entity_id=1 AND input_generation=1 AND published_generation=0
AND source_incarnation=X'09000000000000000000000000000000' AND source_revision=1 AND phase='capture' AND job_next_attempt_ms=1000)
AND (SELECT recipe_version='old_recipe' FROM search_publish_jobs WHERE search_generation_id=2)
AND (SELECT recipe_version='A-CJK-uni-bi-v1' FROM search_publish_jobs WHERE search_generation_id=1)
AND (SELECT pending_subjects=1 AND job_revision=1 FROM search_generations WHERE id=1)
AND (SELECT pending_subjects=8 AND job_revision=1 FROM search_generations WHERE id=2)
AND (SELECT input_sequence=1 AND revision=1 AND active_generation_id=1 AND building_generation_id=2 FROM search_revision)
AND (SELECT count(*)=0 FROM search_own_prefix_docsize)
AND (SELECT count(*)=0 FROM search_fragment_prefix_docsize)
AND (SELECT count(*)=0 FROM search_fragment_lexical_docsize)
AND (SELECT count(*)=0 FROM search_fragment_grams_docsize)
AND (SELECT count(*)=0 FROM catalog_people_names_docsize)`).Scan(&exact); err != nil || !exact {
		t.Fatalf("dual RAW deltas, stored profiles, clock or zero-native proof: %v", err)
	}
	if err := w.db.QueryRowContext(t.Context(), `PRAGMA schema_version`).Scan(&schemaAfter); err != nil || schemaAfter != schemaBefore {
		t.Fatal("RAW helper changed native schema")
	}
}

func TestSearchInputHelperConfigAndExistingOwner(t *testing.T) {
	w, registry := searchInputFixture(t)
	for _, tt := range []struct {
		name, sql string
		want      error
	}{
		{"missing active", `UPDATE search_revision SET active_generation_id=NULL WHERE singleton=1`, errSearchInputConfiguration},
		{"same target", `UPDATE search_revision SET building_generation_id=active_generation_id WHERE singleton=1`, errSearchInputConfiguration},
		{"wrong role", `UPDATE search_generations SET state='retired' WHERE id=1`, errSearchInputConfiguration},
		{"pending overflow", `UPDATE search_generations SET pending_subjects=9223372036854775807 WHERE id=1`, errSearchInputConfiguration},
		{"clock overflow", `UPDATE search_revision SET input_sequence=9223372036854775807 WHERE singleton=1`, errSearchInputConfiguration},
		{"retired entity", `UPDATE catalog_entities SET retired=1 WHERE id=1`, errSearchInputConflict},
		{"prior job", `INSERT INTO search_publish_jobs(entity_id,search_generation_id,input_generation,source_incarnation,source_revision,recipe_version,phase)
VALUES(1,1,1,X'09000000000000000000000000000000',1,'A-CJK-uni-bi-v1','capture')`, errSearchInputConflict},
	} {
		t.Run(tt.name, func(t *testing.T) {
			turn := searchInputTestTurn(t, w, registry, &testCustody{})
			if _, err := turn.transaction.conn.ExecContext(turn.transaction.ctx, tt.sql); err != nil {
				t.Fatal(err)
			}
			if err := applySearchInputIntent(t.Context(), turn, searchInputTestChange()); !errors.Is(err, tt.want) {
				t.Fatalf("wrong disposition: %v", err)
			}
			_ = turn.closeRows()
			_, _ = turn.transaction.rollback(nil)
			searchInputAssertRolledBack(t, w)
		})
	}
}

func TestSearchInputHelperMalformedCapturedProfiles(t *testing.T) {
	w, registry := searchInputFixture(t)
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO search_generations(
id,bank_key,recipe_version,engine_version,unicode_fingerprint,state,created_ms)
VALUES(2,'raw_only_fixture','old_recipe','old_engine',?,'building',1000)`, searchInputExampleFingerprint); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name string
		edit func(*searchpolicy.Profile)
	}{
		{"empty recipe", func(p *searchpolicy.Profile) { p.RecipeVersion = "" }},
		{"oversized recipe", func(p *searchpolicy.Profile) { p.RecipeVersion = strings.Repeat("r", 129) }},
		{"empty engine", func(p *searchpolicy.Profile) { p.EngineVersion = "" }},
		{"oversized engine", func(p *searchpolicy.Profile) { p.EngineVersion = strings.Repeat("e", 513) }},
		{"empty fingerprint", func(p *searchpolicy.Profile) { p.UnicodeFingerprint = "" }},
		{"wrong prefix", func(p *searchpolicy.Profile) { p.UnicodeFingerprint = "wrong:" + strings.Repeat("0", 64) }},
		{"short fingerprint", func(p *searchpolicy.Profile) {
			p.UnicodeFingerprint = searchpolicy.ProfileAlgorithm + ":" + strings.Repeat("0", 63)
		}},
		{"long fingerprint", func(p *searchpolicy.Profile) {
			p.UnicodeFingerprint = searchpolicy.ProfileAlgorithm + ":" + strings.Repeat("0", 65)
		}},
		{"uppercase hex", func(p *searchpolicy.Profile) {
			p.UnicodeFingerprint = searchpolicy.ProfileAlgorithm + ":" + strings.Repeat("A", 64)
		}},
		{"invalid hex", func(p *searchpolicy.Profile) {
			p.UnicodeFingerprint = searchpolicy.ProfileAlgorithm + ":" + strings.Repeat("g", 64)
		}},
	} {
		for _, bankID := range []int64{1, 2} {
			t.Run(tt.name+"/"+string(rune('0'+bankID)), func(t *testing.T) {
				profile := searchpolicy.Profile{RecipeVersion: "different_recipe", EngineVersion: "different_engine", UnicodeFingerprint: searchInputExampleFingerprint}
				tt.edit(&profile)
				custody := &testCustody{}
				turn := searchInputTestTurn(t, w, registry, custody)
				if bankID == 2 {
					if _, err := turn.transaction.conn.ExecContext(turn.transaction.ctx,
						`UPDATE search_revision SET building_generation_id=2 WHERE singleton=1`); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := turn.transaction.conn.ExecContext(turn.transaction.ctx,
					`UPDATE search_generations SET recipe_version=?,engine_version=?,unicode_fingerprint=? WHERE id=?`,
					profile.RecipeVersion, profile.EngineVersion, profile.UnicodeFingerprint, bankID); err != nil {
					t.Fatal(err)
				}
				if _, err := turn.transaction.conn.ExecContext(turn.transaction.ctx,
					`UPDATE catalog_entities SET title='proposed' WHERE id=1`); err != nil {
					t.Fatal(err)
				}
				before := custody.checks
				err := applySearchInputIntent(t.Context(), turn, searchInputTestChange())
				if !errors.Is(err, errSearchInputConfiguration) || custody.checks-before != int(bankID)+1 {
					t.Fatalf("captured bank shape did not refuse before effects: %v; steps=%d", err, custody.checks-before)
				}
				if err := turn.closeRows(); err != nil {
					t.Fatal(err)
				}
				_, _ = turn.transaction.rollback(err)
				searchInputAssertRolledBack(t, w)
			})
		}
	}
}

func TestSearchInputHelperWellFormedProfilesRemainAdmissible(t *testing.T) {
	w, registry := searchInputFixture(t)
	for _, tt := range []struct {
		name    string
		profile searchpolicy.Profile
	}{
		{"current", searchpolicy.Compiled()},
		{"different recipe engine fingerprint", searchpolicy.Profile{
			RecipeVersion: strings.Repeat("r", 128), EngineVersion: strings.Repeat("e", 512),
			UnicodeFingerprint: searchpolicy.ProfileAlgorithm + ":" + strings.Repeat("f", 64),
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			turn := searchInputTestTurn(t, w, registry, &testCustody{})
			if _, err := turn.transaction.conn.ExecContext(turn.transaction.ctx,
				`UPDATE search_generations SET recipe_version=?,engine_version=?,unicode_fingerprint=? WHERE id=1`,
				tt.profile.RecipeVersion, tt.profile.EngineVersion, tt.profile.UnicodeFingerprint); err != nil {
				t.Fatal(err)
			}
			if err := applySearchInputIntent(t.Context(), turn, searchInputTestChange()); err != nil {
				t.Fatal("well-formed captured tuple refused:", err)
			}
			var exact bool
			if err := turn.transaction.conn.QueryRowContext(turn.transaction.ctx, `SELECT
(SELECT recipe_version=? FROM search_publish_jobs WHERE entity_id=1 AND search_generation_id=1)
AND (SELECT pending_subjects=1 AND recipe_version=? AND engine_version=? AND unicode_fingerprint=? FROM search_generations WHERE id=1)
AND (SELECT input_sequence=1 AND revision=1 FROM search_revision)
AND (SELECT count(*)=0 FROM search_own_prefix_docsize)
AND (SELECT count(*)=0 FROM search_fragment_prefix_docsize)
AND (SELECT count(*)=0 FROM search_fragment_lexical_docsize)
AND (SELECT count(*)=0 FROM search_fragment_grams_docsize)
AND (SELECT count(*)=0 FROM catalog_people_names_docsize)`, tt.profile.RecipeVersion,
				tt.profile.RecipeVersion, tt.profile.EngineVersion, tt.profile.UnicodeFingerprint).Scan(&exact); err != nil || !exact {
				t.Fatalf("stored tuple, RAW delta/clock or zero-native state changed: %v", err)
			}
			if err := turn.closeRows(); err != nil {
				t.Fatal(err)
			}
			_, _ = turn.transaction.rollback(nil)
			searchInputAssertRolledBack(t, w)
		})
	}
}

func TestSearchInputHelperCancellationAndProspectiveRefusal(t *testing.T) {
	w, registry := searchInputFixture(t)
	for _, failure := range []string{"cancel", "grant", "quantum"} {
		for _, at := range []int{1, 6, 7, 8} {
			t.Run(failure+"/"+string(rune('0'+at)), func(t *testing.T) {
				custody := &testCustody{}
				turn := searchInputTestTurn(t, w, registry, custody)
				ctx, cancel := context.WithCancel(turn.transaction.ctx)
				turn.transaction.ctx = ctx
				defer cancel()
				n := 0
				want := ErrQuantum
				custody.checkHook = func() {
					n++
					if n != at {
						return
					}
					switch failure {
					case "cancel":
						cancel()
					case "grant":
						custody.checkErr = ErrBusy
					case "quantum":
						turn.transaction.quantum.remaining.WALBytes = turn.transaction.quantum.terminal.WALBytes
					}
				}
				if failure == "cancel" {
					want = context.Canceled
				} else if failure == "grant" {
					want = ErrBusy
				}
				err := applySearchInputIntent(ctx, turn, searchInputTestChange())
				if !errors.Is(err, want) {
					t.Fatalf("missing prospective refusal: %v", err)
				}
				custody.checkHook, custody.checkErr = nil, nil
				_ = turn.closeRows()
				_, _ = turn.transaction.rollback(err)
				searchInputAssertRolledBack(t, w)
			})
		}
	}
}

func TestSearchInputFixtureIsStaleOnActualEngine(t *testing.T) {
	w, _ := searchInputFixture(t)
	var actual searchpolicy.Engine
	if err := w.db.QueryRowContext(t.Context(), `SELECT sqlite_version(),sqlite_source_id()`).Scan(&actual.Version, &actual.SourceID); err != nil {
		t.Fatal(err)
	}
	result := searchpolicy.Compare(searchpolicy.Profile{RecipeVersion: searchpolicy.RecipeV1,
		EngineVersion: searchpolicy.EngineVersion, UnicodeFingerprint: searchInputExampleFingerprint}, actual)
	if result.State != searchpolicy.Stale || !result.EngineAttested {
		t.Fatal("RAW fixture must use an attested engine and stale stored profile")
	}
}

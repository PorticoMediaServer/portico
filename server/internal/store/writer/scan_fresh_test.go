package writer

import (
	"bytes"
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/mediasource"
	"github.com/PorticoMediaServer/portico/server/internal/storage/objectidentity"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchpolicy"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

func inventoryFreshRequest(t *testing.T) *inventoryAcceptCommand {
	t.Helper()
	r, identity := inventoryRequestFixture()
	r.scope = inventoryScope{runID: "fresh-run", libraryKey: "fresh-library", libraryID: 7}
	r.source = inventorySource{id: "fresh-source", incarnation: "fresh-incarnation", rootIdentity: "dev:7:ino:1", resolvedRoot: []byte("/media"), generation: 23}
	r.run.leaseExpiresMS = 4000000000000
	r.directory.revision, r.directory.nextCursor = "directory:013", "42"
	r.files[0].objectIdentity = "dev:7:ino:31"
	e := mediasource.InventoryEvidence{Kind: "local", Scope: "fresh-source:fresh-incarnation", Object: "ino:31", Size: 42, ModifiedNS: -11, ChangeToken: "change:29"}
	var err error
	r.files[0].revision, err = e.Revision()
	if err != nil {
		t.Fatal(err)
	}
	c, err := newInventoryAcceptCommand(identity, r)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// Native protocol fixture only: it owns no production ScanDriver ingress,
// mount-stability/byte-continuity evidence or numerical physical qualification.
func seedInventoryFresh(t *testing.T, e *executor) *inventoryAcceptCommand {
	t.Helper()
	c := inventoryFreshRequest(t)
	for _, q := range []string{
		`INSERT INTO libraries(id,name,kind,root,data_revision,data_order_revision,data_metadata_revision,age_input_revision,age_published_revision) VALUES('fresh-library','Movies','movie','/media',71,73,79,83,83)`,
		`INSERT INTO catalog_libraries(id,library_id,root) VALUES(7,'fresh-library','/media')`,
		`INSERT INTO catalog_kinds VALUES(1,'movie',1,0,1,1,0)`,
		`INSERT INTO library_sources(id,library_id,configured_root,root,incarnation,root_identity,generation,identity_confirmed) VALUES('fresh-source','fresh-library','/media','/media','fresh-incarnation','dev:7:ino:1',23,1)`,
		`INSERT INTO library_scan_policies(library_id,revision) VALUES('fresh-library',7)`,
		`INSERT INTO jobs(id,library_id,status,created_at,processed) VALUES('fresh-run','fresh-library','running','2026-11-06T00:00:00Z',67)`,
		`INSERT INTO inventory_directories(job_id,relative_path,identity,revision,pages,skipped) VALUES('fresh-run','.','directory:11','directory:013',5,3)`,
		`INSERT INTO catalog_membership_clock(singleton,sequence) VALUES(1,89)`,
		`INSERT INTO catalog_age_item_counts VALUES(7,1,0,-1,97)`,
		`INSERT INTO catalog_age_item_counts VALUES(7,1,1,7,101)`,
	} {
		if _, err := e.db.ExecContext(t.Context(), q); err != nil {
			t.Fatal(err)
		}
	}
	// Explicit original scope and full synthetic creation commitment for this
	// native fixture, independent of its N1 command/receipt and mutable parents.
	if _, err := e.db.ExecContext(t.Context(), `INSERT INTO inventory_runs(job_id,source_id,source_generation,root_incarnation,root_identity,policy_revision,scan_mode,
job_lease_owner,job_lease_generation,job_lease_expires_ms,job_revision,command_incarnation,last_command_sequence,acknowledged_command_sequence,discovered,
original_library_id,original_library_key,original_resolved_root,original_follow_symlinks,create_operation_id,create_request_digest)
VALUES('fresh-run','fresh-source',23,'fresh-incarnation','dev:7:ino:1',7,'first-import',13,17,4000000000000,41,?,18,18,59,
7,'fresh-library','/media',0,?,?)`, c.identity.Incarnation[:], []byte("fresh-create-op!"), bytes.Repeat([]byte{0xe1}, 32)); err != nil {
		t.Fatal(err)
	}
	// Well-formed incompatible stored tuple intentionally proves RAW intent
	// acceptance; no profile match is necessary for source visibility.
	if _, err := e.db.ExecContext(t.Context(), `INSERT INTO search_generations(id,bank_key,recipe_version,engine_version,unicode_fingerprint,state,created_ms)
VALUES(1,'raw-active',?,?,?,'active',1000)`, searchpolicy.RecipeV1, searchpolicy.EngineVersion, searchInputExampleFingerprint); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.ExecContext(t.Context(), `UPDATE search_revision SET revision=103,input_sequence=107,active_generation_id=1 WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	return c
}

func inventoryFreshFixture(t *testing.T, decorate ...func(driver.Conn) driver.Conn) (*executor, string, *inventoryAcceptCommand, *stmt.Registry) {
	t.Helper()
	return inventoryFreshFixtureDefinitions(t, scanInventoryFreshDefinitions(), decorate...)
}

func inventoryFreshFixtureDefinitions(t *testing.T, definitions []stmt.Definition, decorate ...func(driver.Conn) driver.Conn) (*executor, string, *inventoryAcceptCommand, *stmt.Registry) {
	t.Helper()
	e, path, c := inventoryFreshSeedFixture(t, decorate...)
	registry := nativeInventoryFixtureRegistry(t, e, definitions, "scan_fresh_test.go")
	return e, path, c, registry
}

// The one existing seed owner, before registry capture; paired read setup may
// attach to this same canonical database outside all command custody.
func inventoryFreshSeedFixture(t *testing.T, decorate ...func(driver.Conn) driver.Conn) (*executor, string, *inventoryAcceptCommand) {
	t.Helper()
	e, path, _ := testExecutor(t, decorate...)
	return e, path, seedInventoryFresh(t, e)
}

func TestInventoryFreshClosedRecipe(t *testing.T) {
	defs, ids := scanInventoryFreshDefinitions(), inventoryFreshIDs()
	if len(defs) != len(ids) {
		t.Fatalf("provider=%d inventory=%d", len(defs), len(ids))
	}
	seen := make(map[stmt.ID]bool)
	for _, d := range defs {
		if seen[d.ID] || inventoryFreshMaxUses(d.ID) < 1 || !d.Plan.Hot || len(d.Plan.Waivers) != 0 {
			t.Fatal("unbounded/duplicate definition", d.ID)
		}
		if issues := plan.LintSQL(d.SQL); len(issues) != 0 {
			t.Fatal(d.ID, issues)
		}
		seen[d.ID] = true
	}
	for _, id := range ids {
		if !seen[id] {
			t.Fatal("missing", id)
		}
	}
	if _, err := stmt.Describe(defs); err != nil {
		t.Fatal(err)
	}
	if inventoryFreshMaxUses(scanReceiptOwner) != 0 || inventoryFreshMaxUses(scanReceiptReclaimHeader) != 0 {
		t.Fatal("history/reclaim entered effects-only recipe")
	}
	if inventoryFreshMaxUses(searchInputBank) != 2 || inventoryFreshMaxUses(searchInputInsert) != 2 || inventoryFreshMaxUses(searchInputPending) != 2 {
		t.Fatal("dual bank uses missing")
	}
}

func TestInventoryFreshObservationExactParity(t *testing.T) {
	for _, rawIdentity := range []string{"dev:7:ino:31", "dev:8:ino:31", "", "native:opaque\x00identity"} {
		t.Run(rawIdentity, func(t *testing.T) {
			base := inventoryFreshRequest(t)
			r := base.request
			r.files = append([]inventoryFile(nil), r.files...)
			r.files[0].objectIdentity = rawIdentity
			stable := objectidentity.StableObjectIdentity(rawIdentity, r.source.rootIdentity)
			object := stable
			if object == "" {
				object = "path:" + r.files[0].assetPath
			}
			evidence := mediasource.InventoryEvidence{Kind: "local", Scope: r.source.id + ":" + r.source.incarnation, Object: object,
				Size: r.files[0].size, ModifiedNS: r.files[0].modifiedNS, ChangeToken: r.files[0].changeToken}
			var err error
			r.files[0].revision, err = evidence.Revision()
			if err != nil {
				t.Fatal(err)
			}
			c, err := newInventoryAcceptCommand(base.identity, r)
			if err != nil {
				t.Fatal(err)
			}
			required, err := inventoryFreshRequiredBytes(c)
			if err != nil || required <= 65536 {
				t.Fatal(required, err)
			}
			got, err := prepareInventoryFreshObservation(c)
			wantJSON, _ := json.Marshal(evidence)
			if err != nil || got.objectIdentity != stable || got.evidenceJSON != string(wantJSON) || got.revision != r.files[0].revision {
				t.Fatal(got, err)
			}
		})
	}
}

func TestInventoryFreshObservationRefusesUnsupportedOrChangedFacts(t *testing.T) {
	for name, change := range map[string]func(*inventoryAcceptCommand){
		"EOF":               func(c *inventoryAcceptCommand) { c.request.directory.complete = true },
		"equivalent_zero":   func(c *inventoryAcceptCommand) { c.request.directory.nextCursor = "0" },
		"repeated_cookie":   func(c *inventoryAcceptCommand) { c.request.directory.expectedCursor = "042" },
		"negative_cookie":   func(c *inventoryAcceptCommand) { c.request.directory.nextCursor = "-1" },
		"opaque_cookie":     func(c *inventoryAcceptCommand) { c.request.directory.nextCursor = "page:42" },
		"wrong_path":        func(c *inventoryAcceptCommand) { c.request.files[0].assetPath = "/other/Arrival.mkv" },
		"unclean_root":      func(c *inventoryAcceptCommand) { c.request.source.resolvedRoot = []byte("/media/.") },
		"wrong_revision":    func(c *inventoryAcceptCommand) { c.request.files[0].revision = "not-an-observation" },
		"unhashed_mutation": func(c *inventoryAcceptCommand) { c.request.files[0].title = "different" },
	} {
		t.Run(name, func(t *testing.T) {
			c := inventoryFreshRequest(t)
			change(c)
			if name != "unhashed_mutation" {
				var err error
				c.digest, err = canonicalRequestDigest(InventoryAccept, inventoryAcceptRequestVersion, c.identity, c.request)
				if err != nil {
					t.Fatal(err)
				}
			}
			if got, err := prepareInventoryFreshObservation(c); err == nil || got != (inventoryObjectFacts{}) {
				t.Fatal("unsupported facts accepted", got, err)
			}
		})
	}
}

// This fixture uses W's actual grant binding/frame, with explicit synthetic
// numerical test inputs. It is neither a runtime constructor nor a production
// ScanDriver/profile. Positive controls cannot borrow plain testCustody.
func inventoryFreshTestTurn(t *testing.T, e *executor, _ string, registry *stmt.Registry) (*commandTurn, *governor.Authority) {
	t.Helper()
	// W owns this actual governor/frame fixture. Only the private TEST memory
	// partition is supplied here; no fixture bridge or Work profile is copied.
	return inventoryMemoryFixtureTurn(t, e, registry, inventoryFreshRequest(t), inventoryMemoryBudget{preparationBytes: 4 << 20, reservedBytes: 4 << 20})
}

func inventoryFreshAssertAbsent(t *testing.T, e *executor) {
	t.Helper()
	var intact bool
	err := e.db.QueryRowContext(t.Context(), `SELECT
(SELECT count(*)=0 FROM catalog_subjects) AND (SELECT count(*)=0 FROM catalog_assets)
AND (SELECT count(*)=0 FROM catalog_entities) AND (SELECT count(*)=0 FROM catalog_identities)
AND (SELECT count(*)=0 FROM inventory_objects) AND (SELECT count(*)=0 FROM scan_command_receipts)
AND (SELECT count(*)=0 FROM scan_command_results) AND (SELECT count(*)=0 FROM catalog_scalar_contributions)
AND (SELECT count(*)=0 FROM catalog_asset_contributions) AND (SELECT count(*)=0 FROM catalog_asset_links)
AND (SELECT count(*)=0 FROM search_publish_jobs) AND (SELECT sequence=89 FROM catalog_membership_clock WHERE singleton=1)
AND (SELECT total=97 FROM catalog_age_item_counts WHERE library_id=7 AND kind=1 AND block_unrated=0 AND admission_age=-1)
AND (SELECT total=101 FROM catalog_age_item_counts WHERE library_id=7 AND kind=1 AND block_unrated=1 AND admission_age=7)
AND (SELECT count(*)=0 FROM catalog_age_item_counts WHERE library_id=7 AND kind=1 AND block_unrated=1 AND admission_age=-1)
AND (SELECT data_revision=71 AND data_order_revision=73 AND data_metadata_revision=79 AND age_input_revision=83 AND age_published_revision=83 AND age_pending_operations=0 FROM libraries WHERE id='fresh-library')
AND (SELECT discovered=59 AND job_revision=41 AND last_command_sequence=18 AND acknowledged_command_sequence=18 FROM inventory_runs WHERE job_id='fresh-run')
AND (SELECT processed=67 FROM jobs WHERE id='fresh-run')
AND (SELECT pages=5 AND skipped=3 AND cursor='' AND state='pending' FROM inventory_directories WHERE job_id='fresh-run' AND relative_path='.')
AND (SELECT progress_present=0 AND progress_last_progress_at IS NULL FROM library_sources WHERE id='fresh-source')
AND (SELECT revision=103 AND input_sequence=107 FROM search_revision WHERE singleton=1)
AND (SELECT pending_subjects=0 FROM search_generations WHERE id=1)`).Scan(&intact)
	if err != nil || !intact {
		t.Fatal("partial fresh cohort escaped rollback", intact, err)
	}
}

func TestInventoryFreshNativeEffectsAndHistoricalEcho(t *testing.T) {
	e, path, c, registry := inventoryFreshFixtureDefinitions(t, inventoryCommandDefinitions())
	turn, a := inventoryFreshTestTurn(t, e, path, registry)
	started := time.Now()
	out, err := applyInventoryAccept(t.Context(), turn, c)
	if err != nil {
		t.Fatal(err)
	}
	if out.Outcome != (ReceiptOutcome{}) || out.AcceptedRunRevision != 42 || out.SourceRevision != c.request.files[0].revision || out.SourceIncarnation != "fresh-incarnation" || out.NextCursor != "42" || out.ResultCount != 1 || len(out.Members) != 1 {
		t.Fatal("wrong native echo or domain invented Applied", out)
	}
	if out.AppliedMS != e.now().UnixMilli() {
		t.Fatal("time not from W frame", out.AppliedMS)
	}
	if err = turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	physical, err := turn.transaction.commit()
	if err != nil || physical.state != committed {
		t.Fatal(physical, err)
	}
	t.Logf("component frame wall duration including effects+physical terminal=%s; synthetic Work/clock, NOT latency qualification", time.Since(started))
	if a.Usage() != ([4]governor.Amounts{}) || e.db.Stats().InUse != 0 {
		t.Fatal("custody remained after physical return")
	}
	m := out.Members[0]
	var exact bool
	err = e.db.QueryRowContext(t.Context(), `SELECT
(SELECT job_revision=? AND discovered=60 AND last_command_sequence=19 AND acknowledged_command_sequence=18 FROM inventory_runs WHERE job_id='fresh-run')
AND (SELECT cursor='42' AND pages=6 AND skipped=3 AND state='pending' FROM inventory_directories WHERE job_id='fresh-run' AND relative_path='.')
AND (SELECT processed=68 FROM jobs WHERE id='fresh-run')
AND (SELECT admission_allow_age=-1 AND admission_known_age IS NULL AND admission_own_age IS NULL AND admission_member_count=0 AND admission_unrated_members=0 AND admission_min_known_age IS NULL
 AND resolution_key=0 AND duration_key=0 AND community_rating_key=0 AND critic_rating_key=0 AND file_size_key=42 AND latest_aired_key='' AND episode_count=0 AND aggregate_publication_id IS NULL AND revision=1 FROM catalog_entities WHERE id=?)
AND (SELECT contributor_revision=e.scalar_source_revision AND membership_sequence=90 AND c.duration IS NULL AND c.community_rating IS NULL AND c.critic_rating IS NULL AND c.resolution IS NULL AND c.latest_aired_key IS NULL AND c.admission_age IS NULL FROM catalog_scalar_contributions c JOIN catalog_entities e ON e.id=c.parent_id WHERE c.parent_id=? AND c.contributor_id=? AND c.source=0)
AND (SELECT membership_refs=1 AND authored_refs=0 AND file_size=42 AND c.file_size_revision=a.file_size_revision FROM catalog_asset_contributions c JOIN catalog_assets a ON a.id=c.asset_id WHERE c.parent_id=? AND c.asset_id=?)
AND (SELECT total=98 FROM catalog_age_item_counts WHERE library_id=7 AND kind=1 AND block_unrated=0 AND admission_age=-1)
AND (SELECT total=101 FROM catalog_age_item_counts WHERE library_id=7 AND kind=1 AND block_unrated=1 AND admission_age=7)
AND (SELECT count(*)=0 FROM catalog_age_item_counts WHERE library_id=7 AND kind=1 AND block_unrated=1 AND admission_age=-1)
AND (SELECT data_revision=72 AND data_order_revision=74 AND data_metadata_revision=80 AND age_input_revision=84 AND age_published_revision=84 AND age_pending_operations=0 FROM libraries WHERE id='fresh-library')
AND (SELECT state='available' AND retired=0 AND analysis_state='pending' AND analysis_revision='' AND has_analysis_probe_bindings=0 AND object_identity='ino:31' AND revision=? FROM inventory_objects WHERE id=?)
AND (SELECT info_epoch=? AND info_owner_object IS NULL AND info_owner_revision IS NULL AND size=42 AND file_size_revision>0 FROM catalog_assets WHERE id=?)
AND (SELECT revision=103 AND input_sequence=108 FROM search_revision WHERE singleton=1)
AND (SELECT pending_subjects=1 FROM search_generations WHERE id=1)
AND (SELECT j.source_revision=1 AND source_incarnation=e.metadata_incarnation AND j.phase='capture' AND input_generation=1 AND published_generation=0 FROM search_publish_jobs j JOIN catalog_entities e ON e.id=j.entity_id WHERE j.entity_id=?)`,
		out.AcceptedRunRevision, m.SubjectID, m.SubjectID, m.SubjectID, m.SubjectID, m.AssetID, out.SourceRevision, m.ObjectID, m.AcceptedAssetRevision, m.AssetID, m.SubjectID).Scan(&exact)
	if err != nil || !exact {
		t.Fatal("incomplete native birth/aggregate/search image", exact, err)
	}
	// Historical receipt decoding must not reconstruct from today's object,
	// directory, source, lease or counters. Destroy/change those CURRENT facts.
	for _, q := range []string{
		`DELETE FROM inventory_objects`,
		`UPDATE inventory_runs SET job_revision=9223372036854775807,discovered=9223372036854775807,job_lease_expires_ms=0 WHERE job_id='fresh-run'`,
		`UPDATE library_sources SET generation=99,incarnation='later-incarnation',root='/replacement' WHERE id='fresh-source'`,
		`UPDATE inventory_directories SET pages=9223372036854775807,cursor='later' WHERE job_id='fresh-run'`,
		`UPDATE catalog_membership_clock SET sequence=9223372036854775807 WHERE singleton=1`,
		`UPDATE libraries SET data_revision=9223372036854775807,data_order_revision=9223372036854775807,data_metadata_revision=9223372036854775807,age_input_revision=9223372036854775807,age_published_revision=9223372036854775807 WHERE id='fresh-library'`,
		`UPDATE catalog_assets SET info_epoch=139,file_size_revision=149 WHERE library_id=7`,
	} {
		if _, err = e.db.ExecContext(t.Context(), q); err != nil {
			t.Fatal(err)
		}
	}
	// Use W's existing closed historical adapter on this separately admitted
	// test frame. Public current-disclosure routing is W's integration test.
	historyTurn, _ := inventoryMemoryFixtureTurn(t, e, registry, c, inventoryMemoryBudget{4 << 20, 4 << 20})
	request, err := c.receiptRequest()
	if err != nil {
		t.Fatal(err)
	}
	got, err := lookupInventoryAccept(t.Context(), historyTurn, request)
	if err != nil || got.result.SourceRevision != out.SourceRevision || got.result.AcceptedRunRevision != 42 || got.digest != out.Digest || len(got.result.Members) != 1 || !reflect.DeepEqual(InventoryMember(got.result.Members[0]), out.Members[0]) {
		t.Fatal("saved receipt reconstructed current facts", got, err)
	}
}

func TestInventoryFreshNativeStaleAndOverflowRefuseWholeCohort(t *testing.T) {
	e, path, c, registry := inventoryFreshFixture(t)
	for name, change := range map[string]string{
		"source_generation":           `UPDATE library_sources SET generation=24 WHERE id='fresh-source'`,
		"root_identity":               `UPDATE library_sources SET root_identity='rebound' WHERE id='fresh-source'`,
		"root_path":                   `UPDATE library_sources SET root='/replacement' WHERE id='fresh-source'`,
		"source_disabled":             `UPDATE library_sources SET enabled=0 WHERE id='fresh-source'`,
		"source_identity_unconfirmed": `UPDATE library_sources SET identity_confirmed=0 WHERE id='fresh-source'`,
		"source_health":               `UPDATE library_sources SET health='offline' WHERE id='fresh-source'`,
		"policy":                      `UPDATE library_scan_policies SET revision=8 WHERE library_id='fresh-library'`,
		"run_revision":                `UPDATE inventory_runs SET job_revision=42 WHERE job_id='fresh-run'`,
		"lease_owner":                 `UPDATE inventory_runs SET job_lease_owner=14 WHERE job_id='fresh-run'`,
		"lease_generation":            `UPDATE inventory_runs SET job_lease_generation=18 WHERE job_id='fresh-run'`,
		"lease_expiry":                `UPDATE inventory_runs SET job_lease_expires_ms=0 WHERE job_id='fresh-run'`,
		"sequence":                    `UPDATE inventory_runs SET last_command_sequence=19 WHERE job_id='fresh-run'`,
		"directory_identity":          `UPDATE inventory_directories SET identity='replacement' WHERE job_id='fresh-run'`,
		"directory_revision":          `UPDATE inventory_directories SET revision='other' WHERE job_id='fresh-run'`,
		"directory_cursor":            `UPDATE inventory_directories SET cursor='41' WHERE job_id='fresh-run'`,
		"directory_state":             `UPDATE inventory_directories SET state='done' WHERE job_id='fresh-run'`,
		"job_status":                  `UPDATE jobs SET status='failed' WHERE id='fresh-run'`,
		"library_kind":                `UPDATE libraries SET kind='music' WHERE id='fresh-library'`,
		"missing_membership":          `DELETE FROM catalog_membership_clock`,
		"membership_exhausted":        `UPDATE catalog_membership_clock SET sequence=9223372036854775807`,
		"count_exhausted":             `UPDATE catalog_age_item_counts SET total=9223372036854775807 WHERE block_unrated=0`,
		"discovered_exhausted":        `UPDATE inventory_runs SET discovered=9223372036854775807`,
		"processed_exhausted":         `UPDATE jobs SET processed=9223372036854775807`,
		"data_exhausted":              `UPDATE libraries SET data_revision=9223372036854775807`,
		"order_exhausted":             `UPDATE libraries SET data_order_revision=9223372036854775807`,
		"metadata_exhausted":          `UPDATE libraries SET data_metadata_revision=9223372036854775807`,
		"age_input_exhausted":         `UPDATE libraries SET age_input_revision=9223372036854775807`,
		"corrupt_future_published":    `UPDATE libraries SET age_published_revision=84`,
		"search_missing_bank":         `UPDATE search_revision SET active_generation_id=NULL`,
		"search_clock_exhausted":      `UPDATE search_revision SET input_sequence=9223372036854775807`,
	} {
		t.Run(name, func(t *testing.T) {
			turn, a := inventoryFreshTestTurn(t, e, path, registry)
			if _, err := turn.transaction.conn.ExecContext(t.Context(), change); err != nil {
				t.Fatal(err)
			}
			out, err := applyInventoryAccept(t.Context(), turn, c)
			if err == nil || !reflect.DeepEqual(out, InventoryResult{}) {
				t.Fatal("fresh stale/overflow yielded data", out, err)
			}
			if closeErr := turn.closeRows(); closeErr != nil {
				t.Fatal(closeErr)
			}
			if _, rollbackErr := turn.transaction.rollback(err); !errors.Is(rollbackErr, err) {
				t.Fatal(rollbackErr)
			}
			if a.Usage() != ([4]governor.Amounts{}) || e.db.Stats().InUse != 0 {
				t.Fatal("refusal retained physical custody")
			}
			inventoryFreshAssertAbsent(t, e)
		})
	}
}

func TestInventoryFreshNativeDirtyAgeAndAbsentBucket(t *testing.T) {
	e, path, c, registry := inventoryFreshFixture(t)
	for _, state := range []struct {
		name                      string
		input, published, pending int64
		absent                    bool
	}{
		{"older_publication", 83, 81, 0, false},
		{"pending_with_equal_clocks", 83, 83, 2, false},
		{"older_and_pending", 83, 81, 2, false},
		{"absent_mode0_bucket", 83, 83, 0, true},
	} {
		t.Run(state.name, func(t *testing.T) {
			turn, _ := inventoryFreshTestTurn(t, e, path, registry)
			if _, err := turn.transaction.conn.ExecContext(t.Context(), `UPDATE libraries SET age_input_revision=?,age_published_revision=?,age_pending_operations=? WHERE id='fresh-library'`, state.input, state.published, state.pending); err != nil {
				t.Fatal(err)
			}
			if state.absent {
				if _, err := turn.transaction.conn.ExecContext(t.Context(), `DELETE FROM catalog_age_item_counts WHERE block_unrated=0`); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := applyInventoryAccept(t.Context(), turn, c); err != nil {
				t.Fatal(err)
			}
			var input, published, pending, total, blocked, wrongBucket int64
			if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT age_input_revision,age_published_revision,age_pending_operations FROM libraries WHERE id='fresh-library'`).Scan(&input, &published, &pending); err != nil {
				t.Fatal(err)
			}
			wantPublished := state.published
			if state.input == state.published && state.pending == 0 {
				wantPublished = state.input + 1
			}
			if input != state.input+1 || published != wantPublished || pending != state.pending {
				t.Fatal("dirty frontier made current", input, published, pending)
			}
			if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT (SELECT total FROM catalog_age_item_counts WHERE block_unrated=0),(SELECT total FROM catalog_age_item_counts WHERE block_unrated=1 AND admission_age=7),(SELECT count(*) FROM catalog_age_item_counts WHERE block_unrated=1 AND admission_age=-1)`).Scan(&total, &blocked, &wrongBucket); err != nil {
				t.Fatal(err)
			}
			wantTotal := int64(98)
			if state.absent {
				wantTotal = 1
			}
			if total != wantTotal || blocked != 101 || wrongBucket != 0 {
				t.Fatal("wrong unrated count", total, blocked, wrongBucket)
			}
			if err := turn.closeRows(); err != nil {
				t.Fatal(err)
			}
			if _, err := turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}
			inventoryFreshAssertAbsent(t, e)
		})
	}
}

func TestInventoryFreshCounterAndPreparationBounds(t *testing.T) {
	for _, v := range []int64{-1, math.MaxInt64} {
		if _, err := inventoryNext(v); err == nil {
			t.Fatal("invalid counter advanced", v)
		}
	}
	if v, err := inventoryNext(math.MaxInt64 - 1); err != nil || v != math.MaxInt64 {
		t.Fatal(v, err)
	}
	if n, err := inventoryFreshRequiredBytes(nil); err == nil || n != 0 {
		t.Fatal(n, err)
	}
	c := inventoryFreshRequest(t)
	if allocs := testing.AllocsPerRun(100, func() {
		if _, err := inventoryFreshRequiredBytes(c); err != nil {
			panic(err)
		}
	}); allocs != 0 {
		t.Fatal("forecast allocated", allocs)
	}
	for name, mutate := range map[string]func(*inventoryAcceptCommand){
		"scope_limit":        func(c *inventoryAcceptCommand) { c.request.source.id = strings.Repeat("x", 1024) },
		"change_token_limit": func(c *inventoryAcceptCommand) { c.request.files[0].changeToken = strings.Repeat("x", 4097) },
		"object_limit":       func(c *inventoryAcceptCommand) { c.request.files[0].objectIdentity = strings.Repeat("x", 8193) },
		"request_limit": func(c *inventoryAcceptCommand) {
			c.request.source.resolvedRoot = make([]byte, maxCanonicalRequestBytes)
		},
	} {
		t.Run(name, func(t *testing.T) {
			c := inventoryFreshRequest(t)
			mutate(c)
			if bytes, err := inventoryFreshRequiredBytes(c); err == nil || bytes != 0 {
				t.Fatal("unsupported evidence allocated forecast", bytes, err)
			}
		})
	}
}

func TestInventoryFreshEscapingPreparationAllocationBound(t *testing.T) {
	base := inventoryFreshRequest(t)
	r := base.request
	r.files = append([]inventoryFile(nil), r.files...)
	r.source.id = strings.Repeat("<&>", 250)
	r.files[0].changeToken = strings.Repeat("\x00", 4096)
	r.files[0].objectIdentity = strings.Repeat("&", 8192)
	evidence := mediasource.InventoryEvidence{Kind: "local", Scope: r.source.id + ":" + r.source.incarnation, Object: r.files[0].objectIdentity,
		Size: math.MaxInt64, ModifiedNS: math.MinInt64, ChangeToken: r.files[0].changeToken}
	r.files[0].size, r.files[0].modifiedNS = evidence.Size, evidence.ModifiedNS
	var err error
	r.files[0].revision, err = evidence.Revision()
	if err != nil {
		t.Fatal(err)
	}
	c, err := newInventoryAcceptCommand(base.identity, r)
	if err != nil {
		t.Fatal(err)
	}
	budget, err := inventoryFreshRequiredBytes(c)
	if err != nil {
		t.Fatal(err)
	}
	result := testing.Benchmark(func(b *testing.B) {
		for range b.N {
			if _, err := prepareInventoryFreshObservation(c); err != nil {
				b.Fatal(err)
			}
		}
	})
	if result.N < 1 || result.AllocedBytesPerOp() < 1 || uint64(result.AllocedBytesPerOp()) > budget {
		t.Fatal("preparation exceeds its whole-frame envelope", result.AllocedBytesPerOp(), budget)
	}
	t.Logf("escaped pure preparation allocates %d bytes/op within %d-byte WHOLE SC envelope; excludes W/native reserve and is not physical peak/hold qualification", result.AllocedBytesPerOp(), budget)
}

func TestInventoryFreshBodyRequiresRealMemoryAndReturnsZeroOnCancel(t *testing.T) {
	e, path, c, registry := inventoryFreshFixture(t)
	plain := scanObjectTestTurn(t, e, scanInventoryFreshDefinitions())
	if out, err := applyInventoryAccept(t.Context(), plain, c); err == nil || !reflect.DeepEqual(out, InventoryResult{}) {
		t.Fatal("Capture/plain custody authorized memory", out, err)
	}
	if err := plain.closeRows(); err != nil {
		t.Fatal(err)
	}
	if _, err := plain.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	turn, _ := inventoryFreshTestTurn(t, e, path, registry)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if out, err := applyInventoryAccept(ctx, turn, c); !errors.Is(err, context.Canceled) || !reflect.DeepEqual(out, InventoryResult{}) {
		t.Fatal("cancel disclosed data", out, err)
	}
	if err := turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	inventoryFreshAssertAbsent(t, e)
}

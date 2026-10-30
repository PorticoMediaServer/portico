package catalog

// P2-59 / O-022 SeparateFolder retirement units: real-baseline source tests.
//
// These tests install the real baseline and drive the compiled units under the
// real phasedpurge engine in a real writer transaction. They add no schema,
// counter or phase. Fixture rows are real domain inputs written into the exact
// tables the existing owners read. The parent Owner used here is a minimal
// stand-in for root: it rechecks the actual mutation generation before every
// turn and stores the one shared cursor in catalog_purge_jobs bound to the
// compiled digest. The two hooks execute the exact existing prepared-media
// cancelTx and downloads revokeReceipt statements. The production wrappers
// (preparedmedia.CancelForRetirementTx, downloads.RevokeForRetirement) delegate
// to those same functions; importing downloads here would create an import
// cycle (downloads -> playback -> catalog), so the test keeps the hook seam and
// the same literal SQL instead.
//
// What is deliberately NOT faked: no unit head, cursor, job state, receipt
// sequence or request state is written by the test's expectations. Every value
// asserted is written by the compiled unit, the existing cancellation/revocation
// statement, or the shared engine's Save/Finalize.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/phasedpurge"
)

type separateFolderRetirementFixture struct {
	c         *catalogtest.Catalog
	m         compactcatalog.EntityMutation
	item      catalogtest.Item
	sibling   catalogtest.Item
	plan      []phasedpurge.Unit
	digest    string
	now       int64
	cancelled []string
	interrupt []string
}

func newSeparateFolderRetirementFixture(t *testing.T) *separateFolderRetirementFixture {
	t.Helper()
	c := catalogtest.Open(t)
	library := c.Library("books", "Books", "audiobook", "/books")
	book := c.Book(library, "Original", "Author")
	item := c.BookFile(book, 1, "/books/Original/part1.m4b")
	siblingBook := c.Book(library, "Sibling", "Author")
	sibling := c.BookFile(siblingBook, 1, "/books/Sibling/part1.m4b")
	f := &separateFolderRetirementFixture{c: c, item: item, sibling: sibling, now: 1_700_000_000_000}
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		ref, err := compactcatalog.BeginEntityMutationTx(ctx, tx, compactcatalog.MutationIntent{
			Entity: item.ID, OwnerKind: "separate", OwnerID: "folder-command", OwnerGeneration: 1, Mode: "separate",
			SplitSourceID: "source", SplitDirectory: "/books/Original", SplitObjectID: "object", SplitObjectRevision: "revision",
		})
		if err != nil {
			return err
		}
		f.m, err = compactcatalog.ReadEntityMutationTx(ctx, tx, ref)
		return err
	})
	f.digest = SeparateFolderRetirementPlanDigest(item.ID, item.Token)
	plan, err := SeparateFolderRetirementUnits(item.ID, f.hooks(), item.Token)
	if err != nil {
		t.Fatal(err)
	}
	f.plan = plan
	c.Exec(`INSERT INTO catalog_purge_jobs(mutation_id,generation,plan_digest,state) VALUES(?,?,?,'running')`,
		f.m.Ref.ID, f.m.Ref.Generation, f.digest)
	return f
}

// hooks is the owner seam. Each body is the existing statement reproduced
// verbatim; the production wrappers call the same cancelTx/revokeReceipt.
func (f *separateFolderRetirementFixture) hooks() SeparateFolderRetirementHooks {
	return SeparateFolderRetirementHooks{
		CancelJob: func(ctx context.Context, tx *sql.Tx, id string) error {
			f.cancelled = append(f.cancelled, id)
			_, err := tx.ExecContext(ctx, `UPDATE prepared_media_jobs SET state=CASE WHEN state='queued' THEN 'cancelled' ELSE 'cancelling' END,
 phase='cancellation requested',revision=revision+1,updated_ms=? WHERE id=? AND state IN('queued','running')`, f.now, id)
			return err
		},
		Interrupt: func(id string) { f.interrupt = append(f.interrupt, id) },
		RevokeReceipt: func(ctx context.Context, tx *sql.Tx, id string) error {
			if _, err := tx.ExecContext(ctx, `UPDATE download_receipt_sequence SET value=value+1 WHERE singleton=1`); err != nil {
				return err
			}
			var sequence int64
			if err := tx.QueryRowContext(ctx, `SELECT value FROM download_receipt_sequence WHERE singleton=1`).Scan(&sequence); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx, `UPDATE download_receipts SET revoked_ms=?,revoked_reason=?,sequence=?,revision=revision+1
 WHERE id=? AND revoked_ms=0`, f.now, "source_changed", sequence, id)
			return err
		},
	}
}

func (f *separateFolderRetirementFixture) owner() phasedpurge.Owner {
	return phasedpurge.Owner{
		ReadBound: 8,
		Before: func(ctx context.Context, tx *sql.Tx) error {
			var generation int64
			if err := tx.QueryRowContext(ctx, `SELECT generation FROM catalog_entity_mutations
 WHERE mutation_id=? AND entity_id=? AND status<>'terminal'`, f.m.Ref.ID, f.item.ID).Scan(&generation); err != nil {
				return err
			}
			if generation != f.m.Ref.Generation {
				return errors.New("separate folder retirement: mutation generation changed")
			}
			return nil
		},
		Save: func(ctx context.Context, tx *sql.Tx, c phasedpurge.Cursor) error {
			return f.saveCursor(ctx, tx, c)
		},
		Finalize: func(ctx context.Context, tx *sql.Tx) error {
			return f.saveCursor(ctx, tx, phasedpurge.Cursor{Unit: len(f.plan)})
		},
	}
}

func (f *separateFolderRetirementFixture) saveCursor(ctx context.Context, tx *sql.Tx, c phasedpurge.Cursor) error {
	raw, err := phasedpurge.EncodeCursor(c)
	if err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE catalog_purge_jobs SET cursor=? WHERE mutation_id=? AND generation=? AND plan_digest=?`,
		raw, f.m.Ref.ID, f.m.Ref.Generation, f.digest)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return errors.New("separate folder retirement: cursor guard lost its command")
	}
	return nil
}

func retirementChanges(ctx context.Context, tx *sql.Tx) (int64, error) {
	var n int64
	err := tx.QueryRowContext(ctx, `SELECT total_changes()`).Scan(&n)
	return n, err
}

// step runs one real engine turn inside one writer transaction, reading the
// durable cursor back from the command's job row. Save/Finalize and the metered
// unit effects commit together.
func (f *separateFolderRetirementFixture) step(t *testing.T) (phasedpurge.Result, int64, error) {
	t.Helper()
	var result phasedpurge.Result
	var effects int64
	ctx := context.Background()
	err := dbwork.WithWriteTx(ctx, f.c.DB, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		var raw string
		if err := tx.QueryRowContext(ctx, `SELECT cursor FROM catalog_purge_jobs
 WHERE mutation_id=? AND generation=? AND plan_digest=?`, f.m.Ref.ID, f.m.Ref.Generation, f.digest).Scan(&raw); err != nil {
			return err
		}
		cursor, err := phasedpurge.DecodeCursor(raw, f.plan)
		if err != nil {
			return err
		}
		before, err := retirementChanges(ctx, tx)
		if err != nil {
			return err
		}
		result, err = phasedpurge.Turn(ctx, tx, f.plan, cursor, f.owner())
		after, meterErr := retirementChanges(ctx, tx)
		if meterErr != nil {
			return meterErr
		}
		effects = after - before
		return err
	})
	return result, effects, err
}

var separateFolderRetirementTables = []string{
	"prepared_media_jobs", "prepared_media_versions", "download_grants", "download_receipts",
	"download_preparations", "download_request_items", "download_requests",
}

func retirementSnapshot(t *testing.T, c *catalogtest.Catalog) []string {
	t.Helper()
	var out []string
	for _, table := range separateFolderRetirementTables {
		rows, err := c.DB.Query(`SELECT * FROM ` + table)
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			rows.Close()
			t.Fatal(err)
		}
		for rows.Next() {
			values, dest := make([]any, len(columns)), make([]any, len(columns))
			for i := range values {
				dest[i] = &values[i]
			}
			if err = rows.Scan(dest...); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			out = append(out, fmt.Sprintf("%s:%#v", table, values))
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	sort.Strings(out)
	return out
}

// retirementRowDiff counts rows that disappeared between two snapshots. Each
// point update removes exactly one old row, so a changed turn must report one.
func retirementRowDiff(before, after []string) int {
	counts := make(map[string]int, len(before))
	for _, row := range before {
		counts[row]++
	}
	for _, row := range after {
		counts[row]--
	}
	removed := 0
	for _, n := range counts {
		if n > 0 {
			removed += n
		}
	}
	return removed
}

func (f *separateFolderRetirementFixture) drive(t *testing.T, max int) int {
	t.Helper()
	for turn := 0; turn < max; turn++ {
		before := retirementSnapshot(t, f.c)
		result, effects, err := f.step(t)
		if err != nil {
			t.Fatalf("turn %d: %v", turn, err)
		}
		if effects > phasedpurge.EffectLimit {
			t.Fatalf("turn %d effects=%d limit=%d", turn, effects, phasedpurge.EffectLimit)
		}
		if result.Reads > phasedpurge.ReadLimit {
			t.Fatalf("turn %d reads=%d limit=%d", turn, result.Reads, phasedpurge.ReadLimit)
		}
		if result.Done {
			return turn + 1
		}
		diff := retirementRowDiff(before, retirementSnapshot(t, f.c))
		if diff == 0 {
			continue
		}
		if diff != 1 {
			t.Fatalf("turn %d changed %d retirement rows; one raw head effect is the contract", turn, diff)
		}
		// Restart at every unit boundary: a freshly compiled plan must accept
		// the cursor this turn made durable.
		fresh, err := SeparateFolderRetirementUnits(f.item.ID, f.hooks(), f.item.Token)
		if err != nil {
			t.Fatal(err)
		}
		var raw string
		if err = f.c.DB.QueryRow(`SELECT cursor FROM catalog_purge_jobs WHERE mutation_id=?`, f.m.Ref.ID).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		decoded, err := phasedpurge.DecodeCursor(raw, fresh)
		if err != nil {
			t.Fatalf("restart at turn %d: %v", turn, err)
		}
		if decoded.Unit != result.Cursor.Unit || decoded.Verify != result.Cursor.Verify {
			t.Fatalf("restart at turn %d decoded %+v, engine saved %+v", turn, decoded, result.Cursor)
		}
	}
	t.Fatalf("retirement did not finish in %d turns", max)
	return max
}

func retirementUnit(t *testing.T, plan []phasedpurge.Unit, name string) phasedpurge.Unit {
	t.Helper()
	for _, unit := range plan {
		if unit.Name == name {
			return unit
		}
	}
	t.Fatalf("unit %s was not compiled", name)
	return phasedpurge.Unit{}
}

func retirementText(t *testing.T, c *catalogtest.Catalog, query string, args ...any) string {
	t.Helper()
	var out string
	if err := c.DB.QueryRow(query, args...).Scan(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func retirementInt(t *testing.T, c *catalogtest.Catalog, query string, args ...any) int64 {
	t.Helper()
	var out int64
	if err := c.DB.QueryRow(query, args...).Scan(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func retirementExplain(t *testing.T, c *catalogtest.Catalog, query string, args ...any) string {
	t.Helper()
	rows, err := c.DB.Query(`EXPLAIN QUERY PLAN `+query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(plan, "\n")
}

// seedClaims writes one original-item claim of every kind, one unrelated
// sibling claim of each kind, and several capturing requests for the original
// item. All rows are inserted before the retirement starts; the sibling rows
// must survive byte for byte.
func (f *separateFolderRetirementFixture) seedClaims(t *testing.T) {
	t.Helper()
	c, item, sibling := f.c, f.item, f.sibling
	c.Exec(`INSERT INTO prepared_media_jobs(id,item_id,asset_id,library_id,profile_id,target_id,selection_json,source_revision,principal_json,state,phase,created_ms,updated_ms) VALUES
 ('job-queued',?,?,'books','profile','target','{}','revision','{}','queued','waiting',1,2),
 ('job-sibling',?,?,'books','profile','target','{}','revision','{}','queued','waiting',1,2)`, item.ID, item.Token, sibling.ID, sibling.Token)
	c.Exec(`INSERT INTO prepared_media_versions(id,item_id,asset_id,library_id,profile_id,target_id,source_revision,selection_json,part_index,input_evidence,transformation_digest,digest,size,facts_json,state,created_ms,job_id) VALUES
 ('version-published',?,?,'books','profile','target','revision','{}',0,'evidence','transform','digest',100,'{}','published',1,'job-queued'),
 ('version-sibling',?,?,'books','profile','target','revision','{}',0,'evidence','transform','digest',100,'{}','published',1,'job-sibling')`, item.ID, item.Token, sibling.ID, sibling.Token)
	c.Exec(`INSERT INTO playback_sessions(id,session_hash,account_id,profile_id,item_id,asset_id,generation,state,grant_hash,grant_token,expires_at,request_id,duration)
 VALUES('session-pin','hash','account','profile',?,?,1,'playing','session-grant','token','2099-01-01T00:00:00Z','request',100)`, item.ID, item.Token)
	c.Exec(`INSERT INTO prepared_media_session_pins(session_id,version_id,digest,size) VALUES('session-pin','version-published','digest',100)`)
	c.Exec(`INSERT INTO download_preparations(id,profile_key,authority,account_id,profile_id,item_id,library_id,quality,origin,state,hash_state,created_ms,updated_ms) VALUES
 ('prep-live','profile','local','account','profile',?,'books','original','item','ready',X'00',1,2),
 ('prep-sibling','profile','local','account','profile',?,'books','original','item','ready',X'00',1,2)`, item.ID, sibling.ID)
	c.Exec(`INSERT INTO download_grants(hash,preparation_id,profile_key,item_id,issued_ms,expires_ms,revoked) VALUES
 ('grant-live','prep-live','profile',?,1,99,0),
 ('grant-retired','prep-live','profile',?,1,99,1),
 ('grant-sibling','prep-sibling','profile',?,1,99,0)`, item.ID, item.ID, sibling.ID)
	c.Exec(`INSERT INTO download_receipt_keys(id,algorithm,private_key,public_key,created_ms) VALUES('key','ed25519',X'01',X'02',1)`)
	c.Exec(`INSERT INTO download_receipts(id,profile_key,authority,account_id,profile_id,item_id,library_id,preparation_id,quality,publication_required,artifact_digest,artifact_bytes,key_id,issued_ms,expires_ms,revoked_ms,revoked_reason,sequence) VALUES
 ('receipt-live','profile','local','account','profile',?,'books','prep-live','original',0,'digest',100,'key',1,99,0,'',0),
 ('receipt-retired','profile','local','account','profile',?,'books','prep-live','original',0,'digest',100,'key',1,99,5,'revoked',0),
 ('receipt-sibling','profile','local','account','profile',?,'books','prep-sibling','original',0,'digest',100,'key',1,99,0,'',0)`,
		item.ID, item.ID, sibling.ID)
	c.Exec(`INSERT INTO download_requests(id,profile_key,operation_id,request_hash,principal,device_id,target_kind,target_id,quality,episodes,keep_next,selection_fence,state,created_ms,updated_ms) VALUES
 ('request-1','profile','op-1','hash','{}','device','item',?,'original','all',0,'fence','capturing',1,2),
 ('request-2','profile','op-2','hash','{}','device','item',?,'original','all',0,'fence','capturing',1,2),
 ('request-3','profile','op-3','hash','{}','device','item',?,'original','all',0,'fence','capturing',1,2),
 ('request-sibling','profile','op-sibling','hash','{}','device','item',?,'original','all',0,'fence','capturing',1,2)`,
		item.ID, item.ID, item.ID, sibling.ID)
	c.Exec(`INSERT INTO download_request_items(request_id,ordinal,item_id,reason) VALUES
 ('request-1',0,?,''),
 ('request-2',0,?,''),
 ('request-3',0,?,''),
 ('request-3',1,?,''),
 ('request-sibling',0,?,'')`, item.ID, item.ID, item.ID, sibling.ID, sibling.ID)
	c.Exec(`UPDATE download_preparations SET source_asset_id=? WHERE id='prep-live'`, item.Token)
	c.Exec(`UPDATE download_preparations SET source_asset_id=? WHERE id='prep-sibling'`, sibling.Token)
	c.Exec(`UPDATE download_request_items SET preparation_id='prep-live' WHERE item_id=?`, item.ID)
}

func TestSeparateFolderRetirementUnitsRefuseMissingHooks(t *testing.T) {
	if _, err := SeparateFolderRetirementUnits(0, SeparateFolderRetirementHooks{}, "asset"); !errors.Is(err, errSeparateFolderRetirementScope) {
		t.Fatalf("missing hooks/item was accepted: %v", err)
	}
	f := newSeparateFolderRetirementFixture(t)
	if _, err := SeparateFolderRetirementUnits(f.item.ID, SeparateFolderRetirementHooks{
		CancelJob: func(context.Context, *sql.Tx, string) error { return nil },
		Interrupt: func(string) {},
	}, f.item.Token); !errors.Is(err, errSeparateFolderRetirementHooks) {
		t.Fatalf("missing receipt hook was accepted: %v", err)
	}
	if f.digest == SeparateFolderRetirementPlanDigest(f.sibling.ID, f.sibling.Token) {
		t.Fatal("plan digest is not bound to the exact original item")
	}
	if _, err := SeparateFolderRetirementUnits(f.item.ID, f.hooks(), ""); !errors.Is(err, errSeparateFolderRetirementScope) {
		t.Fatal("empty moved-asset scope was accepted", err)
	}
}

func TestSeparateFolderRetirementHeadsUseTheirInstalledIndex(t *testing.T) {
	c := catalogtest.Open(t)
	heads := []struct{ name, index, query string }{
		{"cancel", "separate_asset_jobs", separateFolderJobCancelHeadSQL},
		{"quiesce", "separate_asset_jobs", separateFolderJobQuiesceHeadSQL},
		{"version", "separate_asset_versions", separateFolderVersionHeadSQL},
		{"preparation", "separate_asset_preparations", separateAssetPreparationHead},
		{"grant", "separate_preparation_grants", separateAssetGrantHead},
		{"receipt", "separate_preparation_receipts", separateAssetReceiptHead},
		{"request", "separate_preparation_requests", separateAssetRequestHead},
	}
	for _, head := range heads {
		t.Run(head.name, func(t *testing.T) {
			plan := retirementExplain(t, c, head.query, int64(1), "asset")
			if !strings.Contains(plan, "SEARCH") || !strings.Contains(plan, head.index) ||
				strings.Contains(plan, "TEMP B-TREE") || strings.Contains(plan, "SCAN ") {
				t.Fatalf("head is not a bounded prefix seek:\n%s", plan)
			}
		})
	}
	// Pin the source literal independently: SQLite can re-plan a bound value.
	for _, query := range []string{separateFolderRequestTargetHeadSQL, separateFolderRequestTargetProbeSQL} {
		if !strings.Contains(query, "state='capturing'") || strings.Contains(query, "state=?") {
			t.Fatal("capturing retirement must retain its literal partial-index predicate", query)
		}
	}
	if plan := retirementExplain(t, c, separateFolderRequestTargetHeadSQL, int64(1)); !strings.Contains(plan, "download_requests_retirement") {
		t.Fatalf("literal capturing head lost its partial index:\n%s", plan)
	}
	other := retirementExplain(t, c, `SELECT id FROM download_requests WHERE target_id=? AND state='complete' ORDER BY id LIMIT 1`, int64(1))
	if strings.Contains(other, "download_requests_retirement") {
		t.Fatalf("non-capturing head used the capturing-only index:\n%s", other)
	}
}

func TestSeparateFolderRetirementEndsOnlyOriginalClaimsOnePointPerTurn(t *testing.T) {
	f := newSeparateFolderRetirementFixture(t)
	f.seedClaims(t)
	turns := f.drive(t, 96)

	if len(f.cancelled) != 1 || len(f.interrupt) != 1 || f.cancelled[0] != "job-queued" || f.interrupt[0] != "job-queued" {
		t.Fatalf("queued job was not cancelled before commit: cancelled=%v interrupt=%v", f.cancelled, f.interrupt)
	}
	if got := retirementText(t, f.c, `SELECT state FROM prepared_media_jobs WHERE id='job-queued'`); got != "cancelled" {
		t.Fatalf("queued job state=%q", got)
	}
	if got := retirementText(t, f.c, `SELECT state FROM prepared_media_jobs WHERE id='job-sibling'`); got != "queued" {
		t.Fatalf("unrelated prepared job changed: %q", got)
	}
	if got := retirementText(t, f.c, `SELECT state FROM prepared_media_versions WHERE id='version-published'`); got != "deleting" {
		t.Fatalf("published version state=%q", got)
	}
	if got := retirementText(t, f.c, `SELECT state FROM prepared_media_versions WHERE id='version-sibling'`); got != "published" {
		t.Fatalf("unrelated version changed: %q", got)
	}
	// The published version enters the existing deleting state; held playback
	// and the collector's session pin are preserved, not deleted.
	if n := retirementInt(t, f.c, `SELECT count(*) FROM prepared_media_session_pins WHERE session_id='session-pin' AND version_id='version-published'`); n != 1 {
		t.Fatalf("session pin was mutated: %d", n)
	}
	if n := retirementInt(t, f.c, `SELECT count(*) FROM playback_sessions WHERE id='session-pin'`); n != 1 {
		t.Fatalf("playback session was mutated: %d", n)
	}
	if n := retirementInt(t, f.c, `SELECT revoked FROM download_grants WHERE hash='grant-live'`); n != 1 {
		t.Fatalf("live grant revoked=%d", n)
	}
	if n := retirementInt(t, f.c, `SELECT revoked FROM download_grants WHERE hash='grant-retired'`); n != 1 {
		t.Fatalf("already revoked grant was rewritten: %d", n)
	}
	if n := retirementInt(t, f.c, `SELECT revoked FROM download_grants WHERE hash='grant-sibling'`); n != 0 {
		t.Fatalf("unrelated grant revoked=%d", n)
	}
	// The receipt hook keeps the real sequence singleton and feed: one live
	// receipt consumed exactly one revocation number, and the already revoked
	// receipt did not move.
	if got := retirementText(t, f.c, `SELECT revoked_reason FROM download_receipts WHERE id='receipt-live'`); got != "source_changed" {
		t.Fatalf("live receipt reason=%q", got)
	}
	sequence := retirementInt(t, f.c, `SELECT sequence FROM download_receipts WHERE id='receipt-live'`)
	if sequence != 1 {
		t.Fatalf("live receipt sequence=%d", sequence)
	}
	if got := retirementInt(t, f.c, `SELECT value FROM download_receipt_sequence WHERE singleton=1`); got != sequence {
		t.Fatalf("sequence singleton=%d receipt=%d", got, sequence)
	}
	if got := retirementText(t, f.c, `SELECT revoked_reason FROM download_receipts WHERE id='receipt-retired'`); got != "revoked" {
		t.Fatalf("already revoked receipt was rewritten: %q", got)
	}
	if n := retirementInt(t, f.c, `SELECT revoked_ms FROM download_receipts WHERE id='receipt-sibling'`); n != 0 {
		t.Fatalf("unrelated receipt revoked_ms=%d", n)
	}
	// The preparation is a point write: unavailable/source_changed, hash state
	// cleared. Grants were drained by their own unit, never by endClaim here.
	if got := retirementText(t, f.c, `SELECT state FROM download_preparations WHERE id='prep-live'`); got != "unavailable" {
		t.Fatalf("preparation state=%q", got)
	}
	if got := retirementText(t, f.c, `SELECT reason FROM download_preparations WHERE id='prep-live'`); got != "source_changed" {
		t.Fatalf("preparation reason=%q", got)
	}
	if n := retirementInt(t, f.c, `SELECT count(*) FROM download_preparations WHERE id='prep-live' AND hash_state IS NULL`); n != 1 {
		t.Fatalf("preparation hash state was not cleared: %d", n)
	}
	if got := retirementText(t, f.c, `SELECT state FROM download_preparations WHERE id='prep-sibling'`); got != "ready" {
		t.Fatalf("unrelated preparation changed: %q", got)
	}
	// Request items keep their request/ordinal and real summary; the target
	// requests become failed/source_changed with no parent cascade.
	if n := retirementInt(t, f.c, `SELECT count(*) FROM download_request_items WHERE item_id=? AND reason='source_changed'`, f.item.ID); n != 3 {
		t.Fatalf("original request items retired=%d", n)
	}
	if n := retirementInt(t, f.c, `SELECT count(*) FROM download_request_items WHERE request_id='request-3' AND ordinal=1 AND item_id=? AND reason=''`, f.sibling.ID); n != 1 {
		t.Fatalf("request child of another item was cascaded: %d", n)
	}
	if n := retirementInt(t, f.c, `SELECT count(*) FROM download_requests WHERE target_id=? AND state='capturing' AND error_code=''`, f.item.ID); n != 3 {
		t.Fatalf("capturing targets failed=%d", n)
	}
	if got := retirementText(t, f.c, `SELECT state FROM download_requests WHERE id='request-sibling'`); got != "capturing" {
		t.Fatalf("unrelated request changed: %q", got)
	}
	// Every unit's bounded emptiness probe is satisfied by the same transaction
	// graph the units left behind.
	ctx := context.Background()
	dbwork.WithWriteTx(ctx, f.c.DB, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		for _, unit := range f.plan {
			empty, err := unit.Empty(ctx, tx)
			if err != nil {
				t.Fatal(err)
			}
			if !empty {
				t.Fatalf("unit %s left its head non-empty", unit.Name)
			}
		}
		return nil
	})
	var raw string
	if err := f.c.DB.QueryRow(`SELECT cursor FROM catalog_purge_jobs WHERE mutation_id=?`, f.m.Ref.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	final, err := phasedpurge.DecodeCursor(raw, f.plan)
	if err != nil || final.Unit != len(f.plan) {
		t.Fatalf("final cursor=%q decoded=%+v err=%v", raw, final, err)
	}
	if turns > 96 {
		t.Fatalf("unexpected turn count %d", turns)
	}
}

// TestSeparateFolderRetirementYieldsWhileCancelling proves the running path,
// the post-commit Interrupt and the typed retry sentinel: a cancelling job is
// never spun on, never deleted and never reported as drained.
func TestSeparateFolderRetirementYieldsWhileCancelling(t *testing.T) {
	f := newSeparateFolderRetirementFixture(t)
	c := f.c
	c.Exec(`INSERT INTO prepared_media_jobs(id,item_id,asset_id,library_id,profile_id,target_id,selection_json,source_revision,principal_json,state,phase,created_ms,updated_ms) VALUES
 ('job-queued',?,?,'books','profile','target','{}','revision','{}','queued','waiting',1,2),
 ('job-running',?,?,'books','profile','target','{}','revision','{}','running','prepare',1,2)`, f.item.ID, f.item.Token, f.item.ID, f.item.Token)

	for i := 0; i < 2; i++ {
		if _, _, err := f.step(t); err != nil {
			t.Fatalf("cancel turn %d: %v", i, err)
		}
	}
	if len(f.cancelled) != 2 || len(f.interrupt) != 2 {
		t.Fatalf("cancellations=%v interrupts=%v", f.cancelled, f.interrupt)
	}
	if got := retirementText(t, c, `SELECT state FROM prepared_media_jobs WHERE id='job-queued'`); got != "cancelled" {
		t.Fatalf("queued job=%q", got)
	}
	if got := retirementText(t, c, `SELECT state FROM prepared_media_jobs WHERE id='job-running'`); got != "cancelling" {
		t.Fatalf("running job=%q", got)
	}
	var cursorBefore string
	if err := c.DB.QueryRow(`SELECT cursor FROM catalog_purge_jobs WHERE mutation_id=?`, f.m.Ref.ID).Scan(&cursorBefore); err != nil {
		t.Fatal(err)
	}
	result, _, err := f.step(t)
	if !errors.Is(err, ErrSeparateFolderRetirementRetry) {
		t.Fatalf("cancelling job did not yield the typed retry: %v", err)
	}
	if result.Done {
		t.Fatal("cancelling job was reported drained")
	}
	var cursorAfter string
	if err = c.DB.QueryRow(`SELECT cursor FROM catalog_purge_jobs WHERE mutation_id=?`, f.m.Ref.ID).Scan(&cursorAfter); err != nil {
		t.Fatal(err)
	}
	if cursorAfter != cursorBefore {
		t.Fatalf("retry advanced the cursor %q -> %q", cursorBefore, cursorAfter)
	}
	if got := retirementText(t, c, `SELECT state FROM prepared_media_jobs WHERE id='job-running'`); got != "cancelling" {
		t.Fatalf("retry rewrote the job: %q", got)
	}
	// The existing worker generation cleanup finishes elsewhere; once it does,
	// the same walk resumes from its durable cursor and completes.
	c.Exec(`UPDATE prepared_media_jobs SET state='cancelled' WHERE id='job-running'`)
	f.drive(t, 32)
	if got := retirementText(t, c, `SELECT state FROM prepared_media_jobs WHERE id='job-running'`); got != "cancelled" {
		t.Fatalf("cleaned job=%q", got)
	}
	if len(f.interrupt) != 2 {
		t.Fatalf("interrupt was duplicated on resume: %v", f.interrupt)
	}
}

func TestSeparateFolderPreparationGroupDrainsGrantBeforeMark(t *testing.T) {
	f := newSeparateFolderRetirementFixture(t)
	c := f.c
	c.Exec(`INSERT INTO download_preparations(id,profile_key,authority,account_id,profile_id,item_id,library_id,quality,origin,state,hash_state,created_ms,updated_ms)
 VALUES('prep-live','profile','local','account','profile',?,'books','original','item','ready',X'00',1,2)`, f.item.ID)
	c.Exec(`INSERT INTO download_grants(hash,preparation_id,profile_key,item_id,issued_ms,expires_ms,revoked) VALUES('grant-live','prep-live','profile',?,1,99,0)`, f.item.ID)
	c.Exec(`UPDATE download_preparations SET source_asset_id=? WHERE id='prep-live'`, f.item.Token)
	unit := retirementUnit(t, f.plan, "asset-preparation-drain")
	ctx := context.Background()
	var outcome phasedpurge.Outcome
	if err := dbwork.WithWriteTx(ctx, c.DB, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		var err error
		outcome, err = unit.Run(ctx, tx, nil)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if !outcome.Changed {
		t.Fatal("preparation head was not a point write")
	}
	if n := retirementInt(t, c, `SELECT revoked FROM download_grants WHERE hash='grant-live'`); n != 1 {
		t.Fatalf("preparation group did not revoke the exact grant: %d", n)
	}
	if got := retirementText(t, c, `SELECT state FROM download_preparations WHERE id='prep-live'`); got != "ready" {
		t.Fatalf("preparation state=%q", got)
	}
	if n := retirementInt(t, c, `SELECT count(*) FROM download_preparations WHERE id='prep-live' AND reason='' AND hash_state IS NOT NULL`); n != 1 {
		t.Fatalf("preparation was marked before its grant drained: %d", n)
	}
}

func TestSeparateFolderBareTargetPreservesIntentAndChildren(t *testing.T) {
	f := newSeparateFolderRetirementFixture(t)
	c := f.c
	c.Exec(`INSERT INTO download_requests(id,profile_key,operation_id,request_hash,principal,device_id,target_kind,target_id,quality,episodes,keep_next,selection_fence,state,created_ms,updated_ms)
 VALUES('request-target','profile','op-target','hash','{}','device','item',?,'original','all',0,'fence','capturing',1,2)`, f.item.ID)
	c.Exec(`INSERT INTO download_request_items(request_id,ordinal,item_id,reason) VALUES
 ('request-target',0,?,''),('request-target',1,?,'')`, f.item.ID, f.sibling.ID)
	unit := retirementUnit(t, f.plan, "asset-preparation-drain")
	ctx := context.Background()
	if err := dbwork.WithWriteTx(ctx, c.DB, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		outcome, err := unit.Run(ctx, tx, nil)
		if err != nil {
			return err
		}
		if !outcome.Drained {
			t.Fatal("bare target created asset debt")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := retirementText(t, c, `SELECT state FROM download_requests WHERE id='request-target'`); got != "capturing" {
		t.Fatalf("target request state=%q", got)
	}
	if got := retirementText(t, c, `SELECT error_code FROM download_requests WHERE id='request-target'`); got != "" {
		t.Fatalf("target request error=%q", got)
	}
	if n := retirementInt(t, c, `SELECT count(*) FROM download_request_items WHERE request_id='request-target' AND reason=''`); n != 2 {
		t.Fatalf("target unit cascaded request items: %d", n)
	}
	// The partial capturing index survives the point update and the literal
	// head still seeks it, while the probe reports the empty point.
	if n := retirementInt(t, c, `SELECT count(*) FROM sqlite_master WHERE type='index' AND name='download_requests_retirement'`); n != 1 {
		t.Fatalf("capturing index disappeared: %d", n)
	}
	plan := retirementExplain(t, c, separateFolderRequestTargetHeadSQL, f.item.ID)
	if !strings.Contains(plan, "download_requests_retirement") || strings.Contains(plan, "TEMP B-TREE") {
		t.Fatalf("post-update target head plan:\n%s", plan)
	}
	if err := dbwork.WithWriteTx(ctx, c.DB, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		empty, err := unit.Empty(ctx, tx)
		if err != nil {
			return err
		}
		if !empty {
			t.Fatal("retired target still qualifies")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// Old literals remain solely as index negative controls, never compiled into production.
const (
	separateFolderGrantHeadSQL = `SELECT hash FROM download_grants INDEXED BY download_grants_retirement
 WHERE item_id=? AND revoked=0 ORDER BY hash LIMIT 1`
	separateFolderGrantRetireSQL = `UPDATE download_grants SET revoked=1 WHERE hash=? AND revoked=0`
	separateFolderGrantProbeSQL  = `SELECT EXISTS(SELECT 1 FROM download_grants INDEXED BY download_grants_retirement
 WHERE item_id=? AND revoked=0)`

	separateFolderReceiptHeadSQL = `SELECT id FROM download_receipts INDEXED BY download_receipts_retirement
 WHERE item_id=? AND revoked_ms=0 ORDER BY id LIMIT 1`
	separateFolderReceiptProbeSQL = `SELECT EXISTS(SELECT 1 FROM download_receipts INDEXED BY download_receipts_retirement
 WHERE item_id=? AND revoked_ms=0)`

	separateFolderPreparationHeadSQL = `SELECT id FROM download_preparations INDEXED BY download_preparations_retirement
 WHERE item_id=? AND state IN('queued','running','ready','paused','failed') LIMIT 1`
	// This is the existing endClaim state/reason/hash/revision transition as a
	// point write. endClaim's unbounded UPDATE download_grants is intentionally
	// absent: unit 4 already drained grants for this exact item.
	separateFolderPreparationRetireSQL = `UPDATE download_preparations
 SET state='unavailable',reason='source_changed',hash_state=NULL,revision=revision+1,updated_ms=?
 WHERE id=? AND state IN('queued','running','ready','paused','failed')`
	separateFolderPreparationProbeSQL = `SELECT EXISTS(SELECT 1 FROM download_preparations INDEXED BY download_preparations_retirement
 WHERE item_id=? AND state IN('queued','running','ready','paused','failed'))`

	separateFolderRequestItemHeadSQL = `SELECT request_id,ordinal FROM download_request_items INDEXED BY download_request_items_retirement
 WHERE item_id=? AND reason='' ORDER BY request_id,ordinal LIMIT 1`
	separateFolderRequestItemRetireSQL = `UPDATE download_request_items SET reason='source_changed'
 WHERE request_id=? AND ordinal=? AND reason=''`
	separateFolderRequestItemProbeSQL = `SELECT EXISTS(SELECT 1 FROM download_request_items INDEXED BY download_request_items_retirement
 WHERE item_id=? AND reason='')`

	separateFolderRequestTargetHeadSQL = `SELECT id FROM download_requests INDEXED BY download_requests_retirement
 WHERE target_id=? AND state='capturing' ORDER BY id LIMIT 1`
	separateFolderRequestTargetRetireSQL = `UPDATE download_requests SET state='failed',error_code='source_changed',updated_ms=?
 WHERE id=? AND state='capturing'`
	separateFolderRequestTargetProbeSQL = `SELECT EXISTS(SELECT 1 FROM download_requests INDEXED BY download_requests_retirement
 WHERE target_id=? AND state='capturing')`
)

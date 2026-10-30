package compactcatalog

// Canonical child-set materialization for one informational source change.
//
// AdvanceInfoPublicationTx runs this callback only in header phase 2, after the
// raw holder pre-fence has reached EOF, so every holder of the asset is already
// on its accepted OLD frame and no informational reader may resolve the partial
// canonical child sets. It copies the sealed normalized NEW source input of the
// changed families into the EXISTING canonical family tables, one bounded
// quantum at a time, and reports done only when every changed family is fully
// installed and its obsolete canonical rows are gone. It never activates, never
// touches an item head, a link obligation, a display flag or a query cache.
//
// Fixed descriptors, no caller SQL
//
// Family numbers are the literal CREATE TABLE order of the normalized staging
// DDL (1..18); header family_mask selects family f with bit 1<<(f-1):
//
//	 1 asset_stream_facts                10 inventory_disc_facts
//	 2 asset_streams                     11 inventory_disc_titles
//	 3 asset_stream_breadth              12 inventory_disc_clips
//	 4 asset_chapter_facts               13 inventory_disc_streams
//	 5 asset_chapters                    14 inventory_disc_chapters
//	 6 asset_subtitle_facts              15 inventory_disc_media_members
//	 7 subtitle_sidecars                 16 inventory_disc_title_media
//	 8 asset_subtitles                   17 inventory_disc_title_media_streams
//	 9 subtitle_inventory_evidence       18 inventory_disc_title_media_members
//
// Only families present in the mask are read, copied or reconciled; a family
// outside the mask keeps its canonical rows exactly as they are.
//
// Bounded keyset reconciliation, not filtered deletes
//
// A filtered `DELETE ... WHERE NOT EXISTS(<staged>) ... LIMIT n` is NOT raw
// bounded: it scans the whole kept prefix of a family before it finds one
// obsolete row, so a large unchanged prefix turns one quantum into an unbounded
// scan. This reconciler instead walks the canonical key space with one index
// seek per raw key, exactly like a cursor:
//
//	SELECT <tuple> FROM <canonical> c WHERE <exact scope>
//	 [AND (<tuple>) > (<cursor>)] ORDER BY <tuple> LIMIT 1
//
// Each step reads at most one raw key at a time, and a raw key that is KEPT
// costs the same single row of the quantum as a raw key that is removed. The
// cursor is the last fully processed raw key, kept or rejected, and lives in
// the header's copy_phase/copy_id/copy_aux/copy_key..copy_key3 columns. The
// budget is checked after every raw key (8 rows, 256 KiB of key bytes, 8 ms),
// so eight giant keys are never read eagerly. Every tuple is a canonical PK or
// UNIQUE index prefix, so the seek and the ORDER BY are index plans with no
// TEMP sort.
//
// Exact scope
//
// Families 1..9 are asset-scoped: the walk and every insert bind the header
// asset token. Families 10..18 are object-scoped: their complete NEW set
// describes exactly ONE witness object, named by the family manifest's
// witness_ordinal through catalog_info_input_witnesses.object_id, never every
// object of the asset. Two valid observations of one asset therefore survive
// publishing one of them: the other object's canonical facts and title tree are
// outside the walk scope and are never read, updated or removed. A change that
// stages disc facts for a second object cannot be represented by one manifest
// witness, so it is refused (cross-object coalescing fails closed) instead of
// being silently mixed into one canonical scope.
//
// Child-first removal and no cascade fanout
//
// The walk order is children before parents (18,17,16,12,13,14,15,11,10,9,8,
// 6,5,4,3,2,1). Before an obsolete parent row is deleted, every canonical FK
// child table that is not itself reconciled under this change is probed for a
// row under the FULL parent key. A surviving child is an invariant failure
// (ErrInfoInvariant), never a cascade: this reconciler refuses to let an
// uncharged FK cascade delete rows of a family it did not walk. Masked child
// families are reconciled first, so their obsolete rows are already gone and
// their kept rows always have a staged parent.
//
// Insert pass
//
// After every removal, staged rows are installed parent-first (1,2,3,4,5,6,7,8,
// 9,10,11,15,12,13,14,16,17,18) in input_ordinal order, at most eight ordinals
// per quantum. A parent family outside the mask may be staged solely to satisfy
// the NEW source-input foreign keys; it is never installed, and each child row
// still has to find its canonical parent (the per-row EXISTS guard) or the
// exactness check fails the change. Disc titles are parked (+1000000) in a
// bounded walk before the title insert pass, so a swapped title_number cannot
// trip UNIQUE(object_id,title_number) on the way to its final value.
//
// Input contract this reconciler requires of the source-input writer
//
//   - A masked family is the COMPLETE new set for that family and scope, not a
//     delta: every canonical row in scope that the staged set does not name is
//     removed. An unchanged family must be left out of the mask.
//   - Every masked family has a catalog_info_input_families row with sealed=1
//     and rows_staged=rows_expected. The producer's seal is the only place that
//     validates the staged row count; this reconciler never counts a family.
//   - Staged fact revisions are the revisions the canonical row must end at, so
//     a staged revision older than the installed canonical row is an invariant
//     failure (the insert guard skips it and exactness fails the change).
//   - Staged subtitle locators follow the canonical writer: an embedded track's
//     locator is its decimal stream index, a sidecar track's locator is the
//     sidecar path. That pair is the canonical identity, so a kept track keeps
//     its canonical asset_subtitles.id.
//   - A sidecar is identified by path in canonical storage; the canonical row's
//     id is kept and staged subtitle rows resolve their staged sidecar id
//     through that path.
//   - The owner's disc choice wins over a staged older choice: the canonical
//     owner_title_key/choice_revision are kept when the canonical
//     choice_revision is newer.
//
// Cost per quantum
//
// One call reads and writes at most infoCopyRowsPerQuantum (8) raw rows across
// removals, parks and inserts, then persists its cursor and returns. Every
// statement is a fixed index/primary-key access; nothing selects a whole table
// and no delete is unbounded.
//
// Out of scope, deliberately
//
// Copying a family is not publication. Subtitle offered-resource rows
// (subtitle_resources/_revisions/_render_revisions), availability, duration and
// item epochs belong to the apply phase owned by info_publication.go; this file
// writes only the 18 canonical family tables.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// infoCopyFamilies is the fixed number of normalized source-input families.
const infoCopyFamilies = 18

// infoCopyFamilyMask covers families 1..18.
const infoCopyFamilyMask = int64(1)<<infoCopyFamilies - 1

// The raw-row, key-byte and wall-time budget of one materialize call. All three
// are checked after every raw key, never only after a batch.
const (
	infoCopyRowsPerQuantum  = 8
	infoCopyBytesPerQuantum = 256 << 10
	infoCopyTimePerQuantum  = 8 * time.Millisecond
)

// infoCopyCursor is the last fully processed raw key of one step. copy_phase is
// the step index, copy_aux says whether a key is present, copy_key..copy_key3
// hold the text components in tuple order and copy_id holds the single integer
// component (or, in an insert pass, the last installed input_ordinal).
type infoCopyCursor struct {
	valid bool
	id    int64
	text  [3]string
}

// bytes is the charged size of the key: its text components plus the integer.
func (c infoCopyCursor) bytes() int {
	n := 8
	for _, part := range c.text {
		n += len(part)
	}
	return n
}

// infoCopyBudget bounds one materialize call.
type infoCopyBudget struct {
	rows     int
	bytes    int
	deadline time.Time
}

func newInfoCopyBudget() infoCopyBudget {
	return infoCopyBudget{rows: infoCopyRowsPerQuantum, deadline: time.Now().Add(infoCopyTimePerQuantum)}
}

func (b *infoCopyBudget) spend(bytes int) {
	b.rows--
	b.bytes += bytes
}

func (b *infoCopyBudget) exhausted() bool {
	return b.rows <= 0 || b.bytes >= infoCopyBytesPerQuantum || !time.Now().Before(b.deadline)
}

// infoCopyTuple is one fixed canonical key in index order. The optional integer
// component is last, exactly as every family's PK/UNIQUE index declares it, and
// there are never more than three text components.
type infoCopyTuple struct {
	cols []string
	text int
}

func infoCopyTextTuple(cols ...string) infoCopyTuple {
	return infoCopyTuple{cols: cols, text: len(cols)}
}

func infoCopyIntTuple(cols ...string) infoCopyTuple {
	return infoCopyTuple{cols: cols, text: len(cols) - 1}
}

func (t infoCopyTuple) columns(alias string) string {
	parts := make([]string, len(t.cols))
	for i, col := range t.cols {
		parts[i] = alias + "." + col
	}
	return strings.Join(parts, ",")
}

func (t infoCopyTuple) rowValue(alias string) string {
	return "(" + t.columns(alias) + ")"
}

func (t infoCopyTuple) predicate(alias string) string {
	parts := make([]string, len(t.cols))
	for i, col := range t.cols {
		if alias == "" {
			parts[i] = col + "=?"
		} else {
			parts[i] = alias + "." + col + "=?"
		}
	}
	return strings.Join(parts, " AND ")
}

func (t infoCopyTuple) args(c infoCopyCursor) []any {
	out := make([]any, 0, len(t.cols))
	for i := 0; i < t.text; i++ {
		out = append(out, c.text[i])
	}
	if t.text < len(t.cols) {
		out = append(out, c.id)
	}
	return out
}

// scanInfoCopyKey reads one raw key of a walk.
func scanInfoCopyKey(row *sql.Row, tuple infoCopyTuple) (infoCopyCursor, error) {
	var key infoCopyCursor
	var texts [3]string
	var id int64
	ptrs := make([]any, 0, len(tuple.cols))
	for i := 0; i < tuple.text; i++ {
		ptrs = append(ptrs, &texts[i])
	}
	if tuple.text < len(tuple.cols) {
		ptrs = append(ptrs, &id)
	}
	if err := row.Scan(ptrs...); err != nil {
		return key, err
	}
	key.valid = true
	key.id = id
	key.text = texts
	return key, nil
}

// infoCopyScope is the exact canonical scope of one family.
type infoCopyScope int

const (
	// infoCopyNoScope is an unscoped family (shared sidecars, keyed by path).
	infoCopyNoScope infoCopyScope = iota
	// infoCopyAssetScope is one asset, bound to the header asset token.
	infoCopyAssetScope
	// infoCopyObjectScope is one disc witness object.
	infoCopyObjectScope
	// infoCopyObjectAssetScope is one disc witness object and the header asset.
	infoCopyObjectAssetScope
)

func (s infoCopyScope) predicate(alias string) string {
	switch s {
	case infoCopyAssetScope:
		return alias + ".asset_id=?"
	case infoCopyObjectScope:
		return alias + ".object_id=?"
	case infoCopyObjectAssetScope:
		return alias + ".object_id=? AND " + alias + ".asset_id=?"
	}
	return ""
}

func (s infoCopyScope) args(token, object string) []any {
	switch s {
	case infoCopyAssetScope:
		return []any{token}
	case infoCopyObjectScope:
		return []any{object}
	case infoCopyObjectAssetScope:
		return []any{object, token}
	}
	return nil
}

type infoCopyStepKind int

const (
	// infoCopyRemove walks one canonical family and removes obsolete rows.
	infoCopyRemove infoCopyStepKind = iota
	// infoCopyPark walks surviving disc titles and parks their title_number.
	infoCopyPark
	// infoCopyInsert installs staged rows in input_ordinal order.
	infoCopyInsert
)

// infoCopyPrepare filters a staged ordinal batch before it is written. It may
// only drop rows whose canonical parent is already gone, and it fails closed on
// anything else.
type infoCopyPrepare func(context.Context, *sql.Tx, int64, []int64) ([]int64, error)

// infoCopyStep is one fixed reconciler step.
type infoCopyStep struct {
	kind     infoCopyStepKind
	family   int
	table    string // canonical table (remove/park) or staging table (insert)
	tuple    infoCopyTuple
	scope    infoCopyScope
	children []string // canonical FK children that must be empty before DELETE
	walk     string   // SELECT ... WHERE <scope> [AND ...]
	probe    string   // staged keep probe
	del      string   // canonical DELETE by the walk tuple
	park     string   // canonical park UPDATE by the walk tuple
	insert   string   // INSERT ... SELECT template (%s = ordinal placeholders)
	prepare  infoCopyPrepare
}

func infoCopyRemoveStep(family int, table, staged string, tuple infoCopyTuple, scope infoCopyScope, children ...string) infoCopyStep {
	keep := "SELECT EXISTS(SELECT 1 FROM " + staged + " n WHERE n.change_id=? AND " + tuple.predicate("n")
	if family == 11 {
		keep += " AND n.selection_token=(SELECT c.selection_token FROM inventory_disc_titles c WHERE c.object_id=n.object_id AND c.title_key=n.title_key)"
	} else if family == 12 || family == 13 || family == 14 || family == 16 || family == 17 || family == 18 {
		keep += " AND (NOT EXISTS(SELECT 1 FROM catalog_info_changes f WHERE f.id=n.change_id AND f.family_mask&1024<>0) OR EXISTS(SELECT 1 FROM catalog_info_new_disc_titles nt JOIN inventory_disc_titles ct ON ct.object_id=nt.object_id AND ct.title_key=nt.title_key AND ct.selection_token=nt.selection_token WHERE nt.change_id=n.change_id AND nt.object_id=n.object_id AND nt.title_key=n.title_key))"
	}
	keep += ")"
	return infoCopyStep{
		kind:     infoCopyRemove,
		family:   family,
		table:    table,
		tuple:    tuple,
		scope:    scope,
		children: children,
		walk:     "SELECT " + tuple.columns("c") + " FROM " + table + " c WHERE " + scope.predicate("c"),
		probe:    keep,
		del:      "DELETE FROM " + table + " WHERE " + tuple.predicate(""),
	}
}

func infoCopyParkStep(table, staged string, tuple infoCopyTuple, scope infoCopyScope) infoCopyStep {
	return infoCopyStep{
		kind:   infoCopyPark,
		family: 11,
		table:  table,
		tuple:  tuple,
		scope:  scope,
		// The preceding removal pass retained exactly the staged keys. Walk
		// every raw survivor, including an already parked row after recovery.
		walk: "SELECT " + tuple.columns("c") + " FROM " + table + " c WHERE " + scope.predicate("c"),
		park: "UPDATE " + table + " SET title_number=title_number+1000000 WHERE " + tuple.predicate("") + " AND title_number<1000000",
	}
}

func infoCopyInsertStep(family int, staged string, scope infoCopyScope, insert string, prepare infoCopyPrepare) infoCopyStep {
	return infoCopyStep{kind: infoCopyInsert, family: family, table: staged, scope: scope, insert: insert, prepare: prepare}
}

// infoCopySteps is the whole reconciliation. Removals run children before
// parents, then surviving disc titles are parked, then staged rows are
// installed parent before child. Steps of a family outside the change mask are
// skipped. The order is fixed source schema, never caller-controlled SQL.
var infoCopySteps = []infoCopyStep{
	// Replace the former physical cascade one raw request at a time. Requests
	// for a surviving exact title identity remain untouched.
	{
		kind: infoCopyRemove, family: 11, table: "inventory_disc_title_requests", scope: infoCopyObjectScope,
		tuple: infoCopyTextTuple("object_id", "title_key", "job_id"),
		walk:  "SELECT c.object_id,c.title_key,c.job_id FROM inventory_disc_title_requests c INDEXED BY inventory_disc_title_requests_object_id_title_key_fk WHERE c.object_id=?",
		probe: "SELECT EXISTS(SELECT 1 FROM catalog_info_new_disc_titles n JOIN inventory_disc_titles t ON t.object_id=n.object_id AND t.title_key=n.title_key AND t.selection_token=n.selection_token JOIN inventory_disc_title_requests r ON r.object_id=t.object_id AND r.title_key=t.title_key WHERE n.change_id=? AND r.object_id=? AND r.title_key=? AND r.job_id=?)",
		del:   "DELETE FROM inventory_disc_title_requests WHERE object_id=? AND title_key=? AND job_id=?",
	},
	// Disc tree, deepest child first. Every tuple is the canonical PK, so the
	// walk is one index seek and the DELETE names exactly one row.
	infoCopyRemoveStep(18, "inventory_disc_title_media_members", "catalog_info_new_disc_title_media_members",
		infoCopyTextTuple("object_id", "title_key", "path"), infoCopyObjectScope),
	infoCopyRemoveStep(17, "inventory_disc_title_media_streams", "catalog_info_new_disc_title_media_streams",
		infoCopyIntTuple("object_id", "title_key", "stream_index"), infoCopyObjectScope),
	infoCopyRemoveStep(16, "inventory_disc_title_media", "catalog_info_new_disc_title_media",
		infoCopyTextTuple("object_id", "title_key"), infoCopyObjectScope,
		"inventory_disc_title_media_streams", "inventory_disc_title_media_members"),
	infoCopyRemoveStep(12, "inventory_disc_clips", "catalog_info_new_disc_clips",
		infoCopyIntTuple("object_id", "title_key", "sequence"), infoCopyObjectScope),
	infoCopyRemoveStep(13, "inventory_disc_streams", "catalog_info_new_disc_streams",
		infoCopyIntTuple("object_id", "title_key", "stream_order"), infoCopyObjectScope),
	infoCopyRemoveStep(14, "inventory_disc_chapters", "catalog_info_new_disc_chapters",
		infoCopyIntTuple("object_id", "title_key", "chapter_order"), infoCopyObjectScope),
	infoCopyRemoveStep(15, "inventory_disc_media_members", "catalog_info_new_disc_media_members",
		infoCopyTextTuple("object_id", "path"), infoCopyObjectScope),
	// The explicit request pass above has retired only obsolete identities.
	// Every remaining child still vetoes a destructive parent delete.
	infoCopyRemoveStep(11, "inventory_disc_titles", "catalog_info_new_disc_titles",
		infoCopyTextTuple("object_id", "title_key"), infoCopyObjectScope,
		"inventory_disc_clips", "inventory_disc_streams", "inventory_disc_chapters",
		"inventory_disc_title_media", "inventory_disc_title_requests"),
	infoCopyRemoveStep(10, "inventory_disc_facts", "catalog_info_new_disc_facts",
		infoCopyTextTuple("object_id"), infoCopyObjectScope,
		"inventory_disc_titles", "inventory_disc_media_members"),

	// Asset-scoped families, children before parents.
	infoCopyRemoveStep(9, "subtitle_inventory_evidence", "catalog_info_new_subtitle_inventory_evidence",
		infoCopyTextTuple("asset_id"), infoCopyAssetScope),
	infoCopyRemoveStep(8, "asset_subtitles", "catalog_info_new_subtitles",
		infoCopyTextTuple("asset_id", "origin", "locator"), infoCopyAssetScope),
	infoCopyRemoveStep(6, "asset_subtitle_facts", "catalog_info_new_subtitle_facts",
		infoCopyTextTuple("asset_id"), infoCopyAssetScope, "asset_subtitles"),
	infoCopyRemoveStep(5, "asset_chapters", "catalog_info_new_chapters",
		infoCopyIntTuple("asset_id", "chapter_index"), infoCopyAssetScope),
	infoCopyRemoveStep(4, "asset_chapter_facts", "catalog_info_new_chapter_facts",
		infoCopyTextTuple("asset_id"), infoCopyAssetScope, "asset_chapters"),
	infoCopyRemoveStep(3, "asset_stream_breadth", "catalog_info_new_stream_breadth",
		infoCopyIntTuple("asset_id", "stream_index"), infoCopyAssetScope),
	infoCopyRemoveStep(2, "asset_streams", "catalog_info_new_streams",
		infoCopyIntTuple("asset_id", "stream_index"), infoCopyAssetScope, "asset_stream_breadth"),
	infoCopyRemoveStep(1, "asset_stream_facts", "catalog_info_new_stream_facts",
		infoCopyTextTuple("asset_id"), infoCopyAssetScope, "asset_streams"),

	// A renumbering can swap two title numbers, and a swapped pair cannot be
	// applied one row at a time without a transient UNIQUE(object_id,title_number)
	// collision. Park every surviving title number first; the staged upsert below
	// writes the final number for all of them inside the same change.
	infoCopyParkStep("inventory_disc_titles", "catalog_info_new_disc_titles",
		infoCopyTextTuple("object_id", "title_key"), infoCopyObjectScope),

	// Staged rows, parent before child, in input_ordinal order.
	infoCopyInsertStep(1, "catalog_info_new_stream_facts", infoCopyAssetScope,
		`INSERT INTO asset_stream_facts(asset_id,revision,size,modified_ns,fingerprint,detail_version)
SELECT s.asset_id,s.revision,s.size,s.modified_ns,s.fingerprint,s.detail_version FROM catalog_info_new_stream_facts s
WHERE s.change_id=? AND s.asset_id=? AND s.input_ordinal IN(%s)
 AND s.revision>=COALESCE((SELECT f.revision FROM asset_stream_facts f WHERE f.asset_id=s.asset_id),0)
ON CONFLICT(asset_id) DO UPDATE SET revision=excluded.revision,size=excluded.size,modified_ns=excluded.modified_ns,fingerprint=excluded.fingerprint,detail_version=excluded.detail_version`, nil),
	infoCopyInsertStep(2, "catalog_info_new_streams", infoCopyAssetScope,
		`INSERT INTO asset_streams(asset_id,stream_index,type,codec,language,title,channels,channel_layout,is_default,is_forced,color_transfer,color_primaries,detail_json)
SELECT s.asset_id,s.stream_index,s.type,s.codec,s.language,s.title,s.channels,s.channel_layout,s.is_default,s.is_forced,s.color_transfer,s.color_primaries,s.detail_json FROM catalog_info_new_streams s
WHERE s.change_id=? AND s.asset_id=? AND s.input_ordinal IN(%s)
 AND EXISTS(SELECT 1 FROM asset_stream_facts f WHERE f.asset_id=s.asset_id)
ON CONFLICT(asset_id,stream_index) DO UPDATE SET type=excluded.type,codec=excluded.codec,language=excluded.language,title=excluded.title,channels=excluded.channels,channel_layout=excluded.channel_layout,is_default=excluded.is_default,is_forced=excluded.is_forced,color_transfer=excluded.color_transfer,color_primaries=excluded.color_primaries,detail_json=excluded.detail_json`, nil),
	infoCopyInsertStep(3, "catalog_info_new_stream_breadth", infoCopyAssetScope,
		`INSERT INTO asset_stream_breadth(asset_id,stream_index,audio_description,audio_variant,chroma_subsampling,fps_numerator,fps_denominator,frame_rate_mode,interlace_analysis,telecine,dovi_rpu,dovi_base_layer,dovi_metadata_known,dovi_enhancement_type,dovi_cm_version)
SELECT s.asset_id,s.stream_index,s.audio_description,s.audio_variant,s.chroma_subsampling,s.fps_numerator,s.fps_denominator,s.frame_rate_mode,s.interlace_analysis,s.telecine,s.dovi_rpu,s.dovi_base_layer,s.dovi_metadata_known,s.dovi_enhancement_type,s.dovi_cm_version FROM catalog_info_new_stream_breadth s
WHERE s.change_id=? AND s.asset_id=? AND s.input_ordinal IN(%s)
 AND EXISTS(SELECT 1 FROM asset_streams st WHERE st.asset_id=s.asset_id AND st.stream_index=s.stream_index)
ON CONFLICT(asset_id,stream_index) DO UPDATE SET audio_description=excluded.audio_description,audio_variant=excluded.audio_variant,chroma_subsampling=excluded.chroma_subsampling,fps_numerator=excluded.fps_numerator,fps_denominator=excluded.fps_denominator,frame_rate_mode=excluded.frame_rate_mode,interlace_analysis=excluded.interlace_analysis,telecine=excluded.telecine,dovi_rpu=excluded.dovi_rpu,dovi_base_layer=excluded.dovi_base_layer,dovi_metadata_known=excluded.dovi_metadata_known,dovi_enhancement_type=excluded.dovi_enhancement_type,dovi_cm_version=excluded.dovi_cm_version`, nil),
	infoCopyInsertStep(4, "catalog_info_new_chapter_facts", infoCopyAssetScope,
		`INSERT INTO asset_chapter_facts(asset_id,revision,size,modified_ns,status,fingerprint)
SELECT s.asset_id,s.revision,s.size,s.modified_ns,s.status,s.fingerprint FROM catalog_info_new_chapter_facts s
WHERE s.change_id=? AND s.asset_id=? AND s.input_ordinal IN(%s)
 AND s.revision>=COALESCE((SELECT f.revision FROM asset_chapter_facts f WHERE f.asset_id=s.asset_id),0)
ON CONFLICT(asset_id) DO UPDATE SET revision=excluded.revision,size=excluded.size,modified_ns=excluded.modified_ns,status=excluded.status,fingerprint=excluded.fingerprint`, nil),
	infoCopyInsertStep(5, "catalog_info_new_chapters", infoCopyAssetScope,
		`INSERT INTO asset_chapters(asset_id,chapter_index,title,start_seconds,end_seconds)
SELECT s.asset_id,s.chapter_index,s.title,s.start_seconds,s.end_seconds FROM catalog_info_new_chapters s
WHERE s.change_id=? AND s.asset_id=? AND s.input_ordinal IN(%s)
 AND EXISTS(SELECT 1 FROM asset_chapter_facts f WHERE f.asset_id=s.asset_id)
ON CONFLICT(asset_id,chapter_index) DO UPDATE SET title=excluded.title,start_seconds=excluded.start_seconds,end_seconds=excluded.end_seconds`, nil),
	infoCopyInsertStep(6, "catalog_info_new_subtitle_facts", infoCopyAssetScope,
		`INSERT INTO asset_subtitle_facts(asset_id,revision,size,modified_ns,origin_us,timing_known,status,fingerprint)
SELECT s.asset_id,s.revision,s.size,s.modified_ns,s.origin_us,s.timing_known,s.status,s.fingerprint FROM catalog_info_new_subtitle_facts s
WHERE s.change_id=? AND s.asset_id=? AND s.input_ordinal IN(%s)
 AND s.revision>=COALESCE((SELECT f.revision FROM asset_subtitle_facts f WHERE f.asset_id=s.asset_id),0)
ON CONFLICT(asset_id) DO UPDATE SET revision=excluded.revision,size=excluded.size,modified_ns=excluded.modified_ns,origin_us=excluded.origin_us,timing_known=excluded.timing_known,status=excluded.status,fingerprint=excluded.fingerprint`, nil),
	// Sidecars are shared by path across assets: upsert only, never delete, and
	// keep the canonical id.
	infoCopyInsertStep(7, "catalog_info_new_subtitle_sidecars", infoCopyNoScope,
		`INSERT INTO subtitle_sidecars(id,path,size,modified_ns,digest)
SELECT s.id,s.path,s.size,s.modified_ns,s.digest FROM catalog_info_new_subtitle_sidecars s
WHERE s.change_id=? AND s.input_ordinal IN(%s)
ON CONFLICT(path) DO UPDATE SET size=excluded.size,modified_ns=excluded.modified_ns,digest=excluded.digest`, nil),
	infoCopyInsertStep(8, "catalog_info_new_subtitles", infoCopyAssetScope,
		`INSERT INTO asset_subtitles(id,asset_id,origin,locator,stream_index,sidecar_id,format,language,title,is_default,is_forced,reason,hearing_impaired)
SELECT s.id,s.asset_id,s.origin,s.locator,s.stream_index,
 CASE WHEN s.sidecar_id IS NULL THEN NULL ELSE (SELECT c.id FROM subtitle_sidecars c JOIN catalog_info_new_subtitle_sidecars n ON n.path=c.path AND n.change_id=s.change_id WHERE n.id=s.sidecar_id) END,
 s.format,s.language,s.title,s.is_default,s.is_forced,s.reason,s.hearing_impaired FROM catalog_info_new_subtitles s
WHERE s.change_id=? AND s.asset_id=? AND s.input_ordinal IN(%s)
 AND EXISTS(SELECT 1 FROM asset_subtitle_facts f WHERE f.asset_id=s.asset_id)
 AND (s.sidecar_id IS NULL OR EXISTS(SELECT 1 FROM subtitle_sidecars c JOIN catalog_info_new_subtitle_sidecars n ON n.path=c.path AND n.change_id=s.change_id WHERE n.id=s.sidecar_id))
ON CONFLICT(asset_id,origin,locator) DO UPDATE SET stream_index=excluded.stream_index,sidecar_id=excluded.sidecar_id,format=excluded.format,language=excluded.language,title=excluded.title,is_default=excluded.is_default,is_forced=excluded.is_forced,reason=excluded.reason,hearing_impaired=excluded.hearing_impaired`, nil),
	infoCopyInsertStep(9, "catalog_info_new_subtitle_inventory_evidence", infoCopyAssetScope,
		`INSERT INTO subtitle_inventory_evidence(asset_id,revision,evidence)
SELECT s.asset_id,s.revision,s.evidence FROM catalog_info_new_subtitle_inventory_evidence s
WHERE s.change_id=? AND s.asset_id=? AND s.input_ordinal IN(%s)
 AND EXISTS(SELECT 1 FROM asset_subtitle_facts f WHERE f.asset_id=s.asset_id AND f.revision=s.revision)
ON CONFLICT(asset_id) DO UPDATE SET revision=excluded.revision,evidence=excluded.evidence`, nil),

	// Disc observation, scoped to the exact witness object.
	infoCopyInsertStep(10, "catalog_info_new_disc_facts", infoCopyObjectAssetScope,
		`INSERT INTO inventory_disc_facts(object_id,asset_id,source_revision,source_generation,root_incarnation,kind,packaging,encryption,fingerprint,owner_title_key,choice_revision,media_title_key,media_stream_revision,media_chapter_revision)
SELECT s.object_id,s.asset_id,s.source_revision,s.source_generation,s.root_incarnation,s.kind,s.packaging,s.encryption,s.fingerprint,
 CASE WHEN c.asset_id=s.asset_id AND c.choice_revision>s.choice_revision THEN c.owner_title_key ELSE s.owner_title_key END,
 max(CASE WHEN c.asset_id=s.asset_id THEN c.choice_revision ELSE 0 END,s.choice_revision),
 s.media_title_key,s.media_stream_revision,s.media_chapter_revision
FROM catalog_info_new_disc_facts s LEFT JOIN inventory_disc_facts c ON c.object_id=s.object_id
WHERE s.change_id=? AND s.object_id=? AND s.asset_id=? AND s.input_ordinal IN(%s)
 AND EXISTS(SELECT 1 FROM inventory_objects o WHERE o.id=s.object_id)
ON CONFLICT(object_id) DO UPDATE SET asset_id=excluded.asset_id,source_revision=excluded.source_revision,source_generation=excluded.source_generation,root_incarnation=excluded.root_incarnation,kind=excluded.kind,packaging=excluded.packaging,encryption=excluded.encryption,fingerprint=excluded.fingerprint,owner_title_key=excluded.owner_title_key,choice_revision=excluded.choice_revision,media_title_key=excluded.media_title_key,media_stream_revision=excluded.media_stream_revision,media_chapter_revision=excluded.media_chapter_revision`, nil),
	infoCopyInsertStep(11, "catalog_info_new_disc_titles", infoCopyObjectScope,
		`INSERT INTO inventory_disc_titles(object_id,title_key,selection_token,title_number,disc_order,duration_ms,angle_count,playlist,dvd_title,vts,pgc)
SELECT s.object_id,s.title_key,s.selection_token,s.title_number,s.disc_order,s.duration_ms,s.angle_count,s.playlist,s.dvd_title,s.vts,s.pgc FROM catalog_info_new_disc_titles s
WHERE s.change_id=? AND s.object_id=? AND s.input_ordinal IN(%s)
 AND EXISTS(SELECT 1 FROM inventory_disc_facts f WHERE f.object_id=s.object_id)
ON CONFLICT(object_id,title_key) DO UPDATE SET selection_token=excluded.selection_token,title_number=excluded.title_number,disc_order=excluded.disc_order,duration_ms=excluded.duration_ms,angle_count=excluded.angle_count,playlist=excluded.playlist,dvd_title=excluded.dvd_title,vts=excluded.vts,pgc=excluded.pgc`, nil),
	infoCopyInsertStep(15, "catalog_info_new_disc_media_members", infoCopyObjectScope,
		`INSERT INTO inventory_disc_media_members(object_id,path,object_identity,size,modified_ns,required,directory)
SELECT s.object_id,s.path,s.object_identity,s.size,s.modified_ns,s.required,s.directory FROM catalog_info_new_disc_media_members s
WHERE s.change_id=? AND s.object_id=? AND s.input_ordinal IN(%s)
 AND EXISTS(SELECT 1 FROM inventory_disc_facts f WHERE f.object_id=s.object_id)
ON CONFLICT(object_id,path) DO UPDATE SET object_identity=excluded.object_identity,size=excluded.size,modified_ns=excluded.modified_ns,required=excluded.required,directory=excluded.directory`, nil),
	infoCopyInsertStep(12, "catalog_info_new_disc_clips", infoCopyObjectScope,
		`INSERT INTO inventory_disc_clips(object_id,title_key,sequence,file,in_time_90k,out_time_90k)
SELECT s.object_id,s.title_key,s.sequence,s.file,s.in_time_90k,s.out_time_90k FROM catalog_info_new_disc_clips s
WHERE s.change_id=? AND s.object_id=? AND s.input_ordinal IN(%s)
 AND EXISTS(SELECT 1 FROM inventory_disc_titles t WHERE t.object_id=s.object_id AND t.title_key=s.title_key)
ON CONFLICT(object_id,title_key,sequence) DO UPDATE SET file=excluded.file,in_time_90k=excluded.in_time_90k,out_time_90k=excluded.out_time_90k`, nil),
	infoCopyInsertStep(13, "catalog_info_new_disc_streams", infoCopyObjectScope,
		`INSERT INTO inventory_disc_streams(object_id,title_key,stream_order,clip_sequence,pid,kind,coding,language,dv_enhancement,dv_enhancement_known,dv_base_pid,video_role)
SELECT s.object_id,s.title_key,s.stream_order,s.clip_sequence,s.pid,s.kind,s.coding,s.language,s.dv_enhancement,s.dv_enhancement_known,s.dv_base_pid,s.video_role FROM catalog_info_new_disc_streams s
WHERE s.change_id=? AND s.object_id=? AND s.input_ordinal IN(%s)
 AND EXISTS(SELECT 1 FROM inventory_disc_titles t WHERE t.object_id=s.object_id AND t.title_key=s.title_key)
ON CONFLICT(object_id,title_key,stream_order) DO UPDATE SET clip_sequence=excluded.clip_sequence,pid=excluded.pid,kind=excluded.kind,coding=excluded.coding,language=excluded.language,dv_enhancement=excluded.dv_enhancement,dv_enhancement_known=excluded.dv_enhancement_known,dv_base_pid=excluded.dv_base_pid,video_role=excluded.video_role`, nil),
	infoCopyInsertStep(14, "catalog_info_new_disc_chapters", infoCopyObjectScope,
		`INSERT INTO inventory_disc_chapters(object_id,title_key,chapter_order,start_ms)
SELECT s.object_id,s.title_key,s.chapter_order,s.start_ms FROM catalog_info_new_disc_chapters s
WHERE s.change_id=? AND s.object_id=? AND s.input_ordinal IN(%s)
 AND EXISTS(SELECT 1 FROM inventory_disc_titles t WHERE t.object_id=s.object_id AND t.title_key=s.title_key)
ON CONFLICT(object_id,title_key,chapter_order) DO UPDATE SET start_ms=excluded.start_ms`, nil),
	infoCopyInsertStep(16, "catalog_info_new_disc_title_media", infoCopyObjectScope,
		`INSERT INTO inventory_disc_title_media(object_id,title_key,source_revision,fingerprint,revision,container,video_codec,audio_codec,width,height,duration_ms,origin_us,timing_known)
SELECT s.object_id,s.title_key,s.source_revision,s.fingerprint,s.revision,s.container,s.video_codec,s.audio_codec,s.width,s.height,s.duration_ms,s.origin_us,s.timing_known FROM catalog_info_new_disc_title_media s
WHERE s.change_id=? AND s.object_id=? AND s.input_ordinal IN(%s)
 AND EXISTS(SELECT 1 FROM inventory_disc_titles t WHERE t.object_id=s.object_id AND t.title_key=s.title_key)
ON CONFLICT(object_id,title_key) DO UPDATE SET source_revision=excluded.source_revision,fingerprint=excluded.fingerprint,revision=excluded.revision,container=excluded.container,video_codec=excluded.video_codec,audio_codec=excluded.audio_codec,width=excluded.width,height=excluded.height,duration_ms=excluded.duration_ms,origin_us=excluded.origin_us,timing_known=excluded.timing_known`, nil),
	infoCopyInsertStep(17, "catalog_info_new_disc_title_media_streams", infoCopyObjectScope,
		`INSERT INTO inventory_disc_title_media_streams(object_id,title_key,stream_index,type,codec,language,title,channels,channel_layout,is_default,is_forced,color_transfer,color_primaries,detail_json,breadth_json)
SELECT s.object_id,s.title_key,s.stream_index,s.type,s.codec,s.language,s.title,s.channels,s.channel_layout,s.is_default,s.is_forced,s.color_transfer,s.color_primaries,s.detail_json,s.breadth_json FROM catalog_info_new_disc_title_media_streams s
WHERE s.change_id=? AND s.object_id=? AND s.input_ordinal IN(%s)
 AND EXISTS(SELECT 1 FROM inventory_disc_title_media m WHERE m.object_id=s.object_id AND m.title_key=s.title_key)
ON CONFLICT(object_id,title_key,stream_index) DO UPDATE SET type=excluded.type,codec=excluded.codec,language=excluded.language,title=excluded.title,channels=excluded.channels,channel_layout=excluded.channel_layout,is_default=excluded.is_default,is_forced=excluded.is_forced,color_transfer=excluded.color_transfer,color_primaries=excluded.color_primaries,detail_json=excluded.detail_json,breadth_json=excluded.breadth_json`, nil),
	infoCopyInsertStep(18, "catalog_info_new_disc_title_media_members", infoCopyObjectScope,
		`INSERT INTO inventory_disc_title_media_members(object_id,title_key,path,object_identity,size,modified_ns,required,directory)
SELECT s.object_id,s.title_key,s.path,s.object_identity,s.size,s.modified_ns,s.required,s.directory FROM catalog_info_new_disc_title_media_members s
WHERE s.change_id=? AND s.object_id=? AND s.input_ordinal IN(%s)
 AND EXISTS(SELECT 1 FROM inventory_disc_title_media m WHERE m.object_id=s.object_id AND m.title_key=s.title_key)
ON CONFLICT(object_id,title_key,path) DO UPDATE SET object_identity=excluded.object_identity,size=excluded.size,modified_ns=excluded.modified_ns,required=excluded.required,directory=excluded.directory`, nil),
}

// MaterializeInfoChangeTx is the bounded InfoMaterializer callback. It advances
// one quantum of canonical child-set reconciliation for one change id and
// returns done only when every sealed changed family is completely installed.
func MaterializeInfoChangeTx(ctx context.Context, tx *sql.Tx, changeID int64) (bool, error) {
	var token string
	var phase, mask, step, id, aux int64
	var key1, key2, key3 string
	err := tx.QueryRowContext(ctx, `SELECT asset_token,phase,family_mask,copy_phase,copy_id,copy_aux,copy_key,copy_key2,copy_key3
 FROM catalog_info_changes WHERE id=?`, changeID).
		Scan(&token, &phase, &mask, &step, &id, &aux, &key1, &key2, &key3)
	if errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("%w: unknown info change", ErrInfoInvariant)
	}
	if err != nil {
		return false, err
	}
	if phase != 2 {
		// Materialization is legal only behind the completed holder pre-fence.
		return false, fmt.Errorf("%w: info change phase %d is not materialization", ErrInfoInvariant, phase)
	}
	if mask == 0 {
		return true, nil
	}
	if mask&^infoCopyFamilyMask != 0 {
		return false, fmt.Errorf("%w: info family mask %#x is outside families 1..18", ErrInfoInvariant, mask)
	}
	if step < 0 || step > int64(len(infoCopySteps)) {
		return false, fmt.Errorf("%w: info copy step %d", ErrInfoInvariant, step)
	}
	if aux != 0 && aux != 1 {
		return false, fmt.Errorf("%w: info copy cursor flag %d", ErrInfoInvariant, aux)
	}
	cursor := infoCopyCursor{valid: aux == 1, id: id, text: [3]string{key1, key2, key3}}
	object, err := infoCopyWitnessObject(ctx, tx, changeID, mask)
	if err != nil {
		return false, err
	}
	if step == 0 && !cursor.valid {
		if err = verifyInfoCopyInput(ctx, tx, changeID, token, mask, object); err != nil {
			return false, err
		}
	}
	if err = releaseInfoRepointedOwnerTx(ctx, tx, changeID); err != nil {
		return false, err
	}
	discLive, blocked, err := infoCopyDiscCustodyTx(ctx, tx, changeID, token, object)
	if err != nil || blocked {
		return false, err
	}
	run := infoCopyRun{changeID: changeID, token: token, object: object}
	// Timed facts are independent per-title observations. A nonempty family16
	// pins one exact title; an empty family16 is the structure-wide reset.
	if mask&((1<<15)|(1<<16)|(1<<17)) != 0 {
		var expected int64
		e := tx.QueryRowContext(ctx, `SELECT rows_expected FROM catalog_info_input_families WHERE change_id=? AND family=16 AND sealed=1`, changeID).Scan(&expected)
		if e != nil || expected > 1 {
			return false, fmt.Errorf("%w: timed title manifest", ErrInfoInvariant)
		}
		if expected == 1 {
			if e = tx.QueryRowContext(ctx, `SELECT title_key FROM catalog_info_new_disc_title_media WHERE change_id=? AND object_id=? LIMIT 1`, changeID, object).Scan(&run.timedTitle); e != nil {
				return false, e
			}
			run.timedScoped = true
		}
	}
	budget := newInfoCopyBudget()
	next := int(step)
	for next < len(infoCopySteps) && !budget.exhausted() {
		current := &infoCopySteps[next]
		if mask&(int64(1)<<uint(current.family-1)) == 0 || (!discLive && current.family >= 10) {
			next++
			cursor = infoCopyCursor{}
			continue
		}
		var done bool
		switch current.kind {
		case infoCopyRemove, infoCopyPark:
			cursor, done, err = run.walk(ctx, tx, current, cursor, &budget)
		case infoCopyInsert:
			cursor, done, err = run.install(ctx, tx, current, cursor, &budget)
		default:
			return false, fmt.Errorf("%w: unknown info copy step kind", ErrInfoInvariant)
		}
		if err != nil {
			return false, err
		}
		if done {
			next++
			cursor = infoCopyCursor{}
		}
	}
	if next >= len(infoCopySteps) {
		// Hand the header back with the GC counters clear; phase 3 activation and
		// the later copy_family retirement walk start from zero.
		return true, clearInfoCopyCursor(ctx, tx, changeID)
	}
	return false, saveInfoCopyCursor(ctx, tx, changeID, next, cursor)
}

// NewInfoMaterializer returns the callback AdvanceInfoPublicationTx expects.
func NewInfoMaterializer() InfoMaterializer { return MaterializeInfoChangeTx }

// infoCopyWitnessObject resolves the exact object the masked disc families
// describe. Every disc family manifest names its witness through
// witness_ordinal; all masked disc families must agree, because one change
// cannot represent two independent observations of the asset.
func infoCopyWitnessObject(ctx context.Context, tx *sql.Tx, changeID, mask int64) (string, error) {
	for family := 10; family <= infoCopyFamilies; family++ {
		if mask&(int64(1)<<uint(family-1)) == 0 {
			continue
		}
		var witness int
		err := tx.QueryRowContext(ctx, `SELECT witness_ordinal FROM catalog_info_input_families WHERE change_id=? AND family=?`, changeID, family).Scan(&witness)
		if errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("%w: changed family %d has no input manifest", ErrInfoInvariant, family)
		}
		if err != nil {
			return "", err
		}
		w, err := infoInputWitnessAt(ctx, tx, changeID, witness)
		if err != nil {
			return "", err
		}
		if w.ObjectID == "" {
			return "", fmt.Errorf("%w: changed disc family %d names no witness object", ErrInfoInvariant, family)
		}
		return w.ObjectID, nil
	}
	return "", nil
}

// verifyInfoCopyInput proves the sealed input is complete and self-consistent
// before the first canonical row is written. It runs once per change: header
// phase 0 seals the staging tables and the immutability triggers refuse any
// later edit, so the producer's seal is the only place that counts staged rows.
// It never counts a family and never scans another asset's input.
func verifyInfoCopyInput(ctx context.Context, tx *sql.Tx, changeID int64, token string, mask int64, object string) error {
	for family := 1; family <= infoCopyFamilies; family++ {
		if mask&(int64(1)<<uint(family-1)) == 0 {
			continue
		}
		var witness int
		var expected, staged, sealed int64
		err := tx.QueryRowContext(ctx, `SELECT witness_ordinal,rows_expected,rows_staged,sealed FROM catalog_info_input_families WHERE change_id=? AND family=?`, changeID, family).
			Scan(&witness, &expected, &staged, &sealed)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: changed family %d has no input manifest", ErrInfoInvariant, family)
		}
		if err != nil {
			return err
		}
		if sealed != 1 || staged != expected {
			return fmt.Errorf("%w: changed family %d is not sealed at its expected row count", ErrInfoInvariant, family)
		}
		if family < 10 {
			continue
		}
		w, err := infoInputWitnessAt(ctx, tx, changeID, witness)
		if err != nil {
			return err
		}
		if w.ObjectID != object {
			return fmt.Errorf("%w: changed disc family %d names object %q, not %q", ErrInfoInvariant, family, w.ObjectID, object)
		}
	}
	if object == "" {
		return nil
	}
	// Physical custody may move after acceptance. The whole object scope is
	// discharged by MaterializeInfoChangeTx when that exact obligation is gone;
	// it must never fail one child at a time or overwrite the replacement.

	// One manifest witness cannot represent two objects: staged disc facts for
	// any other object are refused instead of being silently coalesced.
	var mixed bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_info_new_disc_facts WHERE change_id=? AND object_id<>?)`, changeID, object).Scan(&mixed); err != nil {
		return err
	}
	if mixed {
		return fmt.Errorf("%w: change stages disc facts for an object other than %s", ErrInfoInvariant, object)
	}
	return nil
}

// infoCopyRun carries the immutable scope of one materialize call.
type infoCopyRun struct {
	changeID    int64
	token       string
	object      string
	timedTitle  string
	timedScoped bool
}

// walkSQL builds the single-key seek of one remove or park step. The bind order
// is always <scope>, [change id], <cursor>, so the statement is a prefix seek on
// the canonical PK/UNIQUE index and never a table scan.
func (r infoCopyRun) walkSQL(step *infoCopyStep, cursor infoCopyCursor) (string, []any) {
	query := step.walk
	args := step.scope.args(r.token, r.object)
	if r.timedScoped && step.family >= 16 && step.family <= 18 {
		query += " AND c.title_key=?"
		args = append(args, r.timedTitle)
	}
	if cursor.valid {
		query += " AND " + step.tuple.rowValue("c") + " > (" + infoCopyPlaceholders(len(step.tuple.cols)) + ")"
		args = append(args, step.tuple.args(cursor)...)
	}
	return query + " ORDER BY " + step.tuple.columns("c") + " LIMIT 1", args
}

// walk advances one remove or park step by at most the remaining quantum. A
// kept raw key costs exactly one row of the quantum, and the cursor is the last
// fully processed key whatever its decision, so a long kept prefix is walked at
// the same fixed rate as a long removed run.
func (r infoCopyRun) walk(ctx context.Context, tx *sql.Tx, step *infoCopyStep, cursor infoCopyCursor, budget *infoCopyBudget) (infoCopyCursor, bool, error) {
	for !budget.exhausted() {
		query, args := r.walkSQL(step, cursor)
		key, err := scanInfoCopyKey(tx.QueryRowContext(ctx, query, args...), step.tuple)
		if errors.Is(err, sql.ErrNoRows) {
			return cursor, true, nil
		}
		if err != nil {
			return cursor, false, err
		}
		budget.spend(key.bytes())
		if step.kind == infoCopyRemove {
			kept, err := r.kept(ctx, tx, step, key)
			if err != nil {
				return cursor, false, err
			}
			if !kept {
				if err = r.proveChildrenEmpty(ctx, tx, step, key); err != nil {
					return cursor, false, err
				}
				if _, err = tx.ExecContext(ctx, step.del, step.tuple.args(key)...); err != nil {
					return cursor, false, err
				}
			}
		} else if _, err = tx.ExecContext(ctx, step.park, step.tuple.args(key)...); err != nil {
			return cursor, false, err
		}
		cursor = key
	}
	return cursor, false, nil
}

// kept reports whether the staged NEW set names this canonical key.
func (r infoCopyRun) kept(ctx context.Context, tx *sql.Tx, step *infoCopyStep, key infoCopyCursor) (bool, error) {
	args := append([]any{r.changeID}, step.tuple.args(key)...)
	var kept bool
	err := tx.QueryRowContext(ctx, step.probe, args...).Scan(&kept)
	return kept, err
}

// proveChildrenEmpty refuses to delete an obsolete parent while any canonical
// FK child still holds a row under the full parent key. A masked child family is
// reconciled before its parent, so a surviving child is an invariant failure and
// never an invitation to let an uncharged cascade remove it.
func (r infoCopyRun) proveChildrenEmpty(ctx context.Context, tx *sql.Tx, step *infoCopyStep, key infoCopyCursor) error {
	for _, child := range step.children {
		var occupied bool
		query := "SELECT EXISTS(SELECT 1 FROM " + child + " WHERE " + step.tuple.predicate("") + ")"
		if err := tx.QueryRowContext(ctx, query, step.tuple.args(key)...).Scan(&occupied); err != nil {
			return err
		}
		if occupied {
			return fmt.Errorf("%w: canonical %s still holds a %s row for (%s); a masked change cannot fan out a cascade",
				ErrInfoInvariant, step.table, child, infoCopyKeyString(step.tuple, key))
		}
	}
	return nil
}

// install copies one batch of staged rows in input_ordinal order. The ordinal is
// the only value read from staging here; the INSERT itself reads and writes the
// typed columns, so no giant row is materialized in Go.
func (r infoCopyRun) install(ctx context.Context, tx *sql.Tx, step *infoCopyStep, cursor infoCopyCursor, budget *infoCopyBudget) (infoCopyCursor, bool, error) {
	limit := budget.rows
	if limit > infoCopyRowsPerQuantum {
		limit = infoCopyRowsPerQuantum
	}
	if limit <= 0 {
		return cursor, false, nil
	}
	last := int64(-1)
	if cursor.valid {
		last = cursor.id
	}
	ordinals, err := stagedInfoCopyOrdinals(ctx, tx, step.table, r.changeID, last, limit)
	if err != nil {
		return cursor, false, err
	}
	if len(ordinals) == 0 {
		return cursor, true, nil
	}
	next := infoCopyCursor{valid: true, id: ordinals[len(ordinals)-1]}
	for range ordinals {
		budget.spend(8)
	}
	apply := ordinals
	if step.prepare != nil {
		if apply, err = step.prepare(ctx, tx, r.changeID, ordinals); err != nil {
			return next, false, err
		}
	}
	if len(apply) == 0 {
		return next, false, nil
	}
	args := make([]any, 0, 2+len(apply))
	args = append(args, r.changeID)
	args = append(args, step.scope.args(r.token, r.object)...)
	for _, ordinal := range apply {
		args = append(args, ordinal)
	}
	result, err := tx.ExecContext(ctx, fmt.Sprintf(step.insert, infoCopyPlaceholders(len(apply))), args...)
	if err != nil {
		return next, false, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return next, false, err
	}
	if int(n) != len(apply) {
		// A skipped row means the canonical parent, revision or scope the staged
		// row needs is not there. Never accept a partially installed family.
		return next, false, fmt.Errorf("%w: family %d installed %d of %d staged rows", ErrInfoInvariant, step.family, n, len(apply))
	}
	return next, false, nil
}

// stagedInfoCopyOrdinals reads at most limit ordinals after the cursor.
func stagedInfoCopyOrdinals(ctx context.Context, tx *sql.Tx, table string, changeID, cursor int64, limit int) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, `SELECT input_ordinal FROM `+table+` WHERE change_id=? AND input_ordinal>? ORDER BY input_ordinal LIMIT ?`, changeID, cursor, limit)
	if err != nil {
		return nil, err
	}
	ordinals := make([]int64, 0, limit)
	for rows.Next() {
		var ordinal int64
		if err = rows.Scan(&ordinal); err != nil {
			break
		}
		ordinals = append(ordinals, ordinal)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	return ordinals, err
}

// saveInfoCopyCursor checkpoints the step and the last fully processed raw key.
// Every cursor column is rewritten, so a stale key never leaks into the next
// step's seek.
func saveInfoCopyCursor(ctx context.Context, tx *sql.Tx, changeID int64, step int, cursor infoCopyCursor) error {
	aux := 0
	if cursor.valid {
		aux = 1
	}
	_, err := tx.ExecContext(ctx, `UPDATE catalog_info_changes SET copy_family=0,copy_phase=?,copy_id=?,copy_aux=?,copy_key=?,copy_key2=?,copy_key3=? WHERE id=? AND phase=2`,
		step, cursor.id, aux, cursor.text[0], cursor.text[1], cursor.text[2], changeID)
	return err
}

// clearInfoCopyCursor hands the header back to the phase machine with every
// cursor column empty.
func clearInfoCopyCursor(ctx context.Context, tx *sql.Tx, changeID int64) error {
	_, err := tx.ExecContext(ctx, `UPDATE catalog_info_changes SET copy_family=0,copy_phase=0,copy_id=0,copy_aux=0,copy_key='',copy_key2='',copy_key3='' WHERE id=? AND phase=2`, changeID)
	return err
}

// infoCopyKeyString renders a raw key for an invariant message.
func infoCopyKeyString(tuple infoCopyTuple, key infoCopyCursor) string {
	parts := make([]string, 0, len(tuple.cols))
	for i := 0; i < tuple.text; i++ {
		parts = append(parts, key.text[i])
	}
	if tuple.text < len(tuple.cols) {
		parts = append(parts, fmt.Sprint(key.id))
	}
	return strings.Join(parts, ",")
}

func infoCopyPlaceholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

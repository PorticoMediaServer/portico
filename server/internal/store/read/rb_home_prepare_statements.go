package read

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// Inert inventory for the large fixed-Home preparation. Every identifier,
// index and predicate is a compiled constant; a request can choose only one of
// the four published rows, never SQL, columns or an index.
//
// The row preparation replaces the small fixed family's four-library /
// thirty-two-entity completion boundary with genuine source-stream frontiers:
//   - catalogue chronology streams (catalog_entities_added_v1 and
//     catalog_entities_content_added) walk every raw date position, including
//     the undated bucket, and stop only on a short window;
//   - the profile's complete current personal rows are walked through the
//     personal_current_live point for the activity rows;
//   - each selected work's children are enumerated completely through the
//     existing show/album/book hierarchy indexes, with cap+1 windows.
//
// Catalogue timestamps are schema-enforced fixed-width, so the new streams read
// no variable octet. Personal text and every binding field keep the already
// registered exact byte points from the fixed family (see
// rb_home_prepare_octets.go); this file adds no second dispatch list.
const (
	// rbHomePrepareRaw bounds one raw chronology/personal window exactly like
	// the common browse window. Rejected rows still consume a position.
	rbHomePrepareRaw = MaxRaw
	// rbHomePrepareLeafWindow bounds how many hierarchy children one snapshot
	// hydrates (personal text and exact binding bytes).
	rbHomePrepareLeafWindow = 16
	// rbHomePrepareChildWindow bounds one work's complete child window. A
	// larger child set is a named pending boundary, never a truncated set.
	rbHomePrepareChildWindow = 128
	// rbHomePreparePersonalWindow bounds one profile personal window.
	rbHomePreparePersonalWindow = 16
	// rbHomePrepareStreamsPerQuantum bounds how many source frontiers one
	// admitted snapshot advances.
	rbHomePrepareStreamsPerQuantum = 4
	// rbHomePrepareQuantaPerAdvance bounds one poll to a few snapshots. The
	// original 12s preparation deadline still bounds the whole job.
	rbHomePrepareQuantaPerAdvance = 8
	// rbHomePrepareRestartLimit mirrors the F-04 content-restart budget.
	rbHomePrepareRestartLimit = 2
	// rbHomePrepareMaxQueries is the finite per-snapshot proof recipe: the
	// manifest/principal frame, every library fence and up to four bounded
	// source legs plus their exact hydration points.
	rbHomePrepareMaxQueries uint16 = 512
	// rbHomePrepareRowLimit is the published newest-works shelf size.
	rbHomePrepareRowLimit = 100
	// rbHomePrepareSelectionCap refuses a candidate set that cannot be completed
	// inside the admitted retained bytes instead of truncating it.
	rbHomePrepareSelectionCap = 4096
	// rbHomePrepareLibraryCap mirrors the manifest's own finite library census.
	rbHomePrepareLibraryCap = 16
	// The retained workspace charges the typed maps and slices this preparation
	// keeps between quanta, not only the hydrated entity values. A map entry,
	// its pointer and the parallel slice slot for one candidate or work, and one
	// child-id reference, are charged before the value is stored so the admitted
	// retained-byte ceiling covers them.
	rbHomePrepareCandidateCost = 160
	rbHomePrepareWorkCost      = 160
	rbHomePrepareChildRefCost  = 32
	// rbHomePreparePendingReserve keeps room for the named pending page when a
	// value or card charge hits the ceiling; the refusal itself must still fit.
	rbHomePreparePendingReserve = 1024
)

// rbHomePrepareEntityColumns is the closed entity projection shared by the
// hierarchy child windows. It matches rb.home.fixed.entity field for field.
const rbHomePrepareEntityColumns = `e.id,e.public_id,e.kind,e.retired,e.revision,e.admission_own_age,e.parent_id,e.show_id,e.season_id,e.album_id,e.book_id,e.season_number,e.episode_number,e.disc_number,e.track_number,e.part_number,
 added_at,last_content_added_at,length(e.numbering),0,e.metadata_revision,e.source_revision,e.identity_revision`

// rbHomePrepareChronologyDefinitions walks one (library, kind) chronology in
// descending (date, id) order. The equal/next split keeps an equal-instant run
// exactly complete across a window boundary; the nulls leg is the undated
// bucket, which sorts after every dated row.
func rbHomePrepareChronologyDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	var out []stmt.Definition
	for _, clock := range []Chronology{DateAdded, ContentAdded} {
		column, index := "added_at", "catalog_entities_added_v1"
		if clock == ContentAdded {
			column, index = "last_content_added_at", "catalog_entities_content_added"
		}
		base := `SELECT id,` + column + `,public_id,retired,admission_own_age,show_id,album_id,book_id FROM catalog_entities INDEXED BY ` + index + ` WHERE library_id=? AND kind=?`
		end := ` ORDER BY ` + column + ` DESC,id DESC LIMIT ?`
		add := func(name, predicate, seek string, params []stmt.Param, args []any) {
			bindings := append([]any{int64(1), int64(1)}, args...)
			bindings = append(bindings, int64(rbHomePrepareRaw+1))
			empty := append([]any(nil), bindings...)
			empty[0] = int64(999999)
			all := append(append([]stmt.Param{i("library"), i("kind")}, params...), i("limit"))
			out = append(out, stmt.Definition{
				ID: readStatementID("rb.home_prepare." + string(clock) + "." + name), Owner: "store/read", Kind: stmt.Read,
				CallSites: []string{"store/read/rb_home_prepare.go:RBHomePreparePreparation.window"},
				SQL:       base + predicate + end, Params: all,
				Cohorts: []stmt.Cohort{{Name: "present", Bindings: bindings}, {Name: "empty", Bindings: empty}},
				Plan:    stmt.Plan{Hot: true, Indexes: []string{index}, Seeks: []string{seek}},
			})
		}
		add("first", " AND "+column+" IS NOT NULL", "library_id=? AND kind=?", nil, nil)
		add("equal", " AND "+column+"=? AND id<?", "library_id=? AND kind=? AND "+column+"=?", []stmt.Param{s("date"), i("after")}, []any{"2026-11-01T00:00:00.000000000Z", int64(1 << 40)})
		add("next", " AND "+column+"<?", "library_id=? AND kind=? AND "+column+"<?", []stmt.Param{s("date")}, []any{"2026-11-01T00:00:00.000000000Z"})
		add("nulls", " AND "+column+" IS NULL AND id<?", "library_id=? AND kind=?", []stmt.Param{i("after")}, []any{int64(1 << 40)})
	}
	return out
}

// rbHomePrepareChildSource is one compiled hierarchy child source. Children are
// reduced as a complete bounded window: the source index proves the parent
// scope and the composition's own ordering is applied later, so no expression
// ORDER BY (and no temporary B-tree) is introduced.
type rbHomePrepareChildSource struct {
	name, from, where, index string
	parentSeek               string
}

func rbHomePrepareChildSources() []rbHomePrepareChildSource {
	return []rbHomePrepareChildSource{
		{
			name: "show", from: "catalog_entities AS e INDEXED BY catalog_entities_show_fk",
			where:      "show_id IS NOT NULL AND show_id=? AND library_id=?",
			index:      "catalog_entities_show_fk",
			parentSeek: "show_id=?",
		},
		{
			name: "album", from: "catalog_entities AS e INDEXED BY catalog_entities_album",
			where:      "album_id IS NOT NULL AND album_id=? AND library_id=?",
			index:      "catalog_entities_album",
			parentSeek: "album_id=?",
		},
		{
			name: "book", from: "catalog_entities AS e INDEXED BY catalog_entities_book",
			where:      "book_id IS NOT NULL AND book_id=? AND library_id=?",
			index:      "catalog_entities_book",
			parentSeek: "book_id=?",
		},
	}
}

func rbHomePrepareChildSelect(source rbHomePrepareChildSource) string {
	return "SELECT " + rbHomePrepareEntityColumns + " FROM " + source.from + " WHERE " + source.where + " LIMIT ?"
}

// rbHomePrepareChildDefinitions emits one complete bounded child window per
// hierarchy source.
func rbHomePrepareChildDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	var out []stmt.Definition
	for _, source := range rbHomePrepareChildSources() {
		present := []any{int64(2000), int64(1), int64(rbHomePrepareChildWindow + 1)}
		empty := []any{int64(999999), int64(1), int64(rbHomePrepareChildWindow + 1)}
		out = append(out, stmt.Definition{
			ID: readStatementID("rb.home_prepare.children." + source.name + ".all"), Owner: "store/read", Kind: stmt.Read,
			CallSites: []string{"store/read/rb_home_prepare.go:RBHomePreparePreparation.readChildren"},
			SQL:       rbHomePrepareChildSelect(source), Params: []stmt.Param{i("parent"), i("library"), i("limit")},
			Cohorts: []stmt.Cohort{{Name: "present", Bindings: present}, {Name: "empty", Bindings: empty}},
			Plan:    stmt.Plan{Hot: true, Indexes: []string{source.index}, Seeks: []string{source.parentSeek}},
		})
	}
	return out
}

// rbHomePreparePersonalDefinitions walks the complete current personal set for
// one profile in item order. It is the activity-row source, not a profile
// catalogue: only rows the owner actually has are returned.
func rbHomePreparePersonalDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	base := `SELECT item_id,library_id FROM personal_items INDEXED BY personal_current_live WHERE profile_id=?`
	plan := stmt.Plan{Hot: true, Indexes: []string{"personal_current_live"}, Seeks: []string{"profile_id=? AND item_id>?"}}
	return []stmt.Definition{
		{ID: readStatementID("rb.home_prepare.personal.first"), Owner: "store/read", Kind: stmt.Read, CallSites: []string{"store/read/rb_home_prepare.go:RBHomePreparePreparation.window"}, SQL: base + ` AND item_id>? ORDER BY item_id LIMIT ?`, Params: []stmt.Param{s("profile"), i("after"), i("limit")}, Cohorts: []stmt.Cohort{{Name: "present", Bindings: []any{"p", int64(0), int64(rbHomePreparePersonalWindow + 1)}}, {Name: "empty", Bindings: []any{"zz", int64(0), int64(rbHomePreparePersonalWindow + 1)}}}, Plan: plan},
	}
}

// rbHomePrepareDefinitions is this task's own inert inventory. Callers compose
// it with the existing browse, saved-view, Home manifest, binding and fixed
// entity points; no shared list is appended.
func rbHomePrepareDefinitions() []stmt.Definition {
	out := rbHomePrepareChronologyDefinitions()
	out = append(out, rbHomePrepareChildDefinitions()...)
	return append(out, rbHomePreparePersonalDefinitions()...)
}

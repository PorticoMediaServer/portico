package writer

import (
	"context"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// The chapter before-image family pair is asset_chapter_facts (parent,3) and
// asset_chapters (child,4). It shares IA21's bounded keyset/dependency walk in
// ia_refresh_streams.go and owns only its statements and typed projection, so
// the two families cannot drift apart. Nothing here mutates, clears a fence or
// retires custody.
const (
	iaRefreshChapterCursor      stmt.ID = "ia.refresh.chapters.cursor"
	iaRefreshChapterFirst       stmt.ID = "ia.refresh.chapters.first"
	iaRefreshChapterSuccessor   stmt.ID = "ia.refresh.chapters.successor"
	iaRefreshChapterParentImage stmt.ID = "ia.refresh.chapters.parent_image"
	iaRefreshChapterChildStart  stmt.ID = "ia.refresh.chapters.child_start"
	iaRefreshChapterChildImage  stmt.ID = "ia.refresh.chapters.child_image"
)

var iaRefreshChapterFamily = iaRefreshWalkFamily{
	parent: 3, child: 4,
	cursor: iaRefreshChapterCursor, first: iaRefreshChapterFirst, successor: iaRefreshChapterSuccessor,
	parentImage: iaRefreshChapterParentImage, childImage: iaRefreshChapterChildImage, childImageStart: iaRefreshChapterChildStart,
}

// A complete typed chapter projection for one asset. Fact nil with no rows means
// the new source exposes no chapter family at all.
type iaRefreshChapterDesired struct {
	Fact *iaRefreshChapterFactRow
	Rows []iaRefreshChapterRow
}

func iaRefreshNextChapterStep(ctx context.Context, turn *commandTurn, q iaRefreshRequest, d iaRefreshChapterDesired) (out iaRefreshWalkStep, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	var desired iaRefreshWalkDesired
	if d.Fact != nil {
		desired.fact = *d.Fact
	}
	for j := range d.Rows {
		desired.rows = append(desired.rows, d.Rows[j])
	}
	return iaRefreshWalkNext(ctx, turn, iaRefreshChapterFamily, q, desired)
}

func iaRefreshNextChapterRestore(ctx context.Context, turn *commandTurn, q iaRefreshRequest) (out iaRefreshWalkStep, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	return iaRefreshWalkRestore(ctx, turn, iaRefreshChapterFamily, q)
}

func iaRefreshChapterDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	defs := []stmt.Definition{
		{ID: iaRefreshChapterCursor, Kind: stmt.Read,
			SQL:    `SELECT state,revision,asset_id,asset_token,expected_epoch,input_revision,after_family,after_key FROM catalog_info_refresh_undo WHERE refresh_id=? AND command_incarnation=?`,
			Params: []stmt.Param{i("refresh"), b("incarnation")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_info_refresh_undo"}},
			Cohorts: []stmt.Cohort{{Name: "walk_cursor", Bindings: []any{int64(1), make([]byte, 16)}}}},
		{ID: iaRefreshChapterFirst, Kind: stmt.Read,
			SQL:    `SELECT chapter_index FROM asset_chapters WHERE asset_id=? ORDER BY chapter_index LIMIT ?`,
			Params: []stmt.Param{t("asset"), i("limit")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_asset_chapters_1"}},
			Cohorts: []stmt.Cohort{{Name: "first_live_child", Bindings: []any{"refresh-asset", int64(1)}}}},
		{ID: iaRefreshChapterSuccessor, Kind: stmt.Read,
			SQL:    `SELECT chapter_index FROM asset_chapters WHERE asset_id=? AND chapter_index>? ORDER BY chapter_index LIMIT ?`,
			Params: []stmt.Param{t("asset"), i("after"), i("limit")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_asset_chapters_1"}},
			Cohorts: []stmt.Cohort{{Name: "next_live_child", Bindings: []any{"refresh-asset", int64(0), int64(1)}}}},
		{ID: iaRefreshChapterParentImage, Kind: stmt.Read,
			SQL:    `SELECT old_exists FROM catalog_info_undo_asset_chapter_facts WHERE refresh_id=? AND asset_id=?`,
			Params: []stmt.Param{i("refresh"), t("asset")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_info_undo_asset_chapter_facts"}},
			Cohorts: []stmt.Cohort{{Name: "parent_before_image", Bindings: []any{int64(1), "refresh-asset"}}}},
		{ID: iaRefreshChapterChildStart, Kind: stmt.Read,
			SQL:    `SELECT chapter_index FROM catalog_info_undo_asset_chapters WHERE refresh_id=? AND asset_id=? ORDER BY chapter_index DESC LIMIT ?`,
			Params: []stmt.Param{i("refresh"), t("asset"), i("limit")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_info_undo_asset_chapters"}},
			Cohorts: []stmt.Cohort{{Name: "highest_child_image", Bindings: []any{int64(1), "refresh-asset", int64(1)}}}},
		{ID: iaRefreshChapterChildImage, Kind: stmt.Read,
			SQL:    `SELECT chapter_index FROM catalog_info_undo_asset_chapters WHERE refresh_id=? AND asset_id=? AND chapter_index<? ORDER BY chapter_index DESC LIMIT ?`,
			Params: []stmt.Param{i("refresh"), t("asset"), i("before"), i("limit")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_info_undo_asset_chapters"}},
			Cohorts: []stmt.Cohort{{Name: "next_child_image", Bindings: []any{int64(1), "refresh-asset", int64(9), int64(1)}}}},
	}
	for j := range defs {
		defs[j].Owner = "store/writer IA refresh chapters"
		defs[j].CallSites = []string{"store/writer/ia_refresh_chapters.go"}
	}
	return defs
}

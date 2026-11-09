package writer

import (
	"context"
	"database/sql"
	"errors"
	"sort"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// IA22 owns the shared sidecar before-image cohort (family6) and its exact
// current-tuple and path/reference witnesses. Sidecars are global rows shared by
// overlapping libraries, so a restore may not overwrite a later accepted tuple,
// take a path another accepted source now owns, or unlink a row still referenced
// by any subtitle. IA23 must capture the family6 cohort before mutating any
// family7 row, so a rollback never needs an uncaptured shared row. IA20's journal performs the CAS; these helpers decide whether
// the caller must defer before touching a shared row.
const (
	iaRefreshSidecarCohortID  stmt.ID = "ia.refresh.sidecars.cohort"
	iaRefreshSidecarCurrentID stmt.ID = "ia.refresh.sidecars.current"
	iaRefreshSidecarPathID    stmt.ID = "ia.refresh.sidecars.path"
)

// iaRefreshSidecarCurrent is one exact current shared tuple, not a digest of it.
type iaRefreshSidecarCurrent struct {
	row   iaRefreshSidecarRow
	found bool
}

func iaRefreshSidecarDefinitions() []stmt.Definition {
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	return []stmt.Definition{
		iaRefreshWalkDefinition(stmt.Definition{
			ID: iaRefreshSidecarCohortID, Kind: stmt.Read,
			SQL:    `SELECT sidecar_id FROM asset_subtitles INDEXED BY asset_subtitles_asset WHERE asset_id=? AND sidecar_id IS NOT NULL LIMIT 257`,
			Params: []stmt.Param{t("asset")},
			Plan:   stmt.Plan{Hot: true, Indexes: []string{"asset_subtitles_asset"}},
		}),
		iaRefreshWalkDefinition(stmt.Definition{
			ID: iaRefreshSidecarCurrentID, Kind: stmt.Read,
			SQL:    `SELECT path,size,modified_ns,digest FROM subtitle_sidecars WHERE id=?`,
			Params: []stmt.Param{t("id")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"subtitle_sidecars"}},
		}),
		iaRefreshWalkDefinition(stmt.Definition{
			ID: iaRefreshSidecarPathID, Kind: stmt.Read,
			SQL:    `SELECT 1 FROM subtitle_sidecars INDEXED BY sqlite_autoindex_subtitle_sidecars_2 WHERE path=? AND id<>? LIMIT 1`,
			Params: []stmt.Param{t("path"), t("id")},
			Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_subtitle_sidecars_2"},
				AutoIndexes: []stmt.AutoIndexBinding{{Table: "subtitle_sidecars", Name: "sqlite_autoindex_subtitle_sidecars_2", Origin: "u", Columns: []stmt.IndexColumn{{Name: "path", Collation: "BINARY"}}}}},
		}),
	}
}

// iaRefreshSidecarPage pages the family6 keys this asset references, ordered by
// the native sidecar id. The cohort read is bounded by the asset's own subtitle
// rows; a pathological asset above iaRefreshSubtitleCap refuses instead of
// trimming a reference that a rollback would then miss. after must be the zero
// key or the last family6 key already captured.
func iaRefreshSidecarPage(ctx context.Context, turn *commandTurn, b iaEntryBudget, asset string, after iaRefreshKey, limit int) (page iaRefreshPage, err error) {
	defer func() { page, err = finishCommand(ctx, commandLifetime(turn), page, err) }()
	if turn == nil || turn.transaction == nil || !iaEntryText(asset) || limit <= 0 || limit > iaRefreshWalkPage {
		return iaRefreshPage{}, ErrInvalid
	}
	if after.family != 0 && (after.family != 6 || !after.valid()) {
		return iaRefreshPage{}, ErrInvalid
	}
	if err = b.check(ctx, turn, int64(iaRefreshSubtitleCap)*iaRefreshKeyAllowance); err != nil {
		return iaRefreshPage{}, err
	}
	rows, err := turn.query(ctx, turn.steps[iaRefreshSidecarCohortID].statementStep, asset)
	if err != nil {
		return iaRefreshPage{}, err
	}
	seen := make(map[string]bool)
	var ids []string
	read := 0
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return iaRefreshPage{}, errors.Join(err, rows.Close())
		}
		read++
		if !iaEntryText(id) || read > iaRefreshSubtitleCap {
			return iaRefreshPage{}, errors.Join(&Refusal{Code: RefusalCapacity}, rows.Close())
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return iaRefreshPage{}, err
	}
	sort.Strings(ids)
	start := 0
	for start < len(ids) && ids[start] <= after.token {
		start++
	}
	ids = ids[start:]
	if len(ids) > limit {
		page.more = true
		ids = ids[:limit]
	}
	page.keys = make([]iaRefreshKey, len(ids))
	for j, id := range ids {
		page.keys[j] = iaRefreshKey{family: 6, token: id}
	}
	return page, nil
}

// iaRefreshSidecarCurrentRow reads the exact current shared tuple for id. A
// caller that captured an older tuple must defer when this differs: the newest
// accepted source owns the row, and a stale restore never rewrites it.
func iaRefreshSidecarCurrentRow(ctx context.Context, turn *commandTurn, b iaEntryBudget, id string) (out iaRefreshSidecarCurrent, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if turn == nil || turn.transaction == nil || !iaEntryText(id) {
		return iaRefreshSidecarCurrent{}, ErrInvalid
	}
	if err = b.check(ctx, turn, iaRefreshKeyAllowance); err != nil {
		return iaRefreshSidecarCurrent{}, err
	}
	got, err := basicStageRead(ctx, turn, iaRefreshSidecarCurrentID, id)
	if err != nil {
		return iaRefreshSidecarCurrent{}, err
	}
	var row iaRefreshSidecarRow
	err = got.Scan(&row.Path, &row.Size, &row.ModifiedNS, &row.Digest)
	if errors.Is(err, sql.ErrNoRows) {
		return iaRefreshSidecarCurrent{}, nil
	}
	if err != nil {
		return iaRefreshSidecarCurrent{}, basicStageError(err)
	}
	row.ID = id
	return iaRefreshSidecarCurrent{row: row, found: true}, nil
}

// iaRefreshSidecarPathHeld reports whether a DIFFERENT sidecar id currently owns
// row's path. subtitle_sidecars.path is UNIQUE, so restoring an old path without
// this check would either fail late or, worse, overwrite another source's row.
func iaRefreshSidecarPathHeld(ctx context.Context, turn *commandTurn, b iaEntryBudget, row iaRefreshSidecarRow) (held bool, err error) {
	defer func() { held, err = finishCommand(ctx, commandLifetime(turn), held, err) }()
	if turn == nil || turn.transaction == nil || !iaEntryText(row.ID) || !iaEntryText(row.Path) {
		return false, ErrInvalid
	}
	if err = b.check(ctx, turn, iaRefreshKeyAllowance); err != nil {
		return false, err
	}
	got, err := basicStageRead(ctx, turn, iaRefreshSidecarPathID, row.Path, row.ID)
	if err != nil {
		return false, err
	}
	var one int64
	err = got.Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, basicStageError(err)
	}
	return true, nil
}

// iaRefreshSidecarUnlinked reports whether no subtitle row of ANY asset still
// references id. A referenced shared sidecar is never unlinked; the caller must
// restore or delete the referencing children first.
func iaRefreshSidecarUnlinked(ctx context.Context, turn *commandTurn, b iaEntryBudget, id string) (unlinked bool, err error) {
	defer func() { unlinked, err = finishCommand(ctx, commandLifetime(turn), unlinked, err) }()
	if turn == nil || turn.transaction == nil || !iaEntryText(id) {
		return false, ErrInvalid
	}
	if err = b.check(ctx, turn, iaRefreshKeyAllowance); err != nil {
		return false, err
	}
	got, err := basicStageRead(ctx, turn, iaRefreshReferences, id)
	if err != nil {
		return false, err
	}
	var one int64
	err = got.Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, basicStageError(err)
	}
	return false, nil
}

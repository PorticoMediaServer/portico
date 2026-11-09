package writer

import (
	"context"
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// IA22 owns the bounded subtitle before-image cohort (family5 fact and family7
// rows) and the native uniqueness witness that stops a restore from overwriting
// another accepted source. IA20's journal writes the exact before-image and
// IA23 composes the restore; nothing here mutates a canonical row, copies new
// state, clears a publication fence, retires a journal or starts another input.
const (
	iaRefreshSubtitleCap            = 256
	iaRefreshWalkPage               = 64
	iaRefreshKeyAllowance           = 160
	iaRefreshSubtitlePageID stmt.ID = "ia.refresh.subtitles.page"
	iaRefreshSubtitleHeldID stmt.ID = "ia.refresh.subtitles.held"
)

// iaRefreshPage is one bounded keyset page and whether its cohort continues.
// The successor is the last returned key, never an OFFSET or a digest.
type iaRefreshPage struct {
	keys []iaRefreshKey
	more bool
}

// iaRefreshWalkDefinition gives an IA22 cohort/witness statement the same
// neutral inert cohort/owner/call-site shape IA20 gives its own row statements.
// These are private reads, not Registered command SQL or Capture receipts.
func iaRefreshWalkDefinition(d stmt.Definition) stmt.Definition {
	var bindings []any
	for _, p := range d.Params {
		switch p.Type {
		case stmt.Integer:
			bindings = append(bindings, int64(1))
		case stmt.Real:
			bindings = append(bindings, float64(1))
		case stmt.Text:
			bindings = append(bindings, "absent")
		case stmt.Blob:
			bindings = append(bindings, make([]byte, 32))
		}
	}
	d.Cohorts = []stmt.Cohort{{Name: "absent", Bindings: bindings}}
	d.Owner = "store/writer IA refresh subtitle and sidecar families"
	d.CallSites = []string{"store/writer/ia_refresh_subtitles.go", "store/writer/ia_refresh_sidecars.go"}
	return d
}

// iaRefreshSubtitleSidecarDefinitions is the IA22 registry slice. IA23 composes
// it with IA20's core and IA21's pair walk on one turn; it stays out of
// iaRefreshDefinitions so no builder edits another builder's registry function.
func iaRefreshSubtitleSidecarDefinitions() []stmt.Definition {
	defs := append([]stmt.Definition{}, iaRefreshSubtitleDefinitions()...)
	return append(defs, iaRefreshSidecarDefinitions()...)
}

func iaRefreshSubtitleDefinitions() []stmt.Definition {
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	return []stmt.Definition{
		iaRefreshWalkDefinition(stmt.Definition{
			ID: iaRefreshSubtitlePageID, Kind: stmt.Read,
			SQL:    `SELECT id FROM asset_subtitles INDEXED BY asset_subtitles_asset WHERE asset_id=? AND id>? ORDER BY id LIMIT ?`,
			Params: []stmt.Param{t("asset"), t("after"), i("limit")},
			Plan:   stmt.Plan{Hot: true, Indexes: []string{"asset_subtitles_asset"}},
		}),
		iaRefreshWalkDefinition(stmt.Definition{
			ID: iaRefreshSubtitleHeldID, Kind: stmt.Read,
			SQL:    `SELECT 1 FROM asset_subtitles INDEXED BY asset_subtitles_asset WHERE asset_id=? AND origin=? AND locator=? AND id<>? LIMIT 1`,
			Params: []stmt.Param{t("asset"), t("origin"), t("locator"), t("id")},
			Plan:   stmt.Plan{Hot: true, Indexes: []string{"asset_subtitles_asset"}},
		}),
	}
}

// The family5 fact has exactly one key per asset, so it needs no SQL. A walker
// takes it before any family7 key: a parent is captured before its children.
func iaRefreshSubtitleFactKey(asset string) (iaRefreshKey, error) {
	k := iaRefreshKey{family: 5, token: asset}
	if !k.valid() {
		return iaRefreshKey{}, ErrInvalid
	}
	return k, nil
}

// iaRefreshSubtitlePage reads one raw SQL keyset page of family7 keys for one
// asset, ordered by the native subtitle id through asset_subtitles_asset. It
// never scans for another asset and never sorts in SQLite. after must be the
// zero key (start) or the last family7 key already captured.
func iaRefreshSubtitlePage(ctx context.Context, turn *commandTurn, b iaEntryBudget, asset string, after iaRefreshKey, limit int) (page iaRefreshPage, err error) {
	defer func() { page, err = finishCommand(ctx, commandLifetime(turn), page, err) }()
	if turn == nil || turn.transaction == nil || !iaEntryText(asset) || limit <= 0 || limit > iaRefreshWalkPage {
		return iaRefreshPage{}, ErrInvalid
	}
	if after.family != 0 && (after.family != 7 || !after.valid()) {
		return iaRefreshPage{}, ErrInvalid
	}
	if err = b.check(ctx, turn, int64(limit+1)*iaRefreshKeyAllowance); err != nil {
		return iaRefreshPage{}, err
	}
	rows, err := turn.query(ctx, turn.steps[iaRefreshSubtitlePageID].statementStep, asset, after.token, int64(limit+1))
	if err != nil {
		return iaRefreshPage{}, err
	}
	keys := make([]iaRefreshKey, 0, limit+1)
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return iaRefreshPage{}, errors.Join(err, rows.Close())
		}
		k := iaRefreshKey{family: 7, token: id}
		if !k.valid() {
			return iaRefreshPage{}, errors.Join(ErrInvalid, rows.Close())
		}
		keys = append(keys, k)
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return iaRefreshPage{}, err
	}
	if len(keys) > limit {
		page.more = true
		keys = keys[:limit]
	}
	page.keys = keys
	return page, nil
}

// iaRefreshSubtitleUniqueHeld reports whether a DIFFERENT accepted subtitle row
// currently holds row's exact (asset_id,origin,locator) native tuple. A restore
// or copy of row must defer on true: a later accepted source's tuple is never
// overwritten just because the journal still remembers the older id.
func iaRefreshSubtitleUniqueHeld(ctx context.Context, turn *commandTurn, b iaEntryBudget, row iaRefreshSubtitleRow) (held bool, err error) {
	defer func() { held, err = finishCommand(ctx, commandLifetime(turn), held, err) }()
	if turn == nil || turn.transaction == nil || !iaEntryText(row.ID) || !iaEntryText(row.AssetID) ||
		!iaEntryText(row.Origin) || !iaEntryText(row.Locator) || (row.Origin != "embedded" && row.Origin != "sidecar") {
		return false, ErrInvalid
	}
	if err = b.check(ctx, turn, iaRefreshKeyAllowance); err != nil {
		return false, err
	}
	got, err := basicStageRead(ctx, turn, iaRefreshSubtitleHeldID, row.AssetID, row.Origin, row.Locator, row.ID)
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

package writer

import (
	"context"
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// The stream before-image family pair is asset_stream_facts (parent,1) and
// asset_streams (child,2). A walk only READS: it names the next key of exactly
// one IA20 Step, in dependency order, from the journal cursor that IA20's
// advance statement already persisted. No function here mutates a row, clears
// the publication fence or retires a journal; IA23 alone finishes a family.
//
// Dependency order is the native foreign key, not a preference:
//   - a child row can only exist while its parent row exists, so the side that
//     KEEPS the parent (install for a write walk, restore for a rollback walk)
//     goes first;
//   - a parent delete cascades, and IA20 refuses it while children exist, so
//     the side that DROPS the parent walks its children first.
//
// The live child keys and the caller's desired child keys are merged as two
// sorted keysets. A live key with no desired row is an explicit absence
// tombstone; a desired key with no live row reads back as absent and is
// captured the same way. A repeated key is never re-captured because the
// journal retains the first before-image (IA20 `hasOld`).
//
// Every read is a keyset successor (`> cursor ORDER BY key LIMIT 1`) or a
// primary-key image lookup, so one turn never scans or buffers a family.
//
// ONE journal cursor is shared by every pair of a refresh, so it normally names
// a key of a DIFFERENT pair when this pair starts (forward: streams 1/2 then
// chapters 3/4; restore: the reverse). A cursor outside this pair therefore
// means "this pair has not started", never a refusal: forward begins as at the
// Begin position and restore begins from this pair's own journalled parent or
// highest child. A cursor inside the pair still resumes at exactly its key.
//
// The iaRefreshWalkKeys refusal bounds the caller's desired child rows. Live
// child keys are never truncated either: each is named and journalled one
// bounded keyset step per turn (tombstone or first old image), so the live side
// is proportional to the native per-asset keyset, not capped here.
const iaRefreshWalkKeys = 4096

const (
	iaRefreshStreamCursor      stmt.ID = "ia.refresh.streams.cursor"
	iaRefreshStreamFirst       stmt.ID = "ia.refresh.streams.first"
	iaRefreshStreamSuccessor   stmt.ID = "ia.refresh.streams.successor"
	iaRefreshStreamParentImage stmt.ID = "ia.refresh.streams.parent_image"
	iaRefreshStreamChildStart  stmt.ID = "ia.refresh.streams.child_start"
	iaRefreshStreamChildImage  stmt.ID = "ia.refresh.streams.child_image"
)

var iaRefreshStreamFamily = iaRefreshWalkFamily{
	parent: 1, child: 2,
	cursor: iaRefreshStreamCursor, first: iaRefreshStreamFirst, successor: iaRefreshStreamSuccessor,
	parentImage: iaRefreshStreamParentImage, childImage: iaRefreshStreamChildImage, childImageStart: iaRefreshStreamChildStart,
}

type iaRefreshWalkFamily struct {
	parent, child            uint8
	cursor, first, successor stmt.ID
	parentImage, childImage  stmt.ID
	childImageStart          stmt.ID
}

type iaRefreshWalkDesired struct {
	fact iaRefreshRecord
	rows []iaRefreshRecord
}

type iaRefreshWalkStep struct {
	key      iaRefreshKey
	row      iaRefreshRecord
	revision int64
	more     bool
}

// IA23/IA24 call these on the SAME command turn that submits the named Step,
// so the cursor and the Step share one snapshot. They never mutate.
func iaRefreshWalkFamilyValid(f iaRefreshWalkFamily) bool {
	return f.parent%2 == 1 && f.child == f.parent+1 && f.child <= 7 &&
		f.cursor != "" && f.first != "" && f.successor != "" &&
		f.parentImage != "" && f.childImage != "" && f.childImageStart != ""
}

// iaRefreshWalkPosition is the ONE persisted walk position re-read for this
// family pair. foreign reports a cursor that names a key of a DIFFERENT pair:
// this pair has not started yet, so its forward walk begins as at the Begin
// position and its restore begins at this pair's own parent or highest child.
type iaRefreshWalkPosition struct {
	state    string
	revision int64
	family   uint8
	key      iaRefreshKey
	foreign  bool
}

// iaRefreshWalkCursor reads the ONE persisted walk position. It also proves the
// journal still belongs to this exact asset/epoch/input, so a misbound request
// cannot walk another asset's family. after_family=0/” is the Begin position.
func iaRefreshWalkCursor(ctx context.Context, turn *commandTurn, f iaRefreshWalkFamily, q iaRefreshRequest) (pos iaRefreshWalkPosition, err error) {
	defer func() { pos, err = finishCommand(ctx, commandLifetime(turn), pos, err) }()
	if turn == nil || turn.transaction == nil || !iaRefreshWalkFamilyValid(f) || q.refreshID <= 0 ||
		q.scope.assetID <= 0 || q.scope.epoch <= 0 || q.scope.inputRevision <= 0 || !iaEntryText(q.scope.token) {
		return iaRefreshWalkPosition{}, invalid("refresh_walk_cursor_scope")
	}
	row, err := basicStageRead(ctx, turn, f.cursor, q.refreshID, q.reference.Identity.Incarnation[:])
	if err != nil {
		return iaRefreshWalkPosition{}, err
	}
	var asset, epoch, input, familyAt int64
	var token, cursor string
	if err = row.Scan(&pos.state, &pos.revision, &asset, &token, &epoch, &input, &familyAt, &cursor); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return iaRefreshWalkPosition{}, ErrResolveRequired
		}
		return iaRefreshWalkPosition{}, basicStageError(err)
	}
	if pos.revision <= 0 || asset != q.scope.assetID || token != q.scope.token || epoch != q.scope.epoch || input != q.scope.inputRevision || familyAt < 0 || familyAt > int64(len(iaRefreshShapes)-1) {
		return iaRefreshWalkPosition{}, ErrResolveRequired
	}
	if familyAt == 0 {
		if cursor != "" {
			return iaRefreshWalkPosition{}, ErrResolveRequired
		}
		return pos, nil
	}
	k, x := iaRefreshDecodeCursor(uint8(familyAt), cursor)
	if x != nil || (k.family == f.parent && k.index != 0) {
		return iaRefreshWalkPosition{}, ErrResolveRequired
	}
	pos.family, pos.key = k.family, k
	if k.family != f.parent && k.family != f.child {
		// Another pair owns the cursor: this pair has not started. Its token is
		// that pair's own key (a sidecar or subtitle id for families 6/7), so it
		// is not compared to this asset token.
		pos.foreign = true
		return pos, nil
	}
	if k.token != q.scope.token {
		return iaRefreshWalkPosition{}, ErrResolveRequired
	}
	return pos, nil
}

// iaRefreshWalkLive returns the smallest live child key after `below` (or the
// smallest overall for a fresh position). Bounded to one native keyset step.
func iaRefreshWalkLive(ctx context.Context, turn *commandTurn, f iaRefreshWalkFamily, token string, below int64, first bool) (index int64, found bool, err error) {
	defer func() {
		v, x := finishCommand(ctx, commandLifetime(turn), struct {
			Index int64
			Found bool
		}{index, found}, err)
		index, found, err = v.Index, v.Found, x
	}()
	var row commandRow
	var x error
	if first {
		row, x = basicStageRead(ctx, turn, f.first, token, int64(1))
	} else {
		row, x = basicStageRead(ctx, turn, f.successor, token, below, int64(1))
	}
	if x != nil {
		return 0, false, x
	}
	if x = row.Scan(&index); errors.Is(x, sql.ErrNoRows) {
		return 0, false, nil
	}
	if x != nil {
		return 0, false, basicStageError(x)
	}
	if !first && index <= below {
		return 0, false, ErrResolveRequired
	}
	return index, true, nil
}

// iaRefreshWalkJournalChild returns the next journalled child key below `below`
// (or the highest overall), so a restore walks only captured rows and never
// reads the mutable new projection.
func iaRefreshWalkJournalChild(ctx context.Context, turn *commandTurn, f iaRefreshWalkFamily, q iaRefreshRequest, below int64, first bool) (index int64, found bool, err error) {
	defer func() {
		v, x := finishCommand(ctx, commandLifetime(turn), struct {
			Index int64
			Found bool
		}{index, found}, err)
		index, found, err = v.Index, v.Found, x
	}()
	var row commandRow
	var x error
	if first {
		row, x = basicStageRead(ctx, turn, f.childImageStart, q.refreshID, q.scope.token, int64(1))
	} else {
		row, x = basicStageRead(ctx, turn, f.childImage, q.refreshID, q.scope.token, below, int64(1))
	}
	if x != nil {
		return 0, false, x
	}
	if x = row.Scan(&index); errors.Is(x, sql.ErrNoRows) {
		return 0, false, nil
	}
	if x != nil {
		return 0, false, basicStageError(x)
	}
	if !first && index >= below {
		return 0, false, ErrResolveRequired
	}
	return index, true, nil
}

// iaRefreshWalkParent reports the journalled parent before-image. `exists` is
// true for an explicit absence tombstone too; `old` is only true for a real old
// row. No row means the parent was never captured.
func iaRefreshWalkParent(ctx context.Context, turn *commandTurn, f iaRefreshWalkFamily, q iaRefreshRequest) (old, exists bool, err error) {
	defer func() {
		v, x := finishCommand(ctx, commandLifetime(turn), struct {
			Old    bool
			Exists bool
		}{old, exists}, err)
		old, exists, err = v.Old, v.Exists, x
	}()
	row, x := basicStageRead(ctx, turn, f.parentImage, q.refreshID, q.scope.token)
	if x != nil {
		return false, false, x
	}
	var present int64
	if x = row.Scan(&present); errors.Is(x, sql.ErrNoRows) {
		return false, false, nil
	}
	if x != nil {
		return false, false, basicStageError(x)
	}
	if present != 0 && present != 1 {
		return false, false, ErrResolveRequired
	}
	return present == 1, true, nil
}

// iaRefreshWalkDesiredCheck closes the desired projection before any key is
// named: one token, a strictly ascending unique child keyset, and no child
// without a parent row.
func iaRefreshWalkDesiredCheck(f iaRefreshWalkFamily, q iaRefreshRequest, d iaRefreshWalkDesired) error {
	token := q.scope.token
	if len(d.rows) > iaRefreshWalkKeys {
		return &Refusal{Code: RefusalCapacity}
	}
	if d.fact == nil && len(d.rows) != 0 {
		return invalid("refresh_walk_child_without_parent")
	}
	if d.fact != nil {
		n, v := d.fact.refreshRecord()
		if n != f.parent || len(v) == 0 || v[0] != token {
			return invalid("refresh_walk_parent_shape")
		}
	}
	var last int64
	for j, r := range d.rows {
		n, v := r.refreshRecord()
		if n != f.child || len(v) < 2 || v[0] != token {
			return invalid("refresh_walk_child_shape")
		}
		index, ok := v[1].(int64)
		if !ok || index < 0 || j > 0 && index <= last {
			return invalid("refresh_walk_child_order")
		}
		last = index
	}
	return nil
}

func iaRefreshWalkDesiredChild(d iaRefreshWalkDesired, f iaRefreshWalkFamily, token string, below int64, first bool) (iaRefreshKey, iaRefreshRecord, bool, error) {
	for _, r := range d.rows {
		n, v := r.refreshRecord()
		if n != f.child || len(v) < 2 {
			return iaRefreshKey{}, nil, false, invalid("refresh_walk_desired_child_shape")
		}
		index, ok := v[1].(int64)
		if !ok || index < 0 {
			return iaRefreshKey{}, nil, false, invalid("refresh_walk_desired_child_key")
		}
		if !first && index <= below {
			continue
		}
		return iaRefreshKey{family: f.child, token: token, index: index}, r, true, nil
	}
	return iaRefreshKey{}, nil, false, nil
}

// iaRefreshWalkNext names the next WRITE-walk Step key for one family pair.
// more=false means every live and desired child key was named and the parent
// was already handled.
func iaRefreshWalkNext(ctx context.Context, turn *commandTurn, f iaRefreshWalkFamily, q iaRefreshRequest, d iaRefreshWalkDesired) (out iaRefreshWalkStep, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if q.purpose != IARefreshStep || q.action != iaEntryApply || q.row != nil || q.key != (iaRefreshKey{}) {
		return iaRefreshWalkStep{}, invalid("refresh_walk_step_binding")
	}
	if err = iaRefreshWalkDesiredCheck(f, q, d); err != nil {
		return iaRefreshWalkStep{}, err
	}
	pos, err := iaRefreshWalkCursor(ctx, turn, f, q)
	if err != nil {
		return iaRefreshWalkStep{}, err
	}
	if pos.state != "copying" {
		return iaRefreshWalkStep{}, ErrConflict
	}
	revision, family, key := pos.revision, pos.family, pos.key
	if pos.foreign {
		// Another pair owns the cursor, so THIS pair has not started. The fixed
		// order is streams then chapters, so begin at the Begin position.
		family, key = 0, iaRefreshKey{}
	}
	parent := iaRefreshKey{family: f.parent, token: q.scope.token}
	switch family {
	case 0:
	case f.parent:
		// The parent is the last side when the new projection has no parent, so
		// naming it again means the walk is done. When a parent is installed
		// first, its children follow.
		if d.fact == nil {
			return iaRefreshWalkStep{revision: revision}, nil
		}
	case f.child:
	default:
		return iaRefreshWalkStep{}, ErrResolveRequired
	}
	if family == 0 && d.fact != nil {
		return iaRefreshWalkStep{key: parent, row: d.fact, revision: revision, more: true}, nil
	}
	first := family != f.child
	var below int64
	if !first {
		below = key.index
	}
	live, hasLive, x := iaRefreshWalkLive(ctx, turn, f, q.scope.token, below, first)
	if x != nil {
		return iaRefreshWalkStep{}, x
	}
	wantKey, wantRow, hasWant, x := iaRefreshWalkDesiredChild(d, f, q.scope.token, below, first)
	if x != nil {
		return iaRefreshWalkStep{}, x
	}
	switch {
	case hasLive && (!hasWant || live < wantKey.index):
		return iaRefreshWalkStep{key: iaRefreshKey{family: f.child, token: q.scope.token, index: live}, revision: revision, more: true}, nil
	case hasWant:
		return iaRefreshWalkStep{key: wantKey, row: wantRow, revision: revision, more: true}, nil
	}
	// No child key left anywhere.
	if family == f.parent || family == f.child && d.fact != nil {
		// The parent is the first side and must already be journalled; anything
		// else would silently skip the parent restore point.
		if _, exists, x := iaRefreshWalkParent(ctx, turn, f, q); x != nil {
			return iaRefreshWalkStep{}, x
		} else if !exists {
			return iaRefreshWalkStep{}, ErrResolveRequired
		}
		return iaRefreshWalkStep{revision: revision}, nil
	}
	return iaRefreshWalkStep{key: parent, revision: revision, more: true}, nil
}

// iaRefreshWalkRestore names the next ROLLBACK-walk Step key. It reads only the
// journal: children drop before a parent delete (so no cascade can escape the
// journal) and a parent that keeps its old row restores before any child row is
// reinserted (the native FK).
func iaRefreshWalkRestore(ctx context.Context, turn *commandTurn, f iaRefreshWalkFamily, q iaRefreshRequest) (out iaRefreshWalkStep, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if q.purpose != IARefreshRollback || q.action != iaEntryApply || q.row != nil || q.key != (iaRefreshKey{}) {
		return iaRefreshWalkStep{}, invalid("refresh_walk_restore_binding")
	}
	pos, err := iaRefreshWalkCursor(ctx, turn, f, q)
	if err != nil {
		return iaRefreshWalkStep{}, err
	}
	if pos.state != "copying" && pos.state != "restoring" {
		return iaRefreshWalkStep{}, ErrConflict
	}
	state, revision, family, key := pos.state, pos.revision, pos.family, pos.key
	old, exists, err := iaRefreshWalkParent(ctx, turn, f, q)
	if err != nil {
		return iaRefreshWalkStep{}, err
	}
	parent := iaRefreshKey{family: f.parent, token: q.scope.token}
	if state == "copying" || family == 0 || pos.foreign {
		// This pair's restore has not started: its parent keeps the old row, so
		// it goes first (the native FK), else children drop from the journal's
		// highest key, else the absent parent goes last (no cascade escapes).
		if old {
			return iaRefreshWalkStep{key: parent, revision: revision, more: true}, nil
		}
		if index, found, x := iaRefreshWalkJournalChild(ctx, turn, f, q, 0, true); x != nil {
			return iaRefreshWalkStep{}, x
		} else if found {
			return iaRefreshWalkStep{key: iaRefreshKey{family: f.child, token: q.scope.token, index: index}, revision: revision, more: true}, nil
		}
		if exists {
			return iaRefreshWalkStep{key: parent, revision: revision, more: true}, nil
		}
		return iaRefreshWalkStep{revision: revision}, nil
	}
	if old {
		switch family {
		case f.parent:
			if index, found, x := iaRefreshWalkJournalChild(ctx, turn, f, q, 0, true); x != nil {
				return iaRefreshWalkStep{}, x
			} else if found {
				return iaRefreshWalkStep{key: iaRefreshKey{family: f.child, token: q.scope.token, index: index}, revision: revision, more: true}, nil
			}
			return iaRefreshWalkStep{revision: revision}, nil
		case f.child:
			if index, found, x := iaRefreshWalkJournalChild(ctx, turn, f, q, key.index, false); x != nil {
				return iaRefreshWalkStep{}, x
			} else if found {
				return iaRefreshWalkStep{key: iaRefreshKey{family: f.child, token: q.scope.token, index: index}, revision: revision, more: true}, nil
			}
			return iaRefreshWalkStep{revision: revision}, nil
		}
		return iaRefreshWalkStep{}, ErrResolveRequired
	}
	switch family {
	case f.child:
		if index, found, x := iaRefreshWalkJournalChild(ctx, turn, f, q, key.index, false); x != nil {
			return iaRefreshWalkStep{}, x
		} else if found {
			return iaRefreshWalkStep{key: iaRefreshKey{family: f.child, token: q.scope.token, index: index}, revision: revision, more: true}, nil
		}
		if exists {
			return iaRefreshWalkStep{key: parent, revision: revision, more: true}, nil
		}
		return iaRefreshWalkStep{revision: revision}, nil
	case f.parent:
		return iaRefreshWalkStep{revision: revision}, nil
	}
	return iaRefreshWalkStep{}, ErrResolveRequired
}

// A complete typed stream projection for one asset. Fact nil with no rows means
// the new source exposes no stream family at all.
type iaRefreshStreamDesired struct {
	Fact *iaRefreshStreamFactRow
	Rows []iaRefreshStreamRow
}

func iaRefreshNextStreamStep(ctx context.Context, turn *commandTurn, q iaRefreshRequest, d iaRefreshStreamDesired) (out iaRefreshWalkStep, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	var desired iaRefreshWalkDesired
	if d.Fact != nil {
		desired.fact = *d.Fact
	}
	for j := range d.Rows {
		desired.rows = append(desired.rows, d.Rows[j])
	}
	return iaRefreshWalkNext(ctx, turn, iaRefreshStreamFamily, q, desired)
}

func iaRefreshNextStreamRestore(ctx context.Context, turn *commandTurn, q iaRefreshRequest) (out iaRefreshWalkStep, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	return iaRefreshWalkRestore(ctx, turn, iaRefreshStreamFamily, q)
}

func iaRefreshStreamDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	defs := []stmt.Definition{
		{ID: iaRefreshStreamCursor, Kind: stmt.Read,
			SQL:    `SELECT state,revision,asset_id,asset_token,expected_epoch,input_revision,after_family,after_key FROM catalog_info_refresh_undo WHERE refresh_id=? AND command_incarnation=?`,
			Params: []stmt.Param{i("refresh"), b("incarnation")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_info_refresh_undo"}},
			Cohorts: []stmt.Cohort{{Name: "walk_cursor", Bindings: []any{int64(1), make([]byte, 16)}}}},
		{ID: iaRefreshStreamFirst, Kind: stmt.Read,
			SQL:    `SELECT stream_index FROM asset_streams WHERE asset_id=? ORDER BY stream_index LIMIT ?`,
			Params: []stmt.Param{t("asset"), i("limit")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_asset_streams_1"}},
			Cohorts: []stmt.Cohort{{Name: "first_live_child", Bindings: []any{"refresh-asset", int64(1)}}}},
		{ID: iaRefreshStreamSuccessor, Kind: stmt.Read,
			SQL:    `SELECT stream_index FROM asset_streams WHERE asset_id=? AND stream_index>? ORDER BY stream_index LIMIT ?`,
			Params: []stmt.Param{t("asset"), i("after"), i("limit")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_asset_streams_1"}},
			Cohorts: []stmt.Cohort{{Name: "next_live_child", Bindings: []any{"refresh-asset", int64(0), int64(1)}}}},
		{ID: iaRefreshStreamParentImage, Kind: stmt.Read,
			SQL:    `SELECT old_exists FROM catalog_info_undo_asset_stream_facts WHERE refresh_id=? AND asset_id=?`,
			Params: []stmt.Param{i("refresh"), t("asset")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_info_undo_asset_stream_facts"}},
			Cohorts: []stmt.Cohort{{Name: "parent_before_image", Bindings: []any{int64(1), "refresh-asset"}}}},
		{ID: iaRefreshStreamChildStart, Kind: stmt.Read,
			SQL:    `SELECT stream_index FROM catalog_info_undo_asset_streams WHERE refresh_id=? AND asset_id=? ORDER BY stream_index DESC LIMIT ?`,
			Params: []stmt.Param{i("refresh"), t("asset"), i("limit")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_info_undo_asset_streams"}},
			Cohorts: []stmt.Cohort{{Name: "highest_child_image", Bindings: []any{int64(1), "refresh-asset", int64(1)}}}},
		{ID: iaRefreshStreamChildImage, Kind: stmt.Read,
			SQL:    `SELECT stream_index FROM catalog_info_undo_asset_streams WHERE refresh_id=? AND asset_id=? AND stream_index<? ORDER BY stream_index DESC LIMIT ?`,
			Params: []stmt.Param{i("refresh"), t("asset"), i("before"), i("limit")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_info_undo_asset_streams"}},
			Cohorts: []stmt.Cohort{{Name: "next_child_image", Bindings: []any{int64(1), "refresh-asset", int64(9), int64(1)}}}},
	}
	for j := range defs {
		defs[j].Owner = "store/writer IA refresh streams"
		defs[j].CallSites = []string{"store/writer/ia_refresh_streams.go"}
	}
	return defs
}

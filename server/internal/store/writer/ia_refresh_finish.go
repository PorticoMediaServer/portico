package writer

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strconv"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// IA23 is the ONLY place that ends a refresh: it either accepts the complete new
// epoch, proves the complete old projection was restored, or (after the caller
// ACKs the terminal result) retires the acknowledged journal in bounded turns.
// Nothing here starts another input, splices a retry into an old walk, or writes
// a canonical media row; IA20/21/22 remain the only capture/mutation owners.
//
// The recovery walk composes the four family registries into one closed proof:
//   - families 1/2 (streams) and 3/4 (chapters) come from IA21's pair walk;
//   - families 5/7 (subtitles) and 6 (shared sidecars) come from IA22.
//
// It never trusts the caller's claim that a cohort is complete. Every journalled
// key is read back on this turn: an accept requires the canonical row to equal
// the journal's last write (so a later publication is never relabelled), and a
// restore requires both the canonical row AND the journal watermark to equal the
// captured before-image (so the fence is cleared only after a complete restore).
const (
	iaRefreshFinishPageLimit    = 64
	iaRefreshReclaimRowsPerTurn = 64

	iaRefreshFinishOwner  stmt.ID = "ia.refresh.finish.owner"
	iaRefreshFinishFence  stmt.ID = "ia.refresh.finish.fence"
	iaRefreshFinishActive stmt.ID = "ia.refresh.finish.active"
)

// The pair families page by the native child index because one journal is scoped
// to exactly one asset token. The id families page by the native row id.
func iaRefreshFinishPageID(f uint8) stmt.ID {
	switch f {
	case 2, 4, 6, 7:
		return stmt.ID("ia.refresh.finish.page" + strconv.Itoa(int(f)))
	}
	return ""
}

// iaRefreshFinishFacts is the current permanent owner/watermark plus the exact
// nullable reclaim fixed slot. The slot group is all-or-nothing: a partially
// written slot is a corrupt journal, never an absent one.
type iaRefreshFinishFacts struct {
	state                          string
	revision, assetID, epoch       int64
	inputRevision                  int64
	token, key                     string
	family, rows, bytes            int64
	last, ack                      int64
	slotSequence                   sql.NullInt64
	slotFamily                     sql.NullInt64
	slotKey                        sql.NullString
	slotTerminal                   sql.NullInt64
	slotRemovedRows, slotRemovedBy sql.NullInt64
}

func (f iaRefreshFinishFacts) slotPresent() bool { return f.slotSequence.Valid }

func readIARefreshFinishFacts(ctx context.Context, turn *commandTurn, refreshID int64, incarnation []byte) (out iaRefreshFinishFacts, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if refreshID <= 0 || len(incarnation) != 16 {
		return out, ErrInvalid
	}
	row, err := basicStageRead(ctx, turn, iaRefreshFinishOwner, refreshID, incarnation)
	if err != nil {
		return out, err
	}
	if err = row.Scan(&out.state, &out.revision, &out.assetID, &out.token, &out.epoch, &out.inputRevision,
		&out.family, &out.key, &out.rows, &out.bytes, &out.last, &out.ack,
		&out.slotSequence, &out.slotFamily, &out.slotKey, &out.slotTerminal, &out.slotRemovedRows, &out.slotRemovedBy); err != nil {
		return out, basicStageError(err)
	}
	if out.state != "copying" && out.state != "restoring" && out.state != "accepted" && out.state != "restored" && out.state != "retired" ||
		out.revision <= 0 || out.revision == math.MaxInt64 || out.assetID <= 0 || out.epoch <= 0 || out.inputRevision <= 0 ||
		!iaEntryText(out.token) || out.family < 0 || out.family > 7 || len(out.key) > 273 ||
		out.rows < 0 || out.bytes < 0 || out.last < 0 || out.ack < 0 || out.ack > out.last || out.last-out.ack > 1 {
		return out, ErrResolveRequired
	}
	if out.slotPresent() {
		if !out.slotFamily.Valid || !out.slotKey.Valid || !out.slotTerminal.Valid || !out.slotRemovedRows.Valid || !out.slotRemovedBy.Valid ||
			out.slotSequence.Int64 <= 0 || out.slotSequence.Int64 > out.last ||
			out.slotFamily.Int64 < 0 || out.slotFamily.Int64 > 7 || len(out.slotKey.String) > 273 ||
			(out.slotTerminal.Int64 != 0 && out.slotTerminal.Int64 != 1) ||
			out.slotRemovedRows.Int64 < 0 || out.slotRemovedBy.Int64 < 0 {
			return out, ErrResolveRequired
		}
	} else if out.slotFamily != (sql.NullInt64{}) || out.slotKey != (sql.NullString{}) ||
		out.slotTerminal != (sql.NullInt64{}) || out.slotRemovedRows != (sql.NullInt64{}) || out.slotRemovedBy != (sql.NullInt64{}) {
		return out, ErrResolveRequired
	}
	return out, nil
}

// The finish turn is a same-turn component of command 127. It is never called
// after an ACK, by another command, or to retry a source: a retry allocates a
// new refresh input instead.
func runIARefreshFinish(ctx context.Context, turn *commandTurn, q iaRefreshRequest, p *usOwnerAuthorityProof, lifetime context.Context, b iaEntryBudget) (out iaRefreshOutcome, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	entry, e := enterIARefresh(ctx, turn, q, p, lifetime, b)
	if e != nil {
		return out, e
	}
	if entry.decision == receiptRetired {
		return iaRefreshOutcome{decision: receiptRetired}, nil
	}
	if entry.decision != receiptFresh {
		return iaRefreshOutcome{entry.decision, entry.saved, entry.digest}, nil
	}
	switch entry.state {
	case "copying":
		return iaRefreshFinishTerminal(ctx, turn, entry, "accepted", false, b)
	case "restoring":
		return iaRefreshFinishTerminal(ctx, turn, entry, "restored", true, b)
	case "accepted", "restored":
		return iaRefreshReclaimTurn(ctx, turn, entry, b)
	}
	// A fresh turn on a retired journal would reopen a completed owner; only the
	// fixed-slot replay path may read it.
	return out, ErrConflict
}

// iaRefreshFinishTerminal either publishes the complete new epoch or closes a
// complete restore. The fence CAS is exact: only this asset/token/library/epoch
// while still pending, and only against the revisions the authority check read.
func iaRefreshFinishTerminal(ctx context.Context, turn *commandTurn, e *iaRefreshEntry, state string, restore bool, b iaEntryBudget) (out iaRefreshOutcome, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if e == nil || e.request.purpose != IARefreshFinalize || (state != "accepted" && state != "restored") {
		return out, ErrInvalid
	}
	id := e.request.reference.Identity
	facts, err := readIARefreshFinishFacts(ctx, turn, e.refreshID, id.Incarnation[:])
	if err != nil {
		return out, err
	}
	s := e.request.scope
	if facts.state != e.state || facts.assetID != s.assetID || facts.token != s.token || facts.epoch != s.epoch ||
		facts.inputRevision != s.inputRevision || facts.revision != e.revision || facts.last != e.owner.lastSequence ||
		facts.ack != e.owner.acknowledgedSequence || facts.rows != e.rows || facts.bytes != e.bytes {
		return out, ErrConflict
	}
	// Prove the whole cohort from the journal and the canonical tables BEFORE the
	// fence moves. A half-copied projection, a half-restored projection or a later
	// publisher all fail closed here.
	if _, err = iaRefreshFinishVerify(ctx, turn, e, restore, b); err != nil {
		return out, err
	}
	now, err := e.checkIARefreshCurrent(ctx, turn)
	if err != nil {
		return out, err
	}
	next := e.assetRevisions[2]
	if next <= 0 || next == math.MaxInt64 {
		return out, ErrConflict
	}
	next++
	row, err := basicStageRead(ctx, turn, iaRefreshFinishFence, next, s.assetID, s.token, s.libraryID, s.epoch, e.assetRevisions[0], e.assetRevisions[1], e.assetRevisions[2])
	if err != nil {
		return out, err
	}
	var got int64
	if err = row.Scan(&got); err != nil {
		return out, basicStageError(err)
	}
	if got != next {
		return out, ErrConflict
	}
	result := iaRefreshResult{refreshID: e.refreshID, revision: e.revision + 1, appliedMS: now, state: state,
		family: facts.family, key: facts.key, rows: e.rows + 1, bytes: e.bytes}
	receiptBytes := iaRefreshReceiptCharge(iaRefreshKind(e.request.purpose), result.state, result.key)
	if result.bytes > maxCanonicalResultBytes-receiptBytes {
		return out, &Refusal{Code: RefusalCapacity}
	}
	result.bytes += receiptBytes
	if err = e.chargeIARefresh(ctx, turn, receiptBytes); err != nil {
		return out, err
	}
	if out.digest, err = iaRefreshResultDigest(e.request, result); err != nil {
		return iaRefreshOutcome{}, err
	}
	if err = iaRefreshExec(ctx, turn, iaRefreshAdvance, result.revision, result.state, id.Sequence, result.family, result.key,
		result.rows, result.bytes, e.refreshID, id.Incarnation[:], e.revision, e.originalState(), e.owner.lastSequence,
		e.owner.acknowledgedSequence, e.originalRows(), e.originalBytes()); err != nil {
		return iaRefreshOutcome{}, err
	}
	if err = iaRefreshExec(ctx, turn, iaRefreshReceipt, e.refreshID, id.Incarnation[:], id.Sequence, id.OperationID[:],
		e.request.reference.RequestDigest[:], iaRefreshKind(e.request.purpose), result.revision, now, result.state,
		result.family, result.key, result.rows, result.bytes, out.digest[:]); err != nil {
		return iaRefreshOutcome{}, err
	}
	if err = e.authority.recheck(ctx, turn); err != nil {
		return iaRefreshOutcome{}, err
	}
	out.result = result
	out.decision = receiptFresh
	return out, nil
}

func iaRefreshReceiptCharge(kind, state, key string) int64 {
	return int64(160 + len(kind) + len(state) + len(key))
}

// iaRefreshFinishVerify composes all four family registries into one bounded
// proof. The journal is the only source of keys: an absent family has no key and
// is not evidence of an empty projection.
func iaRefreshFinishVerify(ctx context.Context, turn *commandTurn, e *iaRefreshEntry, restore bool, b iaEntryBudget) (checked int64, err error) {
	defer func() {
		v, x := finishCommand(ctx, commandLifetime(turn), checked, err)
		checked, err = v, x
	}()
	if e == nil || e.request.purpose != IARefreshFinalize {
		return 0, ErrInvalid
	}
	token := e.request.scope.token
	for _, f := range []uint8{1, 3, 5} {
		if err = iaRefreshFinishVerifyKey(ctx, turn, e, iaRefreshKey{family: f, token: token}, restore, b); err != nil {
			return checked, err
		}
		checked++
	}
	for _, f := range []uint8{2, 4} {
		below := int64(-1)
		for {
			keys, err := iaRefreshFinishPairPage(ctx, turn, e, f, below, iaRefreshFinishPageLimit, b)
			if err != nil {
				return checked, err
			}
			for _, k := range keys {
				if err = iaRefreshFinishVerifyKey(ctx, turn, e, k, restore, b); err != nil {
					return checked, err
				}
				checked++
				below = k.index
			}
			if len(keys) < iaRefreshFinishPageLimit {
				break
			}
		}
	}
	for _, f := range []uint8{6, 7} {
		after := ""
		for {
			keys, err := iaRefreshFinishIDPage(ctx, turn, e, f, after, iaRefreshFinishPageLimit, b)
			if err != nil {
				return checked, err
			}
			for _, k := range keys {
				if err = iaRefreshFinishVerifyKey(ctx, turn, e, k, restore, b); err != nil {
					return checked, err
				}
				checked++
				after = k.token
			}
			if len(keys) < iaRefreshFinishPageLimit {
				break
			}
		}
	}
	return checked, nil
}

func iaRefreshFinishVerifyKey(ctx context.Context, turn *commandTurn, e *iaRefreshEntry, k iaRefreshKey, restore bool, b iaEntryBudget) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if !k.valid() {
		return ErrInvalid
	}
	if err = b.check(ctx, turn, iaRefreshKeyAllowance); err != nil {
		return err
	}
	old, written, post, exists, err := iaRefreshFinishRead(ctx, turn, e, k, true)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	currentImage, _, _, _, err := iaRefreshFinishRead(ctx, turn, e, k, false)
	if err != nil {
		return err
	}
	current, _, err := iaRefreshImageDigest(k, currentImage)
	if err != nil {
		return err
	}
	oldDigest, _, err := iaRefreshImageDigest(k, old)
	if err != nil {
		return err
	}
	if restore {
		// Both the canonical row and the journal's last write must be the old
		// image. A key the caller never rolled back is not a complete restore.
		if written <= 0 || post != oldDigest || current != oldDigest {
			return ErrConflict
		}
		return nil
	}
	// Accept publishes exactly the journal's last write for every key. A later
	// accepted source's row (different digest) is never relabelled as this epoch.
	if post != current {
		return ErrConflict
	}
	return nil
}

// iaRefreshFinishRead is the keyed form of IA20's private charged row reader.
// IA20's reader is bound to one Step key in the request; IA23 names every key in
// the journal cohort itself, so it reads the identical typed image and same-row
// octet charge without trusting a caller-supplied key or digest.
func iaRefreshFinishRead(ctx context.Context, turn *commandTurn, e *iaRefreshEntry, k iaRefreshKey, old bool) (image iaRefreshImage, written int64, digest [32]byte, exists bool, err error) {
	defer func() {
		v, x := finishCommand(ctx, commandLifetime(turn), struct {
			Image   iaRefreshImage
			Written int64
			Digest  [32]byte
			Exists  bool
		}{image, written, digest, exists}, err)
		image, written, digest, exists, err = v.Image, v.Written, v.Digest, v.Exists, x
	}()
	if turn == nil || !k.valid() {
		return image, written, digest, exists, ErrInvalid
	}
	args := k.args()
	op, bytesOp := "current", "bytes"
	if old {
		op, bytesOp = "old", "old_bytes"
		args = append([]any{e.refreshID}, args...)
	}
	if err = e.authority.budget.check(ctx, turn, iaRefreshRowCap); err != nil {
		return
	}
	row, x := basicStageRead(ctx, turn, iaRefreshID(k.family, bytesOp), args...)
	if x != nil {
		err = x
		return
	}
	var bytes int64
	var lengths []sql.NullInt64
	for _, c := range iaRefreshShapes[k.family].columns {
		if c.kind == "TEXT" {
			lengths = append(lengths, sql.NullInt64{})
		}
	}
	var lengthDest []any
	for j := range lengths {
		lengthDest = append(lengthDest, &lengths[j])
	}
	x = row.Scan(lengthDest...)
	for _, n := range lengths {
		if n.Valid {
			if n.Int64 < 0 || n.Int64 > iaRefreshRowCap-bytes {
				err = &Refusal{Code: RefusalCapacity}
				return
			}
			bytes += n.Int64
		}
	}
	if errors.Is(x, sql.ErrNoRows) {
		image = iaRefreshAbsent(k)
		return
	}
	if x != nil {
		err = x
		return
	}
	if bytes < 0 || bytes > iaRefreshRowCap {
		err = &Refusal{Code: RefusalCapacity}
		return
	}
	row, x = basicStageRead(ctx, turn, iaRefreshID(k.family, op), args...)
	if x != nil {
		err = x
		return
	}
	image.values = make([]any, len(iaRefreshShapes[k.family].columns))
	var dest []any
	var present int64
	var saved []byte
	if old {
		dest = []any{&present, &written, &saved}
	}
	for j := range image.values {
		dest = append(dest, &image.values[j])
	}
	if x = row.Scan(dest...); x != nil {
		err = basicStageError(x)
		return
	}
	exists, image.present = true, true
	if old {
		if present != 0 && present != 1 || written <= 0 || written > e.revision || len(saved) != 32 {
			err = ErrResolveRequired
			return
		}
		image.present = present == 1
		copy(digest[:], saved)
	}
	var actual int64
	for _, v := range image.values {
		if s, ok := v.(string); ok {
			actual += int64(len(s))
		}
	}
	if actual != bytes {
		err = ErrResolveRequired
		return
	}
	_, _, err = iaRefreshImageDigest(k, image)
	return
}

// iaRefreshFinishPairPage reads one bounded keyset page of child indexes for the
// journal's own asset. below<0 is the start position; indexes are never negative.
func iaRefreshFinishPairPage(ctx context.Context, turn *commandTurn, e *iaRefreshEntry, f uint8, below int64, limit int, b iaEntryBudget) (keys []iaRefreshKey, err error) {
	defer func() { keys, err = finishCommand(ctx, commandLifetime(turn), keys, err) }()
	id := iaRefreshFinishPageID(f)
	if e == nil || e.request.purpose != IARefreshFinalize || id == "" || below < -1 || limit <= 0 || limit > iaRefreshFinishPageLimit || !iaEntryText(e.request.scope.token) {
		return nil, ErrInvalid
	}
	if err = b.check(ctx, turn, int64(limit+1)*iaRefreshKeyAllowance); err != nil {
		return nil, err
	}
	rows, err := turn.query(ctx, turn.steps[id].statementStep, e.refreshID, e.request.scope.token, below, int64(limit))
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var index int64
		if err = rows.Scan(&index); err != nil {
			return nil, errors.Join(err, rows.Close())
		}
		if index < 0 {
			return nil, errors.Join(ErrResolveRequired, rows.Close())
		}
		keys = append(keys, iaRefreshKey{family: f, token: e.request.scope.token, index: index})
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	return keys, nil
}

func iaRefreshFinishIDPage(ctx context.Context, turn *commandTurn, e *iaRefreshEntry, f uint8, after string, limit int, b iaEntryBudget) (keys []iaRefreshKey, err error) {
	defer func() { keys, err = finishCommand(ctx, commandLifetime(turn), keys, err) }()
	id := iaRefreshFinishPageID(f)
	if e == nil || e.request.purpose != IARefreshFinalize || id == "" || limit <= 0 || limit > iaRefreshFinishPageLimit {
		return nil, ErrInvalid
	}
	if err = b.check(ctx, turn, int64(limit+1)*iaRefreshKeyAllowance); err != nil {
		return nil, err
	}
	rows, err := turn.query(ctx, turn.steps[id].statementStep, e.refreshID, after, int64(limit))
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var token string
		if err = rows.Scan(&token); err != nil {
			return nil, errors.Join(err, rows.Close())
		}
		k := iaRefreshKey{family: f, token: token}
		if !k.valid() {
			return nil, errors.Join(ErrResolveRequired, rows.Close())
		}
		keys = append(keys, k)
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	return keys, nil
}

// iaRefreshFinishDefinitions composes the four family registries IA23 inherits
// into one closed set for the composed caller. Duplicate IDs keep their first
// definition, so IA24 can register this list without editing another owner's
// registry function.
func iaRefreshFinishDefinitions() []stmt.Definition {
	return iaRefreshComposeDefinitions(iaRefreshDefinitions(), iaRefreshStreamDefinitions(),
		iaRefreshChapterDefinitions(), iaRefreshSubtitleSidecarDefinitions(), iaRefreshFinishOwnDefinitions())
}

func iaRefreshComposeDefinitions(lists ...[]stmt.Definition) []stmt.Definition {
	seen := make(map[stmt.ID]bool)
	var defs []stmt.Definition
	for _, list := range lists {
		for _, d := range list {
			if seen[d.ID] {
				continue
			}
			seen[d.ID] = true
			defs = append(defs, d)
		}
	}
	return defs
}

func iaRefreshFinishOwnDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	defs := []stmt.Definition{
		{ID: iaRefreshFinishOwner, Kind: stmt.Read,
			SQL:     `SELECT state,revision,asset_id,asset_token,expected_epoch,input_revision,after_family,after_key,retained_rows,retained_bytes,last_command_sequence,acknowledged_command_sequence,reclaim_command_sequence,reclaim_after_family,reclaim_after_key,reclaim_terminal,reclaim_removed_rows,reclaim_removed_bytes FROM catalog_info_refresh_undo WHERE refresh_id=? AND command_incarnation=?`,
			Params:  []stmt.Param{i("refresh"), b("incarnation")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_info_refresh_undo"}},
			Cohorts: []stmt.Cohort{{Name: "owner_watermark", Bindings: []any{int64(1), make([]byte, 16)}}}},
		{ID: iaRefreshFinishFence, Kind: stmt.Write,
			SQL:     `UPDATE catalog_assets SET info_publication_pending=0,info_invalidation_revision=? WHERE id=? AND token=? AND library_id=? AND info_epoch=? AND info_publication_pending=1 AND playback_origin_revision=? AND prepared_source_revision=? AND info_invalidation_revision=? RETURNING info_invalidation_revision`,
			Params:  []stmt.Param{i("next_invalidation"), i("asset"), t("token"), i("library"), i("epoch"), i("playback"), i("prepared"), i("invalidation")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_assets"}},
			Cohorts: []stmt.Cohort{{Name: "owned_fence", Bindings: []any{int64(2), int64(41), "refresh-asset", int64(7), int64(2), int64(1), int64(1), int64(1)}}}},
		{ID: iaRefreshFinishActive, Kind: stmt.Read,
			SQL:     `SELECT 1 FROM catalog_info_refresh_undo INDEXED BY catalog_info_refresh_active_asset WHERE asset_id=? AND state IN('copying','restoring') AND refresh_id<>? LIMIT 1`,
			Params:  []stmt.Param{i("asset"), i("refresh")},
			Plan:    stmt.Plan{Hot: true, Indexes: []string{"catalog_info_refresh_active_asset"}},
			Cohorts: []stmt.Cohort{{Name: "another_active_refresh", Bindings: []any{int64(41), int64(1)}}}},
		{ID: iaRefreshFinishPageID(2), Kind: stmt.Read,
			SQL:     `SELECT stream_index FROM catalog_info_undo_asset_streams WHERE refresh_id=? AND asset_id=? AND stream_index>? ORDER BY stream_index LIMIT ?`,
			Params:  []stmt.Param{i("refresh"), t("asset"), i("after"), i("limit")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_info_undo_asset_streams"}},
			Cohorts: []stmt.Cohort{{Name: "journalled_stream_child", Bindings: []any{int64(1), "refresh-asset", int64(-1), int64(65)}}}},
		{ID: iaRefreshFinishPageID(4), Kind: stmt.Read,
			SQL:     `SELECT chapter_index FROM catalog_info_undo_asset_chapters WHERE refresh_id=? AND asset_id=? AND chapter_index>? ORDER BY chapter_index LIMIT ?`,
			Params:  []stmt.Param{i("refresh"), t("asset"), i("after"), i("limit")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_info_undo_asset_chapters"}},
			Cohorts: []stmt.Cohort{{Name: "journalled_chapter_child", Bindings: []any{int64(1), "refresh-asset", int64(-1), int64(65)}}}},
		{ID: iaRefreshFinishPageID(6), Kind: stmt.Read,
			SQL:     `SELECT id FROM catalog_info_undo_subtitle_sidecars WHERE refresh_id=? AND id>? ORDER BY id LIMIT ?`,
			Params:  []stmt.Param{i("refresh"), t("after"), i("limit")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_info_undo_subtitle_sidecars"}},
			Cohorts: []stmt.Cohort{{Name: "journalled_sidecar", Bindings: []any{int64(1), "", int64(65)}}}},
		{ID: iaRefreshFinishPageID(7), Kind: stmt.Read,
			SQL:     `SELECT id FROM catalog_info_undo_asset_subtitles WHERE refresh_id=? AND id>? ORDER BY id LIMIT ?`,
			Params:  []stmt.Param{i("refresh"), t("after"), i("limit")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_info_undo_asset_subtitles"}},
			Cohorts: []stmt.Cohort{{Name: "journalled_subtitle", Bindings: []any{int64(1), "", int64(65)}}}},
	}
	for j := range defs {
		defs[j].Owner = "store/writer IA refresh finish"
		defs[j].CallSites = []string{"store/writer/ia_refresh_finish.go", "store/writer/ia_refresh_reclaim.go"}
	}
	return append(defs, iaRefreshReclaimDefinitions()...)
}

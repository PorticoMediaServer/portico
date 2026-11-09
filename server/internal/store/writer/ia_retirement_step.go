package writer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// The pre-C removal owner retains only lease/cursor facts. This private
// participant is NOT IA08, original-result resolution or an Applied command.
// Before activation the outer C-backed retirement recipe owns classification,
// receipt/quota/event, raw victim selection, terminal proof and commit.
// Production server main uses persistence.Open's separate legacy population;
// no legacy service/SQL transaction or public caller can enter this API.
type iaRetirementUnitScope struct {
	kind, target, libraryKey, resolvedRoot                           string
	libraryID, revision, leaseOwner, leaseGeneration, leaseExpiresMS int64
	sourceIncarnation, rootIdentity                                  string
	sourceGeneration                                                 int64
	phase, entityKind, entityCursor, walkRemoved, walkKept           int64
	objectCursor                                                     string
}

func (s iaRetirementUnitScope) leaseArgs() []any {
	return []any{s.kind, s.target, s.revision, s.leaseOwner, s.leaseGeneration, s.leaseExpiresMS, s.phase, s.entityKind, s.entityCursor, s.objectCursor, s.walkRemoved, s.walkKept}
}

type iaRetirementMediaVictim struct {
	assetID                            int64
	assetToken, sessionID, executionID string
	generation, revision               int64
}
type iaRetirementUnitEntry struct {
	authority *iaEntryAuthorization
	scope     iaRetirementUnitScope
	used      bool
}

type iaRetirementUnitResult struct {
	removedRows, variableBytes, removedSessions, removedGuidePins int64
	nextOwnerRevision                                             int64
	assetHasMore                                                  bool
}

func (iaRetirementUnitEntry) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "iaRetirementUnitEntry")
}
func (iaRetirementUnitResult) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "iaRetirementUnitResult")
}
func iaRetirementUnitText(v string, n int) bool {
	return len(v) > 0 && len(v) <= n && utf8.ValidString(v)
}

func enterIARetirementUnit(ctx context.Context, turn *commandTurn, s iaRetirementUnitScope, p *usOwnerAuthorityProof, lifetime context.Context, b iaEntryBudget) (out *iaRetirementUnitEntry, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	if turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != IARetireStep || s.libraryID <= 0 || s.revision <= 0 || s.revision == math.MaxInt64 || s.leaseOwner <= 0 || s.leaseGeneration <= 0 || s.leaseExpiresMS <= 0 ||
		!iaRetirementUnitText(s.target, 128) || !iaRetirementUnitText(s.libraryKey, 128) || !iaRetirementUnitText(s.resolvedRoot, 4096) || s.phase < 0 || s.phase > 5 || s.entityKind < 0 || s.entityCursor < 0 || len(s.objectCursor) > 4096 || !utf8.ValidString(s.objectCursor) || (s.walkRemoved != 0 && s.walkRemoved != 1) || (s.walkKept != 0 && s.walkKept != 1) {
		return nil, ErrInvalid
	}
	switch s.kind {
	case "library":
		if s.target != s.libraryKey || s.sourceIncarnation != "" || s.rootIdentity != "" || s.sourceGeneration != 0 {
			return nil, ErrInvalid
		}
	case "folder":
		if !iaRetirementUnitText(s.sourceIncarnation, 128) || !iaRetirementUnitText(s.rootIdentity, 4096) || s.sourceGeneration <= 0 {
			return nil, ErrInvalid
		}
	default:
		return nil, ErrInvalid
	}
	if err = b.check(ctx, turn, 64<<10); err != nil {
		return nil, err
	}
	a, x := authorizeIAEntryOwner(ctx, turn, p, lifetime, b)
	if x != nil {
		return nil, x
	}
	out = &iaRetirementUnitEntry{authority: a, scope: s}
	if err = out.checkIARetirementUnit(ctx, turn); err != nil {
		return nil, err
	}
	return out, nil
}
func (e *iaRetirementUnitEntry) checkIARetirementUnit(ctx context.Context, turn *commandTurn) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if e == nil || e.authority == nil || e.authority.turn != turn {
		return ErrInvalid
	}
	if err = e.authority.recheck(ctx, turn); err != nil {
		return err
	}
	s := e.scope
	if err = iaRetirementPoint(ctx, turn, iaRetireUnitLease, s.leaseArgs()...); err != nil {
		return err
	}
	now := turn.transaction.writer.now()
	if err = turn.transaction.observeTime(now); err != nil {
		return err
	}
	deadline, ok := turn.transaction.ctx.Deadline()
	if !ok || !now.Before(deadline) || !now.Before(time.UnixMilli(s.leaseExpiresMS)) || deadline.After(time.UnixMilli(s.leaseExpiresMS)) {
		return ErrConflict
	}
	if s.kind == "library" {
		err = iaRetirementPoint(ctx, turn, iaRetireUnitLibrary, s.libraryID, s.libraryKey, s.resolvedRoot)
	} else {
		if err = iaRetirementPoint(ctx, turn, iaRetireUnitNamespace, s.libraryID, s.libraryKey); err != nil {
			return err
		}
		err = iaRetirementPoint(ctx, turn, iaRetireUnitSource, s.target, s.libraryKey, s.resolvedRoot, s.rootIdentity, s.sourceIncarnation, s.sourceGeneration)
	}
	if err != nil {
		return err
	}
	return e.authority.recheck(ctx, turn)
}
func iaRetirementPoint(ctx context.Context, turn *commandTurn, id stmt.ID, args ...any) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	row, err := basicStageRead(ctx, turn, id, args...)
	if err != nil {
		return err
	}
	var yes int64
	if err = row.Scan(&yes); err != nil {
		return basicStageError(err)
	}
	if yes != 1 {
		return ErrConflict
	}
	return nil
}
func (e *iaRetirementUnitEntry) checkIARetirementAsset(ctx context.Context, turn *commandTurn, v iaRetirementMediaVictim) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if v.assetID <= 0 || v.generation <= 0 || v.revision <= 0 || !iaRetirementUnitText(v.assetToken, 512) || !iaRetirementUnitText(v.sessionID, 128) || !iaRetirementUnitText(v.executionID, 512) {
		return ErrInvalid
	}
	if err = e.checkIARetirementUnit(ctx, turn); err != nil {
		return err
	}
	if err = iaRetirementPoint(ctx, turn, iaRetireUnitAsset, v.assetID, v.assetToken, e.scope.libraryID); err != nil {
		return err
	}
	if e.scope.kind == "folder" {
		if err = iaRetirementPoint(ctx, turn, iaRetireUnitSourceAsset, v.assetToken, e.scope.target, e.scope.sourceIncarnation); err != nil {
			return err
		}
	}
	row, x := basicStageRead(ctx, turn, iaRetireUnitSession, v.sessionID, v.assetToken, v.generation, v.revision, v.executionID)
	if x != nil {
		return x
	}
	var generation, revision int64
	if x = row.Scan(&generation, &revision); x != nil {
		return basicStageError(x)
	}
	if generation != v.generation || revision != v.revision {
		return ErrConflict
	}
	return e.authority.recheck(ctx, turn)
}

// One exact released occurrence OR one fully drained media session per unit.
// MP15 exclusively owns pin release/reclaim and physical exit. IA does not issue
// DELETE against any pin table, and ACK/lease never stand in for native exit.
// All outcome debt is from actual rows deleted by this SAME outer transaction;
// the outer owner must rollback the whole prefix on any error.
func retireIAMediaParentUnit(ctx context.Context, turn *commandTurn, e *iaRetirementUnitEntry, v iaRetirementMediaVictim) (out iaRetirementUnitResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if e == nil {
		return out, ErrInvalid
	}
	if err = e.checkIARetirementAsset(ctx, turn, v); err != nil {
		return out, err
	}
	if e.used {
		return out, ErrInvalid
	}
	e.used = true
	// A source/asset join does not establish a media session's original source
	// after its children have drained. The MP seam supplies no such provenance.
	if e.scope.kind == "folder" {
		return out, &Refusal{Code: RefusalUnqualified}
	}
	occurrence, x := selectMPGuidePinOccurrence(ctx, turn, v.sessionID, mpGuidePinOccurrence{})
	if x == nil {
		effect, x := reclaimMPReleasedGuidePin(ctx, turn, occurrence)
		if x != nil {
			return out, basicStageError(x)
		}
		if effect.Rows != 1 || effect.VariableBytes <= 0 || effect.VariableBytes > 8192 {
			return out, ErrResolveRequired
		}
		out.removedRows, out.removedGuidePins, out.variableBytes = effect.Rows, effect.Rows, effect.VariableBytes
		out.assetHasMore = true // This exact session still exists; do not advance past it.
	} else if !errors.Is(x, sql.ErrNoRows) {
		return out, x
	} else {
		if err = requireMPSessionParentDrained(ctx, turn, v.sessionID); err != nil {
			return out, basicStageError(err)
		}
		bytes, x := readIARetirementSessionBytes(ctx, turn, e, v)
		if x != nil {
			return out, x
		}
		if err = wc008DraftChanged(ctx, turn, iaRetireUnitSessionDelete, v.sessionID, v.assetToken, v.generation, v.revision, v.executionID); err != nil {
			return out, err
		}
		out.removedRows, out.removedSessions, out.variableBytes = 1, 1, bytes
		row, x := basicStageRead(ctx, turn, iaRetireUnitSessionRemaining, v.assetToken)
		if x != nil {
			return out, x
		}
		var yes int64
		x = row.Scan(&yes)
		if x == nil {
			if yes != 1 {
				return out, ErrConflict
			}
			out.assetHasMore = true
		} else if !errors.Is(x, sql.ErrNoRows) {
			return out, x
		}
	}
	// The actual outer owner lease/cursor and fresh denial fence are checked on
	// both sides of effects, including parent exit even when no pin child exists.
	if err = e.checkIARetirementUnit(ctx, turn); err != nil {
		return out, err
	}
	args := append([]any{e.scope.revision + 1, int64(1)}, e.scope.leaseArgs()...)
	if err = wc008DraftChanged(ctx, turn, iaRetireUnitOwnerCAS, args...); err != nil {
		return out, err
	}
	e.scope.revision++
	e.scope.walkRemoved = 1
	out.nextOwnerRevision = e.scope.revision
	if err = e.checkIARetirementUnit(ctx, turn); err != nil {
		return iaRetirementUnitResult{}, err
	}
	return out, nil
}

// IA has NO personal-parent erasure capability. Library/asset/source retirement
// preserves personal_items and their stale per-source reading resumes. The UO
// drain/pre-delete participant belongs only to an exact parent already selected
// by its owning account/profile/personal-state erasure. Never use it for merge.
// Any catalogue victim still referenced by personal_items is an explicit hold,
// even after its playback children have gone; retention is not a cascade.
func requireIACataloguePersonalReferencesAbsent(ctx context.Context, turn *commandTurn, e *iaRetirementUnitEntry, itemID int64) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if e == nil || itemID <= 0 {
		return ErrInvalid
	}
	if err = e.checkIARetirementUnit(ctx, turn); err != nil {
		return err
	}
	if err = iaRetirementPoint(ctx, turn, iaRetireUnitSubject, itemID, e.scope.libraryID); err != nil {
		return err
	}
	row, x := basicStageRead(ctx, turn, iaRetireUnitPersonalHold, itemID)
	if x != nil {
		return x
	}
	var yes int64
	x = row.Scan(&yes)
	if x == nil {
		return ErrConflict
	}
	if !errors.Is(x, sql.ErrNoRows) {
		return x
	}
	return e.authority.recheck(ctx, turn)
}

func readIARetirementSessionBytes(ctx context.Context, turn *commandTurn, e *iaRetirementUnitEntry, v iaRetirementMediaVictim) (out int64, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if e == nil || e.authority == nil || e.authority.turn != turn {
		return 0, ErrInvalid
	}
	if err = e.authority.budget.check(ctx, turn, 64<<10); err != nil {
		return 0, err
	}
	row, err := basicStageRead(ctx, turn, iaRetireUnitSessionBytes, v.sessionID, v.generation, v.revision, v.executionID)
	if err != nil {
		return 0, err
	}
	lengths := make([]sql.NullInt64, len(iaRetirementSessionByteColumns))
	dest := make([]any, len(lengths))
	for j := range dest {
		dest[j] = &lengths[j]
	}
	if err = row.Scan(dest...); err != nil {
		return 0, basicStageError(err)
	}
	for _, n := range lengths {
		if n.Valid {
			if n.Int64 < 0 || n.Int64 > (64<<10)-out {
				return 0, &Refusal{Code: RefusalCapacity}
			}
			out += n.Int64
		}
	}
	if out <= 0 {
		return 0, ErrResolveRequired
	}
	return out, e.authority.recheck(ctx, turn)
}

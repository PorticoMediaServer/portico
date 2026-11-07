package writer

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func mgRuleRow(ctx context.Context, t *commandTurn, id stmt.ID, args ...any) (commandRow, error) {
	if t == nil || t.transaction == nil {
		return commandRow{}, ErrInvalid
	}
	switch id {
	case mgRuleRead, mgRuleInsert, mgRuleUpdate, mgRuleSource, mgRuleWork, mgRuleWorkUpdate, mgRuleWorkRead:
	default:
		return commandRow{}, ErrInvalid
	}
	step, ok := t.steps[id]
	if !ok {
		return commandRow{}, ErrInvalid
	}
	return t.queryRow(ctx, step.statementStep, args...)
}
func mgRuleChange(ctx context.Context, t *commandTurn, id stmt.ID, args ...any) error {
	if t == nil || t.transaction == nil {
		return ErrInvalid
	}
	switch id {
	case mgRuleInsert, mgRuleUpdate, mgRuleWork, mgRuleWorkUpdate:
	default:
		return ErrInvalid
	}
	step, ok := t.steps[id]
	if !ok {
		return ErrInvalid
	}
	r, e := t.exec(ctx, step.statementStep, args...)
	if e != nil {
		return e
	}
	n, e := r.RowsAffected()
	if e != nil {
		return e
	}
	if n != 1 {
		return ErrConflict
	}
	return t.checkLifetimes(ctx)
}
func (v mgRuleFacts) values() []any {
	return []any{v.id, v.owner, v.account, v.profile, v.source, v.series, v.title, v.channel, v.config, v.revision, v.enabled, v.deleted, v.updated}
}
func mgRuleScan(row commandRow) (v mgRuleFacts, e error) {
	e = row.Scan(&v.id, &v.owner, &v.account, &v.profile, &v.source, &v.series, &v.title, &v.channel, &v.config, &v.revision, &v.enabled, &v.deleted, &v.updated)
	return
}
func (v mgRuleFacts) valid(o mgRecordingOwner) bool {
	return o.valid() && mgRecordingHex(v.id, 64) && v.owner == o.key() && v.account == o.account && v.profile == o.profile && mgRecordingHex(v.source, 48) && mgRecordingText(v.series, false, 512) && mgRecordingText(v.title, false, 512) && v.title == strings.ToLower(strings.TrimSpace(v.title)) && (v.channel == "" || mgRecordingHex(v.channel, 64)) && (v.series != "" || v.title != "") && mgRecordingText(v.config, true, 65536) && v.revision > 0 && v.updated >= 0 && (v.enabled == 0 || v.enabled == 1) && (v.deleted == 0 || v.deleted == 1) && !(v.deleted == 1 && v.enabled != 0)
}
func saveMGRule(ctx context.Context, t *commandTurn, r mgRuleRequest) (out mgRuleFacts, err error) {
	return mutateMGRule(ctx, t, r, false)
}
func deleteMGRule(ctx context.Context, t *commandTurn, r mgRuleRequest) (out mgRuleFacts, err error) {
	return mutateMGRule(ctx, t, r, true)
}
func mutateMGRule(ctx context.Context, t *commandTurn, r mgRuleRequest, deleting bool) (out mgRuleFacts, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	a, b := r.after, r.before
	if !a.valid(r.owner) || r.sourceRevision < 1 || !mgRecordingHex(r.generation, 48) {
		return out, ErrInvalid
	}
	fresh := b == (mgRuleFacts{})
	if fresh {
		if deleting || a.revision != 1 || a.deleted != 0 {
			return out, ErrInvalid
		}
	} else {
		if !b.valid(r.owner) || b.revision == math.MaxInt64 || a.revision != b.revision+1 || a.updated < b.updated {
			return out, ErrInvalid
		}
		identity := a
		identity.config = b.config
		identity.revision = b.revision
		identity.enabled = b.enabled
		identity.deleted = b.deleted
		identity.updated = b.updated
		if identity != b {
			return out, ErrConflict
		}
		if deleting {
			if b.deleted != 0 || a.deleted != 1 || a.enabled != 0 || a.config != b.config {
				return out, ErrInvalid
			}
		} else if a.deleted != 0 {
			return out, ErrInvalid
		}
	}
	if err = mgRecordingCheckOwner(ctx, t, r.owner); err != nil {
		return out, err
	}
	row, e := mgRuleRow(ctx, t, mgRuleSource, a.source)
	if e != nil {
		return out, e
	}
	var revision, removed int64
	var generation, state string
	if e = row.Scan(&revision, &generation, &state, &removed); e != nil {
		return out, mgPersonalMissing(e)
	}
	if revision != r.sourceRevision || generation != r.generation || removed != 0 || (a.enabled == 1 && state != "active") {
		return out, ErrConflict
	}
	row, e = mgRuleRow(ctx, t, mgRuleRead, a.id)
	if e != nil {
		return out, e
	}
	actual, e := mgRuleScan(row)
	if fresh {
		if !errors.Is(e, sql.ErrNoRows) {
			if e != nil {
				return out, e
			}
			return out, ErrConflict
		}
	} else {
		if e != nil {
			return out, mgPersonalMissing(e)
		}
		if actual != b {
			return out, ErrConflict
		}
	}
	if fresh {
		err = mgRuleChange(ctx, t, mgRuleInsert, append(a.values(), "local")...)
	} else {
		args := append([]any{a.config, a.revision, a.enabled, a.deleted, a.updated}, b.values()...)
		err = mgRuleChange(ctx, t, mgRuleUpdate, args...)
	}
	if err != nil {
		return out, err
	}
	row, e = mgRuleRow(ctx, t, mgRuleWorkRead, a.id)
	if e != nil {
		return out, e
	}
	var oldRevision, oldUpdated, oldJob int64
	var oldGeneration, oldPhase, oldCursor, oldDiagnostic string
	e = row.Scan(&oldRevision, &oldGeneration, &oldPhase, &oldCursor, &oldUpdated, &oldDiagnostic, &oldJob)
	if errors.Is(e, sql.ErrNoRows) {
		err = mgRuleChange(ctx, t, mgRuleWork, a.id, a.revision, r.generation, "existing", "", a.updated, "", a.updated)
	} else if e != nil {
		return out, e
	} else {
		if oldRevision != b.revision || oldJob < 1 || oldJob == math.MaxInt64 {
			return out, ErrConflict
		}
		err = mgRuleChange(ctx, t, mgRuleWorkUpdate, a.revision, r.generation, a.updated, a.updated, a.id, b.revision, oldJob)
	}
	if err != nil {
		return out, err
	}
	row, e = mgRuleRow(ctx, t, mgRuleWorkRead, a.id)
	if e != nil {
		return out, e
	}
	var wr, updated, job int64
	var wg, phase, cursor, diagnostic string
	if e = row.Scan(&wr, &wg, &phase, &cursor, &updated, &diagnostic, &job); e != nil {
		return out, e
	}
	if wr != a.revision || wg != r.generation || phase != "existing" || cursor != "" || updated != a.updated || diagnostic != "" || job != oldJob+1 {
		return out, ErrConflict
	}
	row, e = mgRuleRow(ctx, t, mgRuleRead, a.id)
	if e != nil {
		return out, e
	}
	actual, e = mgRuleScan(row)
	if e != nil {
		return out, e
	}
	if actual != a {
		return out, ErrConflict
	}
	return actual, nil
}

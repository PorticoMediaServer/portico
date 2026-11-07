package writer

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"slices"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// LOCAL DRAFT 611/612, composed on the parent's already-authorized SAME turn.
// The parent's receipt, NORMAL admission, current admin authority, event append
// and finish/commit remain mandatory. No settings result here means Applied.
// No external IO runs in this body. A projector reads a bounded native snapshot,
// leaves the snapshot, preserves unknown TOML keys under a single external IO
// owner, performs IO, then submits this exact generation-bound result. A stale
// result never acknowledges a successor. This CAS cannot serialize file IO;
// the external projection owner must do that before physical replacement.
type uoSettingPatch struct {
	key   UOSettingKey
	value UOValue
}
type uoSettingsCommand struct {
	expectedPolicyRevision             int64
	expectedRevision, atMS, copyBudget int64
	patch                              []uoSettingPatch
}
type uoSettingsEffect struct {
	policyRevision                                      int64
	revision, projectionGeneration, copiedVariableBytes int64
	changed                                             bool
	fields                                              []UOSettingKey
}
type uoProjectionResult struct {
	field                              UOSettingKey
	pendingRevision, currentGeneration int64
	succeeded                          bool
}
type uoProjectionEffect struct {
	removed                            bool
	pendingRevision, currentGeneration int64
}

const (
	uoSettingsHead             stmt.ID = "uo.settings.head"
	uoSettingsCAS              stmt.ID = "uo.settings.cas_revision"
	uoSettingsGeneration       stmt.ID = "uo.settings.cas_generation"
	uoSettingsSource           stmt.ID = "uo.settings.source"
	uoSettingsPending          stmt.ID = "uo.settings.pending"
	uoSettingsPutSource        stmt.ID = "uo.settings.put_source"
	uoSettingsPutPending       stmt.ID = "uo.settings.put_pending"
	uoSettingsListCount        stmt.ID = "uo.settings.list_count"
	uoSettingsListOctets       stmt.ID = "uo.settings.list_octets"
	uoSettingsListValue        stmt.ID = "uo.settings.list_value"
	uoSettingsListPut          stmt.ID = "uo.settings.list_put"
	uoSettingsListDelete       stmt.ID = "uo.settings.list_delete"
	uoSettingsProjectionDelete stmt.ID = "uo.settings.accept_projection"
)

func uoSettingsCurrent(ctx context.Context, t *commandTurn, k UOSettingKey, budget int64) (v UOValue, copied int64, err error) {
	defer func() {
		out, e := finishCommand(ctx, commandLifetime(t), struct {
			v UOValue
			n int64
		}{v, copied}, err)
		v, copied, err = out.v, out.n, e
	}()
	f, ok := uoSettingSpecFor(k)
	if !ok {
		return v, 0, ErrInvalid
	}
	if f.def.Kind == UORetention {
		v = UOValue{Kind: UORetention, Retention: []UOLogRetention{}}
		for _, category := range uoLogCategories() {
			row, e := wc008DraftQuery(ctx, t, "uo.settings.log_retention_value", string(category))
			if e != nil {
				return v, 0, e
			}
			var days int64
			if e = row.Scan(&days); errors.Is(e, sql.ErrNoRows) {
				continue
			}
			if e != nil {
				return v, 0, e
			}
			v.Retention = append(v.Retention, UOLogRetention{category, days})
		}
	} else if f.def.Kind == UOList || f.def.Kind == UORetention {
		step, ok := t.steps[uoSettingsListCount]
		if !ok {
			return v, 0, ErrInvalid
		}
		rows, e := t.query(ctx, step.statementStep, string(k))
		if e != nil {
			return v, 0, e
		}
		n := int64(0)
		for rows.Next() {
			var position int64
			if e = rows.Scan(&position); e != nil {
				_ = rows.Close()
				return v, 0, e
			}
			if n >= 64 || position != n {
				_ = rows.Close()
				return v, 0, ErrResolveRequired
			}
			n++
		}
		if e = errors.Join(rows.Err(), rows.Close()); e != nil {
			return v, 0, e
		}

		v = UOValue{Kind: UOList, List: []string{}}
		for i := int64(0); i < n; i++ {
			size, e := wc008DraftInteger(ctx, t, uoSettingsListOctets, string(k), i)
			if e != nil {
				return v, 0, e
			}
			if size < 0 || size > budget-copied {
				return v, 0, ErrResolveRequired
			}
			row, e := wc008DraftQuery(ctx, t, uoSettingsListValue, string(k), i)
			if e != nil {
				return v, 0, e
			}
			var x string
			if e = row.Scan(&x); e != nil {
				return v, 0, e
			}
			if int64(len(x)) != size {
				return v, 0, ErrResolveRequired
			}
			copied += size
			v.List = append(v.List, x)
		}
	} else {
		if f.def.Kind == UOText {
			row, e := wc008DraftQuery(ctx, t, uoSettingID(k, "octets"))
			if e != nil {
				return v, 0, e
			}
			var n sql.NullInt64
			if e = row.Scan(&n); errors.Is(e, sql.ErrNoRows) && k == UOSettingName {
				return f.def, 0, t.checkLifetimes(ctx)
			} else if e != nil {
				return v, 0, e
			}
			if n.Int64 < 0 || n.Int64 > budget {
				return v, 0, ErrResolveRequired
			}
			copied = n.Int64
		}
		row, e := wc008DraftQuery(ctx, t, uoSettingID(k, "value"))
		if e != nil {
			return v, 0, e
		}
		v = UOValue{Kind: f.def.Kind}
		var absent bool
		switch f.def.Kind {
		case UOText:
			var s sql.NullString
			if e = row.Scan(&s); e != nil {
				return v, 0, e
			}
			absent = !s.Valid
			if int64(len(s.String)) != copied {
				return v, 0, ErrResolveRequired
			}
			v.Text = s.String
		case UOBool:
			var b sql.NullInt64
			if e = row.Scan(&b); e != nil {
				return v, 0, e
			}
			if b.Valid && b.Int64 != 0 && b.Int64 != 1 {
				return v, 0, ErrResolveRequired
			}
			absent = !b.Valid
			v.Bool = b.Int64 == 1
		case UOInteger:
			var n sql.NullInt64
			if e = row.Scan(&n); errors.Is(e, sql.ErrNoRows) && k == UOSettingName {
				return f.def, 0, t.checkLifetimes(ctx)
			} else if e != nil {
				return v, 0, e
			}
			absent = !n.Valid
			v.Number = float64(n.Int64)
		default:
			return v, 0, ErrInvalid
		}
		if absent {
			v = f.def
		}
	}
	canonical, e := UOValidateSetting(k, v)
	if e != nil || !uoValueEqual(canonical, v) {
		return v, 0, ErrResolveRequired
	}
	return canonical, copied, t.checkLifetimes(ctx)
}
func applyUOSettings(ctx context.Context, t *commandTurn, q uoSettingsCommand) (out uoSettingsEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if t == nil || q.expectedRevision < 1 || q.atMS < 0 || q.copyBudget <= 0 || len(q.patch) > 16 {
		return out, ErrInvalid
	}
	if err = t.checkLifetimes(ctx); err != nil {
		return out, err
	}
	patch := slices.Clone(q.patch)
	seen := map[UOSettingKey]bool{}
	for i, p := range patch {
		if seen[p.key] {
			return out, ErrInvalid
		}
		seen[p.key] = true
		patch[i].value, err = UOValidateSetting(p.key, p.value)
		if err != nil {
			return out, err
		}
	}
	row, err := wc008DraftQuery(ctx, t, uoSettingsHead)
	if err != nil {
		return out, err
	}
	if err = row.Scan(&out.revision, &out.projectionGeneration); err != nil {
		return out, err
	}
	if out.revision != q.expectedRevision {
		return out, ErrConflict
	}
	type change struct {
		patch uoSettingPatch
		old   UOValue
	}
	var changes []change
	for _, p := range patch {
		old, n, e := uoSettingsCurrent(ctx, t, p.key, q.copyBudget-out.copiedVariableBytes)
		if e != nil {
			return out, e
		}
		out.copiedVariableBytes += n
		if !uoValueEqual(old, p.value) {
			changes = append(changes, change{p, old})
		}
	}
	if seen[UOSettingAnalysisStorageGiB] || seen[UOSettingAnalysisStageStorageMiB] {
		var storage, stage float64
		for _, key := range []UOSettingKey{UOSettingAnalysisStorageGiB, UOSettingAnalysisStageStorageMiB} {
			var next UOValue
			found := false
			for _, p := range patch {
				if p.key == key {
					next = p.value
					found = true
					break
				}
			}
			if !found {
				v, n, e := uoSettingsCurrent(ctx, t, key, q.copyBudget-out.copiedVariableBytes)
				if e != nil {
					return out, e
				}
				out.copiedVariableBytes += n
				next = v
			}
			if key == UOSettingAnalysisStorageGiB {
				storage = next.Number
			} else {
				stage = next.Number
			}
		}
		if stage > storage*1024 {
			return out, ErrInvalid
		}
	}
	hasCaps, changedCaps := seen[UOSettingPerAccountCap] || seen[UOSettingServerCap], false
	if hasCaps {
		if q.expectedPolicyRevision < 1 {
			return out, ErrInvalid
		}
		out.policyRevision, err = wc008DraftInteger(ctx, t, "uo.settings.policy_revision")
		if err != nil {
			return out, err
		}
		if out.policyRevision != q.expectedPolicyRevision {
			return out, ErrConflict
		}
		for _, c := range changes {
			if c.patch.key == UOSettingPerAccountCap || c.patch.key == UOSettingServerCap {
				changedCaps = true
			}
		}
	}
	if len(changes) == 0 {
		return out, t.checkLifetimes(ctx)
	}
	if out.revision == math.MaxInt64 || out.projectionGeneration > out.revision {
		return out, ErrResolveRequired
	}
	if changedCaps {
		if out.policyRevision == math.MaxInt64 {
			return out, ErrResolveRequired
		}
		n, e := wc008DraftInteger(ctx, t, "uo.settings.policy_cas", out.policyRevision)
		if e != nil {
			return out, e
		}
		if n != out.policyRevision+1 {
			return out, ErrConflict
		}
		out.policyRevision = n
	}
	next, err := wc008DraftInteger(ctx, t, uoSettingsCAS, out.revision)
	if err != nil {
		return out, err
	}
	if next != out.revision+1 {
		return out, ErrConflict
	}
	if err = wc008DraftChanged(ctx, t, uoSettingsGeneration, next, q.atMS, out.projectionGeneration); err != nil {
		return out, err
	}
	for _, c := range changes {
		k, v := c.patch.key, c.patch.value
		if v.Kind == UORetention {
			for _, old := range c.old.Retention {
				found := false
				for _, next := range v.Retention {
					if next.Category == old.Category {
						found = true
						break
					}
				}
				if !found {
					if err = wc008DraftChanged(ctx, t, "uo.settings.log_retention_delete", string(old.Category)); err != nil {
						return out, err
					}
				}
			}
			for _, next := range v.Retention {
				unchanged := false
				for _, old := range c.old.Retention {
					if next == old {
						unchanged = true
						break
					}
				}
				if !unchanged {
					if err = wc008DraftChanged(ctx, t, "uo.settings.log_retention_put", string(next.Category), next.Days); err != nil {
						return out, err
					}
				}
			}
		} else if v.Kind == UOList {
			for i, x := range v.List {
				if i < len(c.old.List) && x == c.old.List[i] {
					continue
				}
				if err = wc008DraftChanged(ctx, t, uoSettingsListPut, string(k), int64(i), x); err != nil {
					return out, err
				}
			}
			for i := len(v.List); i < len(c.old.List); i++ {
				if err = wc008DraftChanged(ctx, t, uoSettingsListDelete, string(k), int64(i)); err != nil {
					return out, err
				}
			}
		} else {
			var native any
			switch v.Kind {
			case UOBool:
				native = v.Bool
			case UOInteger:
				native = int64(v.Number)
			case UOText:
				native = v.Text
			default:
				return out, ErrInvalid
			}
			args := []any{native, q.atMS}
			if k == UOSettingPerAccountCap || k == UOSettingServerCap {
				args = []any{native, out.policyRevision}
			}
			if k == UOSettingName {
				args = []any{native}
			}
			if err = wc008DraftChanged(ctx, t, uoSettingID(k, "update"), args...); err != nil {
				return out, err
			}
		}
		if err = wc008DraftChanged(ctx, t, uoSettingsPutSource, string(k)); err != nil {
			return out, err
		}
		if err = wc008DraftChanged(ctx, t, uoSettingsPutPending, string(k), next); err != nil {
			return out, err
		}
		out.fields = append(out.fields, k)
	}
	out.revision, out.projectionGeneration, out.changed = next, next, true
	return out, t.checkLifetimes(ctx)
}
func acceptUOConfigurationProjection(ctx context.Context, t *commandTurn, q uoProjectionResult) (out uoProjectionEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if t == nil || q.pendingRevision <= 0 || q.currentGeneration < q.pendingRevision {
		return out, ErrInvalid
	}
	if _, ok := uoSettingSpecFor(q.field); !ok {
		return out, ErrInvalid
	}
	if err = t.checkLifetimes(ctx); err != nil {
		return out, err
	}
	row, err := wc008DraftQuery(ctx, t, uoSettingsHead)
	if err != nil {
		return out, err
	}
	var revision, generation int64
	if err = row.Scan(&revision, &generation); err != nil {
		return out, err
	}
	if generation != q.currentGeneration || revision != generation {
		return out, ErrConflict
	}
	row, err = wc008DraftQuery(ctx, t, uoSettingsPending, string(q.field))
	if err != nil {
		return out, err
	}
	var pending int64
	if err = row.Scan(&pending); errors.Is(err, sql.ErrNoRows) {
		return out, ErrConflict
	}
	if err != nil {
		return out, err
	}
	if pending != q.pendingRevision {
		return out, ErrConflict
	}
	out.pendingRevision, out.currentGeneration = pending, generation
	if !q.succeeded {
		return out, t.checkLifetimes(ctx)
	} // failed IO retains durable intent
	if err = wc008DraftChanged(ctx, t, uoSettingsProjectionDelete, string(q.field), q.pendingRevision, q.currentGeneration, q.currentGeneration); err != nil {
		return out, err
	}
	out.removed = true
	return out, t.checkLifetimes(ctx)
}
func uoSettingsDefinitions() []stmt.Definition {
	out := UOSettingsPointDefinitions()
	p := func(n string, t stmt.Type) stmt.Param { return stmt.Param{Name: n, Type: t} }
	add := func(id stmt.ID, kind stmt.Kind, sql string, params []stmt.Param, table string) {
		out = append(out, stmt.Definition{ID: id, Owner: "store/writer UO ordinary settings draft", CallSites: []string{"store/writer/uo_settings.go"}, Kind: kind, SQL: sql, Params: params, Cohorts: []stmt.Cohort{{Name: "bounded_point", Bindings: uoStatementBindings(params)}}, Plan: uoSettingPointPlan(table)})
	}
	for _, f := range uoSettingSpecs {
		if f.def.Kind == UOList || f.def.Kind == UORetention {
			continue
		}
		_, _, update := uoSettingSQL(f.key)
		typ := stmt.Text
		switch f.def.Kind {
		case UOBool:
			typ = stmt.Boolean
		case UOInteger:
			typ = stmt.Integer
		}
		params := []stmt.Param{p("value", typ), p("at", stmt.Integer)}
		if f.key == UOSettingPerAccountCap || f.key == UOSettingServerCap {
			params[1] = p("revision", stmt.Integer)
		}
		if f.key == UOSettingName {
			params = params[:1]
		}
		add(uoSettingID(f.key, "update"), stmt.Write, update, params, "server_settings")
		out[len(out)-1].Plan = uoSettingValuePlan(f.key)
	}
	add("uo.settings.log_retention_put", stmt.Write, `INSERT INTO server_log_retention(category,days) VALUES(?,?) ON CONFLICT(category) DO UPDATE SET days=excluded.days`, []stmt.Param{p("category", stmt.Text), p("days", stmt.Integer)}, "server_log_retention")
	add("uo.settings.log_retention_delete", stmt.Write, `DELETE FROM server_log_retention WHERE category=?`, []stmt.Param{p("category", stmt.Text)}, "server_log_retention")
	add("uo.settings.policy_revision", stmt.Read, `SELECT revision FROM playback_owner_policy WHERE singleton=1`, nil, "playback_owner_policy")
	add("uo.settings.policy_cas", stmt.Write, `UPDATE playback_owner_policy SET revision=revision+1 WHERE singleton=1 AND revision=? RETURNING revision`, []stmt.Param{p("expected", stmt.Integer)}, "playback_owner_policy")
	add(uoSettingsHead, stmt.Read, `SELECT r.revision,s.configuration_pending_revision FROM console_settings_revision AS r JOIN server_settings AS s ON s.singleton=r.singleton WHERE r.singleton=1`, nil, "r")
	add(uoSettingsCAS, stmt.Write, `UPDATE console_settings_revision SET revision=revision+1 WHERE singleton=1 AND revision=? RETURNING revision`, []stmt.Param{p("expected", stmt.Integer)}, "console_settings_revision")
	add(uoSettingsGeneration, stmt.Write, `UPDATE server_settings SET configuration_pending_revision=?,updated_ms=? WHERE singleton=1 AND configuration_pending_revision=?`, []stmt.Param{p("generation", stmt.Integer), p("at", stmt.Integer), p("expected", stmt.Integer)}, "server_settings")
	add(uoSettingsSource, stmt.Read, `SELECT source FROM server_setting_sources WHERE field=?`, []stmt.Param{p("field", stmt.Text)}, "server_setting_sources")
	add(uoSettingsPending, stmt.Read, `SELECT revision FROM server_configuration_pending WHERE field=?`, []stmt.Param{p("field", stmt.Text)}, "server_configuration_pending")
	add(uoSettingsPutSource, stmt.Write, `INSERT INTO server_setting_sources(field,source) VALUES(?,'server') ON CONFLICT(field) DO UPDATE SET source=excluded.source`, []stmt.Param{p("field", stmt.Text)}, "server_setting_sources")
	add(uoSettingsPutPending, stmt.Write, `INSERT INTO server_configuration_pending(field,revision) VALUES(?,?) ON CONFLICT(field) DO UPDATE SET revision=excluded.revision`, []stmt.Param{p("field", stmt.Text), p("revision", stmt.Integer)}, "server_configuration_pending")
	add(uoSettingsListCount, stmt.Read, `SELECT position FROM server_setting_lists WHERE setting=? ORDER BY position LIMIT 65`, []stmt.Param{p("setting", stmt.Text)}, "server_setting_lists")
	point := []stmt.Param{p("setting", stmt.Text), p("position", stmt.Integer)}
	add(uoSettingsListOctets, stmt.Read, `SELECT octet_length(value) FROM server_setting_lists WHERE setting=? AND position=?`, point, "server_setting_lists")
	add(uoSettingsListValue, stmt.Read, `SELECT value FROM server_setting_lists WHERE setting=? AND position=?`, point, "server_setting_lists")
	add(uoSettingsListPut, stmt.Write, `INSERT INTO server_setting_lists(setting,position,value) VALUES(?,?,?) ON CONFLICT(setting,position) DO UPDATE SET value=excluded.value`, append(slices.Clone(point), p("value", stmt.Text)), "server_setting_lists")
	add(uoSettingsListDelete, stmt.Write, `DELETE FROM server_setting_lists WHERE setting=? AND position=?`, point, "server_setting_lists")
	add(uoSettingsProjectionDelete, stmt.Write, `DELETE FROM server_configuration_pending WHERE field=? AND revision=? AND EXISTS(SELECT 1 FROM server_settings WHERE singleton=1 AND configuration_pending_revision=?) AND EXISTS(SELECT 1 FROM console_settings_revision WHERE singleton=1 AND revision=?)`, []stmt.Param{p("field", stmt.Text), p("pending", stmt.Integer), p("current", stmt.Integer), p("console", stmt.Integer)}, "server_configuration_pending")
	return out
}

// Nullable ordinary resource cap. Unlimited is distinct from a positive limit.
// MP admission and US member/security policy remain independent authorities.
type uoNullableCap struct {
	limited bool
	limit   int64
}

func uoCapPatch(key UOSettingKey, cap uoNullableCap) (uoSettingPatch, error) {
	if key != UOSettingPerAccountCap && key != UOSettingServerCap || cap.limited && (cap.limit < 1 || cap.limit > 1000000) || !cap.limited && cap.limit != 0 {
		return uoSettingPatch{}, ErrInvalid
	}
	return uoSettingPatch{key, UOValue{Kind: UOInteger, Number: float64(cap.limit)}}, nil
}

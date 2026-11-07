package writer

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"slices"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// LOCAL DRAFT 610 effect component. The parent's fresh command turn has already
// classified original receipts and authorized this exact current scope. It must
// fund copyBudget from its grant, save the exact result, compose one scoped hint
// via appendWCAPIEvent when changed, and own the physical terminal/ACK path.
// These effects do not establish Applied, replay or current disclosure.
type uoPreferencePatch struct {
	key    UOPreferenceKey
	remove bool
	value  UOValue
}
type uoPreferenceCommand struct {
	address                UOPreferenceAddress
	expectedRevision, atMS int64
	copyBudget             int64               // actual variable-byte capacity funded by parent custody
	patch                  []uoPreferencePatch // <=16 distinct keys; each list <=64
}
type uoPreferenceEffect struct {
	revision, copiedVariableBytes int64
	changed                       bool
	clamped                       []UOPreferenceKey
}

const uoPreferenceWhere = `authority=? AND account_id=? AND profile_id=? AND scope=? AND device_key=?`
const (
	uoPrefRevision    stmt.ID = "uo.preferences.revision"
	uoPrefCreate      stmt.ID = "uo.preferences.create_scope"
	uoPrefCAS         stmt.ID = "uo.preferences.cas_scope"
	uoPrefLengths     stmt.ID = "uo.preferences.value_octets"
	uoPrefValue       stmt.ID = "uo.preferences.value"
	uoPrefListCount   stmt.ID = "uo.preferences.list_count"
	uoPrefListLengths stmt.ID = "uo.preferences.list_octets"
	uoPrefListValue   stmt.ID = "uo.preferences.list_value"
	uoPrefPut         stmt.ID = "uo.preferences.put_value"
	uoPrefDelete      stmt.ID = "uo.preferences.delete_value"
	uoPrefListPut     stmt.ID = "uo.preferences.put_list_item"
	uoPrefListDelete  stmt.ID = "uo.preferences.delete_list_item"
)

func uoPointArgs(a UOPreferenceAddress, k UOPreferenceKey) []any { return append(a.args(), string(k)) }

// The preflight reads actual native bytes in the SAME writer turn before the
// payload is copied. The budget is capacity, never a fabricated admission proof.
func uoPreferenceCurrent(ctx context.Context, t *commandTurn, a UOPreferenceAddress, k UOPreferenceKey, budget int64) (v UOValue, exists bool, copied int64, err error) {
	defer func() {
		var out struct {
			v      UOValue
			exists bool
			copied int64
		}
		out, err = finishCommand(ctx, commandLifetime(t), struct {
			v      UOValue
			exists bool
			copied int64
		}{v, exists, copied}, err)
		v, exists, copied = out.v, out.exists, out.copied
	}()
	args := uoPointArgs(a, k)
	row, e := wc008DraftQuery(ctx, t, uoPrefLengths, args...)
	if e != nil {
		return v, false, 0, e
	}
	var kindN int64
	var textN sql.NullInt64
	if e = row.Scan(&kindN, &textN); errors.Is(e, sql.ErrNoRows) {
		return v, false, 0, nil
	}
	if e != nil {
		return v, false, 0, e
	}
	if kindN < 0 || textN.Valid && textN.Int64 < 0 || kindN > budget || textN.Int64 > budget-kindN {
		return v, false, 0, ErrResolveRequired
	}
	copied = kindN + textN.Int64
	row, e = wc008DraftQuery(ctx, t, uoPrefValue, args...)
	if e != nil {
		return v, false, 0, e
	}
	var kind string
	var b sql.NullInt64
	var n sql.NullFloat64
	var text sql.NullString
	if e = row.Scan(&kind, &b, &n, &text); e != nil {
		return v, false, 0, e
	}
	if int64(len(kind)) != kindN || text.Valid != textN.Valid || int64(len(text.String)) != textN.Int64 {
		return v, false, 0, ErrResolveRequired
	}
	v = UOValue{Kind: UOValueKind(kind), Bool: b.Int64 == 1, Number: n.Float64, Text: text.String}
	switch v.Kind {
	case UOList, UOLanguages:
		count, e := wc008DraftInteger(ctx, t, uoPrefListCount, args...)
		if e != nil {
			return v, false, 0, e
		}
		if count < 0 || count > 64 {
			return v, false, 0, ErrResolveRequired
		}
		v.List = []string{}
		for i := int64(0); i < count; i++ {
			point := append(slices.Clone(args), i)
			size, e := wc008DraftInteger(ctx, t, uoPrefListLengths, point...)
			if e != nil {
				return v, false, 0, e
			}
			if size < 0 || size > budget-copied {
				return v, false, 0, ErrResolveRequired
			}
			r, e := wc008DraftQuery(ctx, t, uoPrefListValue, point...)
			if e != nil {
				return v, false, 0, e
			}
			var x string
			if e = r.Scan(&x); e != nil {
				return v, false, 0, e
			}
			if int64(len(x)) != size {
				return v, false, 0, ErrResolveRequired
			}
			copied += size
			v.List = append(v.List, x)
		}
	}
	canonical, clamped, e := UOValidatePreference(k, a.Scope, v)
	if e != nil || clamped || !uoValueEqual(v, canonical) {
		return v, false, 0, ErrResolveRequired
	}
	return canonical, true, copied, t.checkLifetimes(ctx)
}
func applyUOPreferences(ctx context.Context, t *commandTurn, q uoPreferenceCommand) (out uoPreferenceEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if t == nil || !q.address.Valid() || q.expectedRevision < 1 || q.atMS < 0 || q.copyBudget <= 0 || len(q.patch) > 16 {
		return out, ErrInvalid
	}
	if err = t.checkLifetimes(ctx); err != nil {
		return out, err
	}
	patch := slices.Clone(q.patch)
	seen := map[UOPreferenceKey]bool{}
	for i, p := range patch {
		if seen[p.key] || !UOPreferenceAllowed(p.key, q.address.Scope) || p.remove && !uoValueEqual(p.value, UOValue{}) {
			return out, ErrInvalid
		}
		seen[p.key] = true
		if !p.remove {
			var clamp bool
			patch[i].value, clamp, err = UOValidatePreference(p.key, q.address.Scope, p.value)
			if err != nil {
				return out, err
			}
			if clamp {
				out.clamped = append(out.clamped, p.key)
			}
		}
	}
	row, err := wc008DraftQuery(ctx, t, uoPrefRevision, q.address.args()...)
	if err != nil {
		return out, err
	}
	revision := int64(1)
	exists := true
	if err = row.Scan(&revision); errors.Is(err, sql.ErrNoRows) {
		exists = false
		revision = 1
		err = nil
	}
	if err != nil {
		return out, err
	}
	if revision != q.expectedRevision {
		return out, ErrConflict
	}
	out.revision = revision
	type change struct {
		patch  uoPreferencePatch
		old    UOValue
		exists bool
	}
	var changes []change
	for _, p := range patch {
		old, found, n, e := uoPreferenceCurrent(ctx, t, q.address, p.key, q.copyBudget-out.copiedVariableBytes)
		if e != nil {
			return out, e
		}
		out.copiedVariableBytes += n
		if p.remove && !found || !p.remove && found && uoValueEqual(p.value, old) {
			continue
		}
		changes = append(changes, change{p, old, found})
	}
	if len(changes) == 0 {
		return out, t.checkLifetimes(ctx)
	}
	if revision == math.MaxInt64 {
		return out, ErrResolveRequired
	}
	out.revision = revision + 1
	if !exists {
		err = wc008DraftChanged(ctx, t, uoPrefCreate, append(q.address.args(), out.revision, q.atMS)...)
	} else {
		err = wc008DraftChanged(ctx, t, uoPrefCAS, append([]any{out.revision, q.atMS}, append(q.address.args(), revision)...)...)
	}
	if err != nil {
		return out, err
	}
	for _, c := range changes {
		args := uoPointArgs(q.address, c.patch.key)
		if c.patch.remove {
			if err = wc008DraftChanged(ctx, t, uoPrefDelete, args...); err != nil {
				return out, err
			}
			continue
		}
		v := c.patch.value
		var b, n, s any
		switch v.Kind {
		case UOBool:
			b = v.Bool
		case UOInteger, UONumber:
			n = v.Number
		case UOText, UOLocale:
			s = v.Text
		}
		if !c.exists || v.Kind != UOList && v.Kind != UOLanguages {
			if err = wc008DraftChanged(ctx, t, uoPrefPut, append(slices.Clone(args), string(v.Kind), b, n, s)...); err != nil {
				return out, err
			}
		}
		// Delete changed old positions first: this also permits UNIQUE-backed
		// reordering (A,B -> B,A) without a transient duplicate-value failure.
		for i, x := range c.old.List {
			if i < len(v.List) && x == v.List[i] {
				continue
			}
			if err = wc008DraftChanged(ctx, t, uoPrefListDelete, append(slices.Clone(args), int64(i))...); err != nil {
				return out, err
			}
		}
		// Diff only this key's bounded positions. Unchanged entries stay untouched.
		for i, x := range v.List {
			if i < len(c.old.List) && x == c.old.List[i] {
				continue
			}
			if err = wc008DraftChanged(ctx, t, uoPrefListPut, append(slices.Clone(args), int64(i), x)...); err != nil {
				return out, err
			}
		}
	}
	out.changed = true
	return out, t.checkLifetimes(ctx)
}

// Own finite definitions, deliberately absent from the shared registry. HN must
// adopt the exact octet point shapes before production Capture can register them.
func uoPreferenceDefinitions() []stmt.Definition {
	p := func(n string, t stmt.Type) stmt.Param { return stmt.Param{Name: n, Type: t} }
	address := []stmt.Param{p("authority", stmt.Text), p("account", stmt.Text), p("profile", stmt.Text), p("scope", stmt.Text), p("device", stmt.Text)}
	key := append(slices.Clone(address), p("key", stmt.Text))
	point := append(slices.Clone(key), p("position", stmt.Integer))
	var out []stmt.Definition
	add := func(id stmt.ID, kind stmt.Kind, sql string, params []stmt.Param, table string) {
		out = append(out, stmt.Definition{ID: id, Owner: "store/writer UO preferences draft", CallSites: []string{"store/writer/uo_preferences.go"}, Kind: kind, SQL: sql, Params: params, Cohorts: []stmt.Cohort{{Name: "bounded_point", Bindings: uoStatementBindings(params)}}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{table}}})
	}
	add(uoPrefRevision, stmt.Read, `SELECT revision FROM preference_scopes WHERE `+uoPreferenceWhere, address, "preference_scopes")
	add(uoPrefCreate, stmt.Write, `INSERT INTO preference_scopes(authority,account_id,profile_id,scope,device_key,revision,updated_ms) VALUES(?,?,?,?,?,?,?) ON CONFLICT(authority,account_id,profile_id,scope,device_key) DO NOTHING`, append(slices.Clone(address), p("revision", stmt.Integer), p("at", stmt.Integer)), "preference_scopes")
	add(uoPrefCAS, stmt.Write, `UPDATE preference_scopes SET revision=?,updated_ms=? WHERE `+uoPreferenceWhere+` AND revision=?`, append([]stmt.Param{p("next", stmt.Integer), p("at", stmt.Integer)}, append(slices.Clone(address), p("expected", stmt.Integer))...), "preference_scopes")
	add(uoPrefLengths, stmt.Read, `SELECT octet_length(kind),octet_length(text_value) FROM preference_values WHERE `+uoPreferenceWhere+` AND key=?`, key, "preference_values")
	add(uoPrefValue, stmt.Read, `SELECT kind,bool_value,number_value,text_value FROM preference_values WHERE `+uoPreferenceWhere+` AND key=?`, key, "preference_values")
	add(uoPrefListCount, stmt.Read, `SELECT count(*) FROM preference_list_items WHERE `+uoPreferenceWhere+` AND key=?`, key, "preference_list_items")
	add(uoPrefListLengths, stmt.Read, `SELECT octet_length(value) FROM preference_list_items WHERE `+uoPreferenceWhere+` AND key=? AND position=?`, point, "preference_list_items")
	add(uoPrefListValue, stmt.Read, `SELECT value FROM preference_list_items WHERE `+uoPreferenceWhere+` AND key=? AND position=?`, point, "preference_list_items")
	nullable := func(n string, t stmt.Type) stmt.Param { return stmt.Param{Name: n, Type: t, Nullable: true} }
	add(uoPrefPut, stmt.Write, `INSERT INTO preference_values(authority,account_id,profile_id,scope,device_key,key,kind,bool_value,number_value,text_value) VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(authority,account_id,profile_id,scope,device_key,key) DO UPDATE SET kind=excluded.kind,bool_value=excluded.bool_value,number_value=excluded.number_value,text_value=excluded.text_value`, append(slices.Clone(key), p("kind", stmt.Text), nullable("bool", stmt.Boolean), nullable("number", stmt.Real), nullable("text", stmt.Text)), "preference_values")
	add(uoPrefDelete, stmt.Write, `DELETE FROM preference_values WHERE `+uoPreferenceWhere+` AND key=?`, key, "preference_values")
	add(uoPrefListPut, stmt.Write, `INSERT INTO preference_list_items(authority,account_id,profile_id,scope,device_key,key,position,value) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(authority,account_id,profile_id,scope,device_key,key,position) DO UPDATE SET value=excluded.value`, append(slices.Clone(point), p("value", stmt.Text)), "preference_list_items")
	add(uoPrefListDelete, stmt.Write, `DELETE FROM preference_list_items WHERE `+uoPreferenceWhere+` AND key=? AND position=?`, point, "preference_list_items")
	return out
}

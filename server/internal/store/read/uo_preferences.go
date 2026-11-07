package read

import (
	"context"
	"database/sql"
	"errors"
	"slices"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// LOCAL DRAFT lookup components only, never an endpoint or an authority issuer.
// Parent calls inside a GV-admitted snapshot, funds output through consumption,
// and repeats current principal/scope and scope revision at fresh publication.
// Database handles, rows, callbacks and borrowed bytes never leave this helper.
type uoPreferencePoint struct {
	Key      UOPreferenceKey
	Override bool
	Value    UOValue
}
type uoPreferenceDocument struct {
	Address  UOPreferenceAddress
	Revision int64
	Points   []uoPreferencePoint
}

const uoReadPrefWhere = `authority=? AND account_id=? AND profile_id=? AND scope=? AND device_key=?`

func uoReadPrefID(s string) stmt.ID { return stmt.ID("read.uo.preferences." + s) }
func uoReadPreferenceScope(ctx context.Context, s *governor.ReadScope, a UOPreferenceAddress, keys []UOPreferenceKey) (out uoPreferenceDocument, err error) {
	defer func() {
		if err != nil || ctx == nil || ctx.Err() != nil {
			out = uoPreferenceDocument{}
			if err == nil {
				err = ErrInput
				if ctx != nil {
					err = ctx.Err()
				}
			}
		}
	}()
	if ctx == nil || s == nil || !a.Valid() || len(keys) > 16 {
		return out, ErrInput
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	seen := map[UOPreferenceKey]bool{}
	for _, k := range keys {
		if seen[k] || !UOPreferenceAllowed(k, a.Scope) {
			return out, ErrUnqualified
		}
		seen[k] = true
	}
	args := []any{a.Authority, a.Account, a.Profile, string(a.Scope), a.DeviceKey}
	rows, err := s.Query(uoReadPrefID("revision"), args...)
	if err != nil {
		return out, err
	}
	out.Address, out.Revision = a, 1
	if len(rows) > 1 {
		return out, ErrUnqualified
	}
	if len(rows) == 1 {
		out.Revision = rows[0][0].(int64)
	}
	for _, k := range keys {
		keyArgs := append(slices.Clone(args), string(k))
		row, e := s.QueryExactBytes(uoReadPrefID("value_octets"), uoReadPrefID("value"), []int{0, 3}, keyArgs...)
		if errors.Is(e, sql.ErrNoRows) {
			v, _ := UOPreferenceDefault(k)
			out.Points = append(out.Points, uoPreferencePoint{k, false, v})
			continue
		}
		if e != nil {
			return out, e
		}
		v, e := uoPreferenceValue(row)
		if e != nil {
			return out, e
		}
		if v.Kind == UOList || v.Kind == UOLanguages {
			positions, e := s.Query(uoReadPrefID("list_positions"), keyArgs...)
			if e != nil {
				return out, e
			}
			if len(positions) > 64 {
				return out, ErrUnqualified
			}
			v.List = []string{}
			for i, p := range positions {
				if p[0] != int64(i) {
					return out, ErrUnqualified
				}
				point := append(slices.Clone(keyArgs), int64(i))
				r, e := s.QueryExactBytes(uoReadPrefID("list_octets"), uoReadPrefID("list_value"), []int{0}, point...)
				if e != nil {
					return out, e
				}
				v.List = append(v.List, r[0].(string))
			}
		}
		canonical, clamped, e := UOValidatePreference(k, a.Scope, v)
		if e != nil || clamped || !uoReadValuesEqual(v, canonical) {
			return out, ErrUnqualified
		}
		out.Points = append(out.Points, uoPreferencePoint{k, true, canonical})
	}
	return out, ctx.Err()
}
func uoPreferenceValue(row []any) (UOValue, error) {
	if len(row) != 4 {
		return UOValue{}, ErrUnqualified
	}
	kind, ok := row[0].(string)
	if !ok {
		return UOValue{}, ErrUnqualified
	}
	v := UOValue{Kind: UOValueKind(kind)}
	switch v.Kind {
	case UOBool:
		b, ok := row[1].(int64)
		if !ok || (b != 0 && b != 1) || row[2] != nil || row[3] != nil {
			return v, ErrUnqualified
		}
		v.Bool = b == 1
	case UOInteger, UONumber:
		if row[1] != nil || row[3] != nil {
			return v, ErrUnqualified
		}
		switch n := row[2].(type) {
		case int64:
			v.Number = float64(n)
		case float64:
			v.Number = n
		default:
			return v, ErrUnqualified
		}
	case UOText, UOLocale:
		if row[1] != nil || row[2] != nil {
			return v, ErrUnqualified
		}
		v.Text, ok = row[3].(string)
		if !ok {
			return v, ErrUnqualified
		}
	case UOList, UOLanguages:
		if row[1] != nil || row[2] != nil || row[3] != nil {
			return v, ErrUnqualified
		}
	default:
		return v, ErrUnqualified
	}
	return v, nil
}
func uoReadValuesEqual(a, b UOValue) bool {
	return a.Kind == b.Kind && a.Bool == b.Bool && a.Number == b.Number && a.Text == b.Text && slices.Equal(a.List, b.List)
}

// UOPreferenceDefinitions is inert; does not append to any shared list.
// Exact native octet preflights require HN's closed-shape adoption before Capture.
func UOPreferenceDefinitions() []stmt.Definition {
	p := func(n string, t stmt.Type) stmt.Param { return stmt.Param{Name: n, Type: t} }
	address := []stmt.Param{p("authority", stmt.Text), p("account", stmt.Text), p("profile", stmt.Text), p("scope", stmt.Text), p("device", stmt.Text)}
	key := append(slices.Clone(address), p("key", stmt.Text))
	point := append(slices.Clone(key), p("position", stmt.Integer))
	var out []stmt.Definition
	add := func(id, sql, table string, params []stmt.Param) {
		out = append(out, stmt.Definition{ID: uoReadPrefID(id), Owner: "store/read UO preference draft", CallSites: []string{"store/read/uo_preferences.go"}, Kind: stmt.Read, SQL: sql, Params: params, Cohorts: []stmt.Cohort{{Name: "bounded_point", Bindings: uoStatementBindings(params)}}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{table}}})
	}
	add("revision", `SELECT revision FROM preference_scopes WHERE `+uoReadPrefWhere, "preference_scopes", address)
	add("value_octets", `SELECT octet_length(kind),octet_length(text_value) FROM preference_values WHERE `+uoReadPrefWhere+` AND key=?`, "preference_values", key)
	add("value", `SELECT kind,bool_value,number_value,text_value FROM preference_values WHERE `+uoReadPrefWhere+` AND key=?`, "preference_values", key)
	add("list_positions", `SELECT position FROM preference_list_items WHERE `+uoReadPrefWhere+` AND key=? ORDER BY position LIMIT 65`, "preference_list_items", key)
	add("list_octets", `SELECT octet_length(value) FROM preference_list_items WHERE `+uoReadPrefWhere+` AND key=? AND position=?`, "preference_list_items", point)
	add("list_value", `SELECT value FROM preference_list_items WHERE `+uoReadPrefWhere+` AND key=? AND position=?`, "preference_list_items", point)
	return out
}

package read

import (
	"context"
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// LOCAL DRAFT: bounded ordinary-settings points and durable projection status.
// Parent supplies current administrator authority and repeats it plus exact
// revision/generation at publication; no security settings or secrets selected.
type uoSettingPoint struct {
	Key             UOSettingKey
	Value           UOValue
	Source          string
	PendingRevision int64
}
type uoSettingsDocument struct {
	Revision, ProjectionGeneration int64
	Points                         []uoSettingPoint
}

func uoReadSettingID(k UOSettingKey, suffix string) stmt.ID {
	return stmt.ID("uo.settings." + string(k) + "." + suffix)
}
func uoReadSettings(ctx context.Context, s *governor.ReadScope, keys []UOSettingKey) (out uoSettingsDocument, err error) {
	defer func() {
		if err != nil || ctx == nil || ctx.Err() != nil {
			out = uoSettingsDocument{}
			if err == nil {
				err = ErrInput
				if ctx != nil {
					err = ctx.Err()
				}
			}
		}
	}()
	if ctx == nil || s == nil || len(keys) > 16 {
		return out, ErrInput
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	seen := map[UOSettingKey]bool{}
	for _, k := range keys {
		if _, ok := UOSettingDefault(k); !ok || seen[k] {
			return out, ErrUnqualified
		}
		seen[k] = true
	}
	rows, err := s.Query("read.uo.settings.head")
	if err != nil {
		return out, err
	}
	if len(rows) != 1 {
		return out, ErrUnqualified
	}
	out.Revision, out.ProjectionGeneration = rows[0][0].(int64), rows[0][1].(int64)
	for _, k := range keys {
		v, _ := UOSettingDefault(k)
		if v.Kind == UOList {
			positions, e := s.Query("read.uo.settings.list_positions", string(k))
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
				r, e := s.QueryExactBytes("read.uo.settings.list_octets", "read.uo.settings.list_value", []int{0}, string(k), int64(i))
				if e != nil {
					return out, e
				}
				v.List = append(v.List, r[0].(string))
			}
		} else {
			var row []any
			var e error
			if v.Kind == UOText {
				row, e = s.QueryExactBytes(uoReadSettingID(k, "octets"), uoReadSettingID(k, "value"), []int{0})
			} else {
				r, x := s.Query(uoReadSettingID(k, "value"))
				e = x
				if len(r) == 1 {
					row = r[0]
				}
			}
			if errors.Is(e, sql.ErrNoRows) && k == UOSettingName {
				row = []any{nil}
				e = nil
			}
			if e != nil {
				return out, e
			}
			if len(row) != 1 {
				return out, ErrUnqualified
			}
			if row[0] != nil {
				switch v.Kind {
				case UOText:
					v.Text = row[0].(string)
				case UOInteger:
					v.Number = float64(row[0].(int64))
				case UOBool:
					n := row[0].(int64)
					if n != 0 && n != 1 {
						return out, ErrUnqualified
					}
					v.Bool = n == 1
				}
			}
		}
		canonical, e := UOValidateSetting(k, v)
		if e != nil || !uoReadValuesEqual(v, canonical) {
			return out, ErrUnqualified
		}
		point := uoSettingPoint{Key: k, Value: canonical, Source: "default"}
		row, e := s.QueryExactBytes("read.uo.settings.source_octets", "read.uo.settings.source", []int{0}, string(k))
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return out, e
		}
		if e == nil {
			point.Source = row[0].(string)
			if point.Source != "server" && point.Source != "file" {
				return out, ErrUnqualified
			}
		}
		pending, e := s.Query("read.uo.settings.pending", string(k))
		if e != nil {
			return out, e
		}
		if len(pending) > 1 {
			return out, ErrUnqualified
		}
		if len(pending) == 1 {
			point.PendingRevision = pending[0][0].(int64)
			if point.PendingRevision <= 0 || point.PendingRevision > out.ProjectionGeneration {
				return out, ErrUnqualified
			}
		}
		out.Points = append(out.Points, point)
	}
	return out, ctx.Err()
}
func UOSettingsDefinitions() []stmt.Definition {
	out := UOSettingsPointDefinitions()
	p := func(n string, t stmt.Type) stmt.Param { return stmt.Param{Name: n, Type: t} }
	add := func(id, sql string, params []stmt.Param, table string) {
		out = append(out, stmt.Definition{ID: stmt.ID("read.uo.settings." + id), Owner: "store/read UO ordinary settings draft", CallSites: []string{"store/read/uo_settings.go"}, Kind: stmt.Read, SQL: sql, Params: params, Cohorts: []stmt.Cohort{{Name: "bounded_point", Bindings: uoStatementBindings(params)}}, Plan: uoSettingPointPlan(table)})
	}
	add("head", `SELECT r.revision,s.configuration_pending_revision FROM console_settings_revision AS r JOIN server_settings AS s ON s.singleton=r.singleton WHERE r.singleton=1`, nil, "r")
	field := []stmt.Param{p("field", stmt.Text)}
	add("source_octets", `SELECT octet_length(source) FROM server_setting_sources WHERE field=?`, field, "server_setting_sources")
	add("source", `SELECT source FROM server_setting_sources WHERE field=?`, field, "server_setting_sources")
	add("pending", `SELECT revision FROM server_configuration_pending WHERE field=?`, field, "server_configuration_pending")
	add("list_positions", `SELECT position FROM server_setting_lists WHERE setting=? ORDER BY position LIMIT 65`, field, "server_setting_lists")
	point := []stmt.Param{p("setting", stmt.Text), p("position", stmt.Integer)}
	add("list_octets", `SELECT octet_length(value) FROM server_setting_lists WHERE setting=? AND position=?`, point, "server_setting_lists")
	add("list_value", `SELECT value FROM server_setting_lists WHERE setting=? AND position=?`, point, "server_setting_lists")
	return out
}

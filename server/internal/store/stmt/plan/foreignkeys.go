package plan

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

type ForeignKeyPlan struct {
	Child         string   `json:"child"`
	ID            int64    `json:"id"`
	Parent        string   `json:"parent"`
	ChildColumns  []string `json:"childColumns"`
	ParentColumns []string `json:"parentColumns"`
	Delete        Rows     `json:"delete"`
	Update        Rows     `json:"update"`
	Lookup        Rows     `json:"lookup"`
}

type ForeignKeyAudit struct {
	SchemaBinding *SchemaBinding      `json:"schemaBinding,omitempty"`
	Connection    sqliteconn.Settings `json:"connection"`
	Plans         []ForeignKeyPlan    `json:"plans"`
}

// ForeignKeys checks the actual parent DELETE/key UPDATE plans with FK ON. The
// lookup places parent columns on the left of equality, inheriting their native
// collation and affinity instead of assuming a child's BINARY index suffices.
// All SQL is introspection-derived harness SQL; no mutation is executed.
func ForeignKeys(ctx context.Context, conn *sql.Conn, policy sqliteconn.Config) (ForeignKeyAudit, error) {
	settings, err := sqliteconn.Assert(ctx, conn, policy)
	if err != nil {
		return ForeignKeyAudit{}, fmt.Errorf("plan: unqualified FK connection: %w", err)
	}
	if err := assertNoTriggers(ctx, conn); err != nil {
		return ForeignKeyAudit{}, err
	}
	tables, err := query(ctx, conn, "SELECT name FROM sqlite_schema WHERE type='table' ORDER BY name")
	if err != nil {
		return ForeignKeyAudit{}, err
	}
	result := []ForeignKeyPlan{}
	for _, table := range tables.Values {
		child := table[0].(string)
		rows, err := query(ctx, conn, "PRAGMA foreign_key_list("+identifier(child)+")")
		if err != nil {
			return ForeignKeyAudit{}, err
		}
		groups := map[int64]*ForeignKeyPlan{}
		for _, row := range rows.Values {
			id := row[0].(int64)
			g := groups[id]
			if g == nil {
				g = &ForeignKeyPlan{ID: id, Child: child, Parent: row[2].(string)}
				groups[id] = g
			}
			g.ChildColumns = append(g.ChildColumns, row[3].(string))
			if row[4] == nil {
				g.ParentColumns = append(g.ParentColumns, "")
			} else {
				g.ParentColumns = append(g.ParentColumns, row[4].(string))
			}
		}
		ids := []int64{}
		for id := range groups {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		for _, id := range ids {
			g := groups[id]
			if g.ParentColumns[0] == "" {
				info, err := query(ctx, conn, "PRAGMA table_info("+identifier(g.Parent)+")")
				if err != nil {
					return ForeignKeyAudit{}, err
				}
				keys := map[int64]string{}
				for _, row := range info.Values {
					if order := row[5].(int64); order > 0 {
						keys[order] = row[1].(string)
					}
				}
				for i := range g.ParentColumns {
					key, ok := keys[int64(i+1)]
					if !ok {
						return ForeignKeyAudit{}, fmt.Errorf("plan: unresolved parent PK %s", g.Parent)
					}
					g.ParentColumns[i] = key
				}
			}
			predicates, updates, joins := []string{}, []string{}, []string{}
			args := make([]any, len(g.ParentColumns))
			for i, col := range g.ParentColumns {
				key := identifier(col)
				predicates = append(predicates, key+"=?")
				updates = append(updates, key+"="+key)
				joins = append(joins, "p."+key+"=c."+identifier(g.ChildColumns[i]))
			}
			where := strings.Join(predicates, " AND ")
			g.Delete, err = query(ctx, conn, "EXPLAIN QUERY PLAN DELETE FROM "+identifier(g.Parent)+" WHERE "+where, args...)
			if err != nil {
				return ForeignKeyAudit{}, err
			}
			g.Update, err = query(ctx, conn, "EXPLAIN QUERY PLAN UPDATE "+identifier(g.Parent)+" SET "+strings.Join(updates, ",")+" WHERE "+where, args...)
			if err != nil {
				return ForeignKeyAudit{}, err
			}
			parentWhere := []string{}
			for _, col := range g.ParentColumns {
				parentWhere = append(parentWhere, "p."+identifier(col)+"=?")
			}
			lookup := "SELECT 1 FROM " + identifier(g.Parent) + " AS p CROSS JOIN " + identifier(g.Child) + " AS c WHERE " + strings.Join(parentWhere, " AND ") + " AND " + strings.Join(joins, " AND ")
			g.Lookup, err = query(ctx, conn, "EXPLAIN QUERY PLAN "+lookup, args...)
			if err != nil {
				return ForeignKeyAudit{}, err
			}
			for _, probe := range []Rows{g.Delete, g.Update, g.Lookup} {
				if err := CheckEQP(stmt.Definition{ID: stmt.ID(fmt.Sprintf("FK %s/%d -> %s", child, id, g.Parent)), Plan: stmt.Plan{Hot: true}}, probe); err != nil {
					return ForeignKeyAudit{}, err
				}
			}
			for _, probe := range []*Rows{&g.Delete, &g.Update, &g.Lookup} {
				canonical, err := canonicalEQP(*probe)
				if err != nil {
					return ForeignKeyAudit{}, err
				}
				*probe = canonical
			}

			result = append(result, *g)
		}
	}
	audit := ForeignKeyAudit{Connection: settings, Plans: result}
	catalog, err := query(ctx, conn, "SELECT type,name,tbl_name,rootpage,sql FROM main.sqlite_schema ORDER BY type,name")
	if err != nil {
		return ForeignKeyAudit{}, err
	}
	if checkEmbeddedCatalog(catalog) == nil {
		audit.SchemaBinding = &SchemaBinding{Version: 1, SQLSHA256: schemaSQLSHA()}
	}
	return audit, nil
}
func identifier(value string) string { return `"` + strings.ReplaceAll(value, `"`, `""`) + `"` }

package plan

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Only these compiled bounded title/fact ranges may preflight octets. This
// grants no arbitrary range, expression, identifier or SQL-function permission.
const (
	rbTermsOctetSlot       = 64
	rbTermsOctetLabelSlots = 8
	rbTermsOctetFactLimit  = 2048
	rbTermsOctetEdgeFields = `'tag','label','contentRating','studio','network'`
)

func rbTermsOctetIDParams(prefix string) []stmt.Param {
	out := make([]stmt.Param, rbTermsOctetSlot)
	for i := range out {
		out[i] = stmt.Param{Name: prefix + strconv.Itoa(i), Type: stmt.Integer}
	}
	return out
}
func rbTermsOctetTokenParams() []stmt.Param {
	out := make([]stmt.Param, rbTermsOctetSlot)
	for i := range out {
		out[i] = stmt.Param{Name: "token" + strconv.Itoa(i), Type: stmt.Text}
	}
	return out
}
func rbTermsOctetLabelParams() []stmt.Param {
	out := make([]stmt.Param, rbTermsOctetLabelSlots)
	for i := range out {
		out[i] = stmt.Param{Name: "label" + strconv.Itoa(i), Type: stmt.Text}
	}
	return out
}
func rbTermsOctetMarks(n int) string { return strings.TrimSuffix(strings.Repeat("?,", n), ",") }
func rbTermsOctetIDs() string        { return rbTermsOctetMarks(rbTermsOctetSlot) }

func init() {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }

	// Title source octets: one exact byte point per accepted article/direction
	// /part. The payload walk reuses the same native index and keyset seek.
	for _, keep := range []bool{false, true} {
		key, index, variant := "sort_key", "catalog_entities_title", "ignore."
		if keep {
			key, index, variant = "sort_key_keep_articles", "catalog_entities_title_keep_articles", "keep."
		}
		for _, desc := range []bool{false, true} {
			direction, op, order := "asc.", ">", "ASC"
			if desc {
				direction, op, order = "desc.", "<", "DESC"
			}
			for _, part := range []string{"first", "tail", "next"} {
				where := " WHERE e.library_id=? AND e.kind=?"
				seek := "library_id=? AND kind=?"
				params := []stmt.Param{i("library"), i("kind")}
				if part == "tail" {
					where += " AND e." + key + " COLLATE NOCASE=? AND e.id" + op + "?"
					seek = key + "=? AND id" + op + "?"
					params = append(params, s("key"), i("after"))
				}
				if part == "next" {
					where += " AND e." + key + " COLLATE NOCASE" + op + "?"
					seek = key + op + "?"
					params = append(params, s("key"))
				}
				params = append(params, i("limit"))
				id := stmt.ID("read.browse.rb.filter_terms.entity." + variant + direction + part + ".bytes")
				sqlText := "SELECT e.id,octet_length(e." + key + "),octet_length(e.title) FROM catalog_entities AS e INDEXED BY " + index + where + " ORDER BY e." + key + " COLLATE NOCASE " + order + ",e.id " + order + " LIMIT ?"
				proof := stmt.Plan{Hot: true, Indexes: []string{index}, Seeks: []string{seek}}
				pointOctets.add(octetShape{id: id, sql: sqlText, mismatch: "exact bounded terms title byte-source range required",
					definition: func(d stmt.Definition) bool {
						return d.ID == id && d.SQL == sqlText && d.Owner == "store/read" && d.Kind == stmt.Read && reflect.DeepEqual(d.Params, params) && reflect.DeepEqual(d.Plan, proof)
					},
					audit: func(ctx context.Context, c *sql.Conn) error { return auditRBPersonalTitleIndex(ctx, c, index, key) },
					point: rbTermsTitleOctetPoint(index),
				})
			}
		}
	}

	rbTermsRegisterOctet("edge",
		"SELECT ae.item_id,octet_length(f.field),octet_length(ae.source_value) FROM catalog_item_attribute_edges AS ae INDEXED BY catalog_item_attribute_edges_first CROSS JOIN catalog_attribute_terms AS t ON t.id=ae.term_id CROSS JOIN catalog_attribute_fields AS f ON f.id=t.field_id WHERE ae.item_id IN("+rbTermsOctetIDs()+") AND f.field IN("+rbTermsOctetEdgeFields+") ORDER BY ae.item_id,ae.source_rowid,ae.term_id LIMIT ?",
		append(rbTermsOctetIDParams("item"), i("limit")),
		func() stmt.Plan {
			p := stmt.Plan{Hot: true, Indexes: []string{"catalog_item_attribute_edges_first"}, PrimaryKeys: []string{"t", "f"}, Seeks: []string{"item_id=?"}}
			p.Waivers = []stmt.Waiver{{StatementID: "read.browse.rb.filter_terms.edge.bytes", Stage: "eqp", Form: "BLOOM FILTER", Match: "BLOOM FILTER ON f (id=?)", Reason: "Bounded entity-term/attribute join bloom filter; exact indexed SEARCH lines are separately required"}}
			return p
		}(),
		func(ctx context.Context, c *sql.Conn) error {
			return auditRBFilterTermsIndex(ctx, c, "catalog_item_attribute_edges", "catalog_item_attribute_edges_first", "", []string{"item_id", "source_rowid", "term_id"})
		},
		func(rows Rows) error {
			return rbTermsOctetSearch(rows, []string{
				"SEARCH ae USING INDEX catalog_item_attribute_edges_first (item_id=?)",
				"SEARCH t USING INTEGER PRIMARY KEY (rowid=?)",
				"SEARCH f USING INTEGER PRIMARY KEY (rowid=?)",
			})
		})

	rbTermsRegisterOctet("term",
		"SELECT s.entity_id,octet_length(COALESCE(s.label_override,t.label)) FROM catalog_term_sources AS s CROSS JOIN catalog_terms AS t ON t.id=s.term_id CROSS JOIN catalog_entities AS e ON e.id=s.entity_id WHERE s.entity_id IN("+rbTermsOctetIDs()+") AND t.vocab=1 AND e.retired=0 AND e.library_id=? ORDER BY s.entity_id,s.provider,s.source_id LIMIT ?",
		append(rbTermsOctetIDParams("entity"), i("library"), i("limit")),
		func() stmt.Plan {
			p := stmt.Plan{Hot: true, PrimaryKeys: []string{"s", "t", "e"}, Seeks: []string{"entity_id=?"}}
			p.Waivers = []stmt.Waiver{{StatementID: "read.browse.rb.filter_terms.term.bytes", Stage: "eqp", Form: "BLOOM FILTER", Match: "BLOOM FILTER ON t (id=?)", Reason: "Bounded entity-term source join bloom filter; exact indexed SEARCH lines are separately required"}}
			return p
		}(),
		func(ctx context.Context, c *sql.Conn) error {
			if err := auditRBFilterTermsIndex(ctx, c, "catalog_term_sources", "sqlite_autoindex_catalog_term_sources_1", "pk", []string{"entity_id", "provider", "source_id"}); err != nil {
				return err
			}
			return auditRBFilterTermsRowIDPoint(ctx, c, "catalog_terms")
		},
		func(rows Rows) error {
			return rbTermsOctetSearch(rows, []string{
				"SEARCH s USING PRIMARY KEY (entity_id=?)",
				"SEARCH t USING INTEGER PRIMARY KEY (rowid=?)",
				"SEARCH e USING INTEGER PRIMARY KEY (rowid=?)",
			})
		})

	creditFrom := "catalog_credits AS c INDEXED BY %s CROSS JOIN catalog_entities AS e ON e.id=c.entity_id CROSS JOIN catalog_credit_images AS h ON h.id=c.image_id AND h.entity_id=e.id AND h.entity_incarnation=e.metadata_incarnation AND h.owner_revision=e.credit_owner_revision CROSS JOIN catalog_credit_labels AS r ON r.id=c.role_id CROSS JOIN catalog_credit_labels AS d ON d.id=c.department_id"
	creditWhere := "%s AND e.credit_image_id=c.image_id AND h.sealed=1 AND e.retired=0 AND e.library_id=? AND c.entity_id IN(" + rbTermsOctetIDs() + ")"
	creditBytes := "SELECT c.entity_id,octet_length(c.credited_name),octet_length(r.label),octet_length(d.label) FROM "
	creditPersonBytes := "SELECT c.entity_id,octet_length(c.credited_name),octet_length(r.label),octet_length(d.label),octet_length(p.token) FROM "
	creditPlan := func(index string) stmt.Plan {
		return stmt.Plan{Hot: true, Indexes: []string{index}, PrimaryKeys: []string{"e", "h", "r", "d"}}
	}
	creditAudit := func(index string) func(context.Context, *sql.Conn) error {
		return func(ctx context.Context, c *sql.Conn) error {
			if err := auditRBFilterTermsIndex(ctx, c, "catalog_credits", index, "", nil); err != nil {
				return err
			}
			for _, table := range []string{"catalog_entities", "catalog_credit_images", "catalog_credit_labels"} {
				if err := auditRBFilterTermsRowIDPoint(ctx, c, table); err != nil {
					return err
				}
			}
			return nil
		}
	}
	creditPoint := func(index, seek string) func(Rows) error {
		return func(rows Rows) error {
			return rbTermsOctetSearch(rows, []string{
				"SEARCH c USING INDEX " + index + " (",
				"SEARCH e USING INTEGER PRIMARY KEY (rowid=?)",
				"SEARCH h USING INTEGER PRIMARY KEY (rowid=?)",
				"SEARCH r USING INTEGER PRIMARY KEY (rowid=?)",
				"SEARCH d USING INTEGER PRIMARY KEY (rowid=?)",
			})
		}
	}
	deptSQL := creditBytes + strings.Replace(creditFrom, "%s", "catalog_credits_department", 1) + " WHERE " + strings.Replace(creditWhere, "%s", "c.department_id IN(?,?,?)", 1) + " ORDER BY c.department_id,c.entity_id,c.ord LIMIT ?"
	deptPlan := creditPlan("catalog_credits_department")
	deptPlan.Seeks = []string{"department_id=?"}
	rbTermsRegisterOctet("credit.department", deptSQL,
		append(append([]stmt.Param{i("department0"), i("department1"), i("department2")}, i("library")), append(rbTermsOctetIDParams("entity"), i("limit"))...),
		deptPlan, creditAudit("catalog_credits_department"), creditPoint("catalog_credits_department", "department_id=?"))

	roleSQL := creditBytes + strings.Replace(creditFrom, "%s", "catalog_credits_role", 1) + " WHERE " + strings.Replace(creditWhere, "%s", "c.role_id IN(?,?,?)", 1) + " ORDER BY c.role_id,c.entity_id,c.ord LIMIT ?"
	rolePlan := creditPlan("catalog_credits_role")
	rolePlan.Seeks = []string{"role_id=?"}
	rbTermsRegisterOctet("credit.role", roleSQL,
		append(append([]stmt.Param{i("role0"), i("role1"), i("role2")}, i("library")), append(rbTermsOctetIDParams("entity"), i("limit"))...),
		rolePlan, creditAudit("catalog_credits_role"), creditPoint("catalog_credits_role", "role_id=?"))

	personFrom := "catalog_people AS p CROSS JOIN catalog_credits AS c INDEXED BY catalog_credits_person ON c.person_id=p.id CROSS JOIN catalog_entities AS e ON e.id=c.entity_id CROSS JOIN catalog_credit_images AS h ON h.id=c.image_id AND h.entity_id=e.id AND h.entity_incarnation=e.metadata_incarnation AND h.owner_revision=e.credit_owner_revision CROSS JOIN catalog_credit_labels AS r ON r.id=c.role_id CROSS JOIN catalog_credit_labels AS d ON d.id=c.department_id"
	personSQL := creditPersonBytes + personFrom + " WHERE p.token IN(" + rbTermsOctetIDs() + ") AND e.credit_image_id=c.image_id AND h.sealed=1 AND e.retired=0 AND e.library_id=? AND c.entity_id IN(" + rbTermsOctetIDs() + ") ORDER BY p.token,c.entity_id,c.ord LIMIT ?"
	personPlan := stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_catalog_people_1", "catalog_credits_person"}, PrimaryKeys: []string{"e", "h", "r", "d"}, Seeks: []string{"token=?", "person_id=?"}}
	rbTermsRegisterOctet("credit.person", personSQL,
		append(append(rbTermsOctetTokenParams(), i("library")), append(rbTermsOctetIDParams("entity"), i("limit"))...),
		personPlan,
		func(ctx context.Context, c *sql.Conn) error {
			if err := auditRBFilterTermsIndex(ctx, c, "catalog_people", "sqlite_autoindex_catalog_people_1", "u", []string{"token"}); err != nil {
				return err
			}
			if err := auditRBFilterTermsIndex(ctx, c, "catalog_credits", "catalog_credits_person", "", nil); err != nil {
				return err
			}
			for _, table := range []string{"catalog_entities", "catalog_credit_images", "catalog_credit_labels"} {
				if err := auditRBFilterTermsRowIDPoint(ctx, c, table); err != nil {
					return err
				}
			}
			return nil
		},
		func(rows Rows) error {
			return rbTermsOctetSearch(rows, []string{
				"SEARCH p USING COVERING INDEX sqlite_autoindex_catalog_people_1 (token=?)",
				"SEARCH c USING INDEX catalog_credits_person (",
				"SEARCH e USING INTEGER PRIMARY KEY (rowid=?)",
				"SEARCH h USING INTEGER PRIMARY KEY (rowid=?)",
				"SEARCH r USING INTEGER PRIMARY KEY (rowid=?)",
				"SEARCH d USING INTEGER PRIMARY KEY (rowid=?)",
			})
		})

	labelSQL := "SELECT l.id,octet_length(l.label) FROM catalog_credit_labels AS l INDEXED BY sqlite_autoindex_catalog_credit_labels_1 WHERE l.label=? LIMIT ?"
	labelPlan := stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_catalog_credit_labels_1"}, Seeks: []string{"label=?"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "catalog_credit_labels", Name: "sqlite_autoindex_catalog_credit_labels_1", Origin: "u", Columns: []stmt.IndexColumn{{Name: "label", Collation: "BINARY"}}}}}
	rbTermsRegisterOctet("label", labelSQL,
		[]stmt.Param{{Name: "label", Type: stmt.Text}, i("limit")}, labelPlan,
		func(ctx context.Context, c *sql.Conn) error {
			return auditRBFilterTermsIndex(ctx, c, "catalog_credit_labels", "sqlite_autoindex_catalog_credit_labels_1", "u", []string{"label"})
		},
		func(rows Rows) error {
			return rbTermsOctetSearch(rows, []string{"SEARCH l USING COVERING INDEX sqlite_autoindex_catalog_credit_labels_1 (label=?)"})
		})
}

func rbTermsRegisterOctet(name, sqlText string, params []stmt.Param, proof stmt.Plan, audit func(context.Context, *sql.Conn) error, point func(Rows) error) {
	id := stmt.ID("read.browse.rb.filter_terms." + name + ".bytes")
	pointOctets.add(octetShape{id: id, sql: sqlText, mismatch: "exact bounded terms fact byte-source range required",
		definition: func(d stmt.Definition) bool {
			return d.ID == id && d.SQL == sqlText && d.Owner == "store/read" && d.Kind == stmt.Read && reflect.DeepEqual(d.Params, params) && reflect.DeepEqual(d.Plan, proof)
		},
		audit: audit, point: point,
	})
}

// rbTermsOctetSearch requires the reviewed SEARCH lines, in order, and no
// additional plan rows. It never grants a scan, temp B-tree or subquery.
func rbTermsOctetSearch(rows Rows, wants []string) error {
	var details []string
	for _, row := range rows.Values {
		if len(row) != 4 {
			return fmt.Errorf("terms fact preflight invalid native EQP")
		}
		detail, ok := row[3].(string)
		if !ok {
			return fmt.Errorf("terms fact preflight invalid native EQP")
		}
		// The exact per-statement bloom-filter waiver is proven by Capture.
		if strings.HasPrefix(detail, "BLOOM FILTER ") {
			continue
		}
		details = append(details, detail)
	}
	if len(details) != len(wants) {
		return fmt.Errorf("terms fact preflight requires exactly %d SEARCHes", len(wants))
	}
	for i, detail := range details {
		if !strings.HasPrefix(detail, wants[i]) {
			return fmt.Errorf("terms fact preflight plan changed")
		}
	}
	return nil
}
func rbTermsTitleOctetPoint(index string) func(Rows) error {
	return func(rows Rows) error {
		if len(rows.Values) != 1 || len(rows.Values[0]) != 4 {
			return fmt.Errorf("terms title byte source requires one indexed SEARCH")
		}
		text, ok := rows.Values[0][3].(string)
		if !ok || !strings.HasPrefix(text, "SEARCH e USING INDEX "+index+" (") {
			return fmt.Errorf("terms title byte source requires its exact indexed SEARCH")
		}
		return nil
	}
}

// auditRBFilterTermsIndex proves a named index still has the exact key columns
// the byte point relies on. Empty columns means only the name/origin is checked.
func auditRBFilterTermsIndex(ctx context.Context, c *sql.Conn, table, index, origin string, columns []string) error {
	list, err := query(ctx, c, "PRAGMA main.index_list("+identifier(table)+")")
	if err != nil {
		return err
	}
	found := false
	for _, row := range list.Values {
		if len(row) != 5 {
			return fmt.Errorf("terms fact index metadata changed")
		}
		if row[1] == index {
			if row[4] != int64(0) {
				return fmt.Errorf("terms fact index must be non-partial")
			}
			if origin != "" && row[3] != origin {
				return fmt.Errorf("terms fact index origin changed")
			}
			found = true
		}
	}
	if !found {
		return fmt.Errorf("terms fact native index absent")
	}
	if len(columns) == 0 {
		return nil
	}
	info, err := query(ctx, c, "PRAGMA main.index_xinfo("+identifier(index)+")")
	if err != nil {
		return err
	}
	keys := 0
	for _, row := range info.Values {
		if len(row) != 6 {
			return fmt.Errorf("terms fact key metadata changed")
		}
		if row[5] != int64(1) {
			continue
		}
		if keys >= len(columns) || row[2] != columns[keys] || row[3] != int64(0) || row[4] != "BINARY" {
			return fmt.Errorf("terms fact index key changed")
		}
		keys++
	}
	if len(columns) != 0 && keys != len(columns) {
		return fmt.Errorf("terms fact index key incomplete")
	}
	return nil
}

// auditRBFilterTermsRowIDPoint proves an INTEGER PRIMARY KEY rowid point.
func auditRBFilterTermsRowIDPoint(ctx context.Context, c *sql.Conn, table string) error {
	info, err := query(ctx, c, "PRAGMA main.table_xinfo("+identifier(table)+")")
	if err != nil {
		return err
	}
	found := false
	for _, row := range info.Values {
		if len(row) != 7 {
			return fmt.Errorf("terms fact table metadata changed")
		}
		if row[1] == "id" {
			found = row[2] == "INTEGER" && row[5] == int64(1) && row[6] == int64(0)
		}
	}
	if !found {
		return fmt.Errorf("terms fact exact integer identity required")
	}
	return nil
}

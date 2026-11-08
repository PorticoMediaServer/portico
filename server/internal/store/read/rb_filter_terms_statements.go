package read

import (
	"strconv"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// RBFilterTermsDefinitions is inert compiled inventory for the closed
// genre/tag/label/contentRating/studio/network and credit filter primitive.
// Every request value is a binding: request fields cannot select SQL, an
// identifier, an operator or a source.
//
// The fact points take a fixed 64-slot item identity list so one page of raw
// title rows needs one indexed statement per family. Unused slots bind -1 (or
// the empty text token), which can never match a positive rowid or token.
const rbTermsSlot = MaxRaw

func rbTermsIDParams(prefix string) []stmt.Param {
	out := make([]stmt.Param, rbTermsSlot)
	for i := range out {
		out[i] = stmt.Param{Name: prefix + strconv.Itoa(i), Type: stmt.Integer}
	}
	return out
}
func rbTermsTokenParams() []stmt.Param {
	out := make([]stmt.Param, rbTermsSlot)
	for i := range out {
		out[i] = stmt.Param{Name: "token" + strconv.Itoa(i), Type: stmt.Text}
	}
	return out
}
func rbTermsLabelParams() []stmt.Param {
	out := make([]stmt.Param, rbTermsLabelSlots)
	for i := range out {
		out[i] = stmt.Param{Name: "label" + strconv.Itoa(i), Type: stmt.Text}
	}
	return out
}
func rbTermsMarks(n int) string { return strings.TrimSuffix(strings.Repeat("?,", n), ",") }
func rbTermsIDs() string        { return rbTermsMarks(rbTermsSlot) }

// rbTermsFactLimit bounds one fact statement's raw rows. Reaching it is
// incomplete evidence, never a sampled exact result.
const rbTermsFactLimit = 2048

const (
	rbTermsLabelSlots = 8
	rbTermsEdgeFields = `'tag','label','contentRating','studio','network'`
)

// rbTermsFactShape pairs a fact payload statement with its exact octet
// preflight. Both use the same params, source and order; only the value
// projection differs.
func rbTermsFactShape(name, columns, bytesColumns, from, where, order string, params []stmt.Param, present, empty []any, plan stmt.Plan) []stmt.Definition {
	call := "store/read/rb_filter_terms.go:BrowseReader.TermsFilterWindow"
	tail := " LIMIT ?"
	if order != "" {
		tail = " ORDER BY " + order + tail
	}
	sql := "SELECT " + columns + " FROM " + from + " WHERE " + where + tail
	bytesSQL := "SELECT " + bytesColumns + " FROM " + from + " WHERE " + where + tail
	base := readStatementID("rb.filter_terms." + name)
	return []stmt.Definition{
		{ID: base, SQL: sql, Owner: "store/read", Kind: stmt.Read, CallSites: []string{call}, Params: params, Cohorts: []stmt.Cohort{{Name: "present", Bindings: present}, {Name: "empty", Bindings: empty}}, Plan: plan},
		{ID: readStatementID("rb.filter_terms." + name + ".bytes"), SQL: bytesSQL, Owner: "store/read", Kind: stmt.Read, CallSites: []string{call}, Params: params, Cohorts: []stmt.Cohort{{Name: "present", Bindings: present}, {Name: "empty", Bindings: empty}}, Plan: plan},
	}
}

// RBFilterTermsDefinitions is the complete inert inventory. Only these exact
// statements may run; the plan Capture owns their native receipts.
func RBFilterTermsDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	var defs []stmt.Definition

	// Raw title walk: four video kinds in accepted article-aware title order.
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
			columns := "e.id,e." + key + ",e.public_id,e.title,e.retired,e.revision,e.admission_own_age,e.kind,e.show_id,e.season_id,e.credit_image_id,e.credit_owner_revision,e.metadata_incarnation"
			from := "catalog_entities AS e INDEXED BY " + index
			end := " ORDER BY e." + key + " COLLATE NOCASE " + order + ",e.id " + order + " LIMIT ?"
			base := "SELECT " + columns + " FROM " + from + " WHERE e.library_id=? AND e.kind=?"
			bytesSQL := "SELECT e.id,octet_length(e." + key + "),octet_length(e.title) FROM " + from + " WHERE e.library_id=? AND e.kind=?"
			add := func(part, where, seek string, params []stmt.Param, bindings []any) {
				empty := append([]any(nil), bindings...)
				empty[0] = int64(-1)
				call := "store/read/rb_filter_terms.go:BrowseReader.TermsFilterWindow"
				name := "rb.filter_terms.entity." + variant + direction + part
				proof := stmt.Plan{Hot: true, Indexes: []string{index}, Seeks: []string{seek}}
				defs = append(defs,
					stmt.Definition{ID: readStatementID(name), SQL: base + where + end, Owner: "store/read", Kind: stmt.Read, CallSites: []string{call}, Params: params, Cohorts: []stmt.Cohort{{Name: "present", Bindings: bindings}, {Name: "empty", Bindings: empty}}, Plan: proof},
					stmt.Definition{ID: readStatementID(name + ".bytes"), SQL: bytesSQL + where + end, Owner: "store/read", Kind: stmt.Read, CallSites: []string{call}, Params: params, Cohorts: []stmt.Cohort{{Name: "present", Bindings: bindings}, {Name: "empty", Bindings: empty}}, Plan: proof},
				)
			}
			add("first", "", "library_id=? AND kind=?", []stmt.Param{i("library"), i("kind"), i("limit")}, []any{int64(1), int64(1), int64(MaxRaw)})
			add("tail", " AND e."+key+" COLLATE NOCASE=? AND e.id"+op+"?", key+"=? AND id"+op+"?", []stmt.Param{i("library"), i("kind"), s("key"), i("after"), i("limit")}, []any{int64(1), int64(1), "same", int64(3), int64(MaxRaw)})
			add("next", " AND e."+key+" COLLATE NOCASE"+op+"?", key+op+"?", []stmt.Param{i("library"), i("kind"), s("key"), i("limit")}, []any{int64(1), int64(1), "same", int64(MaxRaw)})
		}
	}

	// Descriptive attribute edges (tag/label/contentRating/studio/network) for
	// one page of item identities, in entity-term/attribute primary-key order.
	edgeFrom := "catalog_item_attribute_edges AS ae INDEXED BY catalog_item_attribute_edges_first CROSS JOIN catalog_attribute_terms AS t ON t.id=ae.term_id CROSS JOIN catalog_attribute_fields AS f ON f.id=t.field_id"
	edgeParams := append(rbTermsIDParams("item"), i("limit"))
	edgePresent := rbTermsSlotBindings()
	edgePresent = append(edgePresent, int64(rbTermsFactLimit))
	edgeEmpty := append([]any(nil), edgePresent...)
	edgeEmpty[0] = int64(-1)
	edgePlan := stmt.Plan{Hot: true, Indexes: []string{"catalog_item_attribute_edges_first"}, PrimaryKeys: []string{"t", "f"}, Seeks: []string{"item_id=?"}}
	edgeDefs := rbTermsFactShape("edge",
		"ae.item_id,f.field,ae.source_value",
		"ae.item_id,octet_length(f.field),octet_length(ae.source_value)", edgeFrom,
		"ae.item_id IN("+rbTermsIDs()+") AND f.field IN("+rbTermsEdgeFields+")",
		"ae.item_id,ae.source_rowid,ae.term_id", edgeParams, edgePresent, edgeEmpty, edgePlan)
	// SQLite 3.53 adds a bloom filter to this bounded join; the exact SEARCH
	// lines are still proven, and the filter only accelerates the same seek.
	for n := range edgeDefs {
		edgeDefs[n].Plan.Waivers = []stmt.Waiver{{StatementID: edgeDefs[n].ID, Stage: "eqp", Form: "BLOOM FILTER", Match: "BLOOM FILTER ON f (id=?)", Reason: "Bounded entity-term/attribute join bloom filter; exact indexed SEARCH lines are separately required"}}
	}
	defs = append(defs, edgeDefs...)

	// Genre vocabulary through the entity-term source primary key.
	termFrom := "catalog_term_sources AS s CROSS JOIN catalog_terms AS t ON t.id=s.term_id CROSS JOIN catalog_entities AS e ON e.id=s.entity_id"
	termParams := append(rbTermsIDParams("entity"), i("library"), i("limit"))
	termPresent := rbTermsSlotBindings()
	termPresent = append(termPresent, int64(1), int64(rbTermsFactLimit))
	termEmpty := append([]any(nil), termPresent...)
	termEmpty[0] = int64(-1)
	termDefs := rbTermsFactShape("term",
		"s.entity_id,COALESCE(s.label_override,t.label)",
		"s.entity_id,octet_length(COALESCE(s.label_override,t.label))", termFrom,
		"s.entity_id IN("+rbTermsIDs()+") AND t.vocab=1 AND e.retired=0 AND e.library_id=?",
		"s.entity_id,s.provider,s.source_id", termParams, termPresent, termEmpty,
		stmt.Plan{Hot: true, PrimaryKeys: []string{"s", "t", "e"}, Seeks: []string{"entity_id=?"}})
	for n := range termDefs {
		termDefs[n].Plan.Waivers = []stmt.Waiver{{StatementID: termDefs[n].ID, Stage: "eqp", Form: "BLOOM FILTER", Match: "BLOOM FILTER ON t (id=?)", Reason: "Bounded entity-term source join bloom filter; exact indexed SEARCH lines are separately required"}}
	}
	defs = append(defs, termDefs...)

	// Credit classes. The department/role statements use the exact named credit
	// indexes; the person statement drives the token unique index and
	// catalog_credits_person. Every variant proves the sealed current image PK
	// and the entity's own credit image identity.
	creditFrom := "catalog_credits AS c INDEXED BY %s CROSS JOIN catalog_entities AS e ON e.id=c.entity_id CROSS JOIN catalog_credit_images AS h ON h.id=c.image_id AND h.entity_id=e.id AND h.entity_incarnation=e.metadata_incarnation AND h.owner_revision=e.credit_owner_revision CROSS JOIN catalog_credit_labels AS r ON r.id=c.role_id CROSS JOIN catalog_credit_labels AS d ON d.id=c.department_id"
	creditWhere := "%s AND e.credit_image_id=c.image_id AND h.sealed=1 AND e.retired=0 AND e.library_id=? AND c.entity_id IN(" + rbTermsIDs() + ")"
	creditColumns := "c.entity_id,c.credited_name,r.label,d.label"
	creditBytes := "c.entity_id,octet_length(c.credited_name),octet_length(r.label),octet_length(d.label)"

	deptParams := append(append([]stmt.Param{i("department0"), i("department1"), i("department2")}, i("library")), append(rbTermsIDParams("entity"), i("limit"))...)
	deptPresent := []any{int64(1), int64(2), int64(3), int64(1)}
	deptPresent = append(deptPresent, rbTermsSlotBindings()...)
	deptPresent = append(deptPresent, int64(rbTermsFactLimit))
	deptEmpty := append([]any(nil), deptPresent...)
	deptEmpty[0] = int64(-1)
	deptPlan := stmt.Plan{Hot: true, Indexes: []string{"catalog_credits_department"}, PrimaryKeys: []string{"e", "h", "r", "d"}, Seeks: []string{"department_id=?"}}
	defs = append(defs, rbTermsFactShape("credit.department",
		creditColumns, creditBytes, strings.Replace(creditFrom, "%s", "catalog_credits_department", 1),
		strings.Replace(creditWhere, "%s", "c.department_id IN(?,?,?)", 1),
		"c.department_id,c.entity_id,c.ord", deptParams, deptPresent, deptEmpty, deptPlan)...)

	roleParams := append(append([]stmt.Param{i("role0"), i("role1"), i("role2")}, i("library")), append(rbTermsIDParams("entity"), i("limit"))...)
	rolePresent := []any{int64(1), int64(2), int64(3), int64(1)}
	rolePresent = append(rolePresent, rbTermsSlotBindings()...)
	rolePresent = append(rolePresent, int64(rbTermsFactLimit))
	roleEmpty := append([]any(nil), rolePresent...)
	roleEmpty[0] = int64(-1)
	rolePlan := stmt.Plan{Hot: true, Indexes: []string{"catalog_credits_role"}, PrimaryKeys: []string{"e", "h", "r", "d"}, Seeks: []string{"role_id=?"}}
	defs = append(defs, rbTermsFactShape("credit.role",
		creditColumns, creditBytes, strings.Replace(creditFrom, "%s", "catalog_credits_role", 1),
		strings.Replace(creditWhere, "%s", "c.role_id IN(?,?,?)", 1),
		"c.role_id,c.entity_id,c.ord", roleParams, rolePresent, roleEmpty, rolePlan)...)

	// The people token unique index is the planner's own covering choice; it is
	// certified by the captured EQP rather than a forced-autoindex binding, so
	// the multi-token IN predicate stays a reviewed SEARCH.
	personFrom := "catalog_people AS p CROSS JOIN catalog_credits AS c INDEXED BY catalog_credits_person ON c.person_id=p.id CROSS JOIN catalog_entities AS e ON e.id=c.entity_id CROSS JOIN catalog_credit_images AS h ON h.id=c.image_id AND h.entity_id=e.id AND h.entity_incarnation=e.metadata_incarnation AND h.owner_revision=e.credit_owner_revision CROSS JOIN catalog_credit_labels AS r ON r.id=c.role_id CROSS JOIN catalog_credit_labels AS d ON d.id=c.department_id"
	personParams := append(append(rbTermsTokenParams(), i("library")), append(rbTermsIDParams("entity"), i("limit"))...)
	personPresent := make([]any, 0, len(personParams))
	for n := 0; n < rbTermsSlot; n++ {
		personPresent = append(personPresent, "person-"+strconv.Itoa(n))
	}
	personPresent = append(personPresent, int64(1))
	personPresent = append(personPresent, rbTermsSlotBindings()...)
	personPresent = append(personPresent, int64(rbTermsFactLimit))
	personEmpty := append([]any(nil), personPresent...)
	personEmpty[0] = ""
	personPlan := stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_catalog_people_1", "catalog_credits_person"}, PrimaryKeys: []string{"e", "h", "r", "d"}, Seeks: []string{"token=?", "person_id=?"}}
	defs = append(defs, rbTermsFactShape("credit.person",
		"c.entity_id,c.credited_name,r.label,d.label,p.token", "c.entity_id,octet_length(c.credited_name),octet_length(r.label),octet_length(d.label),octet_length(p.token)", personFrom,
		"p.token IN("+rbTermsIDs()+") AND e.credit_image_id=c.image_id AND h.sealed=1 AND e.retired=0 AND e.library_id=? AND c.entity_id IN("+rbTermsIDs()+")",
		"p.token,c.entity_id,c.ord", personParams, personPresent, personEmpty, personPlan)...)

	// Credit label dictionary: the exact role/department label ids the public
	// vocabulary names. Missing labels leave that class empty, never guessed.
	// One exact dictionary point per public label. Missing labels leave their
	// class empty; the label text is a compiled constant, never request SQL.
	labelParams := []stmt.Param{s("label"), i("limit")}
	labelPlan := stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_catalog_credit_labels_1"}, Seeks: []string{"label=?"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "catalog_credit_labels", Name: "sqlite_autoindex_catalog_credit_labels_1", Origin: "u", Columns: []stmt.IndexColumn{{Name: "label", Collation: "BINARY"}}}}}
	defs = append(defs, rbTermsFactShape("label",
		"l.id,l.label", "l.id,octet_length(l.label)", "catalog_credit_labels AS l INDEXED BY sqlite_autoindex_catalog_credit_labels_1",
		"l.label=?", "", labelParams, []any{"Acting", int64(1)}, []any{"absent", int64(1)}, labelPlan)...)
	return defs
}

func rbTermsSlotBindings() []any {
	out := make([]any, rbTermsSlot)
	for n := range out {
		out[n] = int64(n + 1)
	}
	return out
}

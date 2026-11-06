package plan

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchpolicy"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"reflect"
	"regexp"
	"slices"
	"strings"
)

// FTSReadAudit is sealed by Capture with the definition and native plans. These
// rowid candidates prove no logical-source population, current head or authority.
type FTSReadAudit struct {
	Descriptor          stmt.FTSRead        `json:"descriptor"`
	Identity            string              `json:"identity"`
	Engine              searchpolicy.Engine `json:"engine"`
	Declaration         string              `json:"declaration"`
	ContentDeclarations Rows                `json:"contentDeclarations"`
	Columns             Rows                `json:"columns"`
	Shadows             Rows                `json:"shadows"`
	Configuration       Rows                `json:"configuration"`
}

var ftsReadDeclarations = map[string]string{
	"search_own_prefix":       "CREATE VIRTUAL TABLE search_own_prefix USING fts5(\n title_prefix_tokens,initial_prefix_tokens,content='search_own_content',content_rowid='id',\n tokenize='ascii',prefix='6 12 18 24 30 36 42 48 54 60 66 72')",
	"search_fragment_prefix":  "CREATE VIRTUAL TABLE search_fragment_prefix USING fts5(\n prefix_tokens,content='search_fragment_content',content_rowid='id',\n tokenize='ascii',prefix='6 12 18 24 30 36 42 48 54 60 66 72')",
	"search_fragment_lexical": "CREATE VIRTUAL TABLE search_fragment_lexical USING fts5(\n lexical_tokens,content='search_fragment_content',content_rowid='id',\n tokenize='unicode61 remove_diacritics 2',prefix='2 3 4')",
	"search_fragment_grams":   "CREATE VIRTUAL TABLE search_fragment_grams USING fts5(\n normalized,content='search_fragment_content',content_rowid='id',tokenize='trigram')",
	"catalog_people_names":    "CREATE VIRTUAL TABLE catalog_people_names USING fts5(name,content='search_person_content',content_rowid='id',tokenize='unicode61 remove_diacritics 2',prefix='2 3 4')",
}
var ftsReadContentHashes = map[string]string{
	"search_own_content":      "1f6c87f9d7295431b28368d5a7b6eca669bd6177228eec90b7b052befed14075",
	"search_person_content":   "3db0f5f7204ddcfa8dad8ddc60918ebaf4df32b1892fcc1e3ff327abe46476e5",
	"search_fragment_content": "aa4ac4a2961e2b0b248af61b478f2bb74e20534897eaa90b9d4c4c4e05e2d8ef",
	"search_publish_jobs":     "8fd976177fc2d7c4bd2485aa739666d24e22a17711eb018f4ef98bd1be7fb7ea",
	"search_publications":     "b6926afdf19ae8bcaed8f4b0ee2003853409764e889321fce2c32dfb373df6c5",
	"search_generations":      "8c985d8043ff6658f0d566e69d3b25b5c17bd5fae92b5fc149fb32f9eb540bfe",
	"search_fragments":        "30787de7a1ba0b7edbd8d0bd15bbb92c35564a5fd640ff112af3eb9a2835c307",
}

func inspectFTSRead(ctx context.Context, conn *sql.Conn, d stmt.Definition) (*FTSReadAudit, error) {
	if _, err := stmt.Describe([]stmt.Definition{d}); err != nil {
		return nil, err
	}
	if d.Plan.NativeRead == nil {
		return nil, fmt.Errorf("missing native read descriptor")
	}
	spec, ok := d.Plan.NativeRead.Specification()
	if !ok {
		return nil, fmt.Errorf("unknown native read slot")
	}
	a := &FTSReadAudit{Descriptor: *d.Plan.NativeRead, Identity: identifier("main") + "." + identifier(spec.Table) + ":fts5"}
	engine, err := sqliteconn.AttestEngine(ctx, conn)
	if err != nil {
		return nil, err
	}
	if engine.Version != searchpolicy.SQLiteVersion || engine.SourceID != searchpolicy.SQLiteSourceID {
		return nil, fmt.Errorf("FTS read requires pinned native engine/source identity")
	}
	a.Engine = searchpolicy.Engine{Version: engine.Version, SourceID: engine.SourceID}
	content := []string{"search_fragment_content", "search_fragments", "search_generations"}
	if spec.Table == "search_own_prefix" {
		content = []string{"search_own_content", "search_publish_jobs", "search_publications", "search_generations"}
	}
	if spec.Table == "catalog_people_names" {
		content = []string{"search_person_content", "search_publish_jobs", "search_publications", "search_generations"}
	}
	names := append([]string{spec.Table}, content...)
	for _, suffix := range []string{"config", "data", "docsize", "idx"} {
		names = append(names, spec.Table+"_"+suffix)
	}
	for _, name := range names {
		var n int64
		if err := conn.QueryRowContext(ctx, "SELECT count(*) FROM temp.sqlite_schema WHERE name=? COLLATE NOCASE", name).Scan(&n); err != nil {
			return nil, err
		}
		if n != 0 {
			return nil, fmt.Errorf("FTS read relation shadowed by TEMP: %s", name)
		}
	}
	var kind string
	var root int64
	if err := conn.QueryRowContext(ctx, "SELECT type,rootpage,sql FROM main.sqlite_schema WHERE name=?", spec.Table).Scan(&kind, &root, &a.Declaration); err != nil {
		return nil, err
	}
	if kind != "table" || root != 0 || a.Declaration != ftsReadDeclarations[spec.Table] {
		return nil, fmt.Errorf("FTS read MAIN declaration differs from fixed fts5 slot")
	}
	// Direct native PRAGMA avoids SQL objects spoofing table classification.
	tables, err := query(ctx, conn, "PRAGMA main.table_list")
	if err != nil {
		return nil, err
	}
	classes := map[string]string{}
	for _, row := range tables.Values {
		if len(row) != 6 {
			return nil, fmt.Errorf("invalid native table list")
		}
		if row[0] == "main" {
			name, ok := row[1].(string)
			class, classOK := row[2].(string)
			if !ok || !classOK {
				return nil, fmt.Errorf("invalid native table classification")
			}
			classes[name] = class
		}
	}
	if classes[spec.Table] != "virtual" {
		return nil, fmt.Errorf("FTS read source is not MAIN virtual")
	}
	a.ContentDeclarations = Rows{Columns: []string{"name", "type", "sql"}, Values: [][]any{}}
	for _, name := range content {
		var class, ddl string
		if err := conn.QueryRowContext(ctx, "SELECT type,sql FROM main.sqlite_schema WHERE name=?", name).Scan(&class, &ddl); err != nil {
			return nil, err
		}
		expectedClass := "table"
		if strings.HasSuffix(name, "_content") {
			expectedClass = "view"
		}
		if class != expectedClass || classes[name] != expectedClass || fmt.Sprintf("%x", sha256.Sum256([]byte(ddl))) != ftsReadContentHashes[name] {
			return nil, fmt.Errorf("FTS read fixed content/generation binding changed: %s", name)
		}
		a.ContentDeclarations.Values = append(a.ContentDeclarations.Values, []any{name, class, ddl})
	}
	a.Columns, err = query(ctx, conn, "PRAGMA main.table_xinfo("+identifier(spec.Table)+")")
	if err != nil {
		return nil, err
	}
	visible := []string{"prefix_tokens"}
	switch spec.Table {
	case "search_own_prefix":
		visible = []string{"title_prefix_tokens", "initial_prefix_tokens"}
	case "search_fragment_lexical":
		visible = []string{"lexical_tokens"}
	case "search_fragment_grams":
		visible = []string{"normalized"}
	case "catalog_people_names":
		visible = []string{"name"}
	}
	expected := Rows{Columns: []string{"cid", "name", "type", "notnull", "dflt_value", "pk", "hidden"}, Values: [][]any{}}
	for i, name := range append(visible, spec.Table, "rank") {
		hidden := int64(0)
		if i >= len(visible) {
			hidden = 1
		}
		expected.Values = append(expected.Values, []any{int64(i), name, "", int64(0), nil, int64(0), hidden})
	}
	if !reflect.DeepEqual(a.Columns, expected) {
		return nil, fmt.Errorf("FTS read column layout changed")
	}
	a.Shadows, err = query(ctx, conn, "SELECT type,name,tbl_name,sql FROM main.sqlite_schema WHERE substr(tbl_name,1,?)=? ORDER BY name", int64(len(spec.Table)+1), spec.Table+"_")
	if err != nil {
		return nil, err
	}
	shadowDDL := map[string]string{"config": "(k PRIMARY KEY, v) WITHOUT ROWID", "data": "(id INTEGER PRIMARY KEY, block BLOB)", "docsize": "(id INTEGER PRIMARY KEY, sz BLOB)", "idx": "(segid, term, pgno, PRIMARY KEY(segid, term)) WITHOUT ROWID"}
	if len(a.Shadows.Values) != 4 {
		return nil, fmt.Errorf("FTS read shadow inventory changed")
	}
	for _, row := range a.Shadows.Values {
		if len(row) != 4 {
			return nil, fmt.Errorf("invalid shadow metadata")
		}
		name, ok := row[1].(string)
		if !ok {
			return nil, fmt.Errorf("invalid shadow name")
		}
		suffix := strings.TrimPrefix(name, spec.Table+"_")
		ddl, known := shadowDDL[suffix]
		if !known || row[0] != "table" || row[2] != name || classes[name] != "shadow" || row[3] != "CREATE TABLE '"+name+"'"+ddl {
			return nil, fmt.Errorf("FTS read shadow binding changed: %s", name)
		}
	}
	a.Configuration, err = query(ctx, conn, "SELECT k,v FROM main."+identifier(spec.Table+"_config")+" ORDER BY k")
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(a.Configuration, Rows{Columns: []string{"k", "v"}, Values: [][]any{{"version", int64(4)}}}) {
		return nil, fmt.Errorf("FTS read native configuration changed")
	}
	return a, nil
}

var rawVtabPointer = regexp.MustCompile(`^vtab:[0-9A-Fa-f]+$`)

// Only VOpen P4 in an audited literal read is mapped. Every other operand is
// copied exactly, including the VFilter selector and all cursor/register IDs.
func canonicalFTSRead(vm Rows, descriptor stmt.FTSRead, audit *FTSReadAudit) (Rows, error) {
	spec, ok := descriptor.Specification()
	if !ok || audit == nil || audit.Descriptor != descriptor || audit.Identity != identifier("main")+"."+identifier(spec.Table)+":fts5" {
		return Rows{}, fmt.Errorf("unknown FTS read identity")
	}
	if !slices.Equal(vm.Columns, []string{"addr", "opcode", "p1", "p2", "p3", "p4", "p5", "comment"}) {
		return Rows{}, fmt.Errorf("unexpected FTS EXPLAIN columns")
	}
	if writesDatabase(vm) {
		return Rows{}, fmt.Errorf("FTS read opened a write transaction")
	}
	out := Rows{Columns: slices.Clone(vm.Columns), Values: make([][]any, len(vm.Values))}
	pointer := ""
	opens, filters := 0, 0
	selector := fmt.Sprintf("M%d", spec.MatchColumn)
	if descriptor.Shape == stmt.FTSNext {
		selector += ">"
	}
	if descriptor.Shape == stmt.FTSPoint {
		selector = "=" + selector
	}
	for i, original := range vm.Values {
		if len(original) != 8 {
			return Rows{}, fmt.Errorf("unexpected FTS VM row")
		}
		row := slices.Clone(original)
		out.Values[i] = row
		if row[1] == "VOpen" {
			raw, ok := row[5].(string)
			if !ok || !rawVtabPointer.MatchString(raw) || strings.Trim(raw[5:], "0") == "" {
				return Rows{}, fmt.Errorf("invalid VOpen P4 pointer")
			}
			if pointer != "" && pointer != raw {
				return Rows{}, fmt.Errorf("VOpen pointers differ within one native plan")
			}
			pointer = raw
			opens++
			row[5] = audit.Identity
		}
		for j, value := range original {
			if text, ok := value.(string); ok && strings.HasPrefix(text, "vtab:") && !(original[1] == "VOpen" && j == 5) {
				return Rows{}, fmt.Errorf("vtab operand outside audited VOpen P4")
			}
		}
		switch row[1] {
		case "VFilter":
			filters++
			if row[5] != selector {
				return Rows{}, fmt.Errorf("FTS VFilter selector changed")
			}
		case "VUpdate", "VBegin", "OpenWrite", "OpenRead", "ReopenIdx", "OpenEphemeral", "SorterOpen", "Insert", "Delete", "IdxInsert", "IdxDelete", "Clear", "Destroy", "Program", "InitCoroutine":
			return Rows{}, fmt.Errorf("unexpected FTS read VM operation %s", row[1])
		}
	}
	if opens != 1 || filters != 1 {
		return Rows{}, fmt.Errorf("unexpected FTS read cursor/filter inventory")
	}
	return out, nil
}

func checkFTSReadEQP(d stmt.Definition, eqp Rows) error {
	if _, err := stmt.Describe([]stmt.Definition{d}); err != nil {
		return err
	}
	if d.Plan.NativeRead == nil {
		return fmt.Errorf("missing FTS read descriptor")
	}
	spec, ok := d.Plan.NativeRead.Specification()
	if !ok {
		return fmt.Errorf("unknown FTS read slot")
	}
	selector := fmt.Sprintf("M%d", spec.MatchColumn)
	index := 64
	if d.Plan.NativeRead.Shape == stmt.FTSNext {
		selector += ">"
	}
	if d.Plan.NativeRead.Shape == stmt.FTSPoint {
		selector = "=" + selector
		index = 0
	}
	detail := fmt.Sprintf("SCAN %s VIRTUAL TABLE INDEX %d:%s", spec.Table, index, selector)
	if len(eqp.Values) != 1 || len(eqp.Values[0]) != 4 || eqp.Values[0][3] != detail {
		return fmt.Errorf("FTS read exact native rowid/MATCH plan required")
	}
	return nil
}

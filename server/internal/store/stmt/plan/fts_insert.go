package plan

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchpolicy"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// FTSInsertAudit pins native format and the external-content relation. It is
// sealed by the same Capture receipt as ordinary statements; it grants no
// source/principal/command/resource custody or permission to write shadows.
type FTSInsertAudit struct {
	Slot          stmt.FTSInsertSlot  `json:"slot"`
	Identity      string              `json:"identity"`
	Engine        searchpolicy.Engine `json:"engine"`
	Declaration   string              `json:"declaration"`
	ContentView   string              `json:"contentView"`
	ContentTables Rows                `json:"contentTables"`
	Columns       Rows                `json:"columns"`
	ShadowObjects Rows                `json:"shadowObjects"`
	ShadowColumns map[string]Rows     `json:"shadowColumns"`
	Configuration Rows                `json:"configuration"`
}

const fragmentContentView = `CREATE VIEW search_fragment_content AS
SELECT * FROM search_fragments
WHERE search_generation_id=(SELECT id FROM search_generations WHERE bank_key='v1')`

func ftsDeclaration(slot stmt.FTSInsertSlot) string {
	switch slot {
	case stmt.SearchFragmentPrefixV1:
		return `CREATE VIRTUAL TABLE search_fragment_prefix USING fts5(
 prefix_tokens,content='search_fragment_content',content_rowid='id',
 tokenize='ascii',prefix='6 12 18 24 30 36 42 48 54 60 66 72')`
	case stmt.SearchFragmentLexicalV1:
		return `CREATE VIRTUAL TABLE search_fragment_lexical USING fts5(
 lexical_tokens,content='search_fragment_content',content_rowid='id',
 tokenize='unicode61 remove_diacritics 2',prefix='2 3 4')`
	case stmt.SearchFragmentGramsV1:
		return `CREATE VIRTUAL TABLE search_fragment_grams USING fts5(
 normalized,content='search_fragment_content',content_rowid='id',tokenize='trigram')`
	default:
		return ""
	}
}

func inspectFTSInsert(ctx context.Context, conn *sql.Conn, d stmt.Definition) (*FTSInsertAudit, error) {
	// Reuse the registry's closed SQL/type/metadata validation. Describe remains
	// inert: only the complete Capture below can mint registration authority.
	if _, err := stmt.Describe([]stmt.Definition{d}); err != nil {
		return nil, err
	}
	spec, ok := d.Plan.NativeInsert.Specification()
	if !ok {
		return nil, fmt.Errorf("unknown FTS INSERT slot")
	}
	a := &FTSInsertAudit{Slot: d.Plan.NativeInsert, Identity: `"main"."` + spec.Table + `":fts5`, ShadowColumns: map[string]Rows{}}
	actual, err := sqliteconn.AttestEngine(ctx, conn)
	if err != nil {
		return nil, err
	}
	if actual.Version != searchpolicy.SQLiteVersion || actual.SourceID != searchpolicy.SQLiteSourceID {
		return nil, fmt.Errorf("FTS INSERT requires the pinned physical engine/source identity")
	}
	a.Engine = searchpolicy.Engine{Version: actual.Version, SourceID: actual.SourceID}
	names := []string{spec.Table, "search_fragment_content", "search_fragments", "search_generations"}
	for _, suffix := range []string{"config", "data", "docsize", "idx"} {
		names = append(names, spec.Table+"_"+suffix)
	}
	for _, name := range names {
		var count int64
		if err := conn.QueryRowContext(ctx, "SELECT count(*) FROM temp.sqlite_schema WHERE name=? COLLATE NOCASE", name).Scan(&count); err != nil {
			return nil, err
		}
		if count != 0 {
			return nil, fmt.Errorf("FTS native relation is shadowed by TEMP: %s", name)
		}
	}
	var kind, classification string
	var root int64
	if err := conn.QueryRowContext(ctx, "SELECT type,rootpage,sql FROM main.sqlite_schema WHERE name=?", spec.Table).Scan(&kind, &root, &a.Declaration); err != nil {
		return nil, err
	}
	if err := conn.QueryRowContext(ctx, "SELECT type FROM pragma_table_list WHERE schema='main' AND name=?", spec.Table).Scan(&classification); err != nil {
		return nil, err
	}
	if kind != "table" || root != 0 || classification != "virtual" || a.Declaration != ftsDeclaration(a.Slot) {
		return nil, fmt.Errorf("FTS slot declaration/classification differs from fixed MAIN format")
	}
	if err := conn.QueryRowContext(ctx, "SELECT type,sql FROM main.sqlite_schema WHERE name=?", "search_fragment_content").Scan(&kind, &a.ContentView); err != nil {
		return nil, err
	}
	if kind != "view" || a.ContentView != fragmentContentView {
		return nil, fmt.Errorf("FTS external-content view differs from fixed v1 relation")
	}
	for _, table := range []string{"search_fragments", "search_generations"} {
		if err := mainBaseTable(ctx, conn, table); err != nil {
			return nil, err
		}
	}
	a.ContentTables, err = query(ctx, conn, "SELECT name,sql FROM main.sqlite_schema WHERE name IN(?,?) ORDER BY name", "search_fragments", "search_generations")
	if err != nil {
		return nil, err
	}
	// These exact adopted declarations define the id/content/generation
	// relation. A later schema change requires explicit review of this pin;
	// it cannot silently acquire an existing native-slot capability.
	contentDDL := map[string]string{
		"search_fragments":   "30787de7a1ba0b7edbd8d0bd15bbb92c35564a5fd640ff112af3eb9a2835c307",
		"search_generations": "8c985d8043ff6658f0d566e69d3b25b5c17bd5fae92b5fc149fb32f9eb540bfe",
	}
	if len(a.ContentTables.Values) != 2 {
		return nil, fmt.Errorf("missing fixed FTS content/generation declarations")
	}
	for _, row := range a.ContentTables.Values {
		if len(row) != 2 {
			return nil, fmt.Errorf("invalid FTS content declaration")
		}
		name, nameOK := row[0].(string)
		ddl, ddlOK := row[1].(string)
		if !nameOK || !ddlOK || fmt.Sprintf("%x", sha256.Sum256([]byte(ddl))) != contentDDL[name] {
			return nil, fmt.Errorf("FTS content/generation declaration changed: %v", row[0])
		}
	}
	a.Columns, err = query(ctx, conn, "PRAGMA main.table_xinfo("+identifier(spec.Table)+")")
	if err != nil {
		return nil, err
	}
	wantColumns := Rows{Columns: []string{"cid", "name", "type", "notnull", "dflt_value", "pk", "hidden"}, Values: [][]any{
		{int64(0), spec.Column, "", int64(0), nil, int64(0), int64(0)},
		{int64(1), spec.Table, "", int64(0), nil, int64(0), int64(1)},
		{int64(2), "rank", "", int64(0), nil, int64(0), int64(1)},
	}}
	if !reflect.DeepEqual(a.Columns, wantColumns) {
		return nil, fmt.Errorf("FTS visible/hidden columns differ from fixed slot")
	}
	// No alternate/extra shadow table or index may inherit this operation.
	a.ShadowObjects, err = query(ctx, conn, `SELECT type,name,tbl_name,sql FROM main.sqlite_schema
WHERE substr(tbl_name,1,?)=? ORDER BY name`, int64(len(spec.Table)+1), spec.Table+"_")
	if err != nil {
		return nil, err
	}
	shadowDDL := map[string]string{
		"config":  "(k PRIMARY KEY, v) WITHOUT ROWID",
		"data":    "(id INTEGER PRIMARY KEY, block BLOB)",
		"docsize": "(id INTEGER PRIMARY KEY, sz BLOB)",
		"idx":     "(segid, term, pgno, PRIMARY KEY(segid, term)) WITHOUT ROWID",
	}
	if len(a.ShadowObjects.Values) != len(shadowDDL) {
		return nil, fmt.Errorf("FTS shadow inventory differs from four fixed objects")
	}
	for _, row := range a.ShadowObjects.Values {
		if len(row) != 4 {
			return nil, fmt.Errorf("invalid FTS shadow metadata")
		}
		name, nameOK := row[1].(string)
		suffix := strings.TrimPrefix(name, spec.Table+"_")
		ddl, known := shadowDDL[suffix]
		if !nameOK || !known || row[0] != "table" || row[2] != name || row[3] != "CREATE TABLE '"+name+"'"+ddl {
			return nil, fmt.Errorf("FTS shadow declaration mismatch: %v", row)
		}
		if err := conn.QueryRowContext(ctx, "SELECT type FROM pragma_table_list WHERE schema='main' AND name=?", name).Scan(&classification); err != nil {
			return nil, err
		}
		if classification != "shadow" {
			return nil, fmt.Errorf("FTS backing object is not a native shadow")
		}
		cols, err := query(ctx, conn, "PRAGMA main.table_xinfo("+identifier(name)+")")
		if err != nil {
			return nil, err
		}
		a.ShadowColumns[name] = cols
	}
	a.Configuration, err = query(ctx, conn, "SELECT k,v FROM main."+identifier(spec.Table+"_config")+" ORDER BY k")
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(a.Configuration, Rows{Columns: []string{"k", "v"}, Values: [][]any{{"version", int64(4)}}}) {
		return nil, fmt.Errorf("FTS configuration differs from fixed format version 4")
	}
	return a, nil
}

// Lead ruling pb-fts-vtab-mapping-ruling-2 permits ONLY PC6/VUpdate/P4 and
// PC9/VBegin/P4, after the complete fixed-slot native audit above. They must
// contain the same raw per-connection pVtab. Every other operand is exact here;
// canonicalVM subsequently applies its existing schema-cookie rule only.
func mapFTSInsertVM(vm Rows, identity string) (Rows, error) {
	if !slices.Equal(vm.Columns, []string{"addr", "opcode", "p1", "p2", "p3", "p4", "p5", "comment"}) || len(vm.Values) != 13 {
		return Rows{}, fmt.Errorf("FTS INSERT must have the exact 13-op VM")
	}
	if identity != `"main"."search_fragment_prefix":fts5` && identity != `"main"."search_fragment_lexical":fts5` && identity != `"main"."search_fragment_grams":fts5` {
		return Rows{}, fmt.Errorf("unknown FTS native identity")
	}
	for _, row := range vm.Values {
		if len(row) != 8 {
			return Rows{}, fmt.Errorf("invalid FTS INSERT VM row")
		}
	}
	ptr, ok := vm.Values[6][5].(string)
	if !ok || !strings.HasPrefix(ptr, "vtab:") || len(ptr) <= len("vtab:") {
		return Rows{}, fmt.Errorf("FTS INSERT has no native pVtab")
	}
	for _, c := range ptr[len("vtab:"):] {
		if !(c >= '0' && c <= '9' || c >= 'A' && c <= 'F' || c >= 'a' && c <= 'f') {
			return Rows{}, fmt.Errorf("invalid native pVtab format")
		}
	}
	if ptr == "vtab:0" {
		return Rows{}, fmt.Errorf("null native pVtab")
	}
	if len(vm.Values[8]) != 8 {
		return Rows{}, fmt.Errorf("invalid FTS transaction opcode")
	}
	cookie, ok := vm.Values[8][4].(int64)
	if !ok || cookie < 0 {
		return Rows{}, fmt.Errorf("invalid FTS schema cookie")
	}
	want := [][]any{
		{int64(0), "Init", int64(0), int64(8), int64(0), nil, int64(0), nil},
		{int64(1), "Variable", int64(2), int64(3), int64(0), nil, int64(0), nil},
		{int64(2), "Null", int64(0), int64(1), int64(0), nil, int64(0), nil},
		{int64(3), "Variable", int64(1), int64(2), int64(0), nil, int64(0), nil},
		{int64(4), "IsNull", int64(2), int64(6), int64(0), nil, int64(0), nil},
		{int64(5), "MustBeInt", int64(2), int64(0), int64(0), nil, int64(0), nil},
		{int64(6), "VUpdate", int64(1), int64(5), int64(1), ptr, int64(2), nil},
		{int64(7), "Halt", int64(0), int64(0), int64(0), nil, int64(0), nil},
		{int64(8), "Transaction", int64(0), int64(1), cookie, "0", int64(1), nil},
		{int64(9), "VBegin", int64(0), int64(0), int64(0), ptr, int64(0), nil},
		{int64(10), "Null", int64(0), int64(4), int64(0), nil, int64(0), nil},
		{int64(11), "Null", int64(0), int64(5), int64(0), nil, int64(0), nil},
		{int64(12), "Goto", int64(0), int64(1), int64(0), nil, int64(0), nil},
	}
	if !reflect.DeepEqual(vm.Values, want) {
		return Rows{}, fmt.Errorf("FTS INSERT differs from exact native VM or raw pVtab pair")
	}
	out := Rows{Columns: slices.Clone(vm.Columns), Values: make([][]any, len(vm.Values))}
	for i, row := range vm.Values {
		out.Values[i] = slices.Clone(row)
	}
	out.Values[6][5], out.Values[9][5] = identity, identity
	return out, nil
}

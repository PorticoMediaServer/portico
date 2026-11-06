package plan

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

type FTSRetainedAudit struct {
	Slot stmt.FTSRetainedSlot `json:"slot"`
	FTSFormatAudit
}

const ownPrefixDeclaration = `CREATE VIRTUAL TABLE search_own_prefix USING fts5(
 title_prefix_tokens,initial_prefix_tokens,content='search_own_content',content_rowid='id',
 tokenize='ascii',prefix='6 12 18 24 30 36 42 48 54 60 66 72')`

const ownPrefixContentView = `CREATE VIEW search_own_content AS
SELECT j.entity_id AS id,p.title_prefix_tokens,p.initial_prefix_tokens
FROM search_publish_jobs j JOIN search_publications p ON p.id=j.published_document_id
WHERE j.entity_id IS NOT NULL AND j.search_generation_id=(SELECT id FROM search_generations WHERE bank_key='v1')
 AND p.generation=j.published_generation`

func inspectFTSRetained(ctx context.Context, conn *sql.Conn, d stmt.Definition) (*FTSRetainedAudit, error) {
	if _, err := stmt.Describe([]stmt.Definition{d}); err != nil {
		return nil, err
	}
	spec, ok := d.Plan.NativeRetained.Specification()
	if !ok {
		return nil, fmt.Errorf("unknown retained-token FTS slot")
	}
	var format *FTSFormatAudit
	var err error
	switch d.Plan.NativeRetained {
	case stmt.SearchOwnPrefixInsertV1, stmt.SearchOwnPrefixDeleteV1:
		format, err = inspectFixedFTSFormat(ctx, conn, spec.Table, spec.Columns, ownPrefixDeclaration, "search_own_content", ownPrefixContentView,
			[]string{"search_generations", "search_publications", "search_publish_jobs"}, map[string]string{
				"search_generations":  "8c985d8043ff6658f0d566e69d3b25b5c17bd5fae92b5fc149fb32f9eb540bfe",
				"search_publications": "b6926afdf19ae8bcaed8f4b0ee2003853409764e889321fce2c32dfb373df6c5",
				"search_publish_jobs": "8fd976177fc2d7c4bd2485aa739666d24e22a17711eb018f4ef98bd1be7fb7ea",
			})
	case stmt.SearchFragmentPrefixDelete, stmt.SearchFragmentLexicalDelete, stmt.SearchFragmentGramsDelete:
		insert := stmt.SearchFragmentPrefixV1
		if d.Plan.NativeRetained == stmt.SearchFragmentLexicalDelete {
			insert = stmt.SearchFragmentLexicalV1
		} else if d.Plan.NativeRetained == stmt.SearchFragmentGramsDelete {
			insert = stmt.SearchFragmentGramsV1
		}
		format, err = inspectFixedFTSFormat(ctx, conn, spec.Table, spec.Columns, ftsDeclaration(insert), "search_fragment_content", fragmentContentView,
			[]string{"search_fragments", "search_generations"}, fragmentContentDDL())
	default:
		return nil, fmt.Errorf("unknown retained-token FTS destination")
	}
	if err != nil {
		return nil, err
	}
	return &FTSRetainedAudit{Slot: d.Plan.NativeRetained, FTSFormatAudit: *format}, nil
}

// Lead-approved CLOSED extension. The original three-INSERT mapper is
// unchanged. Each new operation has its OWN complete exact native template;
// only its two raw-equal VUpdate/VBegin P4 operands acquire the audited identity.
func mapFTSRetainedVM(vm Rows, slot stmt.FTSRetainedSlot, identity string) (Rows, error) {
	spec, ok := slot.Specification()
	if !ok || identity != `"main"."`+spec.Table+`":fts5` || !slices.Equal(vm.Columns, []string{"addr", "opcode", "p1", "p2", "p3", "p4", "p5", "comment"}) {
		return Rows{}, fmt.Errorf("unknown retained-token native identity or VM columns")
	}
	update, begin, transaction, size := 7, 10, 9, 13
	if slot == stmt.SearchOwnPrefixInsertV1 {
		size = 14
	} else if slot == stmt.SearchOwnPrefixDeleteV1 {
		update, begin, transaction, size = 8, 11, 10, 14
	}
	if len(vm.Values) != size {
		return Rows{}, fmt.Errorf("wrong retained-token native VM size")
	}
	for _, row := range vm.Values {
		if len(row) != 8 {
			return Rows{}, fmt.Errorf("invalid retained-token VM row")
		}
	}
	ptr, ok := vm.Values[update][5].(string)
	if !ok || !strings.HasPrefix(ptr, "vtab:") || len(ptr) <= 5 || len(ptr) > 21 || ptr[5] == '0' {
		return Rows{}, fmt.Errorf("missing retained-token native pVtab")
	}
	for _, c := range ptr[5:] {
		if !(c >= '0' && c <= '9' || c >= 'A' && c <= 'F') {
			return Rows{}, fmt.Errorf("invalid retained-token native pVtab")
		}
	}
	cookie, ok := vm.Values[transaction][4].(int64)
	if !ok || cookie < 0 {
		return Rows{}, fmt.Errorf("invalid native schema cookie")
	}
	var want [][]any
	switch slot {
	case stmt.SearchOwnPrefixInsertV1:
		want = [][]any{
			{int64(0), "Init", int64(0), int64(9), int64(0), nil, int64(0), nil},
			{int64(1), "Variable", int64(2), int64(3), int64(0), nil, int64(0), nil},
			{int64(2), "Variable", int64(3), int64(4), int64(0), nil, int64(0), nil},
			{int64(3), "Null", int64(0), int64(1), int64(0), nil, int64(0), nil},
			{int64(4), "Variable", int64(1), int64(2), int64(0), nil, int64(0), nil},
			{int64(5), "IsNull", int64(2), int64(7), int64(0), nil, int64(0), nil},
			{int64(6), "MustBeInt", int64(2), int64(0), int64(0), nil, int64(0), nil},
			{int64(7), "VUpdate", int64(1), int64(6), int64(1), ptr, int64(2), nil},
			{int64(8), "Halt", int64(0), int64(0), int64(0), nil, int64(0), nil},
			{int64(9), "Transaction", int64(0), int64(1), cookie, "0", int64(1), nil},
			{int64(10), "VBegin", int64(0), int64(0), int64(0), ptr, int64(0), nil},
			{int64(11), "Null", int64(0), int64(5), int64(0), nil, int64(0), nil},
			{int64(12), "Null", int64(0), int64(6), int64(0), nil, int64(0), nil},
			{int64(13), "Goto", int64(0), int64(1), int64(0), nil, int64(0), nil},
		}
	case stmt.SearchOwnPrefixDeleteV1:
		want = [][]any{
			{int64(0), "Init", int64(0), int64(10), int64(0), nil, int64(0), nil},
			{int64(1), "Variable", int64(3), int64(3), int64(0), nil, int64(0), nil},
			{int64(2), "Variable", int64(4), int64(4), int64(0), nil, int64(0), nil},
			{int64(3), "Variable", int64(1), int64(5), int64(0), nil, int64(0), nil},
			{int64(4), "Null", int64(0), int64(1), int64(0), nil, int64(0), nil},
			{int64(5), "Variable", int64(2), int64(2), int64(0), nil, int64(0), nil},
			{int64(6), "IsNull", int64(2), int64(8), int64(0), nil, int64(0), nil},
			{int64(7), "MustBeInt", int64(2), int64(0), int64(0), nil, int64(0), nil},
			{int64(8), "VUpdate", int64(1), int64(6), int64(1), ptr, int64(2), nil},
			{int64(9), "Halt", int64(0), int64(0), int64(0), nil, int64(0), nil},
			{int64(10), "Transaction", int64(0), int64(1), cookie, "0", int64(1), nil},
			{int64(11), "VBegin", int64(0), int64(0), int64(0), ptr, int64(0), nil},
			{int64(12), "Null", int64(0), int64(6), int64(0), nil, int64(0), nil},
			{int64(13), "Goto", int64(0), int64(1), int64(0), nil, int64(0), nil},
		}
	case stmt.SearchFragmentPrefixDelete, stmt.SearchFragmentLexicalDelete, stmt.SearchFragmentGramsDelete:
		want = [][]any{
			{int64(0), "Init", int64(0), int64(9), int64(0), nil, int64(0), nil},
			{int64(1), "Variable", int64(3), int64(3), int64(0), nil, int64(0), nil},
			{int64(2), "Variable", int64(1), int64(4), int64(0), nil, int64(0), nil},
			{int64(3), "Null", int64(0), int64(1), int64(0), nil, int64(0), nil},
			{int64(4), "Variable", int64(2), int64(2), int64(0), nil, int64(0), nil},
			{int64(5), "IsNull", int64(2), int64(7), int64(0), nil, int64(0), nil},
			{int64(6), "MustBeInt", int64(2), int64(0), int64(0), nil, int64(0), nil},
			{int64(7), "VUpdate", int64(1), int64(5), int64(1), ptr, int64(2), nil},
			{int64(8), "Halt", int64(0), int64(0), int64(0), nil, int64(0), nil},
			{int64(9), "Transaction", int64(0), int64(1), cookie, "0", int64(1), nil},
			{int64(10), "VBegin", int64(0), int64(0), int64(0), ptr, int64(0), nil},
			{int64(11), "Null", int64(0), int64(5), int64(0), nil, int64(0), nil},
			{int64(12), "Goto", int64(0), int64(1), int64(0), nil, int64(0), nil},
		}
	}
	if !reflect.DeepEqual(vm.Values, want) {
		return Rows{}, fmt.Errorf("retained-token VM differs from exact template or raw-equal pair")
	}
	out := Rows{Columns: slices.Clone(vm.Columns), Values: make([][]any, len(vm.Values))}
	for n, row := range vm.Values {
		out.Values[n] = slices.Clone(row)
	}
	out.Values[update][5], out.Values[begin][5] = identity, identity
	return out, nil
}

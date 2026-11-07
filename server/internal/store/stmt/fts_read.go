package stmt

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// FTSRead declares a finite native read audit. It is inert until Capture mints
// the ordinary opaque receipt; it grants no source, principal or bank authority.
type FTSReadSlot string
type FTSReadShape string
type FTSRead struct {
	Slot  FTSReadSlot  `json:"slot"`
	Shape FTSReadShape `json:"shape"`
}

const (
	FTSFirst                    FTSReadShape = "first"
	FTSNext                     FTSReadShape = "next"
	FTSPoint                    FTSReadShape = "point"
	SearchOwnPrefixReadV1       FTSReadSlot  = "search_own_prefix.v1.read"
	SearchFragmentPrefixReadV1  FTSReadSlot  = "search_fragment_prefix.v1.read"
	SearchFragmentLexicalReadV1 FTSReadSlot  = "search_fragment_lexical.v1.read"
	SearchFragmentGramsReadV1   FTSReadSlot  = "search_fragment_grams.v1.read"
	CatalogPeopleNamesReadV1    FTSReadSlot  = "catalog_people_names.v1.read"
)

type FTSReadSpec struct {
	Table, SQL                          string
	LimitIndex, RowIDIndex, MatchColumn int
}

// Every SQL byte is a compiled literal, never a caller/table/bank interpolation.
func (r FTSRead) Specification() (FTSReadSpec, bool) {
	switch r {
	case FTSRead{SearchOwnPrefixReadV1, FTSFirst}:
		return FTSReadSpec{"search_own_prefix", "SELECT rowid FROM search_own_prefix WHERE search_own_prefix MATCH ? ORDER BY rowid ASC LIMIT ?", 1, -1, 2}, true
	case FTSRead{SearchOwnPrefixReadV1, FTSNext}:
		return FTSReadSpec{"search_own_prefix", "SELECT rowid FROM search_own_prefix WHERE search_own_prefix MATCH ? AND rowid>? ORDER BY rowid ASC LIMIT ?", 2, 1, 2}, true
	case FTSRead{SearchOwnPrefixReadV1, FTSPoint}:
		return FTSReadSpec{"search_own_prefix", "SELECT rowid FROM search_own_prefix WHERE search_own_prefix MATCH ? AND rowid=? LIMIT 1", -1, 1, 2}, true
	case FTSRead{SearchFragmentPrefixReadV1, FTSFirst}:
		return FTSReadSpec{"search_fragment_prefix", "SELECT rowid FROM search_fragment_prefix WHERE search_fragment_prefix MATCH ? ORDER BY rowid ASC LIMIT ?", 1, -1, 1}, true
	case FTSRead{SearchFragmentPrefixReadV1, FTSNext}:
		return FTSReadSpec{"search_fragment_prefix", "SELECT rowid FROM search_fragment_prefix WHERE search_fragment_prefix MATCH ? AND rowid>? ORDER BY rowid ASC LIMIT ?", 2, 1, 1}, true
	case FTSRead{SearchFragmentPrefixReadV1, FTSPoint}:
		return FTSReadSpec{"search_fragment_prefix", "SELECT rowid FROM search_fragment_prefix WHERE search_fragment_prefix MATCH ? AND rowid=? LIMIT 1", -1, 1, 1}, true
	case FTSRead{SearchFragmentLexicalReadV1, FTSFirst}:
		return FTSReadSpec{"search_fragment_lexical", "SELECT rowid FROM search_fragment_lexical WHERE search_fragment_lexical MATCH ? ORDER BY rowid ASC LIMIT ?", 1, -1, 1}, true
	case FTSRead{SearchFragmentLexicalReadV1, FTSNext}:
		return FTSReadSpec{"search_fragment_lexical", "SELECT rowid FROM search_fragment_lexical WHERE search_fragment_lexical MATCH ? AND rowid>? ORDER BY rowid ASC LIMIT ?", 2, 1, 1}, true
	case FTSRead{SearchFragmentLexicalReadV1, FTSPoint}:
		return FTSReadSpec{"search_fragment_lexical", "SELECT rowid FROM search_fragment_lexical WHERE search_fragment_lexical MATCH ? AND rowid=? LIMIT 1", -1, 1, 1}, true
	case FTSRead{SearchFragmentGramsReadV1, FTSFirst}:
		return FTSReadSpec{"search_fragment_grams", "SELECT rowid FROM search_fragment_grams WHERE search_fragment_grams MATCH ? ORDER BY rowid ASC LIMIT ?", 1, -1, 1}, true
	case FTSRead{SearchFragmentGramsReadV1, FTSNext}:
		return FTSReadSpec{"search_fragment_grams", "SELECT rowid FROM search_fragment_grams WHERE search_fragment_grams MATCH ? AND rowid>? ORDER BY rowid ASC LIMIT ?", 2, 1, 1}, true
	case FTSRead{SearchFragmentGramsReadV1, FTSPoint}:
		return FTSReadSpec{"search_fragment_grams", "SELECT rowid FROM search_fragment_grams WHERE search_fragment_grams MATCH ? AND rowid=? LIMIT 1", -1, 1, 1}, true
	case FTSRead{CatalogPeopleNamesReadV1, FTSFirst}:
		return FTSReadSpec{"catalog_people_names", "SELECT rowid FROM catalog_people_names WHERE catalog_people_names MATCH ? ORDER BY rowid ASC LIMIT ?", 1, -1, 1}, true
	case FTSRead{CatalogPeopleNamesReadV1, FTSNext}:
		return FTSReadSpec{"catalog_people_names", "SELECT rowid FROM catalog_people_names WHERE catalog_people_names MATCH ? AND rowid>? ORDER BY rowid ASC LIMIT ?", 2, 1, 1}, true
	case FTSRead{CatalogPeopleNamesReadV1, FTSPoint}:
		return FTSReadSpec{"catalog_people_names", "SELECT rowid FROM catalog_people_names WHERE catalog_people_names MATCH ? AND rowid=? LIMIT 1", -1, 1, 1}, true
	default:
		return FTSReadSpec{}, false
	}
}

func validateFTSReadDefinition(d Definition) error {
	if d.Plan.NativeRead == nil {
		return nil
	}
	spec, ok := d.Plan.NativeRead.Specification()
	if !ok || d.Plan.NativeInsert != "" || d.Plan.NativeRetained != "" || d.Kind != Read || !d.Plan.Hot || d.SQL != spec.SQL || len(d.Plan.Indexes)+len(d.Plan.PrimaryKeys)+len(d.Plan.Seeks)+len(d.Plan.AllowedScans)+len(d.Plan.Waivers)+len(d.Plan.AutoIndexes) != 0 {
		return fmt.Errorf("invalid fixed-slot FTS read definition")
	}
	params := []Param{{Name: "query", Type: Text}}
	if d.Plan.NativeRead.Shape == FTSNext {
		params = append(params, Param{Name: "after_rowid", Type: Integer})
	}
	if d.Plan.NativeRead.Shape == FTSPoint {
		params = append(params, Param{Name: "rowid", Type: Integer})
	} else {
		params = append(params, Param{Name: "limit", Type: Integer})
	}
	if len(d.Params) != len(params) {
		return fmt.Errorf("invalid FTS read parameter inventory")
	}
	for i, p := range params {
		if d.Params[i] != p {
			return fmt.Errorf("invalid FTS read parameter %d", i)
		}
	}
	return nil
}

func validateFTSReadBindings(descriptor *FTSRead, args []any) error {
	if descriptor == nil {
		return nil
	}
	spec, ok := descriptor.Specification()
	if !ok || len(args) < 2 {
		return fmt.Errorf("invalid fixed-slot FTS read bindings")
	}
	query, ok := args[0].(string)
	if !ok || len(query) == 0 || len(query) > 1539 || !utf8.ValidString(query) || strings.IndexByte(query, 0) >= 0 {
		return fmt.Errorf("FTS query requires 1..1539 UTF-8 bytes without NUL")
	}
	if spec.LimitIndex >= 0 {
		if spec.LimitIndex >= len(args) {
			return fmt.Errorf("missing FTS LIMIT")
		}
		limit, ok := args[spec.LimitIndex].(int64)
		if !ok || limit <= 0 || limit > 64 {
			return fmt.Errorf("FTS LIMIT must be an int64 in 1..64")
		}
	}
	if spec.RowIDIndex >= 0 {
		if spec.RowIDIndex >= len(args) {
			return fmt.Errorf("missing FTS rowid")
		}
		rowid, ok := args[spec.RowIDIndex].(int64)
		if !ok || rowid < 0 || (descriptor.Shape == FTSPoint && rowid == 0) {
			return fmt.Errorf("invalid FTS rowid boundary")
		}
	}
	return nil
}

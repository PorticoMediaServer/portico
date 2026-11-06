package stmt

import (
	"fmt"
	"unicode/utf8"
)

// FTSInsertSlot is a closed audit descriptor, never an execution capability.
// No native control, delete, source read, v2 bank or arbitrary virtual table is
// admitted by these three v1 INSERT identities.
type FTSInsertSlot string

const (
	SearchFragmentPrefixV1  FTSInsertSlot = "search_fragment_prefix.v1.insert"
	SearchFragmentLexicalV1 FTSInsertSlot = "search_fragment_lexical.v1.insert"
	SearchFragmentGramsV1   FTSInsertSlot = "search_fragment_grams.v1.insert"
)

// FTSInsertSpec is immutable values-only metadata. Byte/scalar limits derive
// from the current prepared-fragment envelope; they are not a native Work,
// maintenance, allocation, IO, WAL or writer-hold forecast.
type FTSInsertSpec struct {
	Table, Column, SQL string
	MaxBytes, MaxRunes int
}

func (slot FTSInsertSlot) Specification() (FTSInsertSpec, bool) {
	switch slot {
	case SearchFragmentPrefixV1:
		return FTSInsertSpec{"search_fragment_prefix", "prefix_tokens", "INSERT INTO search_fragment_prefix(rowid,prefix_tokens) VALUES(?,?)", 1024, 0}, true
	case SearchFragmentLexicalV1:
		return FTSInsertSpec{"search_fragment_lexical", "lexical_tokens", "INSERT INTO search_fragment_lexical(rowid,lexical_tokens) VALUES(?,?)", 1024, 0}, true
	case SearchFragmentGramsV1:
		return FTSInsertSpec{"search_fragment_grams", "normalized", "INSERT INTO search_fragment_grams(rowid,normalized) VALUES(?,?)", 4096, 1024}, true
	default:
		return FTSInsertSpec{}, false
	}
}

func validateFTSInsertDefinition(d Definition) error {
	if d.Plan.NativeInsert == "" {
		return nil
	}
	spec, ok := d.Plan.NativeInsert.Specification()
	if !ok || d.Kind != Write || d.SQL != spec.SQL || !d.Plan.Hot ||
		len(d.Params) != 2 || d.Params[0] != (Param{Name: "rowid", Type: Integer}) ||
		d.Params[1] != (Param{Name: spec.Column, Type: Text}) ||
		len(d.Plan.Indexes)+len(d.Plan.PrimaryKeys)+len(d.Plan.Seeks)+len(d.Plan.AllowedScans)+len(d.Plan.Waivers)+len(d.Plan.AutoIndexes) != 0 {
		return fmt.Errorf("invalid fixed-slot FTS INSERT definition")
	}
	return nil
}

func validateFTSInsertBindings(slot FTSInsertSlot, args []any) error {
	if slot == "" {
		return nil
	}
	spec, ok := slot.Specification()
	if !ok || len(args) != 2 {
		return fmt.Errorf("invalid fixed-slot FTS bindings")
	}
	rowid, idOK := args[0].(int64)
	text, textOK := args[1].(string)
	// Length before UTF-8/scalar scanning; Bind must refuse before retaining
	// an unbounded TEXT value. Empty native TEXT is required and remains legal.
	if !idOK || rowid <= 0 || !textOK || len(text) > spec.MaxBytes || !utf8.ValidString(text) ||
		(spec.MaxRunes != 0 && utf8.RuneCountInString(text) > spec.MaxRunes) {
		return fmt.Errorf("FTS INSERT requires positive native rowid and bounded UTF-8 TEXT")
	}
	return nil
}

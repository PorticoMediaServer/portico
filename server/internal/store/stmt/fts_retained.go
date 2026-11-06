package stmt

import (
	"fmt"
	"unicode/utf8"
)

// FTSRetainedSlot names five separately audited fixed operations. It is inert
// intent for Capture, never a generic virtual-table/control capability.
type FTSRetainedSlot string

const (
	SearchOwnPrefixInsertV1     FTSRetainedSlot = "search_own_prefix.v1.insert"
	SearchOwnPrefixDeleteV1     FTSRetainedSlot = "search_own_prefix.v1.delete"
	SearchFragmentPrefixDelete  FTSRetainedSlot = "search_fragment_prefix.v1.delete"
	SearchFragmentLexicalDelete FTSRetainedSlot = "search_fragment_lexical.v1.delete"
	SearchFragmentGramsDelete   FTSRetainedSlot = "search_fragment_grams.v1.delete"
)

// Values only; limits bound retained native TEXT before copying/scanning.
// They imply no native work, allocation/debt, hold, WAL or resource forecast.
type FTSRetainedSpec struct {
	Table, SQL string
	Columns    []string
	MaxBytes   []int
	MaxRunes   []int
	Delete     bool
}

func (slot FTSRetainedSlot) Specification() (FTSRetainedSpec, bool) {
	switch slot {
	case SearchOwnPrefixInsertV1:
		return FTSRetainedSpec{"search_own_prefix", "INSERT INTO search_own_prefix(rowid,title_prefix_tokens,initial_prefix_tokens) VALUES(?,?,?)", []string{"title_prefix_tokens", "initial_prefix_tokens"}, []int{65536, 72}, []int{0, 0}, false}, true
	case SearchOwnPrefixDeleteV1:
		return FTSRetainedSpec{"search_own_prefix", "INSERT INTO search_own_prefix(search_own_prefix,rowid,title_prefix_tokens,initial_prefix_tokens) VALUES(?,?,?,?)", []string{"title_prefix_tokens", "initial_prefix_tokens"}, []int{65536, 72}, []int{0, 0}, true}, true
	case SearchFragmentPrefixDelete:
		return FTSRetainedSpec{"search_fragment_prefix", "INSERT INTO search_fragment_prefix(search_fragment_prefix,rowid,prefix_tokens) VALUES(?,?,?)", []string{"prefix_tokens"}, []int{1024}, []int{0}, true}, true
	case SearchFragmentLexicalDelete:
		return FTSRetainedSpec{"search_fragment_lexical", "INSERT INTO search_fragment_lexical(search_fragment_lexical,rowid,lexical_tokens) VALUES(?,?,?)", []string{"lexical_tokens"}, []int{1024}, []int{0}, true}, true
	case SearchFragmentGramsDelete:
		return FTSRetainedSpec{"search_fragment_grams", "INSERT INTO search_fragment_grams(search_fragment_grams,rowid,normalized) VALUES(?,?,?)", []string{"normalized"}, []int{4096}, []int{1024}, true}, true
	default:
		return FTSRetainedSpec{}, false
	}
}

func validateFTSRetainedDefinition(d Definition) error {
	if d.Plan.NativeRetained == "" {
		return nil
	}
	spec, ok := d.Plan.NativeRetained.Specification()
	if !ok || d.Plan.NativeInsert != "" || d.Kind != Write || d.SQL != spec.SQL || !d.Plan.Hot ||
		len(d.Plan.Indexes)+len(d.Plan.PrimaryKeys)+len(d.Plan.Seeks)+len(d.Plan.AllowedScans)+len(d.Plan.Waivers)+len(d.Plan.AutoIndexes) != 0 {
		return fmt.Errorf("invalid closed retained-token FTS definition")
	}
	want := []Param{}
	if spec.Delete {
		want = append(want, Param{Name: "control", Type: Text})
	}
	want = append(want, Param{Name: "rowid", Type: Integer})
	for _, column := range spec.Columns {
		want = append(want, Param{Name: column, Type: Text})
	}
	if len(want) != len(d.Params) {
		return fmt.Errorf("wrong retained-token parameter count")
	}
	for n := range want {
		if want[n] != d.Params[n] {
			return fmt.Errorf("wrong retained-token parameter %d", n)
		}
	}
	return nil
}

func validateFTSRetainedBindings(slot FTSRetainedSlot, args []any) error {
	if slot == "" {
		return nil
	}
	spec, ok := slot.Specification()
	if !ok {
		return fmt.Errorf("unknown retained-token FTS slot")
	}
	idAt := 0
	if spec.Delete {
		idAt = 1
	}
	if len(args) != 1+idAt+len(spec.Columns) {
		return fmt.Errorf("wrong retained-token binding count")
	}
	// Runtime enforcement, not just a cohort example. No merge/rebuild/other
	// hidden FTS control can inherit a delete registration or retain its input.
	if spec.Delete && args[0] != "delete" {
		return fmt.Errorf("retained-token FTS control must be exactly delete")
	}
	rowid, ok := args[idAt].(int64)
	if !ok || rowid <= 0 {
		return fmt.Errorf("retained-token FTS rowid must be positive int64")
	}
	for n := range spec.Columns {
		text, ok := args[idAt+1+n].(string)
		if !ok || len(text) > spec.MaxBytes[n] || !utf8.ValidString(text) ||
			spec.MaxRunes[n] != 0 && utf8.RuneCountInString(text) > spec.MaxRunes[n] {
			return fmt.Errorf("retained-token FTS payload exceeds exact native envelope")
		}
	}
	return nil
}

package writer

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// Exact numeric IDs, names and durability from the pre-split registry. Keep
// legacy identities explicit; new domain commands add tests in their own files.
func TestCommandRegistryLegacyParity(t *testing.T) {
	for _, tc := range []struct {
		symbol     CommandID
		number     CommandID
		name       string
		durability Durability
	}{
		{InventoryAccept, 1, "inventory_accept", Normal},
		{BasicAccept, 2, "basic_accept", Normal},
		{EvidenceStage, 3, "evidence_stage", Normal},
		{CreditStage, 4, "credit_stage", Normal},
		{PeopleStage, 5, "people_stage", Normal},
		{EnrichAccept, 6, "enrich_accept", Normal},
		{ProgressAccept, 7, "progress_accept", Normal},
		{SecurityAccept, 8, "security_accept", Full},
		{SearchFragmentStage, 9, "search_fragment_stage", Normal},
		{SearchHeadPublish, 10, "search_head_publish", Normal},
		{SearchFragmentReclaim, 11, "search_fragment_reclaim", Normal},
		{SearchImageReclaim, 12, "search_image_reclaim", Normal},
		{SearchGenerationAdvance, 13, "search_generation_advance", Normal},
		{SearchGenerationFlip, 14, "search_generation_flip", Normal},
		{PeopleHeadPublish, 15, "people_head_publish", Normal},
		{SearchImageStage, 16, "search_image_stage", Normal},
		{SearchSeal, 17, "search_seal", Normal},
		{EffectiveStage, 18, "effective_stage", Normal},
		{InventoryCreate, 19, "inventory_create", Normal},
		{InventoryClaim, 20, "inventory_claim", Normal},
		{BasicStage, 21, "basic_stage", Normal},
		{BasicSeal, 22, "basic_seal", Normal},
		{EvidenceSeal, 23, "evidence_seal", Normal},
		{CreditSourceSeal, 24, "credit_source_seal", Normal},
	} {
		if tc.symbol != tc.number {
			t.Errorf("%s changed ID: got %d, want %d", tc.name, tc.symbol, tc.number)
		}
		got, err := Describe(tc.number)
		want := Descriptor{ID: tc.number, Name: tc.name, Durability: tc.durability}
		if err != nil || got != want {
			t.Errorf("Describe(%d) = %+v, %v; want %+v", tc.number, got, err, want)
		}
	}
	for _, id := range []CommandID{0, 65535} {
		if d, err := Describe(id); d != (Descriptor{}) || !errors.Is(err, ErrInvalid) {
			t.Fatalf("unknown %d: %+v %v", id, d, err)
		}
	}
}

func TestCommandRegistryRejectsInvalidDeclarations(t *testing.T) {
	for _, tc := range []struct {
		name         string
		declarations []commandRegistration
	}{
		{"duplicate ID", []commandRegistration{{"phase1a", []Descriptor{{1, "one", Normal}, {1, "two", Normal}}}}},
		{"duplicate name", []commandRegistration{{"phase1a", []Descriptor{{1, "same", Normal}}}, {"IA", []Descriptor{{100, "same", Normal}}}}},
		{"unknown durability", []commandRegistration{{"IA", []Descriptor{{100, "one", Durability(99)}}}}},
		{"empty name", []commandRegistration{{"IA", []Descriptor{{100, "", Normal}}}}},
		{"unknown owner", []commandRegistration{{owner: "foreign"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := buildCommandDescriptors(tc.declarations...); err == nil {
				t.Fatal("accepted invalid declaration")
			}
		})
	}
}

func TestCommandRegistryOwnerRanges(t *testing.T) {
	for _, tc := range []struct {
		owner       string
		first, last CommandID
	}{
		{"phase1a", 1, 99}, {"IA", 100, 199}, {"IP", 200, 299}, {"RB", 300, 399}, {"RS", 400, 499},
		{"US", 500, 599}, {"UO", 600, 699}, {"MP", 700, 799}, {"MG", 800, 899},
	} {
		t.Run(tc.owner, func(t *testing.T) {
			for _, id := range []CommandID{tc.first, tc.last} {
				d := Descriptor{ID: id, Name: "boundary", Durability: Normal}
				got, err := buildCommandDescriptors(commandRegistration{tc.owner, []Descriptor{d}})
				if err != nil || got[id] != d {
					t.Fatalf("legal boundary %d: %v %v", id, got, err)
				}
			}
			for _, id := range []CommandID{tc.first - 1, tc.last + 1} {
				if _, err := buildCommandDescriptors(commandRegistration{tc.owner, []Descriptor{{ID: id, Name: "outside", Durability: Normal}}}); err == nil {
					t.Fatalf("accepted foreign ID %d", id)
				}
			}
		})
	}
}

func TestCommandRegistryIDsAreExplicit(t *testing.T) {
	files, err := filepath.Glob("commands_*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, declaration := range file.Decls {
			gen, ok := declaration.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				value := spec.(*ast.ValueSpec)
				typ, ok := value.Type.(*ast.Ident)
				if !ok || typ.Name != "CommandID" || len(value.Values) != len(value.Names) {
					t.Fatalf("%s: every command needs an explicit CommandID number", name)
				}
				for _, expression := range value.Values {
					literal, ok := expression.(*ast.BasicLit)
					if !ok || literal.Kind != token.INT {
						t.Fatalf("%s: command ID must be a numeric literal", name)
					}
				}
			}
		}
	}
}

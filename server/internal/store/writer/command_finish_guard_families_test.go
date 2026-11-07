package writer

import (
	"fmt"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// Each family owns its additive selectors and bypass controls in its own file.
// Shared result types still discover every context-bearing release regardless
// of its name. Scalar/error helpers need an exact name or the existing prefixes.
type commandFinishGuardFamily struct {
	name        string
	resultTypes []string
	names       []string
	prefixes    []string
	bypasses    []string
}

var commandFinishGuardFamilies []commandFinishGuardFamily

// Registration order is immaterial: selectors are a union, and every family's
// bypasses run. Adding a command never requires editing a central family list.
func registerCommandFinishGuard(family commandFinishGuardFamily) {
	commandFinishGuardFamilies = append(commandFinishGuardFamilies, family)
}

func commandFinishGuardResultTypes() map[string]bool {
	types := make(map[string]bool)
	for _, family := range commandFinishGuardFamilies {
		for _, name := range family.resultTypes {
			types[name] = true
		}
	}
	return types
}

func commandFinishGuardNeedsName(name string) bool {
	for _, family := range commandFinishGuardFamilies {
		for _, exact := range family.names {
			if name == exact {
				return true
			}
		}
		for _, prefix := range family.prefixes {
			if strings.HasPrefix(name, prefix) {
				return true
			}
		}
	}
	return false
}

func TestCommandFinishGuardFamilySelectors(t *testing.T) {
	if len(commandFinishGuardFamilies) == 0 {
		t.Fatal("empty finish-guard registration")
	}
	families := make(map[string]bool)
	for _, family := range commandFinishGuardFamilies {
		if family.name == "" || families[family.name] {
			t.Fatalf("empty/duplicate family name %q", family.name)
		}
		families[family.name] = true
		// Exercise actual AST classification, including a result-type-only
		// release and each scalar helper/prefix. The body deliberately bypasses
		// finishCommand; all registered selectors must make it fail the guard.
		bodies := make([]string, 0)
		for _, typ := range family.resultTypes {
			if typ == "" {
				t.Fatal("empty result type", family.name)
			}
			bodies = append(bodies, fmt.Sprintf("func probe(ctx context.Context)(out %s,err error){return out,nil}", typ))
		}
		for _, name := range family.names {
			if name == "" {
				t.Fatal("empty exact name", family.name)
			}
			bodies = append(bodies, fmt.Sprintf("func %s(ctx context.Context)(value int64,err error){return 1,nil}", name))
		}
		for _, prefix := range family.prefixes {
			if prefix == "" {
				t.Fatal("empty name prefix", family.name)
			}
			for _, suffix := range []string{"", "Probe"} {
				bodies = append(bodies, fmt.Sprintf("func %s(ctx context.Context)(err error){return nil}", prefix+suffix))
			}
		}
		for _, body := range bodies {
			file, err := parser.ParseFile(token.NewFileSet(), "selector.go", "package writer\n"+body, 0)
			if err != nil {
				t.Fatal(err)
			}
			if len(unguardedCommandFinishes(file)) != 1 {
				t.Fatalf("%s selector lost: %s", family.name, body)
			}
		}
	}
	// Preserve the name/result-only boundary and the original context filter.
	for _, body := range []string{
		"func unrelated(ctx context.Context)(err error){return nil}",
		"func disclose()(err error){return nil}",
		"func unrelated()(out InventoryResult,err error){return out,nil}",
	} {
		file, err := parser.ParseFile(token.NewFileSet(), "unrelated.go", "package writer\n"+body, 0)
		if err != nil {
			t.Fatal(err)
		}
		if bad := unguardedCommandFinishes(file); len(bad) != 0 {
			t.Fatalf("unrelated function newly guarded: %s", body)
		}
	}
}

package writer

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

func init() {
	registerCommandFinishGuard(commandFinishGuardFamily{
		name:        "uo-personal-flags",
		resultTypes: []string{"uoPersonalFlagsEffect"},
		bypasses:    []string{`func bypass(ctx context.Context)(out uoPersonalFlagsEffect,err error){return out,nil}`},
	})
}

func uoFlagsGuardedFinish(source string) bool {
	file, err := parser.ParseFile(token.NewFileSet(), "uo_personal_flags.go", source, 0)
	if err != nil {
		return false
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "applyUOPersonalFlags" || fn.Body == nil || len(fn.Body.List) == 0 {
			continue
		}
		deferStatement, ok := fn.Body.List[0].(*ast.DeferStmt)
		return ok && guardedFinishDefer(fn, deferStatement)
	}
	return false
}

func TestUOFlagsFinishGuard(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "uo_personal_flags.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "applyUOPersonalFlags" {
			continue
		}
		guard, ok := fn.Body.List[0].(*ast.DeferStmt)
		if !ok || !guardedFinishDefer(fn, guard) {
			t.Fatal("UO effect lacks the shared first-action finish rule")
		}
		return
	}
	t.Fatal("UO effect release missing from its family guard")
}

func TestUOFlagsFinishGuardRejectsRemovedFinish(t *testing.T) {
	source := `package writer
func applyUOPersonalFlags(ctx context.Context, turn *commandTurn, q uoPersonalFlagsInput) (out uoPersonalFlagsEffect, err error) {
defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
return out,nil
}`
	if !uoFlagsGuardedFinish(source) {
		t.Fatal("valid shared finish rejected")
	}
	mutant := strings.Replace(source, "defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()", "", 1)
	if uoFlagsGuardedFinish(mutant) {
		t.Fatal("removed finish was accepted")
	}
}

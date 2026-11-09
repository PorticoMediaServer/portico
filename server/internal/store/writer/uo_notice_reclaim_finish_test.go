package writer

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

func TestUONoticeReclaimFinishGuards(t *testing.T) {
	for file, names := range map[string][]string{
		"uo_notice_reclaim.go": {"reclaimUONotices", "uoNoticeVictims"},
	} {
		f, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		found := map[string]bool{}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			for _, name := range names {
				if fn.Name.Name != name {
					continue
				}
				if fn.Body == nil || len(fn.Body.List) == 0 {
					t.Fatal(name, "has no finish")
				}
				d, ok := fn.Body.List[0].(*ast.DeferStmt)
				if !ok || !guardedFinishDefer(fn, d) {
					t.Fatal(name, "does not use shared first-action finish")
				}
				found[name] = true
			}
		}
		for _, name := range names {
			if !found[name] {
				t.Fatal("missing native effect", name)
			}
		}
	}
}

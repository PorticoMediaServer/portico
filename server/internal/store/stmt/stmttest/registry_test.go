package stmttest_test

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

func TestFixtureAdapterRefusesOrdinaryBinary(t *testing.T) {
	if _, err := stmttest.New(nil, nil); !errors.Is(err, stmttest.ErrTestOnly) {
		t.Fatal("nil test custody accepted", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	// Compile an ordinary program outside the physical store source tree. A
	// tracked testdata/*.go program is excluded by go list but still belongs to
	// the unfiltered connection-opening census, so keep its template non-Go.
	server, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	module, err := os.ReadFile(filepath.Join(server, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	var goVersion string
	for line := range strings.SplitSeq(string(module), "\n") {
		if fields := strings.Fields(line); len(fields) == 2 && fields[0] == "go" {
			goVersion = fields[1]
		}
	}
	if goVersion == "" {
		t.Fatal("server module has no Go version")
	}
	probe, err := os.ReadFile("testdata/productionprobe/main.go.txt")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	probeModule := fmt.Sprintf("module github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest/productionprobe\n\ngo %s\n\nrequire github.com/PorticoMediaServer/portico/server v0.0.0\n\nreplace github.com/PorticoMediaServer/portico/server => %s\n", goVersion, strconv.Quote(filepath.ToSlash(server)))
	for name, data := range map[string][]byte{"go.mod": []byte(probeModule), "main.go": probe} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.CommandContext(ctx, "go", "run", "-mod=mod", ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off")
	out, err := cmd.CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "production fixture registration refused" {
		t.Fatalf("ordinary binary bypass: %v: %s", err, out)
	}
}

func TestNoProductionFixtureImportsOrUnreceiptedRegistration(t *testing.T) {
	root := filepath.Clean("../../../..") // server
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" || d.Name() == "vendor" || strings.HasPrefix(d.Name(), ".") && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		var stmtAlias string
		for _, imp := range file.Imports {
			name, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				return err
			}
			if name == "github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest" {
				t.Errorf("production file imports unit-fixture registration: %s", path)
			}
			if name == "github.com/PorticoMediaServer/portico/server/internal/store/stmt/internal/qualification" {
				relative, err := filepath.Rel(root, path)
				if err != nil {
					return err
				}
				switch filepath.ToSlash(relative) {
				case "internal/store/stmt/registry.go", "internal/store/stmt/plan/plan.go", "internal/store/stmt/stmttest/registry.go":
				default:
					t.Errorf("unapproved receipt-minter import: %s", path)
				}
			}
			if name == "github.com/PorticoMediaServer/portico/server/internal/store/stmt" {
				stmtAlias = "stmt"
				if imp.Name != nil {
					stmtAlias = imp.Name.Name
				}
			}
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "New" {
				return true
			}
			id, ok := sel.X.(*ast.Ident)
			if ok && stmtAlias != "" && id.Name == stmtAlias && len(call.Args) < 2 {
				t.Errorf("production stmt.New lacks a receipt: %s", path)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestFixtureReceiptIsOnlyUnitEvidence(t *testing.T) {
	d := stmt.Definition{ID: "unit.fixture", SQL: "SELECT 1", Owner: "test", CallSites: []string{"registry_test.go"}, Kind: stmt.Read, Cohorts: []stmt.Cohort{{Name: "one"}}}
	r, err := stmttest.New(t, []stmt.Definition{d})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Lookup(d.ID); err != nil {
		t.Fatal(err)
	}
}

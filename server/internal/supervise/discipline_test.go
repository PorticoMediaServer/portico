package supervise

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// A goroutine that can take the process down cannot be intercepted at runtime,
// so the rule is enforced where it is written, exactly as the write-gate rule is
// in internal/dbwork. There were ninety-nine bare `go` statements in this tree
// and every one of them was a whole-process kill waiting for the wrong input; a
// hundredth added next week would be too, and nothing would notice until a
// customer's server restarted for no reason anyone could name.
//
// Test files are exempt: a test is allowed to model a caller that does not
// contain its panics, which is exactly what the containment has to survive.
func bareGoLines(relative string, data []byte) (map[int]bool, error) {
	positions := token.NewFileSet()
	file, err := parser.ParseFile(positions, relative, data, 0)
	if err != nil {
		return nil, err
	}
	lines := map[int]bool{}
	ast.Inspect(file, func(node ast.Node) bool {
		if statement, ok := node.(*ast.GoStmt); ok {
			lines[positions.Position(statement.Go).Line] = true
		}
		return true
	})
	return lines, nil
}

// The maintained upstream driver already owns this cancellation watcher's
// lifetime through its returned cleanup function. Permit exactly its one
// goroutine, without excluding other driver or application code from the rule.
func upstreamInterruptGoLines(relative string, data []byte) (map[int]bool, error) {
	if relative != "internal/thirdparty/sqlite/sqlite.go" {
		return nil, nil
	}
	positions := token.NewFileSet()
	file, err := parser.ParseFile(positions, relative, data, 0)
	if err != nil {
		return nil, err
	}
	var matches []*ast.GoStmt
	functions := 0
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != "interruptOnDone" || function.Recv != nil {
			continue
		}
		functions++
		ast.Inspect(function.Body, func(node ast.Node) bool {
			if statement, ok := node.(*ast.GoStmt); ok {
				matches = append(matches, statement)
			}
			return true
		})
	}
	if functions != 1 || len(matches) != 1 {
		return nil, fmt.Errorf("upstream interruptOnDone exception requires one function and one goroutine; found %d and %d", functions, len(matches))
	}
	if _, ok := matches[0].Call.Fun.(*ast.FuncLit); !ok || len(matches[0].Call.Args) != 0 {
		return nil, fmt.Errorf("upstream interruptOnDone watcher must remain an immediately invoked function literal")
	}
	return map[int]bool{positions.Position(matches[0].Go).Line: true}, nil
}

func TestEveryGoroutineIsStartedThroughThisPackage(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	var offenders []string
	for _, tree := range []string{"internal", "cmd"} {
		walkErr := filepath.Walk(filepath.Join(root, tree), func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			relative, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			relative = filepath.ToSlash(relative)
			// This package is where containment is implemented; it starts the one
			// goroutine everything else goes through.
			if strings.HasPrefix(relative, "internal/supervise/") {
				return nil
			}
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			exceptions, exceptionErr := upstreamInterruptGoLines(relative, data)
			if exceptionErr != nil {
				return exceptionErr
			}
			lines, parseErr := bareGoLines(relative, data)
			if parseErr != nil {
				return parseErr
			}
			for line := range lines {
				if !exceptions[line] {
					offenders = append(offenders, relative+":"+strconv.Itoa(line))
				}
			}
			return nil
		})
		if walkErr != nil {
			t.Fatal(walkErr)
		}
	}
	if len(offenders) > 0 {
		t.Fatalf(`a panic in any of these goroutines takes the whole process down.

Start them through supervise.Go (recover, log, count, end that goroutine) or
supervise.Supervise (the same, then restart with backoff — only for a loop whose
state is in the database rather than in the goroutine):

%s`, strings.Join(offenders, "\n"))
	}
}

func TestUpstreamInterruptExceptionIsLimitedToOneFunctionAndGoroutine(t *testing.T) {
	const path = "internal/thirdparty/sqlite/sqlite.go"
	const valid = "package sqlite\nfunc interruptOnDone() {\n go func() {}()\n}\nfunc other() {\n go func() {}()\n}\n"
	lines, err := upstreamInterruptGoLines(path, []byte(valid))
	if err != nil || len(lines) != 1 || !lines[3] || lines[6] {
		t.Fatalf("exception escaped its function: %v, %v", lines, err)
	}
	if lines, err := upstreamInterruptGoLines("internal/application/sqlite.go", []byte(valid)); err != nil || len(lines) != 0 {
		t.Fatalf("exception escaped its exact file: %v, %v", lines, err)
	}
	for _, invalid := range []string{
		"package sqlite; func other() { go func() {}() }",
		"package sqlite; func interruptOnDone() { go func() {}(); go func() {}() }",
		"package sqlite; func interruptOnDone() { go worker() }",
		"package sqlite; func interruptOnDone() { go func() { go func() {}() }() }",
	} {
		if _, err := upstreamInterruptGoLines(path, []byte(invalid)); err == nil {
			t.Fatal("accepted changed upstream goroutine shape")
		}
	}
}

func TestGoroutineGuardFindsInlineAndMultilineStatements(t *testing.T) {
	lines, err := bareGoLines("app.go", []byte("package app\nfunc f(){ if true { go worker() }; go func(\n){}() }\n"))
	if err != nil || len(lines) != 1 || !lines[2] {
		t.Fatal(lines, err)
	}
}

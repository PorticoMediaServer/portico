package persistence

import (
	"database/sql"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// A statement that names a column or table the schema does not have fails only
// when it runs, and a path that runs rarely can carry one for months. This
// prepares every complete SQL statement written as a literal in the server's
// production code (a string literal, or literals joined with +) against the
// baseline schema and fails on a missing table, column or function. Fragments
// assembled at run time fail to parse and are skipped; the packages' own tests
// cover those.
type productionStatement struct{ where, text string }

// productionStatements are the complete SQL statements written as literals in
// the server's production code, with the tables and functions that exist only
// at run time (temporary tables, functions another package registers).
func productionStatements(t *testing.T) ([]productionStatement, map[string]bool) {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return productionStatementsIn(t, root)
}

func productionStatementsIn(t *testing.T, root string) ([]productionStatement, map[string]bool) {
	t.Helper()
	start := regexp.MustCompile(`(?is)^\s*(SELECT|INSERT|UPDATE|DELETE|WITH|REPLACE)\b`)
	// Tables a statement creates at run time (temporary work tables) and
	// functions another package registers with the driver.
	created := regexp.MustCompile(`(?i)CREATE\s+(?:TEMP|TEMPORARY)\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?(\w+)`)
	registered := regexp.MustCompile(`Register\w*Function\("(\w+)"`)
	var statements []productionStatement
	transient := map[string]bool{"table:sqlite_stat1": true}
	fset := token.NewFileSet()
	files := map[string]*ast.File{}
	var paths []string
	packages := map[string][]*ast.File{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); name == "testdata" || name == "vendor" || strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || strings.HasPrefix(d.Name(), "zz_local_") {
			return nil
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range registered.FindAllStringSubmatch(string(source), -1) {
			transient["function:"+strings.ToLower(m[1])] = true
		}
		file, err := parser.ParseFile(fset, path, source, 0)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		files[rel] = file
		paths = append(paths, rel)
		dir := filepath.Dir(rel)
		packages[dir] = append(packages[dir], file)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	fragments := map[ast.Expr]bool{}
	for _, group := range packages {
		// Resolve package constants across files too. Local/shadowed names
		// already have an Object from the parser; only unresolved names can
		// refer to a sibling declaration. Ambiguous build-specific declarations
		// remain unresolved rather than guessing which file supplies them.
		constants := map[string]*ast.Object{}
		for _, file := range group {
			for name, object := range file.Scope.Objects {
				if object.Kind == ast.Con {
					if _, exists := constants[name]; exists {
						constants[name] = nil
					} else {
						constants[name] = object
					}
				}
			}
		}
		for _, file := range group {
			for _, id := range file.Unresolved {
				if object := constants[id.Name]; object != nil {
					id.Obj = object
				}
			}
		}
		for expression := range concatenatedConstants(group) {
			fragments[expression] = true
		}
	}
	for _, rel := range paths {
		file := files[rel]
		ast.Inspect(file, func(n ast.Node) bool {
			expr, ok := n.(ast.Expr)
			if !ok {
				return true
			}
			if fragments[expr] {
				return false
			}
			// Constants are folded at their declaration or in a surrounding
			// concatenation, not once for every identifier (including its name).
			if _, ok := expr.(*ast.Ident); ok {
				return true
			}
			text, ok := literal(expr)
			if !ok {
				// A statement assembled at run time ("SELECT …" + where): its
				// literal parts are not statements of their own.
				if b, isAdd := expr.(*ast.BinaryExpr); isAdd && b.Op == token.ADD {
					return false
				}
				return true
			}
			for _, m := range created.FindAllStringSubmatch(text, -1) {
				transient["table:"+strings.ToLower(m[1])] = true
			}
			if start.MatchString(text) {
				statements = append(statements, productionStatement{rel + ":" + strconv.Itoa(fset.Position(expr.Pos()).Line), text})
			}
			// A folded expression's parts are not statements of their own.
			return false
		})
	}
	return statements, transient
}

func TestProductionStatementsPrepareAgainstTheBaseline(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "statements.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	statements, transient := productionStatements(t)
	// The title dataset client (internal/enrichment) also reads the dataset
	// file, whose schema is its published contract (testdata/schema.sql):
	// its statements prepare against one schema or the other.
	dataset, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "dataset.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer dataset.Close()
	contract, err := os.ReadFile(filepath.Join("..", "enrichment", "testdata", "schema.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = dataset.Exec(string(contract)); err != nil {
		t.Fatal(err)
	}
	// A delta is applied from a second file of the same schema, attached.
	dataset.SetMaxOpenConns(1)
	deltaPath := filepath.Join(t.TempDir(), "delta.sqlite")
	delta, err := sql.Open("sqlite", deltaPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = delta.Exec(string(contract)); err != nil {
		t.Fatal(err)
	}
	delta.Close()
	if _, err = dataset.Exec(`ATTACH DATABASE ? AS delta`, deltaPath); err != nil {
		t.Fatal(err)
	}
	// Guide ingestion uses a separate SQLite spool. Extract its actual
	// literal DDL and prepare that file's queries against that schema, rather
	// than excluding a table name from the baseline guard globally.

	spools := map[string]*sql.DB{}
	spoolDDL := regexp.MustCompile(`(?i)^CREATE\s+(?:TABLE|INDEX)\s`)
	for _, relative := range []string{"livechannels/programme_spool.go"} {
		spoolFile := filepath.Join("..", relative)
		spool, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "spool.sqlite"))
		if err != nil {
			t.Fatal(err)
		}
		defer spool.Close()
		spools["internal/"+relative+":"] = spool
		file, err := parser.ParseFile(token.NewFileSet(), spoolFile, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		spoolStatements := 0
		ast.Inspect(file, func(node ast.Node) bool {
			expression, ok := node.(ast.Expr)
			if !ok {
				return true
			}
			text, ok := literal(expression)
			if !ok {
				return true
			}
			if spoolDDL.MatchString(text) {
				spoolStatements++
				if _, err := spool.Exec(text); err != nil {
					t.Fatal(err)
				}
			}
			return false
		})
		if spoolStatements == 0 {
			t.Fatalf("%s no longer says what its spool holds", spoolFile)
		}
	}
	// Restore salvage reads the damaged copy attached beside a fresh baseline
	// (salvage.go): prepare its statements against exactly that shape.
	salvageMain, err := Open(filepath.Join(t.TempDir(), "salvage.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer salvageMain.Close()
	damagedPath := filepath.Join(t.TempDir(), "damaged.sqlite")
	damaged, err := Open(damagedPath)
	if err != nil {
		t.Fatal(err)
	}
	damaged.Close()
	salvageMain.SetMaxOpenConns(1)
	if _, err = salvageMain.Exec(`ATTACH DATABASE ? AS damaged`, damagedPath); err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, s := range statements {
		target := db
		for prefix, spool := range spools {
			if strings.HasPrefix(filepath.ToSlash(s.where), prefix) {
				target = spool
				break
			}
		}
		if strings.HasPrefix(filepath.ToSlash(s.where), "internal/persistence/salvage") {
			target = salvageMain
		}
		prepared, err := prepareProductionStatement(target, dataset, s, transient)
		if prepared {
			checked++
		}
		if err != nil {
			t.Errorf("%s: %v\n\t%s", s.where, err, strings.Join(strings.Fields(s.text), " "))
		}
	}
	if checked < 1000 {
		t.Fatalf("only %d statements prepared; the scan is broken", checked)
	}
	t.Logf("%d statements prepared", checked)
}

var missingSQLReference = regexp.MustCompile(`no such (table|column|function)`)
var missingSQLName = regexp.MustCompile(`no such (table|column|function): (?:\w+\.)?(\w+)`)

func prepareProductionStatement(db, dataset *sql.DB, s productionStatement, transient map[string]bool) (bool, error) {
	stmt, err := db.Prepare(s.text)
	if err == nil {
		stmt.Close()
		return true, nil
	}
	if !missingSQLReference.MatchString(err.Error()) {
		return false, nil // incomplete runtime SQL remains covered by its owner
	}
	if dataset != nil && strings.Contains(filepath.ToSlash(s.where), "internal/enrichment/") {
		if other, e := dataset.Prepare(s.text); e == nil {
			other.Close()
			return true, nil
		}
	}
	if m := missingSQLName.FindStringSubmatch(err.Error()); m != nil && m[1] != "column" && transient[m[1]+":"+strings.ToLower(m[2])] {
		return false, nil
	}
	return false, err
}

// A constant used exclusively as an operand of string concatenation is a
// fragment, not a separate statement. Fold it into its consumers instead. If
// it is also passed directly to a query, retain its own schema check. This is
// source evidence, not a SQL heuristic: SELECT without FROM can be valid SQL,
// and SELECT v.id passed to a query must still fail for the missing column.
func concatenatedConstants(files []*ast.File) map[ast.Expr]bool {
	parents := map[ast.Node]ast.Node{}
	var stack []ast.Node
	for _, file := range files {
		ast.Inspect(file, func(n ast.Node) bool {
			if n == nil {
				stack = stack[:len(stack)-1]
				return false
			}
			if len(stack) != 0 {
				parents[n] = stack[len(stack)-1]
			}
			stack = append(stack, n)
			return true
		})
	}
	type uses struct {
		expression ast.Expr
		count      int
		onlyAdd    bool
	}
	constants := map[*ast.Object]*uses{}
	for _, file := range files {
		ast.Inspect(file, func(n ast.Node) bool {
			id, ok := n.(*ast.Ident)
			if !ok || id.Obj == nil || id.Obj.Kind != ast.Con {
				return true
			}
			spec, ok := id.Obj.Decl.(*ast.ValueSpec)
			if !ok || len(spec.Names) != len(spec.Values) {
				return true
			}
			var value ast.Expr
			for i, name := range spec.Names {
				if name == id {
					return true // declaration, not a use
				}
				if name.Obj == id.Obj {
					value = spec.Values[i]
				}
			}
			if value == nil {
				return true
			}
			u := constants[id.Obj]
			if u == nil {
				u = &uses{expression: value, onlyAdd: true}
				constants[id.Obj] = u
			}
			u.count++
			parent := parents[id]
			for {
				if _, ok := parent.(*ast.ParenExpr); !ok {
					break
				}
				parent = parents[parent]
			}
			add, ok := parent.(*ast.BinaryExpr)
			u.onlyAdd = u.onlyAdd && ok && add.Op == token.ADD
			return true
		})
	}
	fragments := map[ast.Expr]bool{}
	for _, u := range constants {
		if u.count > 0 && u.onlyAdd {
			fragments[u.expression] = true
		}
	}
	return fragments
}

// literal folds literals and resolved string constants joined with +.
func literal(e ast.Expr) (string, bool) {
	return constantLiteral(e, map[*ast.Object]bool{})
}

func constantLiteral(e ast.Expr, seen map[*ast.Object]bool) (string, bool) {
	switch v := e.(type) {
	case *ast.BasicLit:
		if v.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(v.Value)
		return s, err == nil
	case *ast.Ident:
		if v.Obj == nil || v.Obj.Kind != ast.Con || seen[v.Obj] {
			return "", false
		}
		spec, ok := v.Obj.Decl.(*ast.ValueSpec)
		if !ok || len(spec.Names) != len(spec.Values) {
			return "", false
		}
		seen[v.Obj] = true
		defer delete(seen, v.Obj)
		for i, name := range spec.Names {
			if name.Obj == v.Obj {
				return constantLiteral(spec.Values[i], seen)
			}
		}
	case *ast.ParenExpr:
		return constantLiteral(v.X, seen)
	case *ast.BinaryExpr:
		if v.Op != token.ADD {
			return "", false
		}
		a, ok := constantLiteral(v.X, seen)
		if !ok {
			return "", false
		}
		b, ok := constantLiteral(v.Y, seen)
		return a + b, ok
	}
	return "", false
}

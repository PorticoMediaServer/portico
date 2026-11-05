package persistence

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/constant"
	"go/parser"
	"go/token"
	"go/types"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Before removing a table's triggers, its census must contain exactly one
// owner. Existing multi-package writers are debt, not new exemptions: every
// added writer fails this guard, including after its last trigger is removed.
// Every SQL execution site with an unresolved argument is an unknown writer,
// including Query/QueryRow/Prepare and dbwork wrappers. Dynamic SQL is pinned
// by file hash for explicit source review; it never becomes a write to no table. A migrated family must also
// remove or constrain any such helper that can write its tables.
type writerCensus struct {
	Tables   map[string][]string      `json:"tables"`
	Dynamic  map[string]derivedSQLPin `json:"dynamic_sql_files"`
	Migrated map[string]string        `json:"migrated_owners"`
	// Scan diagnostics cannot be waived by a checked-in pin or manifest edit.
	UnresolvedReflection []string `json:"-"`
}

// Every unresolved execution site needs an explicit, source-bound effect pin.
// */* means unknown table AND verb, not permission: it blocks every competing
// migrated owner. Only source review can narrow these to named table/verbs.
type derivedSQLEffect struct {
	Table string `json:"table"`
	Verb  string `json:"verb"`
}
type derivedSQLPin struct {
	SHA256  string             `json:"sha256"`
	Effects []derivedSQLEffect `json:"effects"`
}

var updateDerivedWriters = flag.Bool("update-derived-writers", false, "rewrite the derived-writer census for deliberate review")

func TestDerivedTableWritersHaveDeclaredOwners(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	const manifest = "testdata/derived-writers.json"
	want := writerCensus{}
	raw, err := os.ReadFile(manifest)
	if err == nil {
		if err = json.Unmarshal(raw, &want); err != nil {
			t.Fatal(err)
		}
	} else if !*updateDerivedWriters {
		t.Fatal(err)
	}
	tables := map[string]bool{}
	for name := range want.Tables {
		tables[name] = true
	} // keep guarding after trigger drop
	on := regexp.MustCompile(`(?i)CREATE\s+TRIGGER\s+\w+\s+(?:BEFORE|AFTER|INSTEAD\s+OF)\s+(?:INSERT|DELETE|UPDATE(?:\s+OF\s+[\w\s,]+)?)\s+ON\s+(\w+)`)
	for _, m := range on.FindAllStringSubmatch(BaselineSQL(), -1) {
		tables[strings.ToLower(m[1])] = true
	}
	got, err := scanDerivedWritersLogged(root, tables, t.Logf)
	if err != nil {
		t.Fatal(err)
	}

	got.Migrated = want.Migrated
	if got.Migrated == nil {
		got.Migrated = map[string]string{}
	}
	for file, actual := range got.Dynamic {
		pin := want.Dynamic[file]
		if pin.SHA256 == actual.SHA256 {
			actual.Effects = pin.Effects
		}
		// Regeneration never carries a stale narrowing across changed source.
		// An unresolved file remains explicitly unknown until separately reviewed.
		if *updateDerivedWriters && len(actual.Effects) == 0 {
			actual.Effects = []derivedSQLEffect{{Table: "*", Verb: "*"}}
		}
		got.Dynamic[file] = actual
	}

	for _, problem := range derivedOwnershipProblems(got) {
		t.Error(problem)
	}
	if t.Failed() {
		return
	}
	if *updateDerivedWriters {
		data, err := json.MarshalIndent(got, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(manifest, append(data, '\n'), 0644); err != nil {
			t.Fatal(err)
		}
		return
	}
	if !reflect.DeepEqual(want, got) {
		for table, writers := range got.Tables {
			if !reflect.DeepEqual(want.Tables[table], writers) {
				t.Errorf("%s: writers %v, declared %v; consolidate to its owner before dropping triggers", table, writers, want.Tables[table])
			}
		}
		for file, hash := range got.Dynamic {
			if !reflect.DeepEqual(want.Dynamic[file], hash) {
				t.Errorf("%s: dynamic SQL changed; review its full target set and ownership", file)
			}
		}
		for file := range want.Dynamic {
			if got.Dynamic[file].SHA256 == "" {
				t.Errorf("remove retired dynamic SQL census entry %s", file)
			}
		}
		for table := range want.Tables {
			if _, ok := got.Tables[table]; !ok {
				t.Errorf("missing table census %s", table)
			}
		}
	}
}

// Dynamic targets default to unknown, which can reach ANY table. Merely
// pinning a helper's bytes is insufficient to adopt a table: every competing
// dynamic helper must have a source-reviewed complete target set (including
// its callers), or be removed/narrowed to the owner first.
func derivedOwnershipProblems(c writerCensus) []string {
	var problems []string
	for _, site := range c.UnresolvedReflection {
		problems = append(problems, "unresolved reflective selection "+site+"; resolve its types, a source pin cannot waive this")
	}
	verbs := map[string]bool{"*": true, "SELECT": true, "EXPLAIN": true, "INSERT": true, "UPDATE": true, "DELETE": true, "REPLACE": true, "CREATE": true, "ALTER": true, "DROP": true, "PRAGMA": true, "VACUUM": true}
	for file, pin := range c.Dynamic {
		if len(pin.SHA256) != 64 || len(pin.Effects) == 0 {
			problems = append(problems, "unknown SQL writer "+file+" requires an explicit source pin naming table and verb")
		}
		for _, effect := range pin.Effects {
			if (effect.Table != "*" && !derivedName.MatchString(effect.Table)) || !verbs[effect.Verb] {
				problems = append(problems, "invalid table/verb pin for "+file)
			}
		}
	}
	for table, owner := range c.Migrated {
		if !reflect.DeepEqual(c.Tables[table], []string{owner}) {
			problems = append(problems, fmt.Sprintf("migrated table %s must have sole owner %s, got %v", table, owner, c.Tables[table]))
		}
		for file, pin := range c.Dynamic {
			if filepath.ToSlash(filepath.Dir(file)) == owner {
				continue
			}
			mayWrite := len(pin.Effects) == 0
			for _, effect := range pin.Effects {
				if (effect.Table == "*" || effect.Table == table) && effect.Verb != "SELECT" && effect.Verb != "EXPLAIN" {
					mayWrite = true
				}
			}
			if mayWrite {
				problems = append(problems, fmt.Sprintf("dynamic writer %s can bypass %s owner %s", file, table, owner))
			}
		}
	}
	sort.Strings(problems)
	return problems
}

func scanDerivedWriters(root string, tables map[string]bool) (writerCensus, error) {
	return scanDerivedWritersLogged(root, tables, nil)
}

func scanDerivedWritersLogged(root string, tables map[string]bool, logf func(string, ...any)) (writerCensus, error) {
	started := time.Now()
	var sqlTypes, reflectionTypes, callableFlow time.Duration
	out := writerCensus{Tables: map[string][]string{}, Dynamic: map[string]derivedSQLPin{}}
	writers := map[string]map[string]bool{}
	for table := range tables {
		writers[table] = map[string]bool{}
		out.Tables[table] = []string{}
	}

	type sourceFile struct {
		file     *ast.File
		raw      []byte
		relative string
	}
	var files []sourceFile
	packages := map[string][]*ast.File{}
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" || d.Name() == "thirdparty" || strings.HasPrefix(d.Name(), ".") {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		// SCHEMA-V1-PHASE1A-2026-11-05.md assigns the separate store layer
		// its own private writer and static registry. This census guards the
		// old layer only; sibling prefixes such as storefront remain guarded.
		if strings.HasPrefix(rel, "internal/store/") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		file, err := parser.ParseFile(fset, path, raw, 0)
		if err != nil {
			return err
		}
		files = append(files, sourceFile{file, raw, rel})
		packages[filepath.Dir(rel)] = append(packages[filepath.Dir(rel)], file)
		return nil
	})
	if err != nil {
		return out, err
	}
	// Resolve only actual package constants, including siblings. Mutable locals,
	// parameters, helper results and ambiguous build-specific names stay unknown.
	for _, group := range packages {
		constants := map[string]*ast.Object{}
		for _, file := range group {
			for name, obj := range file.Scope.Objects {
				if obj.Kind == ast.Con {
					if _, exists := constants[name]; exists {
						constants[name] = nil
					} else {
						constants[name] = obj
					}
				}
			}
		}
		for _, file := range group {
			for _, id := range file.Unresolved {
				if obj := constants[id.Name]; obj != nil {
					id.Obj = obj
				}
			}
		}
	}
	typeLoader := newDerivedTypeLoader(root, fset, packages)
	reflectionMethods, err := derivedReflectionMethodNames(typeLoader.fallback)
	if err != nil {
		return out, err
	}
	reflectionLoaders, err := derivedReflectionLoaders(typeLoader, reflectionMethods)
	if err != nil {
		return out, err
	}
	defer func() {
		if logf != nil {
			var exports time.Duration
			for _, loader := range reflectionLoaders {
				exports += loader.exportDuration
			}
			logf("derived census timing: total=%s source_files=%d sql_types=%s profile_types=%s callable_flow=%s dependency_exports_subset=%s profiles=%d", time.Since(started).Round(time.Millisecond), len(files), sqlTypes.Round(time.Millisecond), reflectionTypes.Round(time.Millisecond), callableFlow.Round(time.Millisecond), exports.Round(time.Millisecond), len(reflectionLoaders))
		}
	}()
	markDynamic := func(source sourceFile) {
		if _, exists := out.Dynamic[source.relative]; !exists {
			sum := sha256.Sum256(source.raw)
			out.Dynamic[source.relative] = derivedSQLPin{SHA256: hex.EncodeToString(sum[:])}
		}
	}
	byName := map[string]sourceFile{}
	importsReflect := map[string]bool{}
	for _, source := range files {
		byName[source.relative] = source
		for _, spec := range source.file.Imports {
			if path, err := strconv.Unquote(spec.Path.Value); err != nil || path == "reflect" {
				importsReflect[source.relative] = true
				markDynamic(source)
			}
		}
	}
	// Reuse one build profile's checked types for both reflection classification
	// and callable provenance, then release it before checking the next profile.
	// Retaining sixteen full expression graphs needlessly multiplies peak heap.
	for _, loader := range reflectionLoaders {
		profileStarted := time.Now()
		flagged := map[string]bool{}
		for name := range importsReflect {
			flagged[name] = true
		}
		for _, source := range files {
			file := source.file
			match, err := loader.matches(file)
			if err != nil {
				return out, err
			}
			if !match && loader.context != nil {
				continue
			}
			info := loader.forFile(file)
			if loader.exportErr != nil {
				return out, loader.exportErr
			}
			signatures, err := loader.reflectiveSignatures()
			if err != nil {
				return out, err
			}
			ast.Inspect(file, func(node ast.Node) bool {
				if expr, ok := node.(ast.Expr); ok {
					if reflective, unresolved := derivedReflectionSurface(expr, info, reflectionMethods, signatures); reflective {
						markDynamic(source)
						flagged[source.relative] = true
						if unresolved {
							pos := fset.Position(expr.Pos())
							out.UnresolvedReflection = append(out.UnresolvedReflection, fmt.Sprintf("%s:%d:%d", source.relative, pos.Line, pos.Column))
						}
					}
				}
				return true
			})
		}
		reflectionTypes += time.Since(profileStarted)
		callableStarted := time.Now()
		callers, err := derivedCallableFiles(loader, flagged)
		if err != nil {
			return out, err
		}
		for file := range callers {
			markDynamic(byName[file])
		}
		callableFlow += time.Since(callableStarted)
		if loader.context != nil {
			loader.infos = nil
			loader.packages = nil
			loader.fallback = nil
			loader.reflectionSignatures = nil
		}
	}
	for _, source := range files {
		file, rel := source.file, source.relative
		owner := filepath.ToSlash(filepath.Dir(rel))
		typeStarted := time.Now()
		info := typeLoader.forFile(file)
		sqlTypes += time.Since(typeStarted)
		dynamic := func() {
			if _, recorded := out.Dynamic[rel]; recorded {
				return
			}
			sum := sha256.Sum256(source.raw)
			out.Dynamic[rel] = derivedSQLPin{SHA256: hex.EncodeToString(sum[:])}
		}
		record := func(sql string) bool {
			targets, unknown := derivedWriteTargets(sql)
			for _, table := range targets {
				if tables[table] {
					writers[table][owner] = true
				}
			}
			return unknown
		}
		parents := map[ast.Node]ast.Node{}
		var stack []ast.Node
		ast.Inspect(file, func(n ast.Node) bool {
			if n == nil {
				stack = stack[:len(stack)-1]
				return true
			}
			if len(stack) > 0 {
				parents[n] = stack[len(stack)-1]
			}
			stack = append(stack, n)
			return true
		})
		ast.Inspect(file, func(node ast.Node) bool {
			if call, ok := node.(*ast.CallExpr); ok {
				if index, sqlCall := derivedSQLSurface(call.Fun, info); sqlCall {
					if index < 0 || index >= len(call.Args) {
						dynamic()
					} else {
						value := info.Types[call.Args[index]].Value
						if value == nil || value.Kind() != constant.String {
							dynamic()
						} else if record(constant.StringVal(value)) {
							dynamic()
						}
					}
				}
			}
			expr, ok := node.(ast.Expr)
			if !ok {
				return true
			}
			// A captured method/function value can execute SQL without a selector at
			// the eventual call. Treat the escape as unknown instead of losing it.
			if _, sqlSurface := derivedSQLSurface(expr, info); sqlSurface {
				current := node
				for {
					if paren, ok := parents[current].(*ast.ParenExpr); ok {
						current = paren
						continue
					}
					break
				}
				call, isCall := parents[current].(*ast.CallExpr)
				declaration := false
				if field, ok := parents[current].(*ast.Field); ok {
					for _, name := range field.Names {
						if name == current {
							declaration = true
						}
					}
				}
				if selector, ok := parents[current].(*ast.SelectorExpr); ok && selector.Sel == current {
					declaration = true
				}
				if fn, ok := parents[current].(*ast.FuncDecl); ok && fn.Name == current {
					declaration = true
				}
				if !declaration && (!isCall || call.Fun != current) {
					dynamic()
				}
			}
			if _, ident := expr.(*ast.Ident); ident {
				return true
			}
			sql, constant := literal(expr)
			if !constant {
				return true
			}
			if record(sql) {
				// A constant fragment may be part of a complete query declared elsewhere.
				// Its actual execution is checked above; a fragment in a runtime builder
				// remains unknown even when no execution is present in this file.
				inConstant := false
				for parent := parents[node]; parent != nil; parent = parents[parent] {
					if declaration, ok := parent.(*ast.GenDecl); ok {
						inConstant = declaration.Tok == token.CONST
						break
					}
					if _, ok := parent.(*ast.FuncDecl); ok {
						break
					}
				}
				if !inConstant {
					dynamic()
				}
			}
			return false
		})
	}

	for table, owners := range writers {
		for owner := range owners {
			out.Tables[table] = append(out.Tables[table], owner)
		}
		sort.Strings(out.Tables[table])
	}
	return out, err
}

// Names identify candidate execution surfaces only. Parameter positions come
// exclusively from typed signatures/selections; unresolved types stay unknown.
func derivedSQLSurface(expr ast.Expr, info *types.Info) (int, bool) {
	for {
		p, ok := expr.(*ast.ParenExpr)
		if !ok {
			break
		}
		expr = p.X
	}
	name := ""
	switch x := expr.(type) {
	case *ast.SelectorExpr:
		name = x.Sel.Name
	case *ast.Ident:
		name = x.Name
	}
	switch name {
	case "Exec", "Query", "QueryRow", "Prepare", "ExecContext", "QueryContext", "QueryRowContext", "PrepareContext", "ExecWrite":
		return derivedSQLParameter(expr, info), true
	}
	return -1, false
}

// Tokenize SQL so strings and comments cannot masquerade as writes. Quoted
// identifiers, schema-qualified names, CTEs, upserts and multiple statements
// are all seen; a format hole or incomplete target requires explicit review.
var derivedTokens = regexp.MustCompile("(?s)--[^\\n]*(?:\\n|$)|/\\*.*?\\*/|'(?:''|[^'])*'|\"(?:\"\"|[^\"])*\"|`[^`]*`|\\[[^]]*\\]|[A-Za-z_][A-Za-z_0-9]*|[^\\s]")
var derivedName = regexp.MustCompile(`^[a-z_][a-z_0-9]*$`)

func derivedWriteTargets(sql string) (targets []string, dynamic bool) {
	tokens := []string{}
	for _, part := range derivedTokens.FindAllString(sql, -1) {
		if strings.HasPrefix(part, "--") || strings.HasPrefix(part, "/*") {
			continue
		}
		if strings.HasPrefix(part, "'") {
			part = "#" + strings.Trim(part, "'")
		}
		tokens = append(tokens, strings.ToLower(strings.Trim(part, "\"`[]")))
	}
	for i, word := range tokens {
		j := i + 1
		switch word {
		case "insert", "replace":
			if j < len(tokens) && tokens[j] == "or" {
				j += 2
			}
			if j >= len(tokens) || tokens[j] != "into" {
				continue
			}
			j++
		case "delete":
			if j >= len(tokens) || tokens[j] != "from" {
				continue
			}
			j++
		case "update":
			if i > 0 && (tokens[i-1] == "do" || tokens[i-1] == "on" || tokens[i-1] == "after" || tokens[i-1] == "before") {
				continue
			}
			if j < len(tokens) && tokens[j] == "or" {
				j += 2
			}
			if j >= len(tokens) && strings.TrimSpace(sql) == sql {
				continue
			} // keyword classifier, not SQL fragment
		default:
			continue
		}
		if j < len(tokens) && j+1 < len(tokens) && tokens[j+1] == "." {
			j += 2
		}
		if j < len(tokens) {
			tokens[j] = strings.TrimPrefix(tokens[j], "#")
		}
		if j >= len(tokens) || !derivedName.MatchString(tokens[j]) {
			dynamic = true
			continue
		}
		if j+1 == len(tokens) || tokens[j+1] == "%" {
			dynamic = true
		}
		targets = append(targets, tokens[j])
	}
	sort.Strings(targets)
	return targets, dynamic
}

func TestDerivedWriterGuardExcludesOnlySchemaV1StorePrefix(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"internal/store", "internal/store/schema", "internal/store/writer", "internal/storefront", "internal/stores", "other/internal/store"} {
		path := filepath.Join(root, dir, "writer.go")
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		body := "package example\nconst q = `UPDATE catalog_browse_rows SET title=?`"
		if err := os.WriteFile(path, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := scanDerivedWriters(root, map[string]bool{"catalog_browse_rows": true})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"internal/storefront", "internal/stores", "other/internal/store"}
	if !reflect.DeepEqual(got.Tables["catalog_browse_rows"], want) {
		t.Fatalf("census owners = %v, want %v", got.Tables["catalog_browse_rows"], want)
	}
}

func TestDerivedWriterGuardSeesOtherPackagesAndDynamicTargets(t *testing.T) {
	root := t.TempDir()
	for name, body := range map[string]string{
		"owner/a.go":    "package owner\nconst q = `INSERT INTO catalog_browse_rows(entity_id) VALUES(?) ON CONFLICT DO UPDATE SET entity_id=excluded.entity_id`",
		"bypass/b.go":   "package bypass\nconst q = `WITH x AS(SELECT 1) UPDATE main.\"catalog_browse_rows\" SET title='hidden'`",
		"dynamic/c.go":  "package dynamic\nfunc q(table string)string{return `DELETE FROM `+table}",
		"comments/d.go": "package comments\n// UPDATE catalog_browse_rows SET title='ignored'\nconst q = `SELECT 'DELETE FROM catalog_browse_rows' /* UPDATE catalog_browse_rows */`",
	} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := scanDerivedWriters(root, map[string]bool{"catalog_browse_rows": true})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Tables["catalog_browse_rows"], []string{"bypass", "owner"}) || len(got.Dynamic) != 1 || got.Dynamic["dynamic/c.go"].SHA256 == "" {
		t.Fatalf("guard missed bypass: %+v", got)
	}
	pinUnknownDerivedSQL(&got)
	got.Migrated = map[string]string{"catalog_browse_rows": "owner"}
	if problems := derivedOwnershipProblems(got); len(problems) != 2 {
		t.Fatalf("ownership failed to reject a static and dynamic bypass: %v", problems)
	}
	got.Tables["catalog_browse_rows"] = []string{"owner"}
	pinDerivedSQL(&got, "dynamic/c.go", "unrelated_table", "UPDATE")
	if problems := derivedOwnershipProblems(got); len(problems) != 0 {
		t.Fatal(problems)
	}
	pinDerivedSQL(&got, "dynamic/c.go", "catalog_browse_rows", "UPDATE")
	if problems := derivedOwnershipProblems(got); len(problems) != 1 {
		t.Fatalf("reviewed competing target bypassed owner: %v", problems)
	}
	for _, q := range []string{`REPLACE INTO [catalog_browse_rows] VALUES(?)`, `UPDATE 'catalog_browse_rows' SET title='literal'`, `INSERT OR IGNORE INTO ` + "`catalog_browse_rows`" + ` VALUES(?)`, `DELETE FROM catalog_browse_rows; UPDATE catalog_browse_rows SET title=?`} {
		targets, _ := derivedWriteTargets(q)
		if len(targets) == 0 || targets[0] != "catalog_browse_rows" {
			t.Fatal(q, targets)
		}
	}
}

// Review regression: verb and table supplied as helper arguments must not hide
// the competing writer merely because neither literal contains a SQL sentence.
func TestDerivedWriterGuardRejectsArgumentBuiltSQL(t *testing.T) {
	root := t.TempDir()
	for name, body := range map[string]string{
		"owner/a.go": "package owner\nconst q = `UPDATE catalog_browse_rows SET title=?`",
		"bypass/b.go": `package bypass
import "database/sql"
func build(verb, table string) string { return verb+" "+table+" SET title=?" }
func write(db *sql.DB) { db.Exec(build("UPDATE", "catalog_browse_rows"), "wrong") }
`,
	} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := scanDerivedWriters(root, map[string]bool{"catalog_browse_rows": true})
	if err != nil {
		t.Fatal(err)
	}
	pinUnknownDerivedSQL(&got)
	got.Migrated = map[string]string{"catalog_browse_rows": "owner"}
	if got.Dynamic["bypass/b.go"].SHA256 == "" || len(derivedOwnershipProblems(got)) == 0 {
		t.Fatalf("argument-built SQL bypassed migrated owner: %+v", got)
	}
	// A hash alone is not permission. Only a complete, reviewed disjoint target
	// set can narrow an unknown writer; naming this table must continue to block.
	got.Tables["inventory_objects"] = []string{"owner"}
	got.Migrated["inventory_objects"] = "owner"
	if problems := derivedOwnershipProblems(got); len(problems) != 2 {
		t.Fatalf("unknown SQL must block both tables, got %v", problems)
	}
	delete(got.Migrated, "inventory_objects")
	pinDerivedSQL(&got, "bypass/b.go", "catalog_browse_rows", "UPDATE")
	if len(derivedOwnershipProblems(got)) == 0 {
		t.Fatal("competing reviewed target bypassed migrated owner")
	}
	pinDerivedSQL(&got, "bypass/b.go", "unrelated", "UPDATE")
	if problems := derivedOwnershipProblems(got); len(problems) != 0 {
		t.Fatal(problems)
	}
}

func TestDerivedWriterGuardChecksEverySQLExecutionSurface(t *testing.T) {
	for _, method := range []string{"Exec", "Query", "QueryRow", "Prepare", "ExecContext", "QueryContext", "QueryRowContext", "PrepareContext"} {
		t.Run(method, func(t *testing.T) {
			args := `build("UPDATE", "catalog_browse_rows")`
			if strings.HasSuffix(method, "Context") {
				args = "ctx, " + args
			}
			assertUnknownDerivedSQL(t, `package bypass
func build(verb,table string)string{return verb+" "+table+" SET title=?"}
func run(db DB,ctx Context){db.`+method+`(`+args+`)}`)
		})
	}
	for _, tc := range []struct{ name, source string }{
		{"dbwork ExecWrite", `package bypass
import work "github.com/PorticoMediaServer/portico/server/internal/dbwork"
func run(){work.ExecWrite(ctx,db,class,build("UPDATE","catalog_browse_rows"))}`},
		{"dbwork Query", `package bypass
import "github.com/PorticoMediaServer/portico/server/internal/dbwork"
func run(){dbwork.Query(ctx,db,build("UPDATE","catalog_browse_rows"))}`},
		{"dbwork QueryRow", `package bypass
import work "github.com/PorticoMediaServer/portico/server/internal/dbwork"
func run(){work.QueryRow(ctx,db,build("UPDATE","catalog_browse_rows"))}`},
		{"dbwork dot import", `package bypass
import . "github.com/PorticoMediaServer/portico/server/internal/dbwork"
func run(){ExecWrite(ctx,db,class,build("UPDATE","catalog_browse_rows"))}`},
		{"mutable SQL", `package bypass
var query = "SELECT 1"
func run(){db.Query(query)}`},
		{"bound method", `package bypass
func run(){execute:=db.Exec;execute(build("UPDATE","catalog_browse_rows"))}`},
		{"wrapper function value", `package bypass
import work "github.com/PorticoMediaServer/portico/server/internal/dbwork"
func run(){execute:=work.ExecWrite;execute(ctx,db,class,build("UPDATE","catalog_browse_rows"))}`},
		{"mutable wrapper alias shadows import", `package bypass
import work "github.com/PorticoMediaServer/portico/server/internal/dbwork"
func run(){work:=db; work.Query(build("UPDATE","catalog_browse_rows"))}`},
		{"context is not SQL", `package bypass
func run(){db.ExecContext("SELECT 1", build("UPDATE","catalog_browse_rows"))}`},
		{"dbwork context is not SQL", `package bypass
import work "github.com/PorticoMediaServer/portico/server/internal/dbwork"
func run(){work.Query("SELECT 1", db, build("UPDATE","catalog_browse_rows"))}`},
		{"SQL method expression", `package bypass
import "database/sql"
func run(){(*sql.DB).QueryRowContext(db,"SELECT 1",build("UPDATE","catalog_browse_rows"))}`},
		{"prepared no SQL argument", `package bypass
func run(){stmt.Exec()}`},
	} {
		t.Run(tc.name, func(t *testing.T) { assertUnknownDerivedSQL(t, tc.source) })
	}
}

func assertUnknownDerivedSQL(t *testing.T, source string) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "bypass.go"), []byte(source), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := scanDerivedWriters(root, map[string]bool{"catalog_browse_rows": true})
	if err != nil {
		t.Fatal(err)
	}
	if got.Dynamic["bypass.go"].SHA256 == "" {
		t.Fatalf("SQL execution argument was silently omitted: %s", source)
	}
	got.Tables["catalog_browse_rows"] = []string{"owner"}
	pinUnknownDerivedSQL(&got)
	got.Migrated = map[string]string{"catalog_browse_rows": "owner"}
	if len(derivedOwnershipProblems(got)) == 0 {
		t.Fatal("unknown SQL did not block migrated ownership")
	}
}

func TestDerivedWriterGuardResolvesCompileTimeSQL(t *testing.T) {
	root := t.TempDir()
	for name, source := range map[string]string{
		"constants.go": "package owner\nconst verb=\"UPDATE \"\nconst table=\"catalog_browse_rows\"\nconst suffix=\" SET title=?\"\n",
		"calls.go": `package owner
import work "github.com/PorticoMediaServer/portico/server/internal/dbwork"
func run(){
 db.Exec(verb+table+suffix)
 db.Query("SELECT 1")
 db.QueryRowContext(ctx,"UPDATE catalog_browse_rows SET title=? RETURNING title")
 db.PrepareContext(ctx,(verb+table+suffix))
 work.Query(ctx,db,verb+table+suffix)
 work.QueryRow(ctx,db,"SELECT 1")
 work.ExecWrite(ctx,db,class,verb+table+suffix)
}`,
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(source), 0644); err != nil {
			t.Fatal(err)
		}
	}
	writeDerivedTypedFixture(t, root, "owner")
	got, err := scanDerivedWriters(root, map[string]bool{"catalog_browse_rows": true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Dynamic) != 0 || !reflect.DeepEqual(got.Tables["catalog_browse_rows"], []string{"."}) {
		t.Fatalf("compile-time SQL was not resolved: %+v", got)
	}
}

func TestDerivedWriterGuardResolvesEveryConstantExecutionSurface(t *testing.T) {
	for _, method := range []string{"Exec", "Query", "QueryRow", "Prepare", "ExecContext", "QueryContext", "QueryRowContext", "PrepareContext"} {
		t.Run(method, func(t *testing.T) {
			root := t.TempDir()
			args := "query"
			if strings.HasSuffix(method, "Context") {
				args = "ctx,query"
			}
			source := `package owner
const query="UPDATE catalog_browse_rows SET title=?"
func run(){db.` + method + `(` + args + `)}`
			if err := os.WriteFile(filepath.Join(root, "owner.go"), []byte(source), 0644); err != nil {
				t.Fatal(err)
			}
			writeDerivedTypedFixture(t, root, "owner")
			got, err := scanDerivedWriters(root, map[string]bool{"catalog_browse_rows": true})
			if err != nil {
				t.Fatal(err)
			}
			if len(got.Dynamic) != 0 || !reflect.DeepEqual(got.Tables["catalog_browse_rows"], []string{"."}) {
				t.Fatalf("constant %s not resolved: %+v", method, got)
			}
		})
	}
	for _, source := range []string{
		`package dbwork
const query="UPDATE catalog_browse_rows SET title=?"
func run(){ExecWrite(ctx,db,class,query);Query(ctx,db,query);QueryRow(ctx,db,query)}`,
		`package owner
import . "github.com/PorticoMediaServer/portico/server/internal/dbwork"
const query="UPDATE catalog_browse_rows SET title=?"
func run(){ExecWrite(ctx,db,class,query);Query(ctx,db,query);QueryRow(ctx,db,query)}`,
		`package owner
import "database/sql"
const query="UPDATE catalog_browse_rows SET title=?"
func run(){(*sql.DB).QueryRowContext(db,ctx,query)}`,
	} {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "owner.go"), []byte(source), 0644); err != nil {
			t.Fatal(err)
		}
		pkg := "owner"
		if strings.HasPrefix(source, "package dbwork") {
			pkg = "dbwork"
		}
		writeDerivedTypedFixture(t, root, pkg)
		got, err := scanDerivedWriters(root, map[string]bool{"catalog_browse_rows": true})
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Dynamic) != 0 || !reflect.DeepEqual(got.Tables["catalog_browse_rows"], []string{"."}) {
			t.Fatalf("constant wrapper/method expression not resolved: %+v", got)
		}
	}
}

func pinDerivedSQL(c *writerCensus, file, table, verb string) {
	pin := c.Dynamic[file]
	pin.Effects = []derivedSQLEffect{{Table: table, Verb: verb}}
	c.Dynamic[file] = pin
}
func pinUnknownDerivedSQL(c *writerCensus) {
	for file := range c.Dynamic {
		pinDerivedSQL(c, file, "*", "*")
	}
}

func TestDerivedWriterGuardRequiresTableAndVerbPins(t *testing.T) {
	root := t.TempDir()
	source := `package bypass;func run(){db.Exec(build("UPDATE","catalog_browse_rows"))}`
	if err := os.WriteFile(filepath.Join(root, "bypass.go"), []byte(source), 0644); err != nil {
		t.Fatal(err)
	}
	c, err := scanDerivedWriters(root, map[string]bool{"catalog_browse_rows": true})
	if err != nil {
		t.Fatal(err)
	}
	if len(derivedOwnershipProblems(c)) == 0 {
		t.Fatal("unresolved execution passed census without a pin")
	}
	for _, effect := range []derivedSQLEffect{{Table: "catalog_browse_rows"}, {Verb: "UPDATE"}} {
		pin := c.Dynamic["bypass.go"]
		pin.Effects = []derivedSQLEffect{effect}
		c.Dynamic["bypass.go"] = pin
		if len(derivedOwnershipProblems(c)) == 0 {
			t.Fatal("incomplete table/verb pin accepted")
		}
	}
	pinUnknownDerivedSQL(&c)
	if problems := derivedOwnershipProblems(c); len(problems) != 0 {
		t.Fatal(problems)
	}
	c.Tables["catalog_browse_rows"] = []string{"owner"}
	c.Migrated = map[string]string{"catalog_browse_rows": "owner"}
	if len(derivedOwnershipProblems(c)) == 0 {
		t.Fatal("all-table/all-verb pin granted migrated ownership")
	}
	pinDerivedSQL(&c, "bypass.go", "catalog_browse_rows", "SELECT")
	if problems := derivedOwnershipProblems(c); len(problems) != 0 {
		t.Fatal(problems)
	}
}

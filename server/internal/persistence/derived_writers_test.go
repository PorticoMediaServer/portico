package persistence

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Before removing a table's triggers, its census must contain exactly one
// owner. Existing multi-package writers are debt, not new exemptions: every
// added writer fails this guard, including after its last trigger is removed.
// Dynamic-target SQL is pinned by file hash for explicit source review; it is
// never silently treated as a write to no table. A migrated family must also
// remove or constrain any such helper that can write its tables.
type writerCensus struct {
	Tables         map[string][]string `json:"tables"`
	Dynamic        map[string]string   `json:"dynamic_sql_files"`
	Migrated       map[string]string   `json:"migrated_owners"`
	DynamicTargets map[string][]string `json:"reviewed_dynamic_targets"`
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
	got, err := scanDerivedWriters(root, tables)
	if err != nil {
		t.Fatal(err)
	}
	got.Migrated, got.DynamicTargets = want.Migrated, want.DynamicTargets
	if got.Migrated == nil {
		got.Migrated = map[string]string{}
	}
	if got.DynamicTargets == nil {
		got.DynamicTargets = map[string][]string{}
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
			if want.Dynamic[file] != hash {
				t.Errorf("%s: dynamic SQL changed; review its full target set and ownership", file)
			}
		}
		for file := range want.Dynamic {
			if got.Dynamic[file] == "" {
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
	for table, owner := range c.Migrated {
		if !reflect.DeepEqual(c.Tables[table], []string{owner}) {
			problems = append(problems, fmt.Sprintf("migrated table %s must have sole owner %s, got %v", table, owner, c.Tables[table]))
		}
		for file := range c.Dynamic {
			if filepath.ToSlash(filepath.Dir(file)) == owner {
				continue
			}
			targets, reviewed := c.DynamicTargets[file]
			mayWrite := !reviewed
			for _, target := range targets {
				mayWrite = mayWrite || target == table
			}
			if mayWrite {
				problems = append(problems, fmt.Sprintf("dynamic writer %s can bypass %s owner %s", file, table, owner))
			}
		}
	}
	for file := range c.DynamicTargets {
		if _, ok := c.Dynamic[file]; !ok {
			problems = append(problems, "retired dynamic target review: "+file)
		}
	}
	sort.Strings(problems)
	return problems
}

func scanDerivedWriters(root string, tables map[string]bool) (writerCensus, error) {
	out := writerCensus{Tables: map[string][]string{}, Dynamic: map[string]string{}}
	writers := map[string]map[string]bool{}
	for table := range tables {
		writers[table] = map[string]bool{}
		out.Tables[table] = []string{}
	}
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
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		file, err := parser.ParseFile(fset, path, raw, 0)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		owner := filepath.ToSlash(filepath.Dir(rel))
		ast.Inspect(file, func(node ast.Node) bool {
			expr, ok := node.(ast.Expr)
			if !ok {
				return true
			}
			if _, isIdent := expr.(*ast.Ident); isIdent {
				return true
			}
			sql, ok := literal(expr)
			if !ok {
				return true
			}
			targets, dynamic := derivedWriteTargets(sql)
			for _, table := range targets {
				if tables[table] {
					writers[table][owner] = true
				}
			}
			if dynamic {
				sum := sha256.Sum256(raw)
				out.Dynamic[rel] = hex.EncodeToString(sum[:])
			}
			return false
		})
		return nil
	})
	for table, owners := range writers {
		for owner := range owners {
			out.Tables[table] = append(out.Tables[table], owner)
		}
		sort.Strings(out.Tables[table])
	}
	return out, err
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
	if !reflect.DeepEqual(got.Tables["catalog_browse_rows"], []string{"bypass", "owner"}) || len(got.Dynamic) != 1 || got.Dynamic["dynamic/c.go"] == "" {
		t.Fatalf("guard missed bypass: %+v", got)
	}
	got.Migrated = map[string]string{"catalog_browse_rows": "owner"}
	if problems := derivedOwnershipProblems(got); len(problems) != 2 {
		t.Fatalf("ownership failed to reject a static and dynamic bypass: %v", problems)
	}
	got.Tables["catalog_browse_rows"] = []string{"owner"}
	got.DynamicTargets = map[string][]string{"dynamic/c.go": {"unrelated_table"}}
	if problems := derivedOwnershipProblems(got); len(problems) != 0 {
		t.Fatal(problems)
	}
	got.DynamicTargets["dynamic/c.go"] = []string{"catalog_browse_rows"}
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

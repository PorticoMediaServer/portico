package persistence

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func derivedEffectFixture(t *testing.T) (*sql.DB, *derivedEffectGraph) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "effects.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, statement := range []string{
		`PRAGMA foreign_keys=ON`,
		`CREATE TABLE parent(id INTEGER PRIMARY KEY)`,
		`CREATE TABLE child(id INTEGER PRIMARY KEY,parent_id INTEGER REFERENCES parent(id) ON DELETE SET NULL ON UPDATE CASCADE,title TEXT)`,
		`CREATE TABLE audit(id INTEGER PRIMARY KEY,title TEXT)`,
		`CREATE TRIGGER child_changed AFTER UPDATE ON child WHEN NEW.title='never prune this edge' BEGIN INSERT INTO audit(title) VALUES(NEW.title); END`,
		`CREATE VIEW write_view AS SELECT id,title FROM audit`,
		`CREATE TRIGGER view_write INSTEAD OF INSERT ON write_view BEGIN INSERT INTO audit(id,title) VALUES(NEW.id,NEW.title); END`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(statement, err)
		}
	}
	g, err := derivedSchemaEffects(db)
	if err != nil {
		t.Fatal(err)
	}
	return db, g
}

func derivedProofLoader(t *testing.T, root string) (*derivedTypeLoader, *ast.File) {
	t.Helper()
	fset := token.NewFileSet()
	groups := map[string][]*ast.File{}
	var execution *ast.File
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, filepath.Dir(path))
		groups[rel] = append(groups[rel], file)
		if filepath.Base(path) == "calls.go" {
			execution = file
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return newDerivedTypeLoader(root, fset, groups), execution
}

func derivedProofFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, text := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func derivedProofForTest(t *testing.T, root string, g *derivedEffectGraph, kind string) derivedSQLPin {
	t.Helper()
	loader, file := derivedProofLoader(t, root)
	scan := &derivedProofScan{graph: g}
	proof, effects, err := scan.prove(loader, file, "calls.go", derivedSQLPin{Proof: &derivedSQLProof{Kind: kind}})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "calls.go"))
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(raw)
	return derivedSQLPin{SHA256: hex.EncodeToString(hash[:]), Effects: effects, Proof: proof, verified: true}
}

func TestDerivedWriterGuardDependencyClosureCannotInheritSiblingChanges(t *testing.T) {
	_, g := derivedEffectFixture(t)
	files := map[string]string{
		"calls.go":  `package owner; import "database/sql"; func run(db *sql.DB){db.Exec(build())}`,
		"helper.go": `package owner; func build() string { return "UPDATE "+target+" SET title=?" }`,
		"policy.go": `package owner; const target="child"`,
	}
	for _, change := range []string{"sibling constant", "helper body", "new sibling", "removed sibling", "mutable policy", "missing dependency", "schema change", "no proof"} {
		t.Run(change, func(t *testing.T) {
			root := derivedProofFiles(t, files)
			pin := derivedProofForTest(t, root, g, "sql")
			initialLoader, initialFile := derivedProofLoader(t, root)
			initial := &derivedProofScan{graph: g, pins: map[string]derivedSQLPin{"calls.go": pin}}
			accepted := initial.verify(initialLoader, initialFile, "calls.go", derivedSQLPin{SHA256: pin.SHA256})
			if len(initial.problems) != 0 || !reflect.DeepEqual(pin, accepted) {
				t.Fatal("valid closed builder did not qualify", initial.problems)
			}
			if !reflect.DeepEqual(pin.Effects, []derivedSQLEffect{{"audit", "INSERT"}, {"child", "UPDATE"}}) {
				t.Fatal("builder effects omitted trigger", pin.Effects)
			}
			write := func(name, source string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(root, name), []byte(source), 0600); err != nil {
					t.Fatal(err)
				}
			}
			switch change {
			case "sibling constant":
				write("policy.go", `package owner; const target="audit"`)
			case "helper body":
				write("helper.go", `package owner; func build() string { return "DELETE FROM "+target+" WHERE id=?" }`)
			case "new sibling":
				write("extra.go", `package owner; const extra="policy"`)
			case "removed sibling":
				if err := os.Remove(filepath.Join(root, "policy.go")); err != nil {
					t.Fatal(err)
				}
			case "mutable policy":
				write("policy.go", `package owner; var target="child"; func change(){ target="audit" }`)
			case "missing dependency":
				pin.Proof.Dependencies = pin.Proof.Dependencies[:1]
			case "schema change":
				pin.Proof.Schema = strings.Repeat("0", 64)
			case "no proof":
				pin.Proof = nil
			}
			loader, file := derivedProofLoader(t, root)
			scan := &derivedProofScan{graph: g, pins: map[string]derivedSQLPin{"calls.go": pin}}
			actual := scan.verify(loader, file, "calls.go", derivedSQLPin{SHA256: pin.SHA256})
			if len(scan.problems) == 0 || !derivedWildcard(actual) {
				t.Fatalf("stale proof accepted %s: %+v %v", change, actual, scan.problems)
			}
		})
	}
}

func TestDerivedWriterGuardProofCoversAllCallsAndRejectsOpenOrigins(t *testing.T) {
	_, g := derivedEffectFixture(t)
	for name, body := range map[string]string{
		"argument-built verb/table":   `func run(db *sql.DB,verb,table string){db.Exec(build(verb,table))};func build(verb,table string)string{return verb+" "+table+" SET title=?"}`,
		"unknown helper":              `func run(db *sql.DB,query string){db.Query(query)}`,
		"callback":                    `func run(db *sql.DB,query func()string){db.Exec(query())}`,
		"method alias":                `func run(db *sql.DB){execute:=db.Exec;execute("UPDATE child SET title=?")}`,
		"function alias":              `func run(db *sql.DB){execute:=func(){db.Exec("UPDATE child SET title=?")};execute()}`,
		"escaped method":              `func run(db *sql.DB)any{return db.Exec}`,
		"mixed sites":                 `func run(db *sql.DB,query string){db.Query("SELECT 1");db.Exec(query)}`,
		"generic function":            `func run[T ~func()](db *sql.DB,query T){query();db.Query("SELECT 1")}`,
		"reflection":                  `func run(db *sql.DB){reflect.ValueOf(db).MethodByName("Exec").Call(nil)}`,
		"unbound sibling constructor": `func run(db *sql.DB){db.Query("SELECT 1")};func other(table string)string{return "DELETE FROM "+table+" WHERE id=?"}`,
		"interface executor":          `type Executor interface{ Exec(string,...any)(sql.Result,error) };func run(db Executor){db.Exec("UPDATE child SET title=?")}`,
	} {
		t.Run(name, func(t *testing.T) {
			imports := `import "database/sql";`
			if name == "reflection" {
				imports += `import "reflect";`
			}
			root := derivedProofFiles(t, map[string]string{"calls.go": `package owner;` + imports + body})
			loader, file := derivedProofLoader(t, root)
			if _, _, err := (&derivedProofScan{graph: g}).prove(loader, file, "calls.go", derivedSQLPin{Proof: &derivedSQLProof{Kind: "sql"}}); err == nil {
				t.Fatal("open origin narrowed")
			}
		})
	}
	root := derivedProofFiles(t, map[string]string{"calls.go": `package owner;import "database/sql";func run(db *sql.DB){db.Query("SELECT 1");db.QueryRow("UPDATE child SET title=? RETURNING title");db.Exec("DELETE FROM parent WHERE id=?")}`})
	pin := derivedProofForTest(t, root, g, "sql")
	if !reflect.DeepEqual(pin.Effects, []derivedSQLEffect{{"audit", "INSERT"}, {"child", "UPDATE"}, {"parent", "DELETE"}}) {
		t.Fatal("incomplete file effect union", pin.Effects)
	}
}

func TestDerivedWriterGuardImportedPolicyChangeInvalidatesSameEffectProof(t *testing.T) {
	_, g := derivedEffectFixture(t)
	root := derivedProofFiles(t, map[string]string{
		"calls.go":         `package owner;import "database/sql";import "github.com/PorticoMediaServer/portico/server/policy";func run(db *sql.DB){db.Exec("UPDATE child SET title=? WHERE "+policy.Predicate)}`,
		"policy/policy.go": `package policy;const Predicate="id=?"`,
	})
	pin := derivedProofForTest(t, root, g, "sql")
	if err := os.WriteFile(filepath.Join(root, "policy/policy.go"), []byte(`package policy;const Predicate="id<>?"`), 0600); err != nil {
		t.Fatal(err)
	}
	loader, file := derivedProofLoader(t, root)
	scan := &derivedProofScan{graph: g, pins: map[string]derivedSQLPin{"calls.go": pin}}
	actual := scan.verify(loader, file, "calls.go", derivedSQLPin{SHA256: pin.SHA256})
	if len(scan.problems) == 0 || !derivedWildcard(actual) {
		t.Fatal("policy changed with identical table/verb effects but no stale-proof failure", actual)
	}
}

func TestDerivedWriterGuardResolvedNonSQLIdentityIsTruthful(t *testing.T) {
	_, g := derivedEffectFixture(t)
	root := derivedProofFiles(t, map[string]string{"calls.go": `package owner;import "net/url";func run(u *url.URL){u.Query()}`})
	pin := derivedProofForTest(t, root, g, "resolved_call")
	if len(pin.Effects) != 0 || !strings.Contains(strings.Join(pin.Proof.Callees, " "), "net/url") {
		t.Fatal("non-SQL identity invented an effect", pin)
	}
	for _, body := range []string{
		`package owner;import "database/sql";func run(u *sql.DB){u.Query("UPDATE child SET title=?")}`,
		`package owner;type Executor interface{ Query() };func run(u Executor){u.Query()}`,
	} {
		if err := os.WriteFile(filepath.Join(root, "calls.go"), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		loader, file := derivedProofLoader(t, root)
		if _, _, err := (&derivedProofScan{graph: g}).prove(loader, file, "calls.go", pin); err == nil {
			t.Fatal("SQL/interface lookalike inherited non-SQL proof")
		}
	}
}

func TestDerivedWriterGuardFixtureProofReusesRuntimeImportBoundary(t *testing.T) {
	_, g := derivedEffectFixture(t)
	root := derivedProofFiles(t, map[string]string{"internal/catalogtest/calls.go": `package catalogtest;func seed(){}`})
	loader, file := derivedProofLoader(t, root)
	scan := &derivedProofScan{graph: g}
	proof, effects, err := scan.prove(loader, file, "internal/catalogtest/calls.go", derivedSQLPin{Proof: &derivedSQLProof{Kind: "test_fixture"}})
	if err != nil || proof.Kind != "test_fixture" || !derivedWildcard(derivedSQLPin{Effects: effects}) {
		t.Fatal(proof, effects, err)
	}
	if err := os.WriteFile(filepath.Join(root, "runtime.go"), []byte("//go:build future_platform\n\npackage runtime;import _ \"github.com/PorticoMediaServer/portico/server/internal/catalogtest\""), 0600); err != nil {
		t.Fatal(err)
	}
	loader, file = derivedProofLoader(t, root)
	if _, _, err := scan.prove(loader, file, "internal/catalogtest/calls.go", derivedSQLPin{Proof: proof}); err == nil {
		t.Fatal("build-tagged runtime fixture import accepted")
	}
}

func TestDerivedWriterGuardEffectsIncludeRealSQLiteCascadeAndTriggerWrites(t *testing.T) {
	db, g := derivedEffectFixture(t)
	for _, query := range []string{
		`INSERT INTO parent VALUES(1)`,
		`INSERT INTO child VALUES(1,1,'never prune this edge')`,
		`DELETE FROM parent WHERE id=1`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	var rows int
	if err := db.QueryRow(`SELECT count(*) FROM audit`).Scan(&rows); err != nil || rows != 1 {
		t.Fatal("witness did not mutate SQLite", rows, err)
	}
	effects, err := g.direct(`DELETE FROM parent WHERE id=?`)
	if err == nil {
		effects, err = g.expand(effects)
	}
	if err != nil || !reflect.DeepEqual(effects, []derivedSQLEffect{{"audit", "INSERT"}, {"child", "UPDATE"}, {"parent", "DELETE"}}) {
		t.Fatal(effects, err)
	}
	root := derivedProofFiles(t, map[string]string{
		"owner/a.go":  "package owner;const q=`INSERT INTO audit(title) VALUES(?)`",
		"bypass/b.go": "package bypass;const q=`DELETE FROM parent WHERE id=?`",
	})
	scan := &derivedProofScan{graph: g}
	c, err := scanDerivedWritersLogged(root, map[string]bool{"audit": true}, nil, scan)
	if err != nil || !reflect.DeepEqual(c.Tables["audit"], []string{"bypass", "owner"}) {
		t.Fatal("constant parent DML bypassed transitive owner census", c, err)
	}
	pinUnknownDerivedSQL(&c)
	c.Migrated = map[string]string{"audit": "owner"}
	if len(derivedOwnershipProblems(c)) == 0 {
		t.Fatal("constant cascade/trigger bypass accepted sole ownership")
	}
}

func TestDerivedWriterGuardEffectClosureHandlesCyclesViewsAndConflicts(t *testing.T) {
	_, g := derivedEffectFixture(t)
	g.edges[derivedSQLEffect{"audit", "INSERT"}] = []derivedSQLEffect{{"parent", "DELETE"}}
	for _, query := range []string{
		`INSERT INTO write_view(id,title) VALUES(1,'UPDATE fake SET x=1')`,
		`WITH x AS(SELECT 1) INSERT INTO child(id) VALUES(1) ON CONFLICT DO UPDATE SET title='new'`,
		`REPLACE INTO child(id,title) VALUES(1,'new')`,
		`UPDATE OR REPLACE main."child" SET title='new'`,
	} {
		effects, err := g.direct(query)
		if err == nil {
			effects, err = g.expand(effects)
		}
		if err != nil || len(effects) < 2 {
			t.Fatal(query, effects, err)
		}
	}
	for _, query := range []string{`DROP TABLE child`, `PRAGMA foreign_keys=OFF`, `UPDATE %s SET x=?`, `DELETE FROM unknown WHERE id=?`, `ATTACH 'x' AS other`} {
		if _, err := g.direct(query); err == nil {
			t.Fatal("unsupported effect accepted", query)
		}
	}
	g.unknown[derivedSQLEffect{"audit", "INSERT"}] = true
	if _, err := g.expand([]derivedSQLEffect{{"parent", "DELETE"}}); err == nil {
		t.Fatal("partial closure accepted an unknown edge")
	}
}

func TestDerivedWriterGuardSchemaPolicyChangesInvalidateSameEffects(t *testing.T) {
	for _, change := range []string{"trigger policy", "FK policy"} {
		t.Run(change, func(t *testing.T) {
			db, g := derivedEffectFixture(t)
			root := derivedProofFiles(t, map[string]string{"calls.go": `package owner;import "database/sql";func query()string{return "DELETE FROM parent WHERE id=?"};func run(db *sql.DB){db.Exec(query())}`})
			pin := derivedProofForTest(t, root, g, "sql")
			if _, err := db.Exec(`DROP TRIGGER child_changed`); err != nil {
				t.Fatal(err)
			}
			if change == "FK policy" {
				if _, err := db.Exec(`DROP TABLE child;CREATE TABLE child(id INTEGER PRIMARY KEY,parent_id INTEGER DEFAULT 0 REFERENCES parent(id) ON DELETE SET DEFAULT ON UPDATE CASCADE,title TEXT)`); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := db.Exec(`CREATE TRIGGER child_changed AFTER UPDATE ON child WHEN NEW.title='a different policy' BEGIN INSERT INTO audit(title) VALUES(NEW.title); END`); err != nil {
				t.Fatal(err)
			}
			modified, err := derivedSchemaEffects(db)
			if err != nil {
				t.Fatal(err)
			}
			loader, file := derivedProofLoader(t, root)
			scan := &derivedProofScan{graph: modified, pins: map[string]derivedSQLPin{"calls.go": pin}}
			actual := scan.verify(loader, file, "calls.go", derivedSQLPin{SHA256: pin.SHA256})
			if len(scan.problems) == 0 || !derivedWildcard(actual) {
				t.Fatal("schema policy inherited an old proof with identical target effects")
			}
		})
	}
}

func TestDerivedWriterGuardManifestCannotGrantItsOwnProofReceipt(t *testing.T) {
	var pin derivedSQLPin
	if err := json.Unmarshal([]byte(`{"sha256":"`+strings.Repeat("a", 64)+`","effects":[{"table":"*","verb":"SELECT"}],"verified":true,"proof":{"kind":"resolved_call"}}`), &pin); err != nil {
		t.Fatal(err)
	}
	c := writerCensus{Tables: map[string][]string{"audit": {"owner"}}, Dynamic: map[string]derivedSQLPin{"bypass/calls.go": pin}, Migrated: map[string]string{"audit": "owner"}}
	if pin.verified || len(derivedOwnershipProblems(c)) == 0 {
		t.Fatal("unverified manifest effects granted ownership")
	}
}

func TestDerivedWriterGuardClearanceReportDoesNotClearOrDoubleCountFiles(t *testing.T) {
	r := newDerivedClearanceReport()
	r.add("foreign/a.go", "SQL from builder call")
	r.add("foreign/a.go", "incoming/captured/addressed callback")
	r.add("foreign/b.go", "reflect import")
	r.helper("foreign/a.go", "shared")
	r.helper("foreign/a.go", "shared")
	r.helper("foreign/b.go", "shared")
	c := writerCensus{Dynamic: map[string]derivedSQLPin{"foreign/a.go": {}, "foreign/b.go": {}, "owner/c.go": {}}}
	path := filepath.Join(t.TempDir(), "report.json")
	if err := r.write(path, c, "owner"); err != nil {
		t.Fatal(err)
	}
	var report struct {
		Total    int `json:"wildcard_files"`
		Foreign  int `json:"foreign_wildcard_files"`
		Patterns []struct {
			Count int `json:"count"`
		} `json:"primary_patterns"`
		Helpers []struct {
			Count int `json:"count"`
		} `json:"shared_helpers_non_additive"`
	}
	data, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(data, &report) != nil {
		t.Fatal(err)
	}
	if report.Total != 3 || report.Foreign != 2 || len(report.Patterns) != 2 || len(report.Helpers) != 1 || report.Helpers[0].Count != 2 || len(c.Dynamic) != 3 {
		t.Fatalf("diagnostics changed debt or double-counted a file: %+v", report)
	}
}

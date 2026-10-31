package eventfeed

import (
	"database/sql"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// securitySubscription reads the tables Run passes to its security
// WakeOnTableWrites from feed.go itself, so the guard below follows the list
// rather than a copy of it.
func securitySubscription(t *testing.T) []string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "feed.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	calls := 0
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "WakeOnTableWrites" || len(call.Args) < 2 {
			return true
		}
		if signal, ok := call.Args[0].(*ast.Ident); !ok || signal.Name != "security" {
			return true
		}
		calls++
		for _, arg := range call.Args[1:] {
			literal, ok := arg.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				t.Fatalf("the security subscription has a non-literal table argument; this guard reads literals only")
			}
			table, err := strconv.Unquote(literal.Value)
			if err != nil {
				t.Fatal(err)
			}
			tables = append(tables, table)
		}
		return true
	})
	if calls != 1 {
		t.Fatalf("found %d security WakeOnTableWrites calls in feed.go, want exactly 1", calls)
	}
	return tables
}

var triggerWrite = regexp.MustCompile(`(?i)\b(?:INSERT\s+(?:OR\s+\w+\s+)?INTO|REPLACE\s+INTO|UPDATE\s+(?:OR\s+\w+\s+)?|DELETE\s+FROM)\s*["\x60\[]?(\w+)`)

// hiddenSecurityWrites lists every way a committed statement can change a
// subscribed table without its head naming one: a trigger on an unsubscribed
// table whose body writes a subscribed one, or a foreign-key action on a
// subscribed table whose parent is unsubscribed. The exact security wake would
// miss either, and a stream would keep a revoked authority until something else
// woke it.
func hiddenSecurityWrites(t *testing.T, db *sql.DB, subscribed []string) []string {
	t.Helper()
	listed := map[string]bool{}
	for _, table := range subscribed {
		listed[strings.ToLower(table)] = true
	}
	var problems []string
	rows, err := db.Query(`SELECT name,tbl_name,COALESCE(sql,'') FROM sqlite_schema WHERE type='trigger'`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var name, table, body string
		if err = rows.Scan(&name, &table, &body); err != nil {
			t.Fatal(err)
		}
		if listed[strings.ToLower(table)] {
			continue
		}
		// Skip the trigger header: "CREATE TRIGGER x AFTER UPDATE OF c ON t"
		// names its own table, not a write.
		if at := strings.Index(strings.ToUpper(body), "BEGIN"); at >= 0 {
			body = body[at:]
		}
		for _, m := range triggerWrite.FindAllStringSubmatch(body, -1) {
			if listed[strings.ToLower(m[1])] {
				problems = append(problems, "trigger "+name+" on "+table+" writes "+m[1])
			}
		}
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	for _, table := range subscribed {
		keys, err := db.Query(`SELECT "table",on_update,on_delete FROM pragma_foreign_key_list(?)`, table)
		if err != nil {
			t.Fatal(err)
		}
		for keys.Next() {
			var parent, onUpdate, onDelete string
			if err = keys.Scan(&parent, &onUpdate, &onDelete); err != nil {
				t.Fatal(err)
			}
			if listed[strings.ToLower(parent)] {
				continue
			}
			for _, action := range []string{onUpdate, onDelete} {
				if action = strings.ToUpper(action); action == "CASCADE" || strings.HasPrefix(action, "SET ") {
					problems = append(problems, table+" has "+action+" from unsubscribed parent "+parent)
				}
			}
		}
		if err = keys.Err(); err != nil {
			t.Fatal(err)
		}
		keys.Close()
	}
	sort.Strings(problems)
	return problems
}

// TestNoHiddenWritesToTheSecurityTables keeps the exact security wake complete:
// every write that changes a subscribed table is named by its own statement or
// fired from another subscribed table. If one is ever added, add its table to
// the security subscription in Run. The list is pinned at its current 15
// entries so a change to it is a deliberate edit here too.
func TestNoHiddenWritesToTheSecurityTables(t *testing.T) {
	subscribed := securitySubscription(t)
	want := []string{"authorization_session_families", "authorization_family_tokens", "accounts", "direct_memberships", "direct_profiles", "identity_devices", "libraries", "library_sources", "restrictions", "profile_restrictions", "restriction_outbox", "policy", "library_network_policy", "api_credentials", "hosted_profile_revisions"}
	if strings.Join(subscribed, ",") != strings.Join(want, ",") {
		t.Fatalf("security subscription changed:\n got %v\nwant %v\nre-run this guard's census and update the pin", subscribed, want)
	}
	db, err := persistence.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, table := range subscribed {
		var exists bool
		if err = db.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_schema WHERE type='table' AND name=?)`, table).Scan(&exists); err != nil || !exists {
			t.Fatalf("subscribed table %s is not in the baseline: %v", table, err)
		}
	}
	for _, problem := range hiddenSecurityWrites(t, db, subscribed) {
		t.Errorf("%s: the exact security wake cannot see it; add the firing table to Run's security subscription", problem)
	}
}

// TestSecurityWriteGuardCatchesSyntheticViolations proves the guard reads what
// it claims: a trigger from an unsubscribed table, a cascade and a SET NULL
// from an unsubscribed parent are each reported, and the allowed shapes are not.
func TestSecurityWriteGuardCatchesSyntheticViolations(t *testing.T) {
	db, err := dbwork.OpenHandle(filepath.Join(t.TempDir(), "synthetic.sqlite"), dbwork.DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`
CREATE TABLE guarded(id INTEGER PRIMARY KEY, other INTEGER REFERENCES outside(id) ON DELETE CASCADE, near INTEGER REFERENCES outside_two(id) ON UPDATE SET NULL, own INTEGER REFERENCES guarded_parent(id) ON DELETE CASCADE, plain INTEGER REFERENCES outside_three(id));
CREATE TABLE guarded_parent(id INTEGER PRIMARY KEY);
CREATE TABLE outside(id INTEGER PRIMARY KEY);
CREATE TABLE outside_two(id INTEGER PRIMARY KEY);
CREATE TABLE outside_three(id INTEGER PRIMARY KEY);
CREATE TABLE unrelated(id INTEGER PRIMARY KEY);
CREATE TRIGGER hidden AFTER INSERT ON outside BEGIN UPDATE guarded SET id=id WHERE id=NEW.id; END;
CREATE TRIGGER quoted AFTER DELETE ON outside_two BEGIN DELETE FROM "guarded" WHERE id=OLD.id; END;
CREATE TRIGGER allowed AFTER INSERT ON guarded_parent BEGIN INSERT INTO guarded(id) VALUES(NEW.id); END;
CREATE TRIGGER elsewhere AFTER UPDATE OF id ON outside_three BEGIN INSERT INTO unrelated(id) VALUES(NEW.id); END;
`); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(hiddenSecurityWrites(t, db, []string{"guarded", "guarded_parent"}), "\n")
	want := strings.Join([]string{
		"guarded has CASCADE from unsubscribed parent outside",
		"guarded has SET NULL from unsubscribed parent outside_two",
		"trigger hidden on outside writes guarded",
		"trigger quoted on outside_two writes guarded",
	}, "\n")
	if got != want {
		t.Fatalf("synthetic guard result:\n%s\nwant:\n%s", got, want)
	}
}

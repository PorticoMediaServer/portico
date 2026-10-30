package persistence

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// B75 guard (the B68 failure mode): an AFTER/BEFORE UPDATE trigger whose body
// uses INSERT OR IGNORE aborts instead of ignoring when the UPDATE comes from
// an upsert's DO UPDATE path, because SQLite applies the outer statement's
// conflict algorithm. No such trigger may sit on a table that any server code
// or baseline upserts; use INSERT ... ON CONFLICT DO NOTHING in the trigger.
func TestNoInsertOrIgnoreUpdateTriggerOnAnUpsertedTable(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	root := filepath.Join("..", "..")
	var sql strings.Builder
	sql.WriteString(BaselineSQL())
	events := regexp.MustCompile(`(?is)CREATE\s+TRIGGER\s+(?:IF\s+NOT\s+EXISTS\s+)?(\w+)\s+(?:AFTER|BEFORE)\s+UPDATE(?:\s+OF\s+[\w\s,]+?)?\s+ON\s+(\w+)(.*?)\bEND;|DROP\s+TRIGGER\s+(?:IF\s+EXISTS\s+)?(\w+)`)
	type trigger struct{ table, body string }
	current := map[string]trigger{}
	for _, m := range events.FindAllStringSubmatch(sql.String(), -1) {
		if m[4] != "" {
			delete(current, m[4])
			continue
		}
		current[m[1]] = trigger{m[2], m[3]}
	}
	risky := map[string][]string{}
	for name, tr := range current {
		if strings.Contains(strings.ToUpper(tr.body), "INSERT OR IGNORE") {
			risky[tr.table] = append(risky[tr.table], name)
		}
	}
	var code strings.Builder
	code.WriteString(sql.String())
	err := filepath.Walk(filepath.Join(root, "internal"), func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		raw, err := os.ReadFile(path)
		code.Write(raw)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	for table, triggers := range risky {
		upsert := regexp.MustCompile(`(?is)INSERT\s+INTO\s+"?` + table + `"?\b[^;` + "`" + `]*?ON\s+CONFLICT[^;` + "`" + `]*?DO\s+UPDATE`)
		if upsert.MatchString(code.String()) {
			t.Errorf("table %s is upserted, but UPDATE triggers %v use INSERT OR IGNORE (use ON CONFLICT DO NOTHING)", table, triggers)
		}
	}
}

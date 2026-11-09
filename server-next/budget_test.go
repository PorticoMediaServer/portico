// Package servernext holds only the budget test: the limits in RULES.md, enforced.
package servernext

import (
	"bufio"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const (
	maxTables        = 90
	maxChecks        = 250
	maxFileLines     = 1000
	maxLineChars     = 200
	maxDataLayerCode = 80_000
	maxServerCode    = 400_000
)

// Words from the storage protocol this server replaces. RULES.md says why.
var refused = regexp.MustCompile(`(?i)receipt|incarnation|custody|reclaim|\bfences?\b|[a-z]Fence\b`)

func TestBudgets(t *testing.T) {
	var tables, checks, triggers, dataLayer, server int
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		ext := filepath.Ext(path)
		if ext != ".go" && ext != ".sql" {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		lines := 0
		scan := bufio.NewScanner(f)
		scan.Buffer(nil, 1<<20)
		for scan.Scan() {
			line := scan.Text()
			lines++
			if len(line) > maxLineChars {
				t.Errorf("%s:%d is %d characters; the limit is %d", path, lines, len(line), maxLineChars)
			}
			if path != "budget_test.go" && refused.MatchString(line) {
				t.Errorf("%s:%d uses a refused word: %s", path, lines, strings.TrimSpace(line))
			}
			if ext == ".sql" && !strings.HasPrefix(strings.TrimSpace(line), "--") {
				upper := strings.ToUpper(line)
				tables += strings.Count(upper, "CREATE TABLE ")
				triggers += strings.Count(upper, "CREATE TRIGGER ")
				checks += strings.Count(upper, "CHECK (")
			}
		}
		if lines > maxFileLines {
			t.Errorf("%s is %d lines; the limit is %d", path, lines, maxFileLines)
		}
		if ext == ".go" && !strings.HasSuffix(path, "_test.go") {
			server += lines
			if strings.HasPrefix(filepath.ToSlash(path), "internal/db/") {
				dataLayer += lines
			}
		}
		return scan.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("tables %d, checks %d, data-layer lines %d, server lines %d", tables, checks, dataLayer, server)
	if tables > maxTables {
		t.Errorf("%d tables; the budget is %d", tables, maxTables)
	}
	if triggers != 0 {
		t.Errorf("%d triggers; the budget is 0", triggers)
	}
	if checks > maxChecks {
		t.Errorf("%d CHECK constraints; the budget is %d", checks, maxChecks)
	}
	if dataLayer > maxDataLayerCode {
		t.Errorf("internal/db is %d lines; the budget is %d", dataLayer, maxDataLayerCode)
	}
	if server > maxServerCode {
		t.Errorf("the server is %d lines; the budget is %d", server, maxServerCode)
	}
}

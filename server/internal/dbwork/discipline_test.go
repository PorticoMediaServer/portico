package dbwork

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A raw write transaction cannot be intercepted at runtime, so the rule is
// enforced where it is written. Any `BeginTx(ctx, nil)` or `Begin()` outside this
// package escapes the write gate: it would take SQLite's writer without a place
// in the priority ladder, and a second one opened inside a gated transaction
// would wait on a gate its own caller is holding.
//
// Read-only transactions are deliberately allowed: they take no gate, cannot
// block the writer, and are the right shape for a consistent projection.
var (
	// The context argument may itself be a call (`r.Context()`), so the match
	// runs to the transaction options rather than to the first parenthesis.
	rawWriteBegin = regexp.MustCompile(`\.BeginTx\(.*,\s*nil\)|\.Begin\(\)`)
	readOnlyBegin = regexp.MustCompile(`\.BeginTx\([^)]*&sql\.TxOptions\{ReadOnly: true\}\)`)
)

// allowed names the non-database Begin methods and the deliberate exceptions.
var allowed = map[string]bool{
	// Upstream virtual-table Begin is SQLite's transaction callback, not sql.DB.Begin.
	"internal/thirdparty/sqlite/vtab.go": true,
	// The programme spool writes its own private single-connection temporary
	// database file, never the server's; no gate applies to it.
	"internal/livechannels/programme_spool.go": true,
	// OperationScope.Begin is a goroutine lease, not a transaction.
	"internal/storage/observed_playback.go":         true,
	"internal/storage/inventory_page_stream.go":     true,
	"internal/storage/root_registration_unix.go":    true,
	"internal/storage/root_registration_windows.go": true,
	"internal/storage/root_registration_other.go":   true,
}

func TestNoRawWriteTransactionsOutsideDBWork(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	var offenders []string
	walkErr := filepath.Walk(filepath.Join(root, "internal"), func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		relative, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		relative = filepath.ToSlash(relative)
		if strings.HasPrefix(relative, "internal/dbwork/") {
			return nil
		}
		// Test files may hold their own transactions: a test is allowed to model a
		// caller that already owns one, which is exactly what the gate must survive.
		if strings.HasSuffix(relative, "_test.go") {
			return nil
		}
		if allowed[relative] {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for index, line := range strings.Split(string(data), "\n") {
			// This production listing has moved to the lifetime-capped helper;
			// do not let a raw read transaction bypass it again.
			if readOnlyBegin.MatchString(line) && relative != "internal/playbackv1/controls.go" {
				continue
			}
			if rawWriteBegin.MatchString(line) || relative == "internal/playbackv1/controls.go" && readOnlyBegin.MatchString(line) {
				offenders = append(offenders, relative+":"+itoa(index+1)+": "+strings.TrimSpace(line))
			}
		}
		return nil
	})
	if walkErr != nil {
		t.Fatal(walkErr)
	}
	if len(offenders) > 0 {
		t.Fatalf("write transactions must be opened through internal/dbwork so they take a place in the priority ladder:\n%s", strings.Join(offenders, "\n"))
	}
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	digits := []byte{}
	for v > 0 {
		digits = append([]byte{byte('0' + v%10)}, digits...)
		v /= 10
	}
	return string(digits)
}

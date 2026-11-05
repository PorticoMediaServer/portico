package schematest

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
)

func TestMain(m *testing.M) { os.Exit(Run(m)) }

func openClone(t *testing.T, path string) *sql.DB {
	t.Helper()
	cfg := sqliteconn.Config{Role: sqliteconn.Writer, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Full}
	db, _, err := sqliteconn.Open(t.Context(), path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := sqliteconn.Assert(t.Context(), conn, cfg); err != nil {
		t.Fatal(err)
	}
	if err := schema.AssertExisting(t.Context(), conn); err != nil {
		t.Fatal(err)
	}
	return db
}

func digestFile(t *testing.T, path string) [sha256.Size]byte {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.New()
	_, copyErr := io.Copy(h, f)
	if err := errors.Join(copyErr, f.Close()); err != nil {
		t.Fatal(err)
	}
	var sum [sha256.Size]byte
	copy(sum[:], h.Sum(nil))
	return sum
}

func TestCloneIsolationAndPristineTemplate(t *testing.T) {
	a, b := Clone(t), Clone(t)
	if a == b {
		t.Fatal("shared clone path")
	}
	left, right := openClone(t, a), openClone(t, b)
	if _, err := left.ExecContext(t.Context(), `INSERT INTO catalog_credit_labels(label) VALUES('left'); CREATE TABLE test_sequence(id INTEGER PRIMARY KEY AUTOINCREMENT); INSERT INTO test_sequence DEFAULT VALUES; INSERT INTO test_sequence DEFAULT VALUES`); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := right.QueryRowContext(t.Context(), "SELECT count(*) FROM catalog_credit_labels").Scan(&count); err != nil || count != 0 {
		t.Fatal("row leak", count, err)
	}
	if err := right.QueryRowContext(t.Context(), "SELECT count(*) FROM sqlite_schema WHERE name='test_sequence'").Scan(&count); err != nil || count != 0 {
		t.Fatal("schema leak", count, err)
	}
	if _, err := right.ExecContext(t.Context(), "CREATE TABLE test_sequence(id INTEGER PRIMARY KEY AUTOINCREMENT); INSERT INTO test_sequence DEFAULT VALUES"); err != nil {
		t.Fatal(err)
	}
	if err := right.QueryRowContext(t.Context(), "SELECT id FROM test_sequence").Scan(&count); err != nil || count != 1 {
		t.Fatal("sequence leak", count, err)
	}
	base := process.active
	if base.bootstraps.Load() != 1 || base.engine == "" || base.size <= 0 || digestFile(t, base.path) != base.digest {
		t.Fatal("template changed or reinitialized")
	}
	if err := noSidecars(base.path); err != nil {
		t.Fatal(err)
	}
	fa, err := os.Stat(a)
	if err != nil {
		t.Fatal(err)
	}
	fb, err := os.Stat(b)
	if err != nil || os.SameFile(fa, fb) {
		t.Fatal("clones share a file", err)
	}
	// A later clone still matches the pristine canonical layout and bytes.
	c := Clone(t)
	if digestFile(t, c) != base.digest {
		t.Fatal("later clone retained another test's mutations")
	}
	openClone(t, c)
}

func concurrentClones(t *testing.T) []string {
	t.Helper()
	var mu sync.Mutex
	var paths []string
	t.Run("parallel", func(t *testing.T) {
		for i := 0; i < 8; i++ {
			t.Run(fmt.Sprint(i), func(t *testing.T) {
				t.Parallel()
				path := Clone(t)
				db := openClone(t, path)
				if _, err := db.ExecContext(t.Context(), "INSERT INTO catalog_credit_labels(label) VALUES(?)", fmt.Sprint(i)); err != nil {
					t.Fatal(err)
				}
				var count int
				if err := db.QueryRowContext(t.Context(), "SELECT count(*) FROM catalog_credit_labels").Scan(&count); err != nil || count != 1 {
					t.Fatal("concurrent clone leaked rows", count, err)
				}
				mu.Lock()
				paths = append(paths, path)
				mu.Unlock()
			})
		}
	})
	seen := map[string]bool{}
	for _, path := range paths {
		if seen[path] {
			t.Fatal("duplicate concurrent path")
		}
		seen[path] = true
	}
	if len(paths) != 8 || process.active.bootstraps.Load() != 1 {
		t.Fatal("missing clone or multiple bootstraps", len(paths))
	}
	return paths
}

func TestConcurrentCopies(t *testing.T) { concurrentClones(t) }

func TestCloneDQSAndPhysicalReplacement(t *testing.T) {
	db := openClone(t, Clone(t))
	cfg := sqliteconn.Config{Role: sqliteconn.Writer, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Full}
	for i := 0; i < 2; i++ {
		conn, err := db.Conn(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := sqliteconn.Assert(t.Context(), conn, cfg); err != nil {
			conn.Close()
			t.Fatal(err)
		}
		for _, query := range []string{`SELECT "not_a_column"`, `CREATE TABLE dqs_typo(value INTEGER CHECK("not_a_column"))`} {
			if _, err := conn.ExecContext(t.Context(), query); err == nil {
				conn.Close()
				t.Fatal("quoted typo accepted", query)
			}
		}
		if err := conn.Close(); err != nil {
			t.Fatal(err)
		}
		// Test-only replacement; no raw driver or policy setter is needed.
		db.SetConnMaxLifetime(time.Nanosecond)
	}
	if db.Stats().MaxLifetimeClosed == 0 {
		t.Fatal("control did not replace a physical connection")
	}
}

type processReceipt struct {
	Directory string
	Paths     []string
	Calls     int64
	Clones    int64
}

func TestTemplateChild(t *testing.T) {
	mode := os.Getenv("PORTICO_SCHEMA_TEST_CHILD")
	if mode == "" {
		t.Skip("subprocess lifecycle control")
	}
	var paths []string
	switch mode {
	case "unused":
	case "used":
		paths = []string{Clone(t)}
	case "concurrent":
		paths = concurrentClones(t)
	default:
		t.Fatal("unknown child control")
	}
	base := process.active
	receipt := processReceipt{base.dir, paths, base.bootstraps.Load(), base.clones.Load()}
	data, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(os.Getenv("PORTICO_SCHEMA_TEST_RECEIPT"), data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestProcessCleanupAndLaziness(t *testing.T) {
	for _, mode := range []string{"unused", "used", "concurrent"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			tmp := filepath.Join(root, "child-tmp")
			if err := os.Mkdir(tmp, 0700); err != nil {
				t.Fatal(err)
			}
			receiptPath := filepath.Join(root, "receipt.json")
			ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestTemplateChild$", "-test.parallel=2")
			cmd.Env = append(os.Environ(), "PORTICO_SCHEMA_TEST_CHILD="+mode, "PORTICO_SCHEMA_TEST_RECEIPT="+receiptPath, "TMPDIR="+tmp)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("child %v: %s", err, output)
			}
			data, err := os.ReadFile(receiptPath)
			if err != nil {
				t.Fatal(err)
			}
			var receipt processReceipt
			if err := json.Unmarshal(data, &receipt); err != nil {
				t.Fatal(err)
			}
			if mode == "unused" {
				if receipt.Directory != "" || receipt.Calls != 0 || receipt.Clones != 0 {
					t.Fatal("unused process initialized a template")
				}
			} else {
				if receipt.Calls != 1 || receipt.Clones != int64(len(receipt.Paths)) {
					t.Fatal("invalid bootstrap/copy counts", receipt)
				}
				for _, path := range append(receipt.Paths, receipt.Directory) {
					if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
						t.Fatal("child left test data", path, err)
					}
				}
			}
			entries, err := os.ReadDir(tmp)
			if err != nil || len(entries) != 0 {
				t.Fatal("process scratch retained", entries, err)
			}
		})
	}
}

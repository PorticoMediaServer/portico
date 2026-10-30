package dbwork

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// A single pooled connection without foreign-key enforcement is a silent
// corruption vector: rows written through it orphan children of tables declared
// ON DELETE CASCADE, which by definition cannot otherwise happen. The pragma
// travels in the DSN so every connection should inherit it — this opens all of
// them at once and asks each one.
func TestEveryPooledConnectionEnforcesForeignKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.sqlite")
	policy := DefaultPolicy()
	db, err := OpenHandle(path, policy)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	// Hold every connection the policy allows at once, so each is a distinct one.
	connections := make([]*sql.Conn, 0, policy.MaxOpenConns)
	for index := 0; index < policy.MaxOpenConns; index++ {
		conn, err := db.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		connections = append(connections, conn)
	}
	for index, conn := range connections {
		var enforcing int
		if err = conn.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&enforcing); err != nil {
			t.Fatal(err)
		}
		if enforcing != 1 {
			t.Fatalf("pooled connection %d does not enforce foreign keys", index)
		}
	}
	for _, conn := range connections {
		conn.Close()
	}
}

// Disabling enforcement has to be an exception with a guaranteed end, not a
// state a connection can carry back into the pool.
func TestForeignKeysAreRestoredOnEveryPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.sqlite")
	db, err := OpenHandle(path, DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()

	check := func(label string) {
		t.Helper()
		conn, err := db.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		var enforcing int
		if err = conn.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&enforcing); err != nil {
			t.Fatal(err)
		}
		if enforcing != 1 {
			t.Fatalf("after %s a pooled connection was not enforcing foreign keys", label)
		}
	}

	// The ordinary path.
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var inside int
	if err = WithForeignKeysOff(ctx, conn, func() error {
		return conn.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&inside)
	}); err != nil {
		t.Fatal(err)
	}
	if inside != 0 {
		t.Fatal("enforcement was not actually disabled inside the helper")
	}
	conn.Close()
	check("a successful pass")

	// The failing path.
	conn, err = db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = WithForeignKeysOff(ctx, conn, func() error { return sql.ErrNoRows }); err != sql.ErrNoRows {
		t.Fatalf("the caller's error was not returned: %v", err)
	}
	conn.Close()
	check("a failing pass")

	// The panicking path.
	conn, err = db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("the panic did not propagate")
			}
		}()
		_ = WithForeignKeysOff(ctx, conn, func() error { panic("inside") })
	}()
	conn.Close()
	check("a panicking pass")
}

// Concurrent use is the case that matters: one helper disabling enforcement must
// not be visible to anybody else's connection.
func TestDisablingEnforcementIsInvisibleToOtherConnections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.sqlite")
	db, err := OpenHandle(path, DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	held, release := make(chan struct{}), make(chan struct{})
	var wait sync.WaitGroup
	wait.Add(1)
	go func() {
		defer wait.Done()
		conn, err := db.Conn(ctx)
		if err != nil {
			return
		}
		defer conn.Close()
		_ = WithForeignKeysOff(ctx, conn, func() error {
			close(held)
			<-release
			return nil
		})
	}()
	<-held
	other, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var enforcing int
	if err = other.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&enforcing); err != nil {
		t.Fatal(err)
	}
	other.Close()
	close(release)
	wait.Wait()
	if enforcing != 1 {
		t.Fatal("one helper's disabled enforcement was visible on another connection")
	}
}

// The rule is enforced where it is written, like the write-gate rule beside it:
// a bare `PRAGMA foreign_keys=OFF` anywhere else is a connection that may go back
// to the pool unenforcing.
func TestNoRawForeignKeyDisablingOutsideDBWork(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	pattern := regexp.MustCompile(`(?i)PRAGMA\s+foreign_keys\s*=\s*(OFF|0)`)
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
			if strings.HasPrefix(relative, "internal/dbwork/") {
				return nil
			}
			// The restore candidate builder disables enforcement across a dozen
			// early returns, which is more than one helper call can wrap; it defers
			// dbwork.RestoreForeignKeys instead, which has the same guarantee.
			if relative == "internal/restore/backup_snapshot.go" {
				return nil
			}
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			for index, line := range strings.Split(string(data), "\n") {
				if pattern.MatchString(line) {
					offenders = append(offenders, relative+":"+strconv.Itoa(index+1)+": "+strings.TrimSpace(line))
				}
			}
			return nil
		})
		if walkErr != nil {
			t.Fatal(walkErr)
		}
	}
	if len(offenders) > 0 {
		t.Fatalf("foreign-key enforcement is a per-connection setting and this pool reuses connections; disable it only through dbwork.WithForeignKeysOff, which restores it on every path and destroys the connection rather than pooling it if it cannot:\n%s", strings.Join(offenders, "\n"))
	}
}

// A security-fence transaction runs with FULL durability, and the connection it
// borrowed goes back to the pool exactly as it was found. Both halves matter:
// without the first a revocation can be lost to a power cut, and without the
// second every later write on that connection pays for a setting nothing asked
// for.
func TestSecurityFenceWritesAreDurableAndRestoreTheConnection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.sqlite")
	policy := DefaultPolicy()
	db, err := OpenHandle(path, policy)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`CREATE TABLE fence_probe(id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	observed := make(chan string, 1)
	err = WithWriteTx(context.Background(), db, ClassSecurityFence, func(tx *sql.Tx) error {
		var mode int
		if e := tx.QueryRow(`PRAGMA synchronous`).Scan(&mode); e != nil {
			return e
		}
		observed <- strconv.Itoa(mode)
		_, e := tx.Exec(`INSERT INTO fence_probe(id) VALUES(1)`)
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	if mode := <-observed; mode != "2" {
		t.Fatalf("a security-fence transaction ran with synchronous=%s; FULL is 2", mode)
	}
	// Every pooled connection must be back at NORMAL. Asking more times than the
	// pool is wide, with the connections held at once, is what makes this check
	// cover all of them rather than whichever one comes back first.
	conns := make([]*sql.Conn, 0, policy.MaxOpenConns)
	for i := 0; i < policy.MaxOpenConns; i++ {
		conn, e := db.Conn(context.Background())
		if e != nil {
			t.Fatal(e)
		}
		conns = append(conns, conn)
	}
	for index, conn := range conns {
		var mode int
		if e := conn.QueryRowContext(context.Background(), `PRAGMA synchronous`).Scan(&mode); e != nil {
			t.Fatal(e)
		}
		if mode != 1 {
			t.Fatalf("pooled connection %d came back with synchronous=%d rather than NORMAL", index, mode)
		}
		conn.Close()
	}
	// An ordinary write is unchanged: the cost is paid by the fence class only.
	if err = WithWriteTx(context.Background(), db, ClassInteractive, func(tx *sql.Tx) error {
		var mode int
		if e := tx.QueryRow(`PRAGMA synchronous`).Scan(&mode); e != nil {
			return e
		}
		if mode != 1 {
			t.Errorf("an interactive write ran with synchronous=%d", mode)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

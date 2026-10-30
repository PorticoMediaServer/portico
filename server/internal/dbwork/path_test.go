package dbwork

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestPolicyDSNKeepsLiteralPathSeparateFromQuery(t *testing.T) {
	for _, path := range []string{
		"/data/fresh #00/server.sqlite",
		"/data/server?mode=memory&cache=shared.sqlite",
		"relative data/percent%2F space.sqlite",
		"/data/wal_autocheckpoint(1000).sqlite",
	} {
		for _, writer := range []bool{false, true} {
			var dsn string
			var err error
			if writer {
				dsn, err = DefaultPolicy().WriterDSN(path)
			} else {
				dsn, err = DefaultPolicy().DSN(path)
			}
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := url.Parse(dsn)
			if err != nil || parsed.Fragment != "" {
				t.Fatalf("filename became URI syntax: %q: %v", dsn, err)
			}
			encoded, query, ok := strings.Cut(strings.TrimPrefix(dsn, "file:"), "?")
			literal, err := url.PathUnescape(encoded)
			if !ok || err != nil || literal != filepath.ToSlash(path) {
				t.Fatalf("writer=%v path=%q decoded=%q err=%v", writer, path, literal, err)
			}
			params, err := url.ParseQuery(query)
			// The writer DSN also names its pool for the observed driver, which
			// strips the marker before SQLite sees it.
			if writer && (params.Get(roleParameter) != "writer" || len(params) != 3) {
				t.Fatalf("writer DSN does not name its pool: %q", query)
			}
			params.Del(roleParameter)
			if err != nil || len(params) != 2 || params.Get("_txlock") != "immediate" || len(params["_pragma"]) != 8 {
				t.Fatalf("filename changed policy parameters: %q: %v", query, err)
			}
			checkpoint := DefaultPolicy().WALAutocheckpoint
			if writer {
				checkpoint = writerWALAutocheckpoint
			}
			if !slices.Contains(params["_pragma"], fmt.Sprintf("wal_autocheckpoint(%d)", checkpoint)) {
				t.Fatalf("writer=%v checkpoint missing from %q", writer, query)
			}
		}
	}
}

func TestOpenHandleUsesLiteralDatabasePath(t *testing.T) {
	for _, name := range []string{"server.sqlite", "server#00.sqlite", "server?mode=memory&cache=shared.sqlite", "server%2F space.sqlite"} {
		t.Run(name, func(t *testing.T) {
			if runtime.GOOS == "windows" && strings.Contains(name, "?") {
				t.Skip("Windows does not permit question marks in filenames")
			}
			dir := filepath.Join(t.TempDir(), "fresh #00 data%20")
			path := filepath.Join(dir, name)
			db, err := OpenHandle(path, DefaultPolicy())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { db.Close() })
			if _, err = db.Exec(`CREATE TABLE path_probe(value TEXT NOT NULL) STRICT`); err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec(`INSERT INTO path_probe VALUES (?)`, name); err != nil {
				t.Fatal(err)
			}
			var seq int
			var database, actual string
			if err = db.QueryRow(`PRAGMA database_list`).Scan(&seq, &database, &actual); err != nil {
				t.Fatal(err)
			}
			wantInfo, wantErr := os.Stat(path)
			actualInfo, actualErr := os.Stat(actual)
			if wantErr != nil || actualErr != nil || !os.SameFile(wantInfo, actualInfo) {
				t.Fatalf("opened %q instead of literal %q: %v / %v", actual, path, wantErr, actualErr)
			}
			if err = db.Close(); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(path)
			if err != nil || !strings.HasPrefix(string(data), "SQLite format 3\x00") {
				t.Fatalf("literal database missing SQLite header: %v", err)
			}
			reopened, err := OpenHandle(path, DefaultPolicy())
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			var value string
			if err = reopened.QueryRow(`SELECT value FROM path_probe`).Scan(&value); err != nil || value != name {
				t.Fatalf("literal database readback=%q err=%v", value, err)
			}
		})
	}
}

package dbwork_test

import (
	"database/sql"
	"strings"
	"testing"

	_ "github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// In tests, a public-id lookup given an integer id fails loudly instead of
// matching nothing; an arbitrary unknown id is still just unknown.
func TestPidBlobRefusesIntegerIdsInTests(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, arg := range []any{"4821", int64(4821)} {
		var out []byte
		if err = db.QueryRow(`SELECT pid_blob(?)`, arg).Scan(&out); err == nil || !strings.Contains(err.Error(), "integer id") {
			t.Fatalf("pid_blob(%#v): %v", arg, err)
		}
	}
	var out []byte
	if err = db.QueryRow(`SELECT pid_blob('missing')`).Scan(&out); err != nil || out != nil {
		t.Fatalf("unknown id: %v %v", out, err)
	}
	if err = db.QueryRow(`SELECT pid_blob(NULL)`).Scan(&out); err != nil || out != nil {
		t.Fatalf("NULL: %v %v", out, err)
	}
}

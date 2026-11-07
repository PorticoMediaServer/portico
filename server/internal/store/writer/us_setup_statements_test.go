package writer

import (
	"database/sql"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"testing"
)

// Canonical-engine source effects only; no claim of root authority, HN Capture,
// complete saved-result custody, physical qualification or runtime admission.
func TestUSSetupNativeEffects(t *testing.T) {
	w, _ := testCanonicalExecutor(t)
	defs := map[stmt.ID]stmt.Definition{}
	for _, d := range usSetupEffectDefinitions() {
		defs[d.ID] = d
	}
	query := func(id stmt.ID, args ...any) *sql.Row {
		return w.db.QueryRowContext(t.Context(), defs[id].SQL, args...)
	}
	a, b, c := setupTestDigest('a'), setupTestDigest('b'), setupTestDigest('c')
	var singleton int64
	var hash string
	var created int64
	if e := query(usSetupCodeInsert, int64(1), a, int64(1000)).Scan(&singleton, &hash, &created); e != nil || singleton != 1 || hash != a || created != 1000 {
		t.Fatal("code insert", e)
	}
	if e := query(usSetupCodeRotate, b, int64(1100), c).Scan(&singleton, &hash, &created); e != sql.ErrNoRows {
		t.Fatal("stale code", e)
	}
	if e := query(usSetupCodeRotate, b, int64(1200), a).Scan(&singleton, &hash, &created); e != nil || hash != b || created != 1200 {
		t.Fatal("rotation", e)
	}
	if e := query(usSetupCodeInsert, int64(1), a, int64(1300)).Scan(&singleton, &hash, &created); e == nil {
		t.Fatal("singleton replaced")
	}
	var token, peer, origin string
	var expires int64
	scan := func(id stmt.ID, args ...any) error { return query(id, args...).Scan(&token, &peer, &origin, &expires) }
	if e := scan(usSetupAdmissionIssue, a, b, c, int64(1801000)); e != nil || token != a || peer != b || origin != c || expires != 1801000 {
		t.Fatal("issue", e)
	}
	// Existing semantics refresh the same token and replace its peer/origin.
	if e := scan(usSetupAdmissionIssue, a, c, b, int64(1802000)); e != nil || peer != c || origin != b || expires != 1802000 {
		t.Fatal("refresh", e)
	}
	for _, args := range [][]any{
		{a, b, b, int64(1802000), int64(2000)},
		{a, c, c, int64(1802000), int64(2000)},
		{a, c, b, int64(1801000), int64(2000)},
		{a, c, b, int64(1802000), int64(1802000)},
	} {
		if e := scan(usSetupAdmissionConsume, args...); e != sql.ErrNoRows {
			t.Fatal("stale binding/expiry consumed", e)
		}
	}
	if e := scan(usSetupAdmissionConsume, a, c, b, int64(1802000), int64(2000)); e != nil || expires != 1802000 {
		t.Fatal("consume", e)
	}
	if e := scan(usSetupAdmissionConsume, a, c, b, int64(1802000), int64(2000)); e != sql.ErrNoRows {
		t.Fatal("second consume", e)
	}
}

func TestUSSetupStatementInventory(t *testing.T) {
	w, _ := testCanonicalExecutor(t)
	nativeInventoryFixtureRegistry(t, w, usSetupEffectDefinitions(), "us_setup_prepare.go", "us_setup_statements.go", "us_setup_statements_test.go")
}

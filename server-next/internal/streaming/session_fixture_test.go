package streaming

import (
	"database/sql"
	"testing"
)

func firstCatalogAssetToken(t testing.TB, db *sql.DB) string {
	t.Helper()
	var token string
	if err := db.QueryRow(`SELECT token FROM catalog_assets ORDER BY id LIMIT 1`).Scan(&token); err != nil {
		t.Fatal(err)
	}
	return token
}

// Media-only fixtures still need the actual account stream-limit owner. This
// does not bypass admission or synthesize an authorization fallback.
func insertFixtureAccount(t testing.TB, db *sql.DB, account, profile string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES(?,?,X'00',?,1)`, account, account, profile); err != nil {
		t.Fatal(err)
	}
}

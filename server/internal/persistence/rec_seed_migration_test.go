package persistence

import (
	"context"
	"path/filepath"
	"testing"
)

func TestPositiveRecommendationSeedIndexExistsInBaseline(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	path := filepath.Join(t.TempDir(), "state.sqlite")
	db, err := OpenFresh(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO rec_profile_signals(profile_id,work_id,weight,engaged,hidden,at,long,short) VALUES('p',1,2,1,0,'2026-01-01',2,2),('p',2,-1,0,1,'2026-01-02',-1,-1),('p',3,0,1,0,'2026-01-03',0,0)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	upgraded, err := OpenFresh(path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	var rows, positive int
	var digest string
	if err := upgraded.QueryRow(`SELECT count(*) FROM rec_profile_signals WHERE profile_id='p'`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if err := upgraded.QueryRow(`SELECT count(*) FROM rec_profile_signals INDEXED BY rec_profile_signals_positive_recent WHERE profile_id='p' AND weight>0`).Scan(&positive); err != nil {
		t.Fatal(err)
	}
	err = upgraded.QueryRowContext(context.Background(), `SELECT value FROM configuration WHERE key=?`, baselineDigestKey).Scan(&digest)
	if err != nil {
		t.Fatal(err)
	}
	if rows != 3 || positive != 1 || digest != BaselineDigest() {
		t.Fatalf("reopen altered history or omitted index: rows=%d positive=%d digest=%s", rows, positive, digest)
	}
}

package librarychannels

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func TestNinetyDaysOfSchedulesPruneButRetainActivePendingAndPlayback(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "schedules.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`INSERT INTO lc_channels(id,revision,config_json,enabled,position,name,active_generation,state) VALUES('channel',1,'{}',1,0,'Test','g89','ready')`); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	insert := func(id, status, base string, at time.Time) {
		t.Helper()
		if _, err = db.Exec(`INSERT INTO lc_generations(id,channel_id,config_revision,config_json,catalog_fence,seed,status,phase,base_generation,start_ms,end_ms,boundary_ms,cursor_ms,created_ms) VALUES(?,'channel',1,'{}','','',?,'complete',?,0,1,0,0,?)`, id, status, base, at.UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}
	for day := 0; day < 90; day++ {
		insert(fmt.Sprintf("g%d", day), "published", "", now.Add(time.Duration(day-90)*24*time.Hour))
	}
	insert("pending", "pending", "g0", now)
	if _, err = db.Exec(`INSERT INTO lc_playback_refs VALUES('playback','channel','g1','occurrence',1,?)`, now.Add(time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	// One large generation needs several bounded passes; it must not be dropped
	// while child rows remain.
	for i := 0; i < 1300; i++ {
		if _, err = db.Exec(`INSERT INTO lc_used VALUES('g2','rule',?,0)`, fmt.Sprint(i)); err != nil {
			t.Fatal(err)
		}
	}
	store, _ := New(db)
	store.now = func() time.Time { return now }
	passes := 0
	for ; passes < 200; passes++ {
		worked, e := store.PruneGenerations(context.Background())
		if e != nil {
			t.Fatal(e)
		}
		if !worked {
			break
		}
	}
	if passes < 81 || passes >= 200 {
		t.Fatal("cleanup did not drain bounded work", passes)
	}
	var remaining int
	if err = db.QueryRow(`SELECT count(*) FROM lc_generations`).Scan(&remaining); err != nil || remaining != 10 {
		t.Fatal(remaining, err)
	}
	for _, id := range []string{"g0", "g1", "g89", "pending"} {
		var n int
		if err = db.QueryRow(`SELECT count(*) FROM lc_generations WHERE id=?`, id).Scan(&n); err != nil || n != 1 {
			t.Fatal("live generation lost", id, err)
		}
	}
	rows, err := db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("dangling schedule reference")
	}
}

package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func TestTombstonePruneTurnsBoundChildrenAndResume(t *testing.T) {
	c := catalogtest.Open(t)
	c.Exec(`INSERT INTO catalog_title_tombstones(public_id,library_id,title,kind,poster_url,backdrop_url,recording,removed_at,expires_at) VALUES('expired','','Old',1,'','',0,'2000','2000'),('live','','Live',1,'','',0,'2000','2999')`)
	for _, id := range []string{"expired", "live"} {
		for i := 0; i < 23; i++ {
			c.Exec(`INSERT INTO catalog_title_tombstone_viewers VALUES(?,?,?,1)`, id, fmt.Sprint(i), "2000")
			c.Exec(`INSERT INTO catalog_title_tombstone_attributes VALUES(?,2,?,?)`, id, fmt.Sprint(i), "label")
		}
	}
	step := func() bool {
		t.Helper()
		more := false
		if err := dbwork.WithWriteTx(context.Background(), c.DB, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
			var before, after int64
			if err := tx.QueryRow(`SELECT total_changes()`).Scan(&before); err != nil {
				return err
			}
			var err error
			more, err = pruneRemovedTitleTurnTx(context.Background(), tx)
			if err != nil {
				return err
			}
			if err = tx.QueryRow(`SELECT total_changes()`).Scan(&after); err != nil {
				return err
			}
			if after-before > 8 {
				return fmt.Errorf("one prune turn changed %d rows", after-before)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return more
	}
	if !step() {
		t.Fatal("expired child work was not admitted")
	}
	var parent, children int
	if err := c.DB.QueryRow(`SELECT count(*) FROM catalog_title_tombstones WHERE public_id='expired'`).Scan(&parent); err != nil {
		t.Fatal(err)
	}
	if err := c.DB.QueryRow(`SELECT count(*) FROM catalog_title_tombstone_viewers WHERE public_id='expired'`).Scan(&children); err != nil {
		t.Fatal(err)
	}
	if parent != 1 || children != 19 {
		t.Fatal("header cascaded over remaining children", parent, children)
	}
	var seq int
	var name, path string
	if err := c.DB.QueryRow(`PRAGMA database_list`).Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	if err := c.DB.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := persistence.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	c.DB = reopened
	t.Cleanup(func() { reopened.Close() })
	done := false
	for i := 0; i < 16; i++ {
		if !step() {
			done = true
			break
		}
	}
	if !done {
		t.Fatal("prune did not finish from its committed head")
	}
	for _, table := range []string{"catalog_title_tombstones", "catalog_title_tombstone_viewers", "catalog_title_tombstone_attributes"} {
		var expired, live int
		if err = c.DB.QueryRow(`SELECT count(*) FROM ` + table + ` WHERE public_id='expired'`).Scan(&expired); err != nil {
			t.Fatal(err)
		}
		if err = c.DB.QueryRow(`SELECT count(*) FROM ` + table + ` WHERE public_id='live'`).Scan(&live); err != nil {
			t.Fatal(err)
		}
		want := 23
		if table == "catalog_title_tombstones" {
			want = 1
		}
		if expired != 0 || live != want {
			t.Fatal(table, expired, live)
		}
	}
}

func TestTombstonePruneEmptySkipsWriterAndUsesExpirySeek(t *testing.T) {
	c := catalogtest.Open(t)
	s := New(c.DB)
	before := dbwork.WriteGate().Stats().Acquired
	if err := s.PruneRemovedTitles(context.Background()); err != nil {
		t.Fatal(err)
	}
	if after := dbwork.WriteGate().Stats().Acquired; after != before {
		t.Fatalf("empty pruning acquired a writer: %d→%d", before, after)
	}
	rows, err := c.DB.Query(`EXPLAIN QUERY PLAN ` + expiredTombstoneHeadSQL)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seek := false
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(detail, "catalog_title_tombstones_expiry") && strings.Contains(detail, "SEARCH") {
			seek = true
		}
		if strings.Contains(detail, "SCAN") || strings.Contains(detail, "TEMP B-TREE") {
			t.Fatal(detail)
		}
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !seek {
		t.Fatal("expiry seek not used")
	}
}

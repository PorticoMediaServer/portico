package playbackv1

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestQueueSweepPreservesQueueRefreshedAfterSelection(t *testing.T) {
	c, s, _, q := sonicQueueFixture(t)
	now := time.Unix(1700000000, 0)
	s.Now = func() time.Time { return now }
	c.Exec(`INSERT INTO identity_devices(id,account_id,installation_id,name,platform,app,app_version,first_seen,last_seen) VALUES('device','account','installation','Fixture','test','test','1','now','now')`)
	c.Exec(`UPDATE queues_v1 SET updated_ms=? WHERE id=?`, now.Add(-queueIdleLifetime-time.Hour).UnixMilli(), q.id)
	var selected string
	if err := c.DB.QueryRow(`SELECT id FROM queues_v1 WHERE updated_ms<?`, now.Add(-queueIdleLifetime).UnixMilli()).Scan(&selected); err != nil {
		t.Fatal(err)
	}
	var before int
	if err := c.DB.QueryRow(`SELECT count(*) FROM queue_v1_chunks`).Scan(&before); err != nil || before == 0 {
		t.Fatalf("fixture chunks=%d: %v", before, err)
	}
	// The device wins the writer after selection and before removal starts.
	c.Exec(`UPDATE queues_v1 SET updated_ms=? WHERE id=?`, now.UnixMilli(), q.id)
	if err := s.removeQueue(context.Background(), selected); err != nil {
		t.Fatal(err)
	}
	fresh, err := loadQueue(context.Background(), c.DB, q.id)
	if err != nil || fresh.ownerID != "device" {
		t.Fatalf("refreshed queue retired: %+v %v", fresh, err)
	}
	var after int
	if err = c.DB.QueryRow(`SELECT count(*) FROM queue_v1_chunks`).Scan(&after); err != nil || after != before {
		t.Fatalf("refreshed queue lost chunks: %d -> %d (%v)", before, after, err)
	}
}

func TestQueueSweepRetiresBeforeChunkRemovalAndResumes(t *testing.T) {
	c, s, caller, q := sonicQueueFixture(t)
	c.Exec(`CREATE TRIGGER stop_queue_chunk_delete BEFORE DELETE ON queue_v1_chunks BEGIN SELECT RAISE(ABORT,'injected sweep interruption'); END`)
	if err := s.removeQueue(context.Background(), q.id); err == nil {
		t.Fatal("injected chunk failure was ignored")
	}
	fresh, err := loadQueue(context.Background(), c.DB, q.id)
	if err != nil || fresh.ownerID != "!device" || ownsQueue(caller, fresh) {
		t.Fatalf("interrupted sweep left an owned queue: %+v %v", fresh, err)
	}
	c.Exec(`DROP TRIGGER stop_queue_chunk_delete`)
	if err = s.removeQueue(context.Background(), q.id); err != nil {
		t.Fatal(err)
	}
	if _, err = loadQueue(context.Background(), c.DB, q.id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("retired queue survived resumed sweep: %v", err)
	}
	var chunks int
	if err = c.DB.QueryRow(`SELECT count(*) FROM queue_v1_chunks`).Scan(&chunks); err != nil || chunks != 0 {
		t.Fatalf("resumed sweep left chunks=%d: %v", chunks, err)
	}
}

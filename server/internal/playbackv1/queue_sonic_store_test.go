package playbackv1

import (
	"context"
	"database/sql"
	"reflect"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/sonic"
)

func TestSonicWindowStorageFreezesModelAndFallbackAndCascadesWithQueue(t *testing.T) {
	c := catalogtest.Open(t)
	c.Exec(`INSERT INTO queues_v1(id,owner_kind,owner_id,authority,account_id,profile_id,revision,created_ms,updated_ms) VALUES('q_sonic','device','device','local','account','profile',1,0,0)`)
	key := sonicWindowKey{queue: "q_sonic", seed: 29, lap: 1, window: 0}
	key.contents[0] = 1
	ordinary := []int64{4, 8, 1}
	model := &sonic.Identity{ID: sonic.ModelID, Version: sonic.ModelVersion, SHA256: sonic.ModelSHA256}
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		if _, err := captureSonicWindowTx(ctx, tx, key, ordinary, []int64{4, 1, 8}, nil); err == nil {
			t.Fatal("ordinary fallback accepted a different order")
		}
		first, err := captureSonicWindowTx(ctx, tx, key, ordinary, ordinary, nil)
		if err != nil {
			return err
		}
		second, err := captureSonicWindowTx(ctx, tx, key, ordinary, []int64{4, 1, 8}, model)
		if err != nil {
			return err
		}
		if first.model != nil || second.model != nil || !reflect.DeepEqual(first.ordinals, second.ordinals) {
			t.Fatal("later analysis rewrote captured ordinary fallback", first, second)
		}
		key.window++
		first, err = captureSonicWindowTx(ctx, tx, key, ordinary, []int64{4, 1, 8}, model)
		if err != nil {
			return err
		}
		second, err = captureSonicWindowTx(ctx, tx, key, ordinary, ordinary, nil)
		if err != nil {
			return err
		}
		if first.model == nil || second.model == nil || *first.model != *second.model || !reflect.DeepEqual(first.ordinals, second.ordinals) {
			t.Fatal("captured model/order did not survive a later fallback", first, second)
		}
		if _, err = tx.ExecContext(ctx, `UPDATE queue_v1_sonic_windows SET model_id='' WHERE queue_id='q_sonic'`); err == nil {
			t.Fatal("captured row allowed UPDATE")
		}
		return nil
	})
	c.Exec(`DELETE FROM queues_v1 WHERE id='q_sonic'`)
	var count int
	if err := c.DB.QueryRow(`SELECT count(*) FROM queue_v1_sonic_windows`).Scan(&count); err != nil || count != 0 {
		t.Fatal("queue deletion retained captured windows", count, err)
	}
}

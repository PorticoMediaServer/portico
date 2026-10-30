package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/apikit/apierror"
	"github.com/PorticoMediaServer/portico/apikit/idempotency"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func TestTransactionalIdempotencyReplayIsolationAndRollback(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "idempotency.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(8)
	ctx := context.Background()
	store := idempotency.Store{
		Lifetime: time.Hour,
		Begin: func(ctx context.Context) (idempotency.Transaction, error) {
			return dbwork.Begin(ctx, db, dbwork.ClassInteractive)
		},
		SweepBegin: func(ctx context.Context) (idempotency.Transaction, error) {
			return dbwork.Begin(ctx, db, dbwork.ClassMaintenance)
		},
	}
	if _, err = db.Exec(`CREATE TABLE applied(value INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	mutate := func(tx *sql.Tx) (idempotency.Result, error) {
		_, err := tx.Exec(`INSERT INTO applied VALUES(1)`)
		return idempotency.Result{Status: 201, Body: []byte(`{"id":"operation"}`), Headers: map[string]string{"Location": "/v1/operations/operation", "ETag": `"r1"`}}, err
	}
	request := func(body string) idempotency.Request {
		return idempotency.Request{Method: "POST", Path: "/v1/items/one", RawQuery: "mode=full&hint=first", Body: []byte(body)}
	}
	var wg sync.WaitGroup
	errs := make(chan error, 10)
	for range 10 {
		wg.Go(func() {
			_, err := store.Execute(ctx, "viewer/device", "request_000000001", request(`{"a":1,"b":2}`), mutate)
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err = db.QueryRow(`SELECT count(*) FROM applied`).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	replay, err := store.Execute(ctx, "viewer/device", "request_000000001", request(`{"b":2,"a":1}`), mutate)
	if err != nil || !replay.Replayed || replay.Status != 201 || replay.Headers["Location"] != "/v1/operations/operation" || replay.Headers["ETag"] != `"r1"` {
		t.Fatal(replay, err)
	}
	_, err = store.Execute(ctx, "viewer/device", "request_000000001", request(`{"a":2}`), mutate)
	var conflict *apierror.Error
	if !errors.As(err, &conflict) || conflict.Code != "idempotency_key_reused" {
		t.Fatal(err)
	}
	if _, err = store.Execute(ctx, "other/device", "request_000000001", request(`{"a":2}`), mutate); err != nil {
		t.Fatal(err)
	}
	for _, changed := range []idempotency.Request{
		{Method: "POST", Path: "/v1/items/two", RawQuery: "mode=full&hint=first", Body: []byte(`{"a":1,"b":2}`)},
		{Method: "PATCH", Path: "/v1/items/one", RawQuery: "mode=full&hint=first", Body: []byte(`{"a":1,"b":2}`)},
		{Method: "POST", Path: "/v1/items/one", RawQuery: "mode=brief&hint=first", Body: []byte(`{"a":1,"b":2}`)},
	} {
		_, err = store.Execute(ctx, "viewer/device", "request_000000001", changed, mutate)
		if !errors.As(err, &conflict) || conflict.Code != "idempotency_key_reused" {
			t.Fatalf("method/path/query change replayed: %v", err)
		}
	}
	_, err = store.Execute(ctx, "viewer/device", "request_000000002", request(`{}`), func(tx *sql.Tx) (idempotency.Result, error) {
		_, _ = mutate(tx)
		return idempotency.Result{}, errors.New("rollback")
	})
	if err == nil {
		t.Fatal("failed mutation committed")
	}
	if err = db.QueryRow(`SELECT count(*) FROM applied`).Scan(&count); err != nil || count != 2 {
		t.Fatal("rollback", count, err)
	}
	if _, err = store.Execute(ctx, "viewer/device", "request_000000002", request(`{}`), mutate); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE api_idempotency SET expires_at=0 WHERE scope='viewer/device' AND key='request_000000002'`); err != nil {
		t.Fatal(err)
	}
	removed, err := store.Sweep(ctx, 1)
	if err != nil || removed != 1 {
		t.Fatalf("bounded maintenance receipt sweep: removed=%d err=%v", removed, err)
	}
}

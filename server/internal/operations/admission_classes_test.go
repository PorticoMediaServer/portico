package operations

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
)

// PERF-S22: the owner's console work and viewers' domain work (bulk jobs,
// container resets) are admitted against separate bounds on active rows, so
// viewers' jobs never refuse the owner's scan and the owner's queue never
// refuses a viewer's "mark watched".
func TestAdmissionClassesDoNotCrowdEachOtherOut(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	store, p, auth := consoleFixture(t)
	scheduler := NewScheduler(store)
	if e := scheduler.Register(Adapter{Kind: "class-test", Lane: "maintenance", ValidateTx: func(context.Context, *sql.Tx, string) error { return nil }, Maintenance: func(context.Context, string) error { return nil }}); e != nil {
		t.Fatal(e)
	}
	seed := func(trigger string, n int) {
		t.Helper()
		tx, e := store.DB.Begin()
		if e != nil {
			t.Fatal(e)
		}
		for i := 0; i < n; i++ {
			if _, e = tx.Exec(`INSERT INTO console_operations(id,kind,resource,actor,trigger,state,phase,revision,attempt,created_ms,updated_ms,next_ms,domain_id,error_code,predecessor,settings_revision) VALUES(?,'seed',?,'a',?,'queued','waiting',1,0,0,0,0,'','','',0)`, fmt.Sprintf("%s-%05d", trigger, i), fmt.Sprintf("%s-%05d", trigger, i), trigger); e != nil {
				t.Fatal(e)
			}
		}
		if e = tx.Commit(); e != nil {
			t.Fatal(e)
		}
	}
	domain := func(resource string) error {
		tx, e := store.DB.Begin()
		if e != nil {
			return e
		}
		defer tx.Rollback()
		if _, e = scheduler.EnqueueDomainTx(context.Background(), tx, "class-test", resource, "viewer"); e != nil {
			return e
		}
		return tx.Commit()
	}
	console := func(key string) error {
		_, e := scheduler.Enqueue(context.Background(), p, auth, RunJob{Kind: "class-test", Resource: key, IdempotencyKey: key})
		return e
	}

	seed("owner", maxActiveConsoleOperations)
	if e := console("console-full"); !errors.Is(e, ErrCapacity) {
		t.Fatalf("console work past its bound admitted: %v", e)
	}
	if e := domain("domain-while-console-full"); e != nil {
		t.Fatalf("a full console queue refused a viewer's job: %v", e)
	}
	seed("domain", maxActiveDomainOperations-1)
	if e := domain("domain-full"); !errors.Is(e, ErrCapacity) {
		t.Fatalf("domain work past its bound admitted: %v", e)
	}
	if _, e := store.DB.Exec(`UPDATE console_operations SET state='succeeded' WHERE id='owner-00000'`); e != nil {
		t.Fatal(e)
	}
	if e := console("console-freed"); e != nil {
		t.Fatalf("a full domain queue refused the owner's work: %v", e)
	}
}

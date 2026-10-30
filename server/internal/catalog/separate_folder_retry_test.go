package catalog

import (
	"context"
	"database/sql"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func TestSeparateFolderAdmissionRetriesWithUnrelatedWriter(t *testing.T) {
	f := newSeparateFolderCommandFixture(t, 2)
	f.c.Exec(`INSERT INTO configuration(key,value) VALUES('separate-test-noise','0')`)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	kick, committed, stopped := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var writerErr error
	var commits atomic.Int32
	go func() {
		defer close(stopped)
		select {
		case <-kick:
		case <-ctx.Done():
			return
		}
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for {
			_, writerErr = dbwork.ExecWrite(ctx, f.c.DB, dbwork.ClassBackgroundMedia, `UPDATE configuration SET value=CAST(value AS INTEGER)+1 WHERE key='separate-test-noise'`)
			if writerErr != nil {
				return
			}
			if commits.Add(1) == 1 {
				close(committed)
			}
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
		}
	}()
	t.Cleanup(func() { cancel(); <-stopped })
	calls := 0
	authorize := func(*sql.Tx) error {
		calls++
		if calls == 1 {
			// Force the first unrelated commit inside the initial proof window.
			// Subsequent commits run at 1 Hz; no fixed wait is used to win a race.
			close(kick)
			select {
			case <-committed:
			case <-stopped:
				return writerErr
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}
	ref, public, done, err := New(f.c.DB).admitSeparateFolder(dbwork.WithPlan(ctx, dbwork.DefaultPlan(dbwork.ClassInteractive)), "music", f.objects[0], "r1", authorize)
	cancel()
	<-stopped
	if writerErr != nil && !errors.Is(writerErr, context.Canceled) {
		t.Fatal(writerErr)
	}
	if err != nil || ref.ID == "" || public != "" || done {
		t.Fatalf("admission under unrelated writer: ref=%+v public=%q done=%v error=%v", ref, public, done, err)
	}
	if calls != 4 || commits.Load() < 1 {
		t.Fatalf("expected a single conflicted proof then successful refresh: authorizations=%d commits=%d", calls, commits.Load())
	}
	if rows(t, f.c, `SELECT count(*) FROM catalog_albums`) != 2 || rows(t, f.c, `SELECT count(*) FROM catalog_entity_mutations WHERE parent_mutation_id IS NULL`) != 1 {
		t.Fatal("proof refresh duplicated target or command")
	}
}

func TestSeparateFolderResumeIgnoresUnrelatedPublication(t *testing.T) {
	for _, complete := range []bool{false, true} {
		t.Run(map[bool]string{false: "active", true: "completed"}[complete], func(t *testing.T) {
			f := newSeparateFolderCommandFixture(t, 2)
			f.c.Exec(`INSERT INTO configuration(key,value) VALUES('separate-test-noise','0')`)
			s := New(f.c.DB)
			var held compactcatalog.MutationRef
			var expected string
			var err error
			if complete {
				expected, err = s.SeparateFolder(context.Background(), "music", f.objects[0], "r1", func(*sql.Tx) error { return nil })
				if err != nil || expected == "" {
					t.Fatalf("complete command: %q %v", expected, err)
				}
			} else {
				held = separateFolderCommandAdmitFixture(t, f, f.objects[0])
			}
			p, err := s.separateFolderProof(context.Background(), "music", f.objects[0], "r1", func(*sql.Tx) error { return nil })
			if err != nil || p.ready {
				t.Fatalf("resume should need no window: ready=%v error=%v", p != nil && p.ready, err)
			}
			if _, err = dbwork.ExecWrite(context.Background(), f.c.DB, dbwork.ClassBackgroundMedia, `UPDATE configuration SET value='1' WHERE key='separate-test-noise'`); err != nil {
				t.Fatal(err)
			}
			if p.publication == dbwork.Publications() {
				t.Fatal("fixture did not create an intervening publication")
			}
			ctx := context.WithValue(context.Background(), separateFolderProofKey{}, p)
			err = dbwork.WithWriteTx(ctx, f.c.DB, dbwork.ClassInteractive, func(tx *sql.Tx) error {
				ref, public, done, e := separateFolderAdmitTx(ctx, tx, "music", f.objects[0], "r1")
				if e != nil {
					return e
				}
				if complete {
					if !done || public != expected {
						t.Fatalf("completed receipt: done=%v public=%q expected=%q", done, public, expected)
					}
				} else if done || public != "" || ref != held {
					t.Fatalf("active resume changed owner: ref=%+v held=%+v public=%q done=%v", ref, held, public, done)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if rows(t, f.c, `SELECT count(*) FROM catalog_albums`) != 2 {
				t.Fatal("resume created a duplicate target")
			}
		})
	}
}

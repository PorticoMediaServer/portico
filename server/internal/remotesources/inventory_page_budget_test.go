package remotesources

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

func TestInventoryPagesSelectBoundedImmutableSourceWindowsAndResume(t *testing.T) {
	f := newNativeFixture(t)
	ctx := context.Background()
	pending, err := f.s.ListInventoryPage(ctx, f.job, f.root, "", 2)
	if err != nil || !pending.Pending || pending.Limit != 2 {
		t.Fatalf("pending %+v %v", pending, err)
	}
	f.release(t)
	waitNative(t, f, func() bool {
		if f.count(t, `SELECT ready FROM remote_listing_sessions WHERE id=?`, pending.ID) == 1 {
			return true
		}
		// Source-process startup can legitimately report capacity_busy while
		// the configuration check retires. Exercise the worker's next bounded
		// poll instead of waiting forever for an attempt that already ended.
		page, err := f.s.ListInventoryPage(ctx, f.job, f.root, pending.Next, pending.Limit)
		if err != nil && !errors.Is(err, storage.ErrBusy) {
			t.Fatal(err)
		}
		if err == nil && page.ID != pending.ID {
			t.Fatal("poll replaced immutable source listing")
		}
		return false
	})
	if err := dbwork.WithWriteTx(ctx, f.db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error { return f.s.CommitPage(ctx, tx, pending) }); err != nil {
		t.Fatal(err)
	}
	first, err := f.s.ListInventoryPage(ctx, f.job, f.root, pending.Next, 2)
	if err != nil || len(first.Entries) != 2 || first.Complete {
		t.Fatalf("first page %+v %v", first, err)
	}
	wide, err := f.s.ListPage(ctx, f.job, f.root, pending.Next)
	if err != nil || len(wide.Entries) != 128 || wide.Entries[0] != first.Entries[0] || wide.Entries[1] != first.Entries[1] {
		t.Fatal("page limit changed source evidence", err)
	}
	commit := func(page storage.RemotePage) error {
		return dbwork.WithWriteTx(ctx, f.db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error { return f.s.CommitPage(ctx, tx, page) })
	}
	for _, limit := range []int{0, 129, 128} {
		forged := first
		forged.Limit = limit
		if err := commit(forged); !errors.Is(err, storage.ErrRemoteCursor) {
			t.Fatalf("forged page limit %d accepted: %v", limit, err)
		}
	}
	// A transaction that aborts leaves both source observations and continuation
	// untouched; the same immutable page is then accepted exactly once.
	rollback := errors.New("cancel publication")
	err = dbwork.WithWriteTx(ctx, f.db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
		if err := f.s.CommitPage(ctx, tx, first); err != nil {
			return err
		}
		return rollback
	})
	if !errors.Is(err, rollback) || f.count(t, `SELECT count(*) FROM remote_scan_observations`) != 0 {
		t.Fatal("rollback exposed partial page", err)
	}
	if err := commit(first); err != nil {
		t.Fatal(err)
	}
	if err := commit(first); err != nil {
		t.Fatal("exact replay failed", err)
	}
	if f.count(t, `SELECT count(*) FROM remote_scan_observations`) != 2 {
		t.Fatal("replay duplicated source observations")
	}
	seen := map[string]bool{}
	for _, e := range first.Entries {
		seen[e.Path] = true
	}
	after := first.Next
	for {
		page, err := f.s.ListInventoryPage(ctx, f.job, f.root, after, 2)
		if err != nil || page.Pending || len(page.Entries) > 2 {
			t.Fatalf("bounded page %+v %v", page, err)
		}
		for _, e := range page.Entries {
			if seen[e.Path] {
				t.Fatal("repeated raw name", e.Path)
			}
			seen[e.Path] = true
		}
		if err := commit(page); err != nil {
			t.Fatal(err)
		}
		if page.Complete {
			break
		}
		after = page.Next
	}
	if len(seen) != 130 || f.count(t, `SELECT count(*) FROM remote_scan_observations`) != 130 || f.starts(t) != 1 {
		t.Fatal("bounded traversal lost names or restarted source")
	}
}

func TestInventoryPageLimitMayShrinkAtAnExistingKeyBoundary(t *testing.T) {
	f := newNativeFixture(t)
	ctx := context.Background()
	pending, err := f.s.ListPage(ctx, f.job, f.root, "")
	if err != nil {
		t.Fatal(err)
	}
	f.release(t)
	waitNative(t, f, func() bool {
		if f.count(t, `SELECT ready FROM remote_listing_sessions WHERE id=?`, pending.ID) == 1 {
			return true
		}
		// Source-process startup can legitimately report capacity_busy while
		// the configuration check retires. Exercise the worker's next bounded
		// poll instead of waiting forever for an attempt that already ended.
		page, err := f.s.ListInventoryPage(ctx, f.job, f.root, pending.Next, pending.Limit)
		if err != nil && !errors.Is(err, storage.ErrBusy) {
			t.Fatal(err)
		}
		if err == nil && page.ID != pending.ID {
			t.Fatal("poll replaced immutable source listing")
		}
		return false
	})
	if err := dbwork.WithWriteTx(ctx, f.db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error { return f.s.CommitPage(ctx, tx, pending) }); err != nil {
		t.Fatal(err)
	}
	wide, err := f.s.ListPage(ctx, f.job, f.root, pending.Next)
	if err != nil {
		t.Fatal(err)
	}
	err = dbwork.WithWriteTx(ctx, f.db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error { return f.s.CommitPage(ctx, tx, wide) })
	if err != nil {
		t.Fatal(err)
	}
	last, err := f.s.ListInventoryPage(ctx, f.job, f.root, wide.Next, 2)
	if err != nil || !last.Complete || len(last.Entries) != 2 || last.Entries[0].Path <= wide.Entries[len(wide.Entries)-1].Path {
		t.Fatalf("shrinking source page skipped/rewound: %+v %v", last, err)
	}
}

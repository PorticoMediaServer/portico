package catalog

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func TestDashboardPeopleTotalUsesPanePeriodAndFiltersBeyondPage(t *testing.T) {
	db := homeFixture(t)
	service := New(db)
	item := homeNames(t, db)["m1"].ID
	until := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	insert := func(playback, account, profile, library, title string, kind int, at time.Time) {
		t.Helper()
		_, err := db.Exec(`INSERT INTO play_history(playback_id,authority,account_id,profile_id,user_name,profile_name,item_id,library_id,kind,title,started_ms,updated_ms) VALUES(?,'local',?,?,?,'Kids',?,?,?,?,?,?)`, playback, account, profile, account, item, library, kind, title, at.UnixMilli(), at.UnixMilli())
		if err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 137; i++ {
		account := fmt.Sprintf("account-%03d", i)
		insert(account, account, "primary", "movies", "Movie", 1, until.Add(-time.Hour))
	}
	// Multiple plays and profiles belong to one actual account.
	insert("second-profile", "account-000", "secondary", "movies", "Unique match", 1, until.Add(-2*time.Hour))
	insert("music", "music-person", "primary", "music", "Track", 7, until.Add(-time.Hour))
	insert("ten-days", "ten-day-person", "primary", "movies", "Older movie", 1, until.AddDate(0, 0, -10))
	insert("forty-days", "forty-day-person", "primary", "movies", "Older movie", 1, until.AddDate(0, 0, -40))
	insert("at-until", "excluded-current-quarter", "primary", "movies", "Movie", 1, until)

	base := PlayHistoryQuery{TimeZone: "Asia/Kathmandu", Until: until.Format(time.RFC3339), Period: "7d", Limit: 100}
	check := func(q PlayHistoryQuery, want int64) {
		t.Helper()
		err := dbwork.WithReadSnapshot(context.Background(), db, func(ctx context.Context) error {
			r, err := service.historyInterval(ctx, q)
			if err != nil {
				return err
			}
			total, err := readDashboardPeopleTotal(ctx, dbwork.Snapshot(ctx), q, r, false)
			if err != nil {
				return err
			}
			if total == nil || *total != want {
				t.Fatalf("query=%+v total=%v want=%d", q, total, want)
			}
			page, err := service.DashboardPeople(ctx, q)
			if err != nil {
				return err
			}
			if int64(len(page.Items)) > want || len(page.Items) > q.Limit {
				t.Fatal("page exceeded actual scope total", page, want)
			}
			if want > 100 && (len(page.Items) != 100 || page.NextCursor == "") {
				t.Fatal("fixture did not exercise a real second page", page)
			}
			if page.NextCursor != "" {
				q.Cursor = page.NextCursor
				later, err := readDashboardPeopleTotal(ctx, dbwork.Snapshot(ctx), q, r, false)
				if err != nil || later != nil {
					t.Fatal("cursor page invented a total", later, err)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	check(base, 138)
	q := base
	q.Period = "30d"
	check(q, 139)
	q.Period = "90d"
	check(q, 140)
	q = base
	q.Media = "movies"
	check(q, 137)
	q.LibraryID = "music"
	check(q, 0)
	q = base
	q.LibraryID = "music"
	check(q, 1)
	q = base
	q.Search = "Unique match"
	check(q, 1)
	q = base
	q.AccountID = "account-000"
	check(q, 1)
	q.AccountID = "absent-account"
	check(q, 0)
}

func TestDashboardPeopleTotalBorrowsReadAndRefreshIncludesLateAccount(t *testing.T) {
	db := homeFixture(t)
	service := New(db)
	item := homeNames(t, db)["m1"].ID
	at := time.Date(2026, 10, 22, 11, 45, 0, 0, time.UTC).UnixMilli()
	q := PlayHistoryQuery{TimeZone: "UTC", Until: "2026-10-22T12:00:00Z", Period: "today", Limit: 100}
	insert := func(playback, account string) {
		t.Helper()
		if _, err := db.Exec(`INSERT INTO play_history(playback_id,authority,account_id,profile_id,user_name,item_id,library_id,kind,title,started_ms,updated_ms) VALUES(?,'local',?,'primary',?,?,'movies',1,'Movie',?,?)`, playback, account, account, item, at, at); err != nil {
			t.Fatal(err)
		}
	}
	insert("initial", "initial-person")
	check := func(ctx context.Context, want int64) error {
		r, err := service.historyInterval(ctx, q)
		if err != nil {
			return err
		}
		total, err := readDashboardPeopleTotal(ctx, dbwork.Snapshot(ctx), q, r, false)
		if err != nil {
			return err
		}
		page, err := service.DashboardPeople(ctx, q)
		if err != nil {
			return err
		}
		if total == nil || *total != want || int64(len(page.Items)) != want {
			t.Fatal("mixed count/page reads", total, page, want)
		}
		return nil
	}
	if err := dbwork.WithReadSnapshot(context.Background(), db, func(ctx context.Context) error {
		if err := check(ctx, 1); err != nil {
			return err
		}
		// Genuine late event-time fact becomes known during this existing read.
		insert("late", "late-person")
		return check(ctx, 1)
	}); err != nil {
		t.Fatal(err)
	}
	if err := dbwork.WithReadSnapshot(context.Background(), db, func(ctx context.Context) error { return check(ctx, 2) }); err != nil {
		t.Fatal(err)
	}
}

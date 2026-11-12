package db

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

// A play whose library has been removed is named only to an asker with every library: an
// administrator held to some libraries never learns what was played in one they were not
// given, the day it is removed. The list shows them the play with its title withheld (the
// lead's ruling, 12 Nov); the summary counts it for nobody but those with every library.
func TestARemovedLibrarysPlaysAreNotForALimitedAdministrator(t *testing.T) {
	ctx := context.Background()
	d, err := Open(ctx, t.TempDir()+"/main.db")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	now := time.Now().UnixMilli()
	err = d.write(ctx, User, func(tx *sql.Tx) error {
		for _, q := range []string{
			`INSERT INTO users (id, public_id, username, role, all_libraries, created_at, updated_at) VALUES (1, 'usr_o', 'owner', 'owner', 1, 0, 0)`,
			`INSERT INTO users (id, public_id, username, role, all_libraries, created_at, updated_at) VALUES (2, 'usr_h', 'held', 'admin', 0, 0, 0)`,
			`INSERT INTO libraries (id, public_id, name, kind, created_at, updated_at) VALUES (1, 'lib_k', 'Kids', 'movies', 0, 0)`,
			`INSERT INTO library_access (library_id, user_id) VALUES (1, 2)`,
		} {
			if _, err := tx.Exec(q); err != nil {
				return err
			}
		}
		for n, library := range []any{1, nil} {
			if _, err := tx.Exec(`INSERT INTO play_history (profile_name, account_name, client_event_id, played_at, item_kind, title, user_id, library_id)
				VALUES ('owner', 'owner', ?, ?, 1, ?, 1, ?)`, n, now-int64(n), []string{"Kids film", "Removed library's film"}[n], library); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for asker, want := range map[int64]int{1: 2, 2: 1} {
		plays, _, err := d.Plays(ctx, PlayQuery{Asker: asker, Limit: 10})
		named := 0
		for _, p := range plays {
			if !p.Withheld && p.Title != "" {
				named++
			}
		}
		if err != nil || len(plays) != 2 || named != want {
			t.Errorf("asker %d read %d plays, %d named, want 2 and %d: %v", asker, len(plays), named, want, err)
		}
		sum, err := d.SummarizePlays(ctx, PlayQuery{Asker: asker})
		if err != nil || len(sum.Titles) != want {
			t.Errorf("asker %d's summary has %d titles, want %d: %v", asker, len(sum.Titles), want, err)
		}
	}
}

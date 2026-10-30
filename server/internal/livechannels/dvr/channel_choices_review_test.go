package dvr

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func TestChannelChoicesPagePastRestrictedPrefix(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db, err := persistence.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	src, gen := strings.Repeat("a", 48), strings.Repeat("b", 48)
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO live_source_identities VALUES(?)`, []any{src}},
		{`INSERT INTO live_generations VALUES(?,?,'Guide',2,400,0,'now')`, []any{gen, src}},
		{`INSERT INTO live_sources VALUES(?,'Aerial',1,'active',2,?,'now')`, []any{src, gen}},
	} {
		if _, err = db.Exec(q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 400; i++ {
		id := fmt.Sprintf("%064x", i+1)
		if _, err = db.Exec(`INSERT INTO live_channel_versions(generation_id,channel_id,provider_key,name,number,source_number,group_name,position,locator_sealed,available_start,available_end) VALUES(?,?,'provider','Channel','1',NULL,'',?,x'00','','')`, gen, id, i); err != nil {
			t.Fatal(err)
		}
	}
	for _, first := range []int{1, 31, 351, 401} {
		t.Run(fmt.Sprint(first), func(t *testing.T) {
			threshold := fmt.Sprintf("%064x", first)
			auth := func(context.Context, *sql.Tx, bool) (string, func(string, string) bool, error) {
				return "restricted", func(source, channel string) bool {
					return source == src && (channel == "" || channel >= threshold)
				}, nil
			}
			owner := livechannels.Owner{Authority: "local", AccountID: "account", ProfileID: "profile"}
			s := &Store{db: db}
			for _, invalid := range []string{"-1", "00", "x", strings.Repeat("a", 64), strings.Repeat("1", 200)} {
				if _, err := s.ChannelChoices(context.Background(), auth, owner, src, "", gen+"."+invalid); !errors.Is(err, ErrInvalid) {
					t.Fatalf("accepted malformed position %q: %v", invalid, err)
				}
			}
			if _, err := s.ChannelChoices(context.Background(), auth, owner, src, "", strings.Repeat("c", 48)+".0"); !errors.Is(err, ErrConflict) {
				t.Fatalf("accepted changed generation: %v", err)
			}
			cursor := ""
			seen := map[string]bool{}
			for pages := 0; pages < 20; pages++ {
				page, err := s.ChannelChoices(context.Background(), auth, owner, src, "", cursor)
				if err != nil {
					t.Fatal(err)
				}
				if len(page.Channels) > 30 {
					t.Fatal("unbounded visible page")
				}
				for _, ch := range page.Channels {
					if ch.ID < threshold || seen[ch.ID] {
						t.Fatalf("excluded or duplicated channel %s", ch.ID)
					}
					seen[ch.ID] = true
				}
				if first > 1 && page.NextCursor != "" {
					for i := 1; i < first; i++ {
						if strings.Contains(page.NextCursor, fmt.Sprintf("%064x", i)) {
							t.Fatalf("cursor reveals excluded channel %d", i)
						}
					}
				}
				if page.NextCursor == "" {
					break
				}
				if page.NextCursor == cursor {
					t.Fatal("cursor did not advance")
				}
				cursor = page.NextCursor
			}
			if len(seen) != 401-first {
				t.Fatalf("got %d allowed choices, want %d", len(seen), 401-first)
			}
		})
	}
}

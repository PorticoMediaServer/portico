package operations

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func seedOperationItem(t testing.TB, db *sql.DB, library, key, title string) (string, int64) {
	t.Helper()
	var id int64
	var public string
	err := dbwork.WithWriteTx(context.Background(), db, dbwork.ClassInteractive, func(tx *sql.Tx) error {
		lib, err := compactcatalog.LibraryTx(context.Background(), tx, library)
		if err != nil {
			return err
		}
		id, _, err = compactcatalog.UpsertEntityTx(context.Background(), tx, compactcatalog.Entity{Library: lib, Kind: compactcatalog.Movie, Key: "fixture:" + key, Title: title})
		if err != nil {
			return err
		}
		return tx.QueryRow(`SELECT pid(public_id) FROM catalog_entities WHERE id=?`, id).Scan(&public)
	})
	if err != nil {
		t.Fatalf("seed operation item %q: %v", key, err)
	}
	return public, id
}

func setOperationItemTitle(t testing.TB, db *sql.DB, id int64, title string) {
	t.Helper()
	err := dbwork.WithWriteTx(context.Background(), db, dbwork.ClassInteractive, func(tx *sql.Tx) error {
		return compactcatalog.SetFieldsTx(context.Background(), tx, id, compactcatalog.Owner, map[string]any{"title": title})
	})
	if err != nil {
		t.Fatal(err)
	}
}

func seedOperationAsset(t testing.TB, db *sql.DB, path string) (string, int64) {
	t.Helper()
	var id int64
	var token string
	err := dbwork.WithWriteTx(context.Background(), db, dbwork.ClassInteractive, func(tx *sql.Tx) error {
		var err error
		id, token, err = compactcatalog.UpsertAssetTx(context.Background(), tx, compactcatalog.Asset{Path: path, Size: 1, ModifiedNS: 1, Container: "mkv", VideoCodec: "h264", AudioCodec: "aac", Width: 1920, Height: 1080, Duration: 60})
		return err
	})
	if err != nil {
		t.Fatalf("seed operation asset %q: %v", path, err)
	}
	return token, id
}

// historyFixture seeds v1 sessions across the retention window: films played
// directly, and every third one converted.
func historyFixture(t *testing.T, s *Store, count int) {
	t.Helper()
	if _, e := s.DB.Exec(`INSERT INTO libraries(id,name,kind,root) VALUES('library-one','Films','movie','/films')`); e != nil {
		t.Fatal(e)
	}
	for i := 1; i <= count; i++ {
		_, itemID := seedOperationItem(t, s.DB, "library-one", fmt.Sprintf("item-%d", i), fmt.Sprintf("Film %d", i))
		presentation := `{"mode":"direct","url":"/secret","decision":{"video":{"action":"direct","reasons":[]}}}`
		if i%3 == 0 {
			presentation = `{"mode":"stream","url":"/secret","decision":{"video":{"action":"transcode","reasons":[]},"audio":{"action":"copy","reasons":[]}}}`
		}
		created := s.now() - int64(count-i+1)*1000 // the last seeded is the newest
		if _, e := s.DB.Exec(`INSERT INTO playback_v1_sessions(id,device_id,account_id,profile_id,authority,start_key,start_digest,kind,role,state,item_id,request,revision,generation,presentation,lease_expires_ms,created_ms,updated_ms) VALUES(?,'device','owner','primary','local',?,'','vod','viewer','playing',?,'{"quality":{"mode":"original"}}',1,1,?,?,?,?)`,
			fmt.Sprintf("play-%d", i), fmt.Sprintf("key-%d", i), itemID, presentation, created+60000, created, created); e != nil {
			t.Fatal(e)
		}
	}
}

func TestPlaybackHistoryPagesNewestFirstWithoutRepeating(t *testing.T) {
	s, _, auth := consoleFixture(t)
	ctx := context.Background()
	historyFixture(t, s, 7)

	seen := map[string]bool{}
	page, e := s.PlaybackHistory(ctx, auth, "24h", "", 3)
	if e != nil {
		t.Fatal(e)
	}
	if len(page.Items) != 3 || page.NextCursor == "" {
		t.Fatal("first page", page)
	}
	if page.Items[0].ID != "play-7" || page.Items[0].Title != "Film 7" || page.Items[0].LibraryName != "Films" {
		t.Fatal("newest first with its item and library", page.Items[0])
	}
	if page.Items[0].EndedAt != nil || page.Items[0].DurationSeconds != nil || page.Items[0].State != "playing" {
		t.Fatal("a running session has no end time and must not invent one", page.Items[0])
	}
	if first := page.Items[0]; first.DeliveryMode != "direct" || first.DeliveryReason != "original" || first.QualityMode != "original" {
		t.Fatal("a direct play reports its delivery and quality", first)
	}
	if second := page.Items[1]; second.ID != "play-6" || second.DeliveryMode != "stream" || second.DeliveryReason != "video_conversion" {
		t.Fatal("a converted play reports its strategy", second)
	}
	total := 0
	for cursor := ""; ; {
		page, e := s.PlaybackHistory(ctx, auth, "24h", cursor, 3)
		if e != nil {
			t.Fatal(e)
		}
		for _, v := range page.Items {
			if seen[v.ID] {
				t.Fatal("a row was returned twice", v.ID)
			}
			seen[v.ID] = true
			total++
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if total != 7 {
		t.Fatal("paging must reach every row exactly once", total)
	}
	if _, e = s.PlaybackHistory(ctx, auth, "3y", "", 10); !errors.Is(e, ErrInvalid) {
		t.Fatal("an unknown period must be refused", e)
	}
	if _, e = s.PlaybackHistory(ctx, auth, "24h", "", 500); !errors.Is(e, ErrInvalid) {
		t.Fatal("an unbounded page must be refused", e)
	}
}

func TestPlaybackHistoryReportsRecordedCompletionAndExportsSafely(t *testing.T) {
	s, _, auth := consoleFixture(t)
	ctx := context.Background()
	historyFixture(t, s, 2)
	_, formulaID := seedOperationItem(t, s.DB, "library-one", "item-2", "Film 2")
	setOperationItemTitle(t, s.DB, formulaID, "=cmd|calc")
	// A v1 session records its own end.
	if _, e := s.DB.Exec(`UPDATE playback_v1_sessions SET ended_ms=created_ms+42000,end_reason='transferred' WHERE id='play-1'`); e != nil {
		t.Fatal(e)
	}
	page, e := s.PlaybackHistory(ctx, auth, "24h", "", 10)
	if e != nil {
		t.Fatal(e)
	}
	var ended *PlaybackHistoryEntry
	for i := range page.Items {
		if page.Items[i].ID == "play-1" {
			ended = &page.Items[i]
		}
	}
	if ended == nil || ended.EndedAt == nil || ended.DurationSeconds == nil || *ended.DurationSeconds != 42 || ended.State != "transferred" {
		t.Fatal("an ended session must report its recorded duration and why it ended", ended)
	}

	csv, e := s.PlaybackHistoryCSV(ctx, auth, "24h")
	if e != nil {
		t.Fatal(e)
	}
	lines := strings.Split(strings.TrimSpace(csv), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "id,viewer,title") {
		t.Fatal("export must have a header and one row per occurrence", lines)
	}
	if !strings.Contains(csv, `"'=cmd|calc"`) {
		t.Fatal("a title that looks like a formula must be neutralised", csv)
	}
}

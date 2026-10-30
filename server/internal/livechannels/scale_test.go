package livechannels

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestLargeStageInterruptedAfterCommittedProgrammeBatch(t *testing.T) {
	s := testStore(t)
	in := testInput()
	in.Playlist = "#EXTM3U\n#EXTINF:-1 tvg-id=\"one\",One\nhttps://fixture.invalid/one\n"
	var xml strings.Builder
	xml.WriteString("<tv>")
	start, _ := time.Parse(time.RFC3339, "2026-09-05T12:00:00Z")
	for i := 0; i < 300; i++ {
		a := start.Add(time.Duration(i) * time.Minute)
		fmt.Fprintf(&xml, `<programme id="programme-%d" channel="one" start="%s" stop="%s"><title>Programme %d</title></programme>`, i, a.Format("20060102150405 -0700"), a.Add(time.Minute).Format("20060102150405 -0700"), i)
	}
	xml.WriteString("</tv>")
	in.Guide = xml.String()
	var firstBatch *sql.Tx
	owner := testAuthority("viewer", true, true)
	a := func(ctx context.Context, tx *sql.Tx, admin bool) (string, func(string, string) bool, error) {
		var staged int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM live_programmes`).Scan(&staged); err != nil {
			return "", nil, err
		}
		if staged == StageBatch {
			if firstBatch == nil {
				firstBatch = tx // Post-work authorization of the first batch.
			} else if firstBatch != tx {
				// The next transaction can see the committed first batch. Do not
				// count authorizer invocations: checks within a batch may change.
				return "", nil, context.DeadlineExceeded
			}
		}
		return owner(ctx, tx, admin)
	}
	if _, e := s.Save(context.Background(), a, in); e != ErrUnavailable {
		t.Fatal(e)
	}
	var rows int
	if e := s.db.QueryRow(`SELECT count(*) FROM live_programmes`).Scan(&rows); e != nil || rows != StageBatch {
		t.Fatal("first batch was not retained", rows, e)
	}
	sources, e := s.Sources(context.Background(), owner)
	if e != nil || len(sources) != 0 {
		t.Fatal("partial source became current")
	}
	saved, e := s.Save(context.Background(), owner, in)
	if e != nil || saved.Programmes != 300 {
		t.Fatal("resume failed", e)
	}
	var unique, total int
	s.db.QueryRow(`SELECT count(DISTINCT id),count(*) FROM live_programmes WHERE generation_id=?`, saved.Generation).Scan(&unique, &total)
	if unique != 300 || total != 300 {
		t.Fatal("resume duplicated programmes", unique, total)
	}
}
func TestGuideContinuesAcrossDeniedCandidateBatch(t *testing.T) {
	s := testStore(t)
	in := testInput()
	var playlist strings.Builder
	playlist.WriteString("#EXTM3U\n")
	for i := 0; i < 600; i++ {
		fmt.Fprintf(&playlist, "#EXTINF:-1 tvg-id=\"ch-%03d\",Channel %03d\nhttps://fixture.invalid/%d\n", i, i, i)
	}
	in.Playlist = playlist.String()
	in.Guide = `<tv><programme channel="ch-599" start="20260905120000 +0000" stop="20260905130000 +0000"><title>Authorized last programme</title></programme></tv>`
	owner := testAuthority("owner", true, true)
	saved, e := s.Save(context.Background(), owner, in)
	if e != nil {
		t.Fatal(e)
	}
	allowedID := id(saved.ID, "ch-599")
	viewer := func(context.Context, *sql.Tx, bool) (string, func(string, string) bool, error) {
		return "restricted-viewer", func(sid, cid string) bool { return cid == allowedID }, nil
	}
	q := testQuery()
	first, e := s.Guide(context.Background(), viewer, q)
	if e != nil || first.State != "continuation-required" || first.NextCursor == "" || len(first.Channels) != 0 || len(first.Sources) != 0 {
		t.Fatal("denied prefix exposed or lost", first, e)
	}
	q.Cursor = first.NextCursor
	second, e := s.Guide(context.Background(), viewer, q)
	if e != nil || len(second.Channels) != 1 || second.Channels[0].ID != allowedID || second.NextCursor != "" || len(second.Sources) != 1 || second.Sources[0].AvailableStart != "2026-09-05T12:00:00Z" {
		t.Fatal("authorized tail unreachable", second, e)
	}
}

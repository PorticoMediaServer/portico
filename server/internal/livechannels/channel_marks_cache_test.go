package livechannels

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
)

func TestSourceChannelMarksCacheReusesSnapshotAndBoundsRetention(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s := testStore(t)
	ctx := context.Background()
	in := testInput()
	in.Guide = "<tv/>"
	saved, err := s.Save(ctx, testAuthority("owner", true, true), in)
	if err != nil {
		t.Fatal(err)
	}
	read := func(source string) map[string]ChannelMark {
		t.Helper()
		tx, e := s.db.Begin()
		if e != nil {
			t.Fatal(e)
		}
		defer tx.Rollback()
		marks, e := SourceChannelMarksTx(ctx, tx, s.db, source)
		if e != nil {
			t.Fatal(e)
		}
		return marks
	}
	expected := read(saved.ID)
	if len(expected) != 2 {
		t.Fatal(expected)
	}
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	// Make a full scan impossible without changing the cache key. A warm read
	// must still work; an explicitly uncached read proves the probe is effective.
	if _, err = tx.Exec("ALTER TABLE live_channel_versions RENAME TO marks_unavailable"); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	got, err := SourceChannelMarksTx(ctx, tx, s.db, saved.ID)
	if err != nil || len(got) != len(expected) {
		tx.Rollback()
		t.Fatal(got, err)
	}
	if _, err = SourceChannelMarksTx(ctx, tx, nil, saved.ID); err == nil {
		tx.Rollback()
		t.Fatal("uncached probe unexpectedly read renamed table")
	}
	tx.Rollback()
	for i := 0; i < markCacheEntries+1; i++ {
		in.ID = fmt.Sprintf("%048x", i+10)
		in.RequestID = fmt.Sprintf("%048x", i+100)
		next, e := s.Save(ctx, testAuthority("owner", true, true), in)
		if e != nil {
			t.Fatal(e)
		}
		read(next.ID)
	}
	sourceMarkCache.Lock()
	defer sourceMarkCache.Unlock()
	total := 0
	for _, entry := range sourceMarkCache.entries {
		total += len(entry.marks)
		if entry.key.db == s.db && entry.key.source == saved.ID {
			t.Error("least recently used source retained")
		}
	}
	if len(sourceMarkCache.entries) > markCacheEntries || total > markCacheChannels {
		t.Fatalf("unbounded cache: %d entries, %d channels", len(sourceMarkCache.entries), total)
	}
}

func TestSourceChannelMarksWriteRollbackCannotPoisonCache(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	in := testInput()
	in.Guide = "<tv/>"
	in.Playlist = "#EXTM3U\n#EXTINF:-1 tvg-id=\"one\" tvg-chno=\"1\",One\nhttps://fixture.invalid/one\n#EXTINF:-1 tvg-id=\"two\" tvg-chno=\"2\",One\nhttps://fixture.invalid/two\n"
	saved, err := s.Save(ctx, testAuthority("owner", true, true), in)
	if err != nil {
		t.Fatal(err)
	}
	var channel string
	if err = s.db.QueryRow("SELECT channel_id FROM live_channel_versions WHERE generation_id=? AND number='1'", saved.Generation).Scan(&channel); err != nil {
		t.Fatal(err)
	}
	write := func(number string, commit bool) {
		t.Helper()
		tx, e := s.db.Begin()
		if e != nil {
			t.Fatal(e)
		}
		defer tx.Rollback()
		if _, e = tx.Exec("INSERT INTO admin_channel_numbers VALUES(?,?,?,'',0,1)", saved.ID, channel, number); e != nil {
			t.Fatal(e)
		}
		if _, e = tx.Exec("INSERT INTO admin_documents VALUES(?,2,'{}',1)", "live-source:"+saved.ID); e != nil {
			t.Fatal(e)
		}
		marks, e := SourceChannelMarksTx(ctx, tx, nil, saved.ID)
		if e != nil || marks[saved.ID+"\x00"+channel].Text != "O "+number {
			t.Fatal(marks, e)
		}
		if commit {
			if e = tx.Commit(); e != nil {
				t.Fatal(e)
			}
		}
	}
	write("8", false)
	write("9", true)
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	marks, err := SourceChannelMarksTx(ctx, tx, s.db, saved.ID)
	if err != nil || marks[saved.ID+"\x00"+channel].Text != "O 9" {
		t.Fatal(marks, err)
	}
	// The same transaction API is used by recording enrichment and only marks
	// references authorized for this caller.
	channels := []Channel{{SourceID: saved.ID, ID: channel}, {SourceID: "denied", ID: channel}}
	err = s.ChannelMarksTx(ctx, tx, func(context.Context, *sql.Tx) (DirectoryScope, error) {
		return DirectoryScope{Fence: "viewer", AllowedLiveSources: []string{saved.ID}}, nil
	}, channels)
	if err != nil || channels[0].Mark == nil || *channels[0].Mark != marks[saved.ID+"\x00"+channel] || channels[1].Mark != nil {
		t.Fatal(channels, err)
	}
}

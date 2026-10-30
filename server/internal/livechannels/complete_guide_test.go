package livechannels

import (
	"context"
	"fmt"
	"testing"
)

func TestGuideReturnsEveryProgrammeInChannelPage(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	a := testAuthority("viewer", true, true)
	if _, err := s.Save(ctx, a, testInput()); err != nil {
		t.Fatal(err)
	}
	first, err := s.Guide(ctx, a, testQuery())
	if err != nil || len(first.Channels) != 1 {
		t.Fatal(err)
	}
	channel := first.Channels[0]
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10001; i++ {
		id := fmt.Sprintf("extra-%05d", i)
		_, err = tx.Exec(`INSERT INTO live_programmes(generation_id,id,channel_id,provider_key,title,start_utc,end_utc,lineage) VALUES(?,?,?,?,'Programme','2026-09-05T12:00:00Z','2026-09-05T13:00:00Z','test')`, channel.Generation, id, channel.ID, id)
		if err != nil {
			break
		}
	}
	if err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	got, err := s.Guide(ctx, a, testQuery())
	if err != nil || len(got.Channels) != 1 || len(got.Channels[0].Programmes) < 10001 {
		t.Fatalf("programmes=%d: %v", len(got.Channels[0].Programmes), err)
	}
}

// The guide can answer for the channels on screen only, and list channels
// without programmes, so a large lineup isn't walked for every time window.
func TestGuideNamedChannelsAndChannelRowsOnly(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	a := testAuthority("viewer", true, true)
	if _, err := s.Save(ctx, a, testInput()); err != nil {
		t.Fatal(err)
	}
	all, err := s.Guide(ctx, a, testQuery())
	if err != nil || len(all.Channels) != 1 || len(all.Channels[0].Programmes) == 0 {
		t.Fatalf("setup: %+v %v", all.Channels, err)
	}
	id := all.Channels[0].ID
	q := testQuery()
	q.ChannelIDs = []string{"not-a-channel"}
	none, err := s.Guide(ctx, a, q)
	if err != nil || len(none.Channels) != 0 {
		t.Fatalf("another channel's id returned %d channels: %v", len(none.Channels), err)
	}
	q.ChannelIDs = []string{id}
	named, err := s.Guide(ctx, a, q)
	if err != nil || len(named.Channels) != 1 || len(named.Channels[0].Programmes) != len(all.Channels[0].Programmes) {
		t.Fatalf("named channel: %+v %v", named.Channels, err)
	}
	q.ChannelIDs, q.NoProgrammes = nil, true
	rows, err := s.Guide(ctx, a, q)
	if err != nil || len(rows.Channels) != 1 || len(rows.Channels[0].Programmes) != 0 {
		t.Fatalf("channel rows only: %+v %v", rows.Channels, err)
	}
	q.NoProgrammes, q.ChannelIDs = false, make([]string, 51)
	if _, err = s.Guide(ctx, a, q); err != ErrInvalid {
		t.Fatalf("51 channel ids accepted: %v", err)
	}
}

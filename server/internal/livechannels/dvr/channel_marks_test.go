package dvr

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
)

func TestRecordingMarksUseCompleteAuthorizedCurrentLineup(t *testing.T) {
	s, source, programme, authority := sharingStore(t)
	ctx := context.Background()
	owner := livechannels.Owner{Authority: "local", AccountID: "account", ProfileID: "profile"}
	r, err := s.Schedule(ctx, authority, owner, ScheduleInput{RequestID: strings.Repeat("c", 48), Occurrence: Occurrence{SourceID: source.ID, ChannelID: programme.ChannelID, Generation: source.Generation, ProgrammeID: programme.ID}})
	if err != nil {
		t.Fatal(err)
	}
	// A second, server-hidden channel, which is NOT on the recording page, changes
	// the first channel's collision disambiguation. Neither page nor title may
	// become the naming or seed authority.
	playlist := "#EXTM3U\n#EXTINF:-1 tvg-id=\"one\" tvg-chno=\"1\",One\nhttps://provider.invalid/one\n#EXTINF:-1 tvg-id=\"other\" tvg-chno=\"2\",One\nhttps://provider.invalid/other\n"
	source, err = s.live.Save(ctx, authority, livechannels.SourceInput{ID: source.ID, RequestID: strings.Repeat("d", 48), ExpectedRevision: source.Revision, Name: "Antenna", TunerCount: 1, Playlist: playlist})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = dbwork.ExecWrite(ctx, s.db, dbwork.ClassInteractive, `INSERT INTO admin_channel_numbers SELECT ?,channel_id,'','',1,1 FROM live_channel_versions WHERE generation_id=? AND channel_id<>?`, source.ID, source.Generation, programme.ChannelID); err != nil {
		t.Fatal(err)
	}
	var expected *livechannels.ChannelMark
	err = dbwork.WithReadSnapshot(ctx, s.db, func(ctx context.Context) error {
		tx := dbwork.Snapshot(ctx)
		channels := []livechannels.Channel{{SourceID: source.ID, ID: programme.ChannelID}}
		if err := s.live.ChannelMarksTx(ctx, tx, func(context.Context, *sql.Tx) (livechannels.DirectoryScope, error) {
			return livechannels.DirectoryScope{Fence: "directory", Viewer: owner, AllowedLiveSources: []string{source.ID}}, nil
		}, channels); err != nil {
			return err
		}
		expected = channels[0].Mark
		return nil
	})
	if err != nil || expected == nil || expected.Seed != source.ID+":"+programme.ChannelID || expected.Text != "O 1" {
		t.Fatalf("fixture lacks full-domain collision: %+v %v", expected, err)
	}
	page, err := s.List(ctx, authority, owner, Query{State: "all", Limit: 1})
	if err != nil || len(page.Recordings) != 1 || page.Recordings[0].Channel.Mark == nil || *page.Recordings[0].Channel.Mark != *expected {
		t.Fatalf("recording page mark: %+v %v", page, err)
	}
	detail, err := s.Detail(ctx, authority, owner, r.ID, "")
	if err != nil || detail.Recording.Channel.Mark == nil || *detail.Recording.Channel.Mark != *expected {
		t.Fatalf("detail mark: %+v %v", detail, err)
	}
	// A recording remains listed when its current channel is server-hidden.
	// Visibility cannot strip or alter the authoritative mark on that recording.
	if _, err = dbwork.ExecWrite(ctx, s.db, dbwork.ClassInteractive, `INSERT INTO admin_channel_numbers VALUES(?,?,'','',1,2)`, source.ID, programme.ChannelID); err != nil {
		t.Fatal(err)
	}
	detail, err = s.Detail(ctx, authority, owner, r.ID, "")
	if err != nil || detail.Recording.Channel.Mark == nil || *detail.Recording.Channel.Mark != *expected {
		t.Fatalf("hidden recording channel mark: %+v %v", detail, err)
	}
	// A retained old guide may still supply the recorded name. Its removed
	// channel has no CURRENT mark and must not acquire an invented seed.
	if _, err = s.live.Save(ctx, authority, livechannels.SourceInput{ID: source.ID, RequestID: strings.Repeat("e", 48), ExpectedRevision: source.Revision, Name: "Antenna", TunerCount: 1, Playlist: "#EXTM3U\n#EXTINF:-1 tvg-id=\"other\",Other\nhttps://provider.invalid/other\n"}); err != nil {
		t.Fatal(err)
	}
	detail, err = s.Detail(ctx, authority, owner, r.ID, "")
	if err != nil || detail.Recording.Channel.Mark != nil {
		t.Fatalf("removed current channel acquired a mark: %+v %v", detail, err)
	}
}

func TestRecordingMarksClearOnDeniedOrChangedAuthority(t *testing.T) {
	s, source, programme, _ := sharingStore(t)
	ctx := context.Background()
	owner := livechannels.Owner{Authority: "local", AccountID: "account", ProfileID: "profile"}
	for _, changed := range []bool{false, true} {
		err := dbwork.WithReadSnapshot(ctx, s.db, func(ctx context.Context) error {
			calls := 0
			authority := func(context.Context, *sql.Tx, bool) (string, func(string, string) bool, error) {
				calls++
				fence := "before"
				if changed && calls > 1 {
					fence = "after"
				}
				return fence, func(string, string) bool { return changed }, nil
			}
			channel := RecordingChannel{ID: programme.ChannelID, Mark: &livechannels.ChannelMark{Text: "stale", Seed: "stale"}}
			err := s.recordingChannelMarksTx(ctx, dbwork.Snapshot(ctx), authority, owner, []recordingChannelRef{{source: source.ID, channel: &channel}})
			if channel.Mark != nil || changed && err == nil || !changed && err != nil {
				t.Fatalf("denied/changed=%v retained mark %+v err=%v", changed, channel.Mark, err)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

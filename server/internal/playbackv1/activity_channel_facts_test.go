package playbackv1

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func TestActivityProgrammeUsesCommittedStartAndPublicSelectionOnly(t *testing.T) {
	raw := `{"reference":{"sourceId":"source","channelId":"channel"},"name":"Channel","number":"4","title":"Programme","entryStartMs":1792756800000,"entryEndMs":1792760400000,"sourceOffsetMs":5000,"assetId":"private-asset","sourceFence":"private-fence","url":"https://private.invalid/?token=secret"}`
	f := readSelectedActivityFacts(raw)
	if f.SourceID != "source" || f.ChannelID != "channel" || f.Name != "Channel" || f.Number != "4" || f.Title != "Programme" || f.StartsAt != "2026-10-23T12:00:00Z" || f.EndsAt != "2026-10-23T13:00:00Z" {
		t.Fatal(f)
	}
	encoded, e := json.Marshal(f)
	if e != nil {
		t.Fatal(e)
	}
	for _, secret := range []string{"private-asset", "private-fence", "private.invalid", "token=", "sourceOffset"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatal("private selection leaked", string(encoded))
		}
	}
	for _, start := range []string{"0", "-1", "1792760400000", "1792760400001"} {
		changed := strings.Replace(raw, "1792756800000", start, 1)
		got := readSelectedActivityFacts(changed)
		if got.StartsAt != "" || got.EndsAt != f.EndsAt {
			t.Fatal("unknown start inferred", start, got)
		}
	}
	if got := readSelectedActivityFacts(`{"title":"Only end known","entryEndMs":1792760400000}`); got.StartsAt != "" || got.EndsAt != f.EndsAt {
		t.Fatal(got)
	}
	if got := readSelectedActivityFacts(`{"title":"No range","entryStartMs":1792756800000}`); got.StartsAt != "" || got.EndsAt != "" || got.Title != "" {
		t.Fatal("made programme range", got)
	}
	if got := readSelectedActivityFacts(`{"title":`); got != (selectedActivityFacts{}) {
		t.Fatal("malformed committed selection", got)
	}
	if got := readSelectedActivityFacts(`{"reference":{"sourceId":"https://private.invalid/?grant=secret","channelId":"/private/channel"}}`); got.SourceID != "" || got.ChannelID != "" {
		t.Fatal("private locator became a channel identity", got)
	}
}

func TestAdminSessionsBorrowCallerReadForPresentFacts(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	c := catalogtest.Open(t)
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	s := &Service{DB: c.DB, Now: func() time.Time { return now }}
	insertPausedSession(t, c.DB, "session", "vod", "paused", now.UnixMilli(), 0)
	err := dbwork.WithReadSnapshot(context.Background(), c.DB, func(ctx context.Context) error {
		first, e := s.AdminSessions(ctx, 10, "")
		if e != nil {
			return e
		}
		c.Exec(`UPDATE playback_v1_sessions SET ended_ms=1 WHERE id='session'`)
		second, e := s.AdminSessions(ctx, 10, "")
		if e != nil {
			return e
		}
		if first.Totals.Streams != 1 || second.Totals.Streams != 1 || len(second.Items) != 1 {
			t.Fatal("session facts opened an independent read", first, second)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := s.AdminSessions(context.Background(), 10, "")
	if err != nil || fresh.Totals.Streams != 0 || len(fresh.Items) != 0 {
		t.Fatal("fresh read froze live status", fresh, err)
	}
}

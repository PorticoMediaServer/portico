package playbackv1

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"strings"
	"testing"
	"time"
)

func TestActivitySummarySessionsExactTotalAndRealDisplay(t *testing.T) {
	c := catalogtest.Open(t)
	lib := c.Library("tv", "TV Shows", "tv", "/private")
	show := c.Show(lib, "A show", 2025)
	season := c.Season(show, 2)
	episode := c.Episode(show, season, 3, "/private/episode.mkv")
	c.Drain()
	digest := strings.Repeat("a", 64)
	c.Exec(`INSERT INTO artwork_blobs(digest,mime,width,height,bytes,created_at,status) VALUES(?,'image/png',400,600,100,'now','ready'); INSERT OR IGNORE INTO artwork_sources(digest,accepted_mime,accepted_width,accepted_height,small_blob,large_blob,ambient_blob,large_edge,small_outlier,large_outlier,encoder_revision,created_at) SELECT digest,mime,width,height,digest,digest,digest,1920,0,0,1,created_at FROM artwork_blobs`, digest)
	c.Exec(`INSERT INTO artwork_candidates(id,kind,entity_id,role,subject,provider,image_id,origin,attribution,source_fence,observed_at) VALUES('art','show',?,'poster','','fixture','one','local','fixture','fence','now')`, show.ID)
	c.Exec(`INSERT INTO artwork_selections(kind,entity_id,role,subject,candidate_id,digest,locked,revision,actor,observed_at) SELECT column1,column2,column3,column4,column5,column6,column8,column9,column10,column11 FROM (VALUES('show',?,'poster','','art',?,?,0,1,'fixture','now'))`, show.ID, digest, digest)
	now := time.Now()
	s := &Service{DB: c.DB, Now: func() time.Time { return now }}
	for n := 0; n < 7; n++ {
		insertPausedSession(t, c.DB, fmt.Sprint(n), "vod", "playing", now.UnixMilli()+int64(n), 0)
		if _, e := c.DB.Exec(`UPDATE playback_v1_sessions SET item_id=?,presentation=?,position_ms=1234 WHERE id=?`, episode.ID, `{"url":"/private-capability-token","decision":{"video":{"action":"transcode"}},"quality":{"mode":"fixed","id":"1080p-low"},"videoHeight":720}`, fmt.Sprint(n)); e != nil {
			t.Fatal(e)
		}
	}
	// Expired and ended sessions never inflate the count.
	if _, e := c.DB.Exec(`UPDATE playback_v1_sessions SET ended_ms=1 WHERE id='0';UPDATE playback_v1_sessions SET lease_expires_ms=1 WHERE id='1'`); e != nil {
		t.Fatal(e)
	}
	out, e := s.ActivitySummarySessions(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if out.Count != 5 || len(out.Items) != 3 || out.ConvertingCount != 5 || out.Overflow.Count != 2 || out.Overflow.ConvertingCount != 2 {
		t.Fatalf("cap/total: %+v", out)
	}
	for _, v := range out.Items {
		if v.Item == nil || v.Item.Title != "Episode 3" || v.Item.ShowTitle != "A show" || v.Item.ShowID != show.Public || v.Item.Kind != "episode" || v.Item.EpisodeNumbering != "seasonal" || len(v.Item.Art) != 1 || v.Item.Art[0].ID != show.Public || v.Item.Art[0].Kind != "show" || v.Item.SeasonNumber == nil || *v.Item.SeasonNumber != 2 || v.Item.EpisodeNumber == nil || *v.Item.EpisodeNumber != 3 || v.Quality == nil || v.Quality.ID != "1080p-low" || v.Quality.MaxHeight != 0 || v.VideoHeight != 720 || v.Kind != "vod" || v.Decision.Video == nil || v.Decision.Video.Action != "transcode" || v.PositionMs != 1234 {
			t.Fatalf("wrong real facts: %+v %+v", v, v.Item)
		}
	}
	raw, _ := json.Marshal(out)
	if strings.Contains(string(raw), "private") || strings.Contains(string(raw), "capability") {
		t.Fatal("session capability/path leaked", string(raw))
	}
	if _, e = c.DB.Exec(`UPDATE playback_v1_sessions SET presentation='{"decision":{"video":{"action":"transcode"}}}'`); e != nil {
		t.Fatal(e)
	}
	out, e = s.ActivitySummarySessions(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	for _, v := range out.Items {
		if v.Quality != nil || v.VideoHeight != 0 {
			t.Fatal("unknown conversion height was fabricated")
		}
	}
}
func TestActivitySummarySessionsAudioAlbumArtist(t *testing.T) {
	c := catalogtest.Open(t)
	lib := c.Library("music", "Music", "music", "/music")
	artist := c.Artist(lib, "Artist")
	album := c.Album(artist, "Album", 2024)
	song := c.Song(album, 1, "/music/song.mp3", "Song")
	c.Drain()
	now := time.Now()
	insertPausedSession(t, c.DB, "audio", "audio", "paused", now.UnixMilli(), 0)
	if _, e := c.DB.Exec(`UPDATE playback_v1_sessions SET item_id=? WHERE id='audio'`, song.ID); e != nil {
		t.Fatal(e)
	}
	out, e := (&Service{DB: c.DB}).ActivitySummarySessions(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if len(out.Items) != 1 || out.Items[0].Item.Artist != "Artist" || out.Items[0].Item.Album != "Album" || out.Items[0].State != "paused" {
		t.Fatalf("audio display: %+v", out)
	}
}

// Exercise the production projection after SessionPlan, including replacement
// of a previous height with unknown geometry or a copied picture.
func TestActivitySummarySessionsCommittedVideoHeight(t *testing.T) {
	for _, tc := range []struct {
		name    string
		quality Quality
		plan    playback.DeliveryPlan
		want    int
	}{
		{"fixed capped below requested ladder", Quality{Mode: QualityFixed, ID: "1080p-low"}, playback.DeliveryPlan{Mode: "hls", SourceVideoCodec: "h264", VideoAction: "convert", TargetHeight: 720, Trace: &playback.DecisionTrace{Video: &playback.TraceVideo{Width: 1920, Height: 1080}}}, 720},
		{"automatic on a real rung", Quality{Mode: QualityAutomatic, ID: "1080p-low"}, playback.DeliveryPlan{Mode: "hls", SourceVideoCodec: "h264", VideoAction: "convert", TargetHeight: 480, Trace: &playback.DecisionTrace{Video: &playback.TraceVideo{Width: 1920, Height: 1080}}}, 480},
		{"numeric limit still subject to committed cap", Quality{Mode: QualityLimit, MaxHeight: 1080}, playback.DeliveryPlan{Mode: "hls", SourceVideoCodec: "h264", VideoAction: "convert", TargetHeight: 720, Trace: &playback.DecisionTrace{Video: &playback.TraceVideo{Width: 1920, Height: 1080}}}, 720},
		{"unknown geometry", Quality{Mode: QualityFixed, ID: "1080p-low"}, playback.DeliveryPlan{Mode: "hls", SourceVideoCodec: "h264", VideoAction: "convert", TargetHeight: 720}, 0},
		{"audio-only conversion", Quality{Mode: QualityAutomatic}, playback.DeliveryPlan{Mode: "hls", SourceVideoCodec: "h264", VideoAction: "copy", AudioAction: "convert", Trace: &playback.DecisionTrace{Video: &playback.TraceVideo{Width: 1920, Height: 1080}}}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if e := tc.quality.Validate("quality"); e != nil {
				t.Fatal(e)
			}
			c := catalogtest.Open(t)
			now := time.Now()
			s := &Service{DB: c.DB, Now: func() time.Time { return now }}
			view := Presentation{Quality: &tc.quality, URL: "/private-capability", VideoHeight: 2160}
			s.describe(&view, tc.plan, playback.V1Choice{})
			if view.Decision.Video == nil {
				t.Fatal("video decision missing from committed presentation")
			}
			if view.VideoHeight != tc.want {
				t.Fatalf("committed height: %d, want %d", view.VideoHeight, tc.want)
			}
			raw, e := json.Marshal(view)
			if e != nil {
				t.Fatal(e)
			}
			insertPausedSession(t, c.DB, "session", "vod", "playing", now.UnixMilli(), 0)
			c.Exec(`UPDATE playback_v1_sessions SET presentation=? WHERE id='session'`, string(raw))
			out, e := s.ActivitySummarySessions(context.Background())
			if e != nil {
				t.Fatal(e)
			}
			if len(out.Items) != 1 || out.Items[0].VideoHeight != tc.want || out.Items[0].Quality == nil || *out.Items[0].Quality != tc.quality || out.Items[0].Kind != "vod" {
				t.Fatalf("committed presentation lost: %+v", out)
			}
			wire, e := json.Marshal(out)
			if e != nil {
				t.Fatal(e)
			}
			if strings.Contains(string(wire), "private-capability") || tc.want == 0 && strings.Contains(string(wire), "videoHeight") {
				t.Fatalf("unknown/private fact escaped: %s", wire)
			}
		})
	}
}

func TestActivitySummaryPrioritizesFullActiveSetBeforeThreeAndCountsOverflow(t *testing.T) {
	c := catalogtest.Open(t)
	now := time.UnixMilli(1_790_000_000_000)
	s := &Service{DB: c.DB, Now: func() time.Time { return now }}
	// Four ordinary newer sessions would fill the old pre-priority cap.
	for n := 0; n < 4; n++ {
		insertPausedSession(t, c.DB, fmt.Sprintf("new-%d", n), "vod", "playing", now.UnixMilli()+int64(n), 0)
	}
	for _, v := range []struct {
		id, state, presentation string
		at                      int64
	}{
		{"old-problem", "buffering", `{"decision":{"video":{"action":"direct","reasons":[]}}}`, 10},
		{"old-video", "playing", `{"decision":{"video":{"action":"transcode","reasons":[]}}}`, 20},
		{"old-audio", "playing", `{"decision":{"audio":{"action":"transcode","reasons":[]}}}`, 30},
		{"old-burn", "playing", `{"decision":{"subtitles":{"action":"burn","reasons":[]}}}`, 0},
	} {
		insertPausedSession(t, c.DB, v.id, "vod", v.state, now.UnixMilli()-20000+v.at, 0)
		c.Exec(`UPDATE playback_v1_sessions SET presentation=? WHERE id=?`, v.presentation, v.id)
	}
	// A requested 2160 ceiling is no committed conversion. Malformed JSON is unknown.
	c.Exec(`UPDATE playback_v1_sessions SET presentation='{"quality":{"mode":"limit","maxHeight":2160},"decision":{"video":{"action":"copy","reasons":[]}}}' WHERE id='new-3'`)
	c.Exec(`UPDATE playback_v1_sessions SET presentation='not-json' WHERE id='new-2'`)
	insertPausedSession(t, c.DB, "ended", "vod", "error", now.UnixMilli()+100, 0)
	insertPausedSession(t, c.DB, "expired", "vod", "error", now.UnixMilli()+100, 0)
	c.Exec(`UPDATE playback_v1_sessions SET ended_ms=1 WHERE id='ended'`)
	c.Exec(`UPDATE playback_v1_sessions SET lease_expires_ms=? WHERE id='expired'`, now.UnixMilli())
	out, err := s.ActivitySummarySessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if out.Count != 8 || out.ConvertingCount != 3 || out.Overflow.Count != 5 || out.Overflow.ConvertingCount != 1 || len(out.Items) != 3 {
		t.Fatalf("full-set totals: %+v", out)
	}
	for n, id := range []string{"old-problem", "old-audio", "old-video"} {
		if out.Items[n].ID != id {
			t.Fatalf("priority before cap: %+v", out.Items)
		}
	}
	if out.Items[0].Problem == "" || out.Items[0].Converting || out.Items[1].Problem != "" || !out.Items[1].Converting {
		t.Fatalf("actual state/decisions: %+v", out.Items)
	}
	// Dashboard uses the same qualified priority and keeps the requested page cap.
	page, err := s.AdminSessions(context.Background(), 4, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 4 || page.Items[0].ID != "old-problem" {
		t.Fatalf("legacy pagination changed: %+v", page)
	}
}

func TestActivitySummaryRealUsernameAbsentHostedAndStableRecentOrder(t *testing.T) {
	c := catalogtest.Open(t)
	c.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id) VALUES('owner','real_username',X'00','primary')`)
	c.Exec(`UPDATE direct_profiles SET name='Different profile' WHERE id='primary'`)
	now := time.UnixMilli(1_790_000_000_000)
	for _, id := range []string{"b", "a", "c", "d"} {
		insertPausedSession(t, c.DB, id, "audio", "paused", now.UnixMilli(), 0)
	}
	c.Exec(`UPDATE playback_v1_sessions SET authority='hosted' WHERE id='a'`)
	c.Exec(`UPDATE playback_v1_sessions SET account_id='missing-account',profile_id='missing-profile',device_id='missing-device' WHERE id='b'`)
	out, err := (&Service{DB: c.DB, Now: func() time.Time { return now }}).ActivitySummarySessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if out.Count != 4 || out.ConvertingCount != 0 || out.Overflow.Count != 1 || out.Overflow.ConvertingCount != 0 {
		t.Fatalf("ordinary totals: %+v", out)
	}
	for n, id := range []string{"a", "b", "c"} {
		if out.Items[n].ID != id || out.Items[n].StartedAt != now.UTC().Format(time.RFC3339Nano) {
			t.Fatalf("stable real start: %+v", out.Items)
		}
	}
	if out.Items[0].User.Username != "" || out.Items[1].User.Username != "" || out.Items[2].User.Username != "real_username" || out.Items[2].Profile.Name != "Different profile" {
		t.Fatalf("username provenance: %+v", out.Items)
	}
	if out.Items[0].Profile.Name != "" || out.Items[1].Profile.Name != "" || out.Items[1].Device.Name != "" {
		t.Fatalf("missing names replaced by identifiers: %+v", out.Items)
	}
	wire, _ := json.Marshal(out.Items[0])
	if strings.Contains(string(wire), "username") || strings.Contains(string(wire), `"user":{"id":"owner","name"`) {
		t.Fatalf("unavailable username invented: %s", wire)
	}
	// Revoking/renaming the actual local account is reflected in the next snapshot.
	c.Exec(`UPDATE accounts SET username='renamed_username' WHERE id='owner'`)
	out, err = (&Service{DB: c.DB, Now: func() time.Time { return now }}).ActivitySummarySessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if out.Items[2].User.Username != "renamed_username" {
		t.Fatal("cached identity escaped source change")
	}
}

func TestActivityDashboardFirstTenGlobalTotalsAndQualifiedProblems(t *testing.T) {
	c := catalogtest.Open(t)
	now := time.UnixMilli(1_790_000_000_000)
	s := &Service{DB: c.DB, Now: func() time.Time { return now }}
	for n := 0; n < 58; n++ {
		id := fmt.Sprintf("s%02d", n)
		insertPausedSession(t, c.DB, id, "vod", "playing", now.UnixMilli()+int64(n), 0)
		c.Exec(`UPDATE playback_v1_sessions SET bandwidth_kbps=100,location='remote' WHERE id=?`, id)
	}
	c.Exec(`UPDATE playback_v1_sessions SET state='paused',location='local' WHERE id IN('s00','s01')`)
	// One momentary buffer, an error, and a requested quality do not qualify.
	c.Exec(`UPDATE playback_v1_sessions SET state='buffering' WHERE id='s57'`)
	c.Exec(`UPDATE playback_v1_sessions SET state='error' WHERE id='s56'`)
	c.Exec(`UPDATE playback_v1_sessions SET presentation='{"quality":{"mode":"fixed","id":"1080p-low"}}' WHERE id='s55'`)
	c.Exec(`UPDATE playback_v1_sessions SET presentation='{"decision":{"video":{"action":"transcode","reasons":[]}},"bitrateKbps":500}',bandwidth_kbps=0 WHERE id='s02'`)
	c.Exec(`UPDATE playback_v1_sessions SET state='buffering',updated_ms=? WHERE id='s03'`, now.UnixMilli()-10000)
	page, err := s.AdminSessions(context.Background(), 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 10 || page.Items[0].ID != "s03" || page.Items[0].Problem != "buffering" || page.Items[1].ID != "s02" || page.Items[2].ID != "s57" || page.Items[2].Problem != "" || page.Totals.Streams != 58 || page.Totals.Paused != 2 || page.Totals.Converting != 1 || page.Totals.BandwidthKbps != 6200 || page.Totals.People != 1 || page.Totals.AtHome != 2 || page.Totals.Away != 56 {
		t.Fatalf("bounded page and global totals: %+v", page)
	}
	summary, err := s.ActivitySummarySessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for n, v := range summary.Items {
		if v.ID != page.Items[n].ID {
			t.Fatal("popover/Dashboard order diverged")
		}
	}
	seen := map[string]bool{}
	cursor := ""
	for {
		page, err = s.AdminSessions(context.Background(), 10, cursor)
		if err != nil {
			t.Fatal(err)
		}
		if page.Totals.Streams != 58 || len(page.Items) > 10 {
			t.Fatal("page-local total substituted")
		}
		for _, v := range page.Items {
			if seen[v.ID] {
				t.Fatal("duplicate page item", v.ID)
			}
			seen[v.ID] = true
		}
		if page.Page.NextCursor == "" {
			break
		}
		cursor = page.Page.NextCursor
	}
	if len(seen) != 58 {
		t.Fatal("Show all omitted streams", len(seen))
	}
	for _, cursor := range []string{"t:0:id", "a:3:0:id", "a:0:-1:id", "a:0:0:"} {
		if _, err = s.AdminSessions(context.Background(), 10, cursor); err == nil {
			t.Fatal("invalid cursor accepted", cursor)
		}
	}
	// Repeated buffering reports retain their first transition; leaving clears it.
	c.Exec(`UPDATE playback_v1_sessions SET state='buffering',updated_ms=? WHERE id='s03'`, now.UnixMilli())
	summary, err = s.ActivitySummarySessions(context.Background())
	if err != nil || summary.Items[0].Problem != "buffering" {
		t.Fatal(summary, err)
	}
	c.Exec(`UPDATE playback_v1_sessions SET state='playing' WHERE id='s03'`)
	summary, err = s.ActivitySummarySessions(context.Background())
	if err != nil || summary.Items[0].ID != "s02" {
		t.Fatal(summary, err)
	}
}

func TestActivityHostedNamesUseCanonicalMembershipAndExactProfile(t *testing.T) {
	c := catalogtest.Open(t)
	now := time.Now()
	c.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('canonical','actual_username',X'00','own-profile',1)`)
	c.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('unrelated','different_username',X'00','different-profile',1)`)
	c.Exec(`UPDATE direct_profiles SET name='Chosen profile' WHERE id='own-profile'`)
	c.Exec(`INSERT INTO account_portico_links(account_id,hosted_account_id,consent) VALUES('canonical','hosted-account','private signed assertion with personal display name')`)
	insertPausedSession(t, c.DB, "hosted", "audio", "playing", now.UnixMilli(), 0)
	c.Exec(`UPDATE playback_v1_sessions SET authority='hosted',account_id='hosted-account',profile_id='own-profile' WHERE id='hosted'`)
	s := &Service{DB: c.DB}
	out, err := s.ActivitySummarySessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if out.Items[0].User.Username != "actual_username" || out.Items[0].Profile.Name != "Chosen profile" {
		t.Fatal(out)
	}
	raw, _ := json.Marshal(out)
	if strings.Contains(string(raw), "assertion") || strings.Contains(string(raw), "display name") {
		t.Fatal("private identity escaped", string(raw))
	}
	c.Exec(`UPDATE playback_v1_sessions SET profile_id='different-profile'`)
	out, err = s.ActivitySummarySessions(context.Background())
	if err != nil || out.Items[0].Profile.Name != "" {
		t.Fatal("unrelated profile crossed account fence", out, err)
	}
}

func TestActivityConversionSpeedFreshnessAndMediaGeneration(t *testing.T) {
	c := catalogtest.Open(t)
	lib := c.Library("movies", "Movies", "movie", "/private")
	movie := c.Movie(lib, "/private/movie.mp4", "Movie", 2026)
	_, asset := c.File(movie.ID, "/private/speed.mp4", 60)
	now := time.Now()
	s := &Service{DB: c.DB, Now: func() time.Time { return now }}
	c.Exec(`INSERT INTO playback_sessions(id,session_hash,account_id,profile_id,item_id,asset_id,generation,state,grant_hash,grant_token,expires_at,request_id,duration) VALUES('media','hash','owner','primary',?,?,1,'playing','grant','private-token','2099-01-01T00:00:00Z','request',60)`, movie.ID, asset)
	insertPausedSession(t, c.DB, "measured", "vod", "playing", now.UnixMilli(), 0)
	c.Exec(`UPDATE playback_v1_sessions SET media_session_id='media',media_generation=1,presentation='{"decision":{"video":{"action":"transcode","reasons":[]}}}' WHERE id='measured'`)
	c.Exec(`INSERT INTO playback_conversion_observations VALUES('media',1,'producer',0.5,?)`, now.UnixMilli())
	out, err := s.ActivitySummarySessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if out.Items[0].Problem != "behind" || out.Items[0].Transcode == nil || out.Items[0].Transcode.Speed != 0.5 {
		t.Fatal(out)
	}
	now = now.Add(5001 * time.Millisecond)
	out, err = s.ActivitySummarySessions(context.Background())
	if err != nil || out.Items[0].Problem != "" || out.Items[0].Transcode != nil {
		t.Fatal("stale speed treated as current", out, err)
	}
	c.Exec(`UPDATE playback_conversion_observations SET observed_ms=?,generation=2`, now.UnixMilli())
	out, err = s.ActivitySummarySessions(context.Background())
	if err != nil || out.Items[0].Transcode != nil {
		t.Fatal("replaced media generation escaped", out, err)
	}
	c.Exec(`UPDATE playback_conversion_observations SET generation=1`)
	c.Exec(`UPDATE playback_v1_sessions SET state='paused'`)
	out, err = s.ActivitySummarySessions(context.Background())
	if err != nil || out.Items[0].Problem != "" {
		t.Fatal("paused converter classified behind", out, err)
	}
	c.Exec(`DELETE FROM playback_conversion_observations`)
	out, err = s.ActivitySummarySessions(context.Background())
	if err != nil || out.Items[0].Transcode != nil {
		t.Fatal("finished producer speed retained", out, err)
	}
}

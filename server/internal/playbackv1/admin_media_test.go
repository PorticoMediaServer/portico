package playbackv1

import (
	"context"
	"encoding/json"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/telemetry"
	"strings"
	"testing"
	"time"
)

func TestDashboardSessionActualSourceRateAndPrivateProjection(t *testing.T) {
	c := catalogtest.Open(t)
	lib := c.Library("movies", "Movies", "movie", "/private")
	movie := c.Movie(lib, "/private/movie.mkv", "Movie", 2026)
	_, asset := c.File(movie.ID, "/private/source.mkv", 60)
	c.Exec(`INSERT INTO asset_stream_facts VALUES(?,1,1000,1,'fingerprint',1)`, asset)
	c.Exec(`INSERT INTO asset_streams(asset_id,stream_index,type,codec,language,title,channels,channel_layout,is_default,is_forced,color_transfer,detail_json)
 VALUES(?,0,'video','hevc','','',0,'',1,0,'smpte2084','{"profile":"Main 10","width":3840,"height":2160}'),(?,1,'audio','flac','en','',2,'stereo',1,0,'','{"bitDepth":24,"sampleRate":96000}')`, asset, asset)
	c.Exec(`INSERT INTO playback_sessions(id,session_hash,account_id,profile_id,item_id,asset_id,generation,state,grant_hash,grant_token,expires_at,request_id,duration) VALUES('media','hash','owner','primary',?,?,1,'playing','grant','private-token','2099-01-01T00:00:00Z','request',60)`, movie.ID, asset)
	now := time.Now()
	var traffic telemetry.Traffic
	traffic.Add("media", "198.51.100.2", true, 500000, now)
	s := &Service{DB: c.DB, Traffic: &traffic, Now: func() time.Time { return now }}
	insertPausedSession(t, c.DB, "session", "vod", "playing", now.UnixMilli(), 0)
	c.Exec(`UPDATE playback_v1_sessions SET item_id=?,media_session_id='media',audio_track_id='a1',presentation='{"url":"/private-token","decision":{"video":{"action":"copy","reasons":[]}},"bitrateKbps":20000}' WHERE id='session'`, movie.ID)
	out, e := s.AdminSessions(context.Background(), 10, "")
	if e != nil {
		t.Fatal(e)
	}
	a := out.Items[0]
	if a.Media == nil || a.Media.Video == nil || a.Media.Audio == nil || a.Media.Video.Height != 2160 || a.Media.Audio.SampleRate != 96000 || a.Media.VideoHardware != nil || a.CurrentBitrateKbps == nil || *a.CurrentBitrateKbps != 800 || a.BitrateKbps != 20000 || a.Address != "198.51.100.2" {
		t.Fatalf("%+v %+v", a, a.Media)
	}
	raw, _ := json.Marshal(out)
	if strings.Contains(string(raw), "private-token") || strings.Contains(string(raw), "/private/") {
		t.Fatal("capability/source leaked", string(raw))
	}
	c.Exec(`UPDATE playback_v1_sessions SET state='paused' WHERE id='session'`)
	out, e = s.AdminSessions(context.Background(), 10, "")
	if e != nil || *out.Items[0].CurrentBitrateKbps != 0 || out.Items[0].BitrateKbps != 20000 {
		t.Fatal(out, e)
	}
}

package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/access"
	"github.com/PorticoMediaServer/portico/server/internal/audiofacts"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
)

// memberLimitsFixture is a v1 fixture with the per-member limits store wired
// in, so admission runs instead of passing everything through.
func memberLimitsFixture(t *testing.T, films int) *v1Fixture {
	t.Helper()
	f := newV1Fixture(t, films)
	f.deps.Access = AccessArea{Access: access.New(f.db)}
	f.handler = New(f.deps)
	f.deps.v1 = f.v1
	return f
}

func memberWithSettledCatalogue(t *testing.T, f *v1Fixture) (string, string) {
	t.Helper()
	member, film := f.member()
	settleCompactCatalogue(t, f.db)
	return member, film
}

// pinLimitsTime pins admission's clock; schedules are evaluated in UTC.
func pinLimitsTime(f *v1Fixture, at time.Time) {
	f.deps.Access.Access.Now = func() time.Time { return at }
}

func mondayInside() time.Time  { return time.Date(2026, 9, 14, 11, 0, 0, 0, time.UTC) }
func mondayOutside() time.Time { return time.Date(2026, 9, 14, 14, 0, 0, 0, time.UTC) }

const mondayWindow = `{"timezone":"UTC","windows":[{"days":[1],"startMinute":600,"endMinute":780}]}`

func setMemberLimits(t *testing.T, f *v1Fixture, body string) {
	t.Helper()
	var limits struct {
		MaxStreams int `json:"maxStreams"`
	}
	if err := json.Unmarshal([]byte(body), &limits); err != nil {
		t.Fatal(err)
	}
	if limits.MaxStreams > 0 {
		if _, err := f.db.Exec(`INSERT INTO account_stream_limits(account_id,mode,stream_limit) VALUES('member','limit',?) ON CONFLICT(account_id) DO UPDATE SET mode=excluded.mode,stream_limit=excluded.stream_limit`, limits.MaxStreams); err != nil {
			t.Fatal(err)
		}
	}
	var rest map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &rest); err != nil {
		t.Fatal(err)
	}
	delete(rest, "maxStreams")
	normalized, err := json.Marshal(rest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`INSERT INTO access_limits VALUES('member',1,0,?) ON CONFLICT(account_id) DO UPDATE SET body=excluded.body,revision=access_limits.revision+1`, string(normalized)); err != nil {
		t.Fatal(err)
	}
}

// libraryChannelRow publishes the channel id a v1 channel start names, with no
// media behind it: admission runs before anything touches the channel engine.
func libraryChannelRow(t *testing.T, f *v1Fixture) {
	t.Helper()
	if _, err := f.db.Exec(`INSERT INTO lc_channels(id,revision,config_json,enabled,position,name,active_generation,state) VALUES('channel',1,'{}',1,0,'Members channel','generation','ready')`); err != nil {
		t.Fatal(err)
	}
}

func startChannel(t *testing.T, f *v1Fixture, token, key string) (int, string) {
	t.Helper()
	w := f.raw("POST", "/v1/playback/sessions", token, map[string]string{"Idempotency-Key": key}, map[string]any{"channelId": "library:channel", "state": "playing"})
	return w.Code, v1Code(w)
}

// memberChannelEngine stands in for the linear runtime: configured, and
// accepting every start. Admission runs before it is touched, so denials never
// reach it and allows play through it.
type memberChannelEngine struct{}

func (memberChannelEngine) Configured() bool { return true }
func (memberChannelEngine) StartTx(context.Context, *sql.Tx, identity.Principal, playback.ChannelStart) (playback.LinearSelection, error) {
	return playback.LinearSelection{}, nil
}
func (memberChannelEngine) ReadTx(context.Context, *sql.Tx, identity.Principal, string) (playback.ChannelState, error) {
	return playback.ChannelState{}, nil
}
func (memberChannelEngine) SetIntentTx(context.Context, *sql.Tx, identity.Principal, string, playback.LinearDesired) (playback.LinearDesired, error) {
	return playback.LinearDesired{}, nil
}
func (memberChannelEngine) ObserveTx(context.Context, *sql.Tx, string, string, int64, int64, string) error {
	return nil
}
func (memberChannelEngine) EndTx(context.Context, *sql.Tx, string) (bool, error) {
	return true, nil
}

func channelFixture(t *testing.T, f *v1Fixture) {
	t.Helper()
	f.v1.Channels = memberChannelEngine{}
	libraryChannelRow(t, f)
}

// rawFrom is raw from a given peer address: httptest's default peer is a
// routable address (remote), while loopback or a private range is the LAN.
// The server decides remote/local from the connection, never from the client.
func (f *v1Fixture) rawFrom(peer, method, path, token string, headers map[string]string, body any) *httptest.ResponseRecorder {
	f.t.Helper()
	var data []byte
	if body != nil {
		if s, ok := body.(string); ok {
			data = []byte(s)
		} else {
			data, _ = json.Marshal(body)
		}
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(data))
	r.RemoteAddr = peer
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		ct := "application/json"
		if method == "PATCH" {
			ct = "application/merge-patch+json"
		}
		r.Header.Set("Content-Type", ct)
	}
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	return w
}

// recordingChannelEngine stands in for the linear runtime like
// memberChannelEngine, but also records the channel row exactly as the real
// engine stores it (client_profile_json is json.Marshal of the start profile),
// so tests can read back the profile the start handed the engine.
type recordingChannelEngine struct {
	memberChannelEngine
}

func (recordingChannelEngine) StartTx(ctx context.Context, tx *sql.Tx, p identity.Principal, in playback.ChannelStart) (playback.LinearSelection, error) {
	profile, _ := json.Marshal(in.Profile)
	selection, _ := json.Marshal(playback.LinearSelection{})
	if _, err := tx.ExecContext(ctx, `INSERT INTO playback_channel_sessions(session_id,authority,account_id,profile_id,server_id,role,account_epoch,family_id,kind,source_id,channel_id,selection_json,transport_json,client_profile_json,created_ms) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		in.SessionID, p.Authority, p.AccountID, p.ProfileID, p.ServerID, p.Role, p.Epoch, "test-family", in.Channel.Kind, in.Channel.SourceID, in.Channel.ChannelID, string(selection), `{}`, string(profile), time.Now().UnixMilli()); err != nil {
		return playback.LinearSelection{}, err
	}
	return playback.LinearSelection{}, nil
}

// memberDeviceID is the device a bearer token was issued to.
func memberDeviceID(t *testing.T, f *v1Fixture, token string) string {
	t.Helper()
	var device string
	if err := f.db.QueryRow(`SELECT d.device_id FROM authorization_family_tokens t JOIN identity_device_families d ON d.family_id=t.family_id WHERE t.token_hash=?`, identity.Digest(token)).Scan(&device); err != nil {
		t.Fatal(err)
	}
	return device
}

// devicePlannerProfile is the engine a channel start plans from for this
// device: the native engine of its published document, or the baseline.
func devicePlannerProfile(t *testing.T, f *v1Fixture, device string) playback.ClientProfile {
	t.Helper()
	var document string
	if err := f.db.QueryRow(`SELECT document FROM playback_device_capabilities WHERE device_id=?`, device).Scan(&document); err == nil && document != "" {
		if d, err := playback.ParseCapabilityDocument([]byte(document)); err == nil {
			return d.Profile(playback.EngineNative)
		}
	}
	return playback.BaselineClientProfile()
}

// A channel start holds a stream: with maxStreams 1 and a live VOD session the
// start is refused, and once that session ends the start plays.
func TestMemberLimitsChannelStartCountsStreams(t *testing.T) {
	f := memberLimitsFixture(t, 1)
	member, film := memberWithSettledCatalogue(t, f)
	channelFixture(t, f)
	setMemberLimits(t, f, `{"maxStreams":1}`)
	var first playbackv1.SessionView
	f.callAs(member, "POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": "streams-vod-000001"}, startBody(film, nil), 201, &first)
	if code, v1code := startChannel(t, f, member, "streams-chan-000001"); code != 409 || v1code != "stream_limit_reached" {
		t.Fatalf("channel start past the stream limit: %d %s", code, v1code)
	}
	f.callAs(member, "DELETE", "/v1/playback/sessions/"+first.ID, nil, nil, 204, nil)
	var started playbackv1.SessionView
	w := f.raw("POST", "/v1/playback/sessions", member, map[string]string{"Idempotency-Key": "streams-chan-000002"}, map[string]any{"channelId": "library:channel", "state": "playing"})
	_ = json.Unmarshal(w.Body.Bytes(), &started)
	if w.Code != 201 || started.ID == "" {
		t.Fatalf("channel start after the slot freed: %d %s", w.Code, w.Body.String())
	}
	// The playing channel holds the slot again: a VOD start is refused too.
	if code, v1code := startChannel(t, f, member, "streams-chan-000003"); code != 409 || v1code != "stream_limit_reached" {
		t.Fatalf("second channel start past the stream limit: %d %s", code, v1code)
	}
}

// A channel start checks the schedule and then the channel allow/deny list: a
// denied channel inside the hours is hidden, every channel outside them is
// refused, and an allowed channel inside them passes admission.
func TestMemberLimitsChannelStartScheduleAndPolicy(t *testing.T) {
	f := memberLimitsFixture(t, 1)
	member, _ := memberWithSettledCatalogue(t, f)
	channelFixture(t, f)
	setMemberLimits(t, f, `{"schedule":`+mondayWindow+`,"channelPolicy":{"mode":"deny","channels":["library:channel"]}}`)
	pinLimitsTime(f, mondayInside())
	if code, v1code := startChannel(t, f, member, "channel-pol-000001"); code != 404 || v1code != "not_found" {
		t.Fatalf("denied channel inside the hours: %d %s", code, v1code)
	}
	pinLimitsTime(f, mondayOutside())
	if code, v1code := startChannel(t, f, member, "channel-pol-000002"); code != 409 || v1code != "stream_limit_reached" {
		t.Fatalf("channel start outside the hours: %d %s", code, v1code)
	}
	setMemberLimits(t, f, `{"schedule":`+mondayWindow+`,"channelPolicy":{"mode":"allow","channels":["library:channel"]}}`)
	pinLimitsTime(f, mondayInside())
	if code, v1code := startChannel(t, f, member, "channel-pol-000003"); code != 201 {
		t.Fatalf("allowed channel inside the hours: %d %s", code, v1code)
	}
}

// A remote member's bitrate cap reaches the transcode decision on an item start
// and on a queue-entry start alike. The fixture has no converter, so a source
// over the cap cannot be served at all (422 unsupported_media); without the cap
// the same source plays direct.
func TestMemberLimitsRemoteCapReachesItemAndQueueStarts(t *testing.T) {
	f := memberLimitsFixture(t, 1)
	member, film := memberWithSettledCatalogue(t, f)
	asset := assetID(t, f.db, film)
	// A 20 Mbit/s source (size over duration); the cap below is 3 Mbit/s.
	setAssetSize(t, f, asset, 1500000000)
	setMemberLimits(t, f, `{"remoteBitrateKbps":3000}`)
	w := f.raw("POST", "/v1/playback/sessions", member, map[string]string{"Idempotency-Key": "cap-item-00000001"}, startBody(film, nil))
	if w.Code != 422 || v1Code(w) != "unsupported_media" {
		t.Fatalf("capped item start: %d %s", w.Code, w.Body.String())
	}
	w = f.raw("POST", "/v1/queues", member, map[string]string{"Idempotency-Key": "cap-queue-00000001"}, map[string]any{"segments": []any{map[string]any{"source": queueItems(film)}}, "startPlayback": map[string]any{"state": "playing"}})
	if w.Code != 422 || v1Code(w) != "unsupported_media" {
		t.Fatalf("capped queue-entry start: %d %s", w.Code, w.Body.String())
	}
	setMemberLimits(t, f, `{}`)
	var s playbackv1.SessionView
	f.callAs(member, "POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": "cap-item-00000002"}, startBody(film, nil), 201, &s)
	if s.Presentation.Decision.Video == nil || s.Presentation.Decision.Video.Action != "direct" {
		t.Fatalf("uncapped item start: %+v", s.Presentation.Decision)
	}
	settleCompactCatalogue(t, f.db)
	var q playbackv1.QueueReply
	f.callAs(member, "POST", "/v1/queues", map[string]string{"Idempotency-Key": "cap-queue-00000002"}, map[string]any{"segments": []any{map[string]any{"source": queueItems(film)}}, "startPlayback": map[string]any{"state": "playing"}}, 201, &q)
	if q.Session == nil || q.Session.Presentation.Decision.Video == nil || q.Session.Presentation.Decision.Video.Action != "direct" {
		t.Fatalf("uncapped queue-entry start: %+v", q.Session)
	}
}

// A remote member's bitrate cap narrows the profile the channel start hands the
// linear engine: the stored playback_channel_sessions.client_profile_json has
// every video codec's maxBitrateBps at or under the 3 Mbit/s cap. The same
// member on the LAN stores the device's planner profile un-narrowed.
func TestMemberLimitsChannelStartStoresRemoteCap(t *testing.T) {
	f := memberLimitsFixture(t, 1)
	member, _ := f.member()
	f.v1.Channels = recordingChannelEngine{}
	libraryChannelRow(t, f)
	setMemberLimits(t, f, `{"remoteBitrateKbps":3000}`)
	deviceProfile := devicePlannerProfile(t, f, memberDeviceID(t, f, member))

	start := func(peer, key string) playbackv1.SessionView {
		t.Helper()
		w := f.rawFrom(peer, "POST", "/v1/playback/sessions", member, map[string]string{"Idempotency-Key": key}, map[string]any{"channelId": "library:channel", "state": "playing"})
		if w.Code != 201 {
			t.Fatalf("channel start from %s: %d %s", peer, w.Code, w.Body.String())
		}
		var s playbackv1.SessionView
		if err := json.Unmarshal(w.Body.Bytes(), &s); err != nil || s.ID == "" {
			t.Fatalf("channel start from %s: %v %s", peer, err, w.Body.String())
		}
		return s
	}
	stored := func(id string) string {
		t.Helper()
		var raw string
		if err := f.db.QueryRow(`SELECT client_profile_json FROM playback_channel_sessions WHERE session_id=?`, id).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		return raw
	}

	remote := stored(start("203.0.113.9:51000", "cap-chan-remote-01").ID)
	wantRemote, _ := json.Marshal(playback.WithVideoBitrateCeiling(deviceProfile, 3000000))
	if remote != string(wantRemote) {
		t.Fatalf("remote stored profile:\n%s\nwant:\n%s", remote, wantRemote)
	}
	var parsed playback.ClientProfile
	if err := json.Unmarshal([]byte(remote), &parsed); err != nil {
		t.Fatal(err)
	}
	if len(parsed.Video) == 0 {
		t.Fatal("remote stored profile has no video codecs")
	}
	for _, v := range parsed.Video {
		if v.MaxBitrateBPS > 3000000 {
			t.Fatalf("remote codec over the cap: %+v", v)
		}
	}

	lan := stored(start("127.0.0.1:51000", "cap-chan-lan-000001").ID)
	wantLAN, _ := json.Marshal(deviceProfile)
	if lan != string(wantLAN) {
		t.Fatalf("LAN stored profile:\n%s\nwant:\n%s", lan, wantLAN)
	}
}

// A remote member's quality change mid-session goes through the same admission
// as a start: above the cap it is refused exactly as a capped start with the
// same quality would be (422 unsupported_media here, where the fixture has no
// converter); without the cap the same change plays.
func TestMemberLimitsPatchAppliesRemoteCapLikeStart(t *testing.T) {
	f := memberLimitsFixture(t, 1)
	member, film := memberWithSettledCatalogue(t, f)
	asset := assetID(t, f.db, film)
	// A 20 Mbit/s source (size over duration); the cap below is 3 Mbit/s.
	setAssetSize(t, f, asset, 1500000000)
	setMemberLimits(t, f, `{}`)
	var s playbackv1.SessionView
	f.callAs(member, "POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": "cap-patch-00000001"}, startBody(film, nil), 201, &s)
	if s.Presentation.Decision.Video == nil || s.Presentation.Decision.Video.Action != "direct" {
		t.Fatalf("uncapped item start: %+v", s.Presentation.Decision)
	}
	setMemberLimits(t, f, `{"remoteBitrateKbps":3000}`)
	quality := map[string]any{"mode": "limit", "maxVideoBitrateKbps": 100000}
	w := f.raw("PATCH", "/v1/playback/sessions/"+s.ID, member, map[string]string{"If-Match": s.Revision}, map[string]any{"quality": quality})
	if w.Code != 422 || v1Code(w) != "unsupported_media" {
		t.Fatalf("capped PATCH: %d %s", w.Code, w.Body.String())
	}
	// A fresh capped start with the same quality is refused identically.
	w = f.raw("POST", "/v1/playback/sessions", member, map[string]string{"Idempotency-Key": "cap-patch-00000002"}, startBody(film, map[string]any{"quality": quality}))
	if w.Code != 422 || v1Code(w) != "unsupported_media" {
		t.Fatalf("capped start with the same quality: %d %s", w.Code, w.Body.String())
	}
	// Without the cap the same PATCH plays a new generation.
	setMemberLimits(t, f, `{}`)
	var s2 playbackv1.SessionView
	f.callAs(member, "POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": "cap-patch-00000003"}, startBody(film, nil), 201, &s2)
	var changed playbackv1.SessionView
	f.callAs(member, "PATCH", "/v1/playback/sessions/"+s2.ID, map[string]string{"If-Match": s2.Revision}, map[string]any{"quality": quality}, 200, &changed)
	if changed.Presentation.Generation != 2 {
		t.Fatalf("uncapped PATCH: %+v", changed.Presentation)
	}
}

// memberSongs turns the member's library into songs the member's device
// decodes, so queue transitions run without real audio.
func memberSongs(t *testing.T, f *v1Fixture, token, film string) []string {
	t.Helper()
	_ = token
	musicRoot := filepath.Join(f.root, "kids-music")
	if err := os.MkdirAll(musicRoot, 0700); err != nil {
		t.Fatal(err)
	}
	music, err := f.cat.Create("Kids Music", "music", musicRoot)
	if err != nil {
		t.Fatal(err)
	}
	var kids string
	if err := f.db.QueryRow(`SELECT l.library_id FROM catalog_libraries l JOIN catalog_entities e ON e.library_id=l.id WHERE e.public_id=pid_blob(?) LIMIT 1`, film).Scan(&kids); err != nil {
		t.Fatal(err)
	}
	allowed, err := json.Marshal([]string{kids, music.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE direct_memberships SET allowed_libraries=? WHERE account_id='member'`, string(allowed)); err != nil {
		t.Fatal(err)
	}
	c := catalogtest.New(t, f.db)
	library := c.Handle(music.ID)
	artist := c.Artist(library, "Queue Artist")
	album := c.Album(artist, "Queue Album", 2026)
	songs := make([]string, 0, 3)
	for i := 0; i < 3; i++ {
		song := c.Song(album, i+1, filepath.Join(musicRoot, fmt.Sprintf("track-%d.mp4", i+1)), fmt.Sprintf("Track %d", i+1))
		if err := audiofacts.Save(context.Background(), f.db, song.Token, audiofacts.Facts{Container: "mp4", Codec: "aac", SampleRate: 44100, Channels: 2, RawFrames: 26460000 + 2128, DurationFrames: 26460000, StartFrames: 2112, EndFrames: 16, TrimSource: "itunsmpb", DecoderConfig: "EhA="}); err != nil {
			t.Fatal(err)
		}
		songs = append(songs, song.Public)
	}
	c.Drain()
	caps := webCapabilities()
	caps["audioDecode"] = []map[string]any{{"codec": "aac", "containers": []string{"mp4", "adts"}, "maxSampleRate": 96000, "maxChannels": 8}}
	var doc playbackv1.CapabilitiesDocument
	if w := f.raw("GET", "/v1/me/devices/current/capabilities", token, nil, nil); w.Code == 200 {
		_ = json.Unmarshal(w.Body.Bytes(), &doc)
		f.callAs(token, "PUT", "/v1/me/devices/current/capabilities", map[string]string{"If-Match": playbackv1.ETag(doc.Revision)}, caps, 204, nil)
		return songs
	}
	f.callAs(token, "PUT", "/v1/me/devices/current/capabilities", nil, caps, 204, nil)
	return songs
}

func assetID(t *testing.T, db *sql.DB, item string) int64 {
	t.Helper()
	c := catalogtest.New(t, db)
	var asset int64
	if err := db.QueryRow(`SELECT asset_id FROM catalog_asset_links WHERE entity_id=? ORDER BY part_index LIMIT 1`, c.ID(item)).Scan(&asset); err != nil {
		t.Fatal(err)
	}
	return asset
}

func setAssetSize(t *testing.T, f *v1Fixture, asset int64, size int64) {
	t.Helper()
	c := catalogtest.New(t, f.db)
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetAssetTx(ctx, tx, asset, map[string]any{"size": size, "duration": 600.0})
	})
	c.Drain()
}

func queueHead(t *testing.T, f *v1Fixture, token, id string) playbackv1.QueueView {
	t.Helper()
	var head playbackv1.QueueView
	f.callAs(token, "GET", "/v1/queues/"+id, nil, nil, 200, &head)
	return head
}

// Transitions admit like starts: outside the member's hours advancing to the
// next entry is refused and preparing it is withheld (404, like any
// inaccessible entry); inside them the preparation commits a session that
// inherits the queue's state.
func TestMemberLimitsTransitionsAdmit(t *testing.T) {
	f := memberLimitsFixture(t, 0)
	member, film := memberWithSettledCatalogue(t, f)
	songs := memberSongs(t, f, member, film)
	setMemberLimits(t, f, `{"schedule":`+mondayWindow+`}`)
	pinLimitsTime(f, mondayInside())
	var q playbackv1.QueueReply
	f.callAs(member, "POST", "/v1/queues", map[string]string{"Idempotency-Key": "trans-queue-000001"},
		map[string]any{"segments": []any{map[string]any{"source": queueItems(songs...)}}, "startPlayback": map[string]any{"state": "playing"}}, 201, &q)
	if q.Session == nil {
		t.Fatal("no session started with the queue")
	}
	pinLimitsTime(f, mondayOutside())
	w := f.raw("POST", "/v1/queues/"+q.Queue.ID+":advance", member, map[string]string{"If-Match": q.Queue.Revision}, map[string]any{"reason": "next"})
	if w.Code != 409 || v1Code(w) != "stream_limit_reached" {
		t.Fatalf("advance outside the hours: %d %s", w.Code, w.Body.String())
	}
	head := queueHead(t, f, member, q.Queue.ID)
	w = f.raw("POST", "/v1/queues/"+q.Queue.ID+":prepare-next", member,
		map[string]string{"If-Match": head.Revision, "Idempotency-Key": "trans-prep-000001"},
		map[string]any{"sessionId": q.Session.ID, "sessionGeneration": q.Session.Presentation.Generation})
	if w.Code != 404 || v1Code(w) != "not_found" {
		t.Fatalf("prepare outside the hours: %d %s", w.Code, w.Body.String())
	}
	pinLimitsTime(f, mondayInside())
	var p playbackv1.PreparedNext
	f.callAs(member, "POST", "/v1/queues/"+q.Queue.ID+":prepare-next",
		map[string]string{"If-Match": head.Revision, "Idempotency-Key": "trans-prep-000002"},
		map[string]any{"sessionId": q.Session.ID, "sessionGeneration": q.Session.Presentation.Generation}, 200, &p)
	if p.ItemID == "" || p.Presentation.AudioRender == nil {
		t.Fatalf("no preparation inside the hours: %+v", p)
	}
	var done playbackv1.QueueReply
	f.callAs(member, "POST", "/v1/queues/"+q.Queue.ID+":commit-next", nil, map[string]any{"token": p.Token}, 200, &done)
	if done.Session == nil || done.Session.ID == q.Session.ID {
		t.Fatalf("no new session committed: %+v", done.Session)
	}
}

// A member at their stream limit keeps using the stream they hold: a quality
// change and a queue transition continue it, so neither counts as a second
// stream. A genuinely new stream is still refused.
func TestMemberLimitsContinuingStreamIsNotASecondStream(t *testing.T) {
	f := memberLimitsFixture(t, 0)
	member, film := memberWithSettledCatalogue(t, f)
	setMemberLimits(t, f, `{"maxStreams":1}`)
	var s playbackv1.SessionView
	f.callAs(member, "POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": "one-stream-000001"}, startBody(film, nil), 201, &s)
	var changed playbackv1.SessionView
	f.callAs(member, "PATCH", "/v1/playback/sessions/"+s.ID, map[string]string{"If-Match": s.Revision}, map[string]any{"quality": map[string]any{"mode": "limit", "maxVideoBitrateKbps": 100000}}, 200, &changed)
	if changed.Presentation.Generation != 2 {
		t.Fatalf("quality change at the limit: generation %d", changed.Presentation.Generation)
	}
	w := f.raw("POST", "/v1/playback/sessions", member, map[string]string{"Idempotency-Key": "one-stream-000002"}, startBody(film, nil))
	if w.Code != 409 || v1Code(w) != "stream_limit_reached" {
		t.Fatalf("a second stream at the limit: %d %s", w.Code, w.Body.String())
	}
	f.callAs(member, "DELETE", "/v1/playback/sessions/"+s.ID, nil, nil, 204, nil)

	songs := memberSongs(t, f, member, film)
	var q playbackv1.QueueReply
	f.callAs(member, "POST", "/v1/queues", map[string]string{"Idempotency-Key": "one-stream-queue-01"},
		map[string]any{"segments": []any{map[string]any{"source": queueItems(songs...)}}, "startPlayback": map[string]any{"state": "playing"}}, 201, &q)
	if q.Session == nil {
		t.Fatal("no session started with the queue")
	}
	head := queueHead(t, f, member, q.Queue.ID)
	var p playbackv1.PreparedNext
	f.callAs(member, "POST", "/v1/queues/"+q.Queue.ID+":prepare-next",
		map[string]string{"If-Match": head.Revision, "Idempotency-Key": "one-stream-prep-01"},
		map[string]any{"sessionId": q.Session.ID, "sessionGeneration": q.Session.Presentation.Generation}, 200, &p)
	var done playbackv1.QueueReply
	f.callAs(member, "POST", "/v1/queues/"+q.Queue.ID+":commit-next", nil, map[string]any{"token": p.Token}, 200, &done)
	if done.Session == nil || done.Session.ID == q.Session.ID {
		t.Fatalf("the next entry did not continue at the limit: %+v", done.Session)
	}
}

// A channel switch at the stream limit is not a second stream: with
// maxStreams 1, starting channel B with replacesSessionId = A's session plays
// and ends A as replaced, while a start without replacesSessionId while B
// plays is still refused.
func TestMemberLimitsChannelSwitchAtStreamLimit(t *testing.T) {
	f := memberLimitsFixture(t, 1)
	member, _ := f.member()
	channelFixture(t, f)
	setMemberLimits(t, f, `{"maxStreams":1}`)
	var a playbackv1.SessionView
	f.callAs(member, "POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": "switch-a-00000001"}, map[string]any{"channelId": "library:channel", "state": "playing"}, 201, &a)
	var b playbackv1.SessionView
	f.callAs(member, "POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": "switch-b-00000001"}, map[string]any{"channelId": "library:channel", "state": "playing", "replacesSessionId": a.ID}, 201, &b)
	if b.ID == "" || b.ID == a.ID {
		t.Fatalf("no new session for the switch: %+v", b)
	}
	var reason string
	var ended int64
	if err := f.db.QueryRow(`SELECT end_reason, ended_ms FROM playback_v1_sessions WHERE id=?`, a.ID).Scan(&reason, &ended); err != nil || reason != "replaced" || ended == 0 {
		t.Fatalf("replaced session: reason %q ended %d err %v", reason, ended, err)
	}
	w := f.raw("POST", "/v1/playback/sessions", member, map[string]string{"Idempotency-Key": "switch-c-00000001"}, map[string]any{"channelId": "library:channel", "state": "playing"})
	if w.Code != 409 || v1Code(w) != "stream_limit_reached" {
		t.Fatalf("a new stream at the limit: %d %s", w.Code, w.Body.String())
	}
}

// A switch can't borrow another device's slot: replacesSessionId must name a
// session of the caller's own device. From a second device of the same member
// the switch is refused (the field is invalid), and a plain start there is a
// second stream and refused at the limit; the first device keeps playing.
func TestMemberLimitsChannelSwitchIsTheSameDevicesOwnStream(t *testing.T) {
	f := memberLimitsFixture(t, 1)
	member, _ := f.member()
	channelFixture(t, f)
	setMemberLimits(t, f, `{"maxStreams":1}`)
	principal, err := f.id.Authenticate(member)
	if err != nil {
		t.Fatal(err)
	}
	second := f.deviceFor(principal.AccountID, principal.ProfileID, "member", "member-second-device").AccessToken
	var a playbackv1.SessionView
	f.callAs(member, "POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": "steal-a-000000001"}, map[string]any{"channelId": "library:channel", "state": "playing"}, 201, &a)
	w := f.raw("POST", "/v1/playback/sessions", second, map[string]string{"Idempotency-Key": "steal-b-000000001"}, map[string]any{"channelId": "library:channel", "state": "playing", "replacesSessionId": a.ID})
	if w.Code != 400 {
		t.Fatalf("a switch from another device: %d %s", w.Code, w.Body.String())
	}
	w = f.raw("POST", "/v1/playback/sessions", second, map[string]string{"Idempotency-Key": "steal-c-000000001"}, map[string]any{"channelId": "library:channel", "state": "playing"})
	if w.Code != 409 || v1Code(w) != "stream_limit_reached" {
		t.Fatalf("a second device at the limit: %d %s", w.Code, w.Body.String())
	}
	var ended int64
	if err := f.db.QueryRow(`SELECT ended_ms FROM playback_v1_sessions WHERE id=?`, a.ID).Scan(&ended); err != nil || ended != 0 {
		t.Fatalf("the first device's stream was ended (%d, %v)", ended, err)
	}
	// Switching twice in a row on the first device stays one stream.
	var b, c playbackv1.SessionView
	f.callAs(member, "POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": "steal-d-000000001"}, map[string]any{"channelId": "library:channel", "state": "playing", "replacesSessionId": a.ID}, 201, &b)
	f.callAs(member, "POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": "steal-e-000000001"}, map[string]any{"channelId": "library:channel", "state": "playing", "replacesSessionId": b.ID}, 201, &c)
	var live int
	if err := f.db.QueryRow(`SELECT count(*) FROM playback_v1_sessions WHERE account_id=? AND ended_ms=0`, principal.AccountID).Scan(&live); err != nil || live != 1 {
		t.Fatalf("%d live sessions after two switches (%v), want 1", live, err)
	}
}

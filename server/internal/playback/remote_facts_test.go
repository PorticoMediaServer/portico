package playback

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
)

// strmProbe is the confined ffprobe document of a STRM target: one video and
// one audio stream.
func strmProbe(format, video, profile, pixFmt string, height int, audio string) []byte {
	return []byte(fmt.Sprintf(`{"format":{"format_name":%q,"duration":"600.000000","start_time":"0.000000","bit_rate":"6000000"},"streams":[
{"index":0,"codec_type":"video","codec_name":%q,"profile":%q,"pix_fmt":%q,"width":%d,"height":%d,"bit_rate":"5500000","color_transfer":"bt709","disposition":{"default":1}},
{"index":1,"codec_type":"audio","codec_name":%q,"channels":2,"channel_layout":"stereo","bit_rate":"192000","tags":{"language":"eng"},"disposition":{"default":1}}]}`, format, video, profile, pixFmt, height*16/9, height, audio))
}

// measuredSTRM is what a session stores and every plan reads back: the facts
// round-trip through remote_playback.facts_json before they are planned.
func measuredSTRM(t *testing.T, probe []byte) DeliverySource {
	t.Helper()
	measured, err := assets.DecodeProbe(probe)
	if err != nil {
		t.Fatal(err)
	}
	facts, err := measuredRemoteFacts(measured)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := facts.encode()
	if err != nil {
		t.Fatal(err)
	}
	stored, err := decodeRemoteFacts(raw)
	if err != nil {
		t.Fatal(err)
	}
	return stored.source("strm-asset")
}

// FMT-20 (JUSTIN #7): the server opens a STRM and relays, remuxes or converts
// it from what it measured; the player never gets the URL and the planner no
// longer passes every STRM through untouched.
func TestSTRMPlansFromMeasuredFacts(t *testing.T) {
	policy := openPolicy()
	// A declared client: its codec, chroma and bit-depth limits are real.
	client := chromeProfile()
	for i := range client.Video {
		if client.Video[i].Codec == "h264" {
			client.Video[i].ChromaSubsampling = []string{"4:2:0"}
		}
	}
	plan := func(source DeliverySource, target QualityTarget) DeliveryPlan {
		return planFor(t, DeliveryInput{SourceID: source.ID, Source: &source, Client: client, Server: plannerToolchain(), Policy: policy, Target: target, Config: DefaultDeliveryConfiguration(), TranscodingEnabled: true})
	}
	auto := autoTarget(policy, 0)

	compatible := measuredSTRM(t, strmProbe("mov,mp4,m4a,3gp,3g2,mj2", "h264", "High", "yuv420p", 1080, "aac"))
	if compatible.Video == nil || compatible.Video.Profile != "High" || compatible.Video.BitDepth != 8 || compatible.Video.ChromaSubsampling != "4:2:0" || len(compatible.Audio) != 1 || compatible.Audio[0].Language != "eng" || !compatible.Remote || compatible.BitRate == 0 {
		t.Fatalf("measured facts lost in facts_json: %+v %+v", compatible, compatible.Video)
	}
	relayed := plan(compatible, auto)
	if relayed.Mode != "remote" || relayed.Strategy != DeliveryOriginal || relayed.SourceContainer != "strm" || relayed.OutputContainer != "mov" || !containsString(relayed.ReasonCodes, ReasonRemotePreflightCompatible) {
		t.Fatalf("a playable STRM is not relayed as it is: %+v", relayed)
	}

	// The cases the old byte relay let through unchecked.
	for name, source := range map[string]DeliverySource{
		"High 4:4:4":     measuredSTRM(t, strmProbe("mov,mp4,m4a,3gp,3g2,mj2", "h264", "High 4:4:4 Predictive", "yuv444p", 1080, "aac")),
		"High 10":        measuredSTRM(t, strmProbe("mov,mp4,m4a,3gp,3g2,mj2", "h264", "High 10", "yuv420p10le", 1080, "aac")),
		"Matroska HEVC":  measuredSTRM(t, strmProbe("matroska,webm", "hevc", "Main 10", "yuv420p10le", 2160, "eac3")),
		"Matroska H.264": measuredSTRM(t, strmProbe("matroska,webm", "h264", "High", "yuv420p", 1080, "aac")),
	} {
		p := plan(source, auto)
		if p.Mode != "hls" || p.Strategy == DeliveryOriginal || p.SourceContainer != "strm" {
			t.Fatalf("%s: a STRM the device cannot play as it is was not remuxed or converted: %+v", name, p)
		}
	}
	remuxed := plan(measuredSTRM(t, strmProbe("matroska,webm", "h264", "High", "yuv420p", 1080, "aac")), auto)
	if remuxed.Strategy != DeliveryCopyRemux || remuxed.VideoAction != "copy" {
		t.Fatalf("a STRM whose only obstacle is its container is not remuxed: %+v", remuxed)
	}

	capped := autoTarget(policy, 0)
	capped.MaxVideoHeight = 720
	if p := plan(compatible, capped); p.Mode != "hls" || p.VideoAction != "convert" || p.TargetHeight > 720 {
		t.Fatalf("a numeric height limit reached a STRM unchecked: %+v", p)
	}
	limited := autoTarget(policy, 0)
	limited.MaxVideoBitrateBPS = 2_000_000
	if p := plan(compatible, limited); p.Mode != "hls" || p.VideoAction != "convert" {
		t.Fatalf("a bitrate ceiling reached a STRM unchecked: %+v", p)
	}
}

func TestSTRMFactsRefuseMediaWithoutPictureOrTiming(t *testing.T) {
	audioOnly := []byte(`{"format":{"format_name":"mp3","duration":"10"},"streams":[{"index":0,"codec_type":"audio","codec_name":"mp3","channels":2}]}`)
	untimed := strmProbe("mov,mp4,m4a,3gp,3g2,mj2", "h264", "High", "yuv420p", 1080, "aac")
	untimed = []byte(strings.Replace(string(untimed), `"duration":"600.000000"`, `"duration":"N/A"`, 1))
	for name, probe := range map[string][]byte{"audio only": audioOnly, "no duration": untimed} {
		measured, err := assets.DecodeProbe(probe)
		if err != nil {
			t.Fatal(name, err)
		}
		if _, err := measuredRemoteFacts(measured); err == nil {
			t.Fatalf("%s: unplayable STRM facts were accepted", name)
		}
	}
	for _, raw := range []string{"", "{}", `{"container":"mov"}`, "not json"} {
		if _, err := decodeRemoteFacts(raw); err == nil {
			t.Fatalf("malformed facts_json %q decoded", raw)
		}
	}
}

type strmWindows struct{}

func (strmWindows) OpenPlaybackWindow(context.Context, string) (subtitles.WindowInput, error) {
	return nil, errors.New("no window in this test")
}

// A STRM the device cannot take as it is starts as server-made HLS from the
// facts measured at acceptance; a re-plan reads those facts back and the
// grant never reaches the descriptor, which holds the URL.
func TestSTRMSessionThatNeedsConversionIsServedAsHLS(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id) VALUES('owner','owner',X'00','profile')`); err != nil {
		t.Fatal(err)
	}
	_, item, asset := catalogFixture(t, db, "lib", "/test", compactcatalog.Movie, "film", "Film", compactcatalog.Asset{Path: "/test/film.strm", Size: 100, ModifiedNS: 1, Container: "strm"})
	hash := identity.Digest("access")
	insertFixtureSession(t, db, hash, "owner", "profile", "owner", time.Now().UTC().Add(time.Hour).Format(time.RFC3339))
	p := identity.Principal{Hash: hash, Viewer: identity.Viewer{AccountID: "owner", ProfileID: "profile", Authority: "local", Role: "owner"}}
	s := New(db)
	h := &HLS{db: db, WindowInputs: strmWindows{}}
	s.ConfigureHLS(h)
	probe := strmProbe("matroska,webm", "hevc", "Main 10", "yuv420p10le", 2160, "eac3")
	s.ConfigureRemote(&Remote{db: db, SubtitleInspector: preparationInspector{inspect: func(context.Context, string, string) (subtitles.SourceMediaInfo, error) {
		return subtitles.SourceMediaInfo{Container: "matroska,webm", VideoCodec: "hevc", AudioCodec: "eac3", DurationUS: 600_000_000, Evidence: "remote:fixture", DescriptorDigest: "digest", Probe: probe}, nil
	}}})
	session, err := s.CreateV1(context.Background(), p, item, "strm-hls", V1Choice{QualityID: "original"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if session.Mode != "hls" || !strings.HasSuffix(session.StreamURL, "/master.m3u8") {
		t.Fatalf("a STRM the baseline device cannot play was not made into HLS: %+v", session)
	}
	plan, err := loadDeliveryPlan(context.Background(), db, session.ID)
	if err != nil || plan == nil || plan.SourceContainer != "strm" || plan.SourceVideoCodec != "hevc" || plan.Mode != "hls" {
		t.Fatalf("plan does not read the remote window input: %+v %v", plan, err)
	}
	var facts, evidence string
	if err = db.QueryRow(`SELECT rp.facts_json,sr.evidence FROM remote_playback rp JOIN subtitle_remote_sessions sr ON sr.session_id=rp.session_id WHERE rp.session_id=?`, session.ID).Scan(&facts, &evidence); err != nil || evidence != "remote:fixture" || !strings.Contains(facts, `"hevc"`) {
		t.Fatalf("session does not keep its measured facts and retained version: %q %q %v", facts, evidence, err)
	}

	// A re-plan reads the session's measured facts, not the descriptor row.
	if err = dbwork.WithWriteTx(context.Background(), db, dbwork.ClassEstablishedPlayback, func(tx *sql.Tx) error {
		source, err := h.loadSessionDeliverySourceTx(context.Background(), tx, session.ID, asset)
		if err != nil {
			return err
		}
		if !source.Remote || source.Container != "matroska" || source.Video == nil || source.Video.Codec != "hevc" || source.Video.BitDepth != 10 || len(source.Audio) != 1 || source.Audio[0].Codec != "eac3" {
			t.Fatalf("re-plan source is not the measured media: %+v", source)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	grant := strings.TrimSuffix(strings.TrimPrefix(session.StreamURL, "/v1/media/"), "/master.m3u8")
	if _, _, handled, err := s.OpenRemote(context.Background(), grant, "GET", "", false); !handled || !errors.Is(err, ErrIncompatible) {
		t.Fatalf("an HLS STRM grant fell through to the descriptor file: %v %v", handled, err)
	}
	// Changing the library's network approvals stops it like a relayed STRM.
	if _, err = s.ApproveNetworkRoots(context.Background(), "lib", nil); err != nil {
		t.Fatal(err)
	}
	var state string
	if err = db.QueryRow(`SELECT state FROM playback_sessions WHERE id=?`, session.ID).Scan(&state); err != nil || state != "stopped" {
		t.Fatalf("an HLS STRM session outlived its network approval: %q %v", state, err)
	}
}

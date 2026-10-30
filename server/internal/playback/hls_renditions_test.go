package playback

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/apispec"
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/decodertest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestPlannerBuildsOneRenditionPerTrack(t *testing.T) {
	source := videoSource("mkv", "h264", assets.StreamDetail{Width: 320, Height: 180, BitDepth: 8, DynamicRange: "sdr"}, SourceAudio{Codec: "ac3", Channels: 6, Language: "eng", Default: true}, SourceAudio{Codec: "aac", Channels: 2, Language: "fra"}, SourceAudio{Codec: "aac", Channels: 2, Language: "eng", Commentary: true})
	p := decide(t, source, chromeProfile(), nil)
	if p.VideoAction != "copy" || len(p.Renditions) != 3 || p.Renditions[0].Action != "convert" || p.Renditions[0].Channels != 2 || !p.Renditions[0].Default || p.Renditions[1].Action != "copy" {
		t.Fatalf("%+v", p)
	}
	raw := string(hlsMasterManifest(&p))
	if strings.Count(raw, "TYPE=AUDIO") != 3 || strings.Contains(raw, "describes-video") || !strings.Contains(raw, "AUTOSELECT=NO") {
		t.Fatal(raw)
	}
}

func renditionFixture(t *testing.T) (context.Context, *Service, *HLS, Session, string) {
	return renditionFixtureAudio(t, false)
}
func renditionFixtureAudio(t *testing.T, mixed bool) (context.Context, *Service, *HLS, Session, string) {
	t.Helper()
	binary := decodertest.QualifiedFFmpeg(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	root := t.TempDir()
	path := filepath.Join(root, "multi.mkv")
	args := []string{"-v", "error", "-y", "-f", "lavfi", "-i", "testsrc2=size=160x90:rate=24", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000", "-f", "lavfi", "-i", "sine=frequency=880:sample_rate=48000", "-f", "lavfi", "-i", "sine=frequency=220:sample_rate=48000", "-t", "40", "-map", "0:v", "-map", "1:a", "-map", "2:a", "-map", "3:a", "-c:v", "libx264", "-preset", "ultrafast", "-g", "96", "-keyint_min", "96", "-sc_threshold", "0", "-pix_fmt", "yuv420p", "-c:a", "aac", path}
	if mixed {
		args = append(args[:len(args)-1], "-c:a:1", "ac3", "-ac:a:1", "6", "-c:a:2", "eac3", "-ac:a:2", "6", path)
	}
	if out, e := exec.CommandContext(ctx, binary, args...).CombinedOutput(); e != nil {
		cancel()
		t.Fatalf("%v %s", e, out)
	}
	facts, e := (assets.Probe{}).Inspect(ctx, path)
	if e != nil {
		t.Fatal(e)
	}
	info, _ := os.Stat(path)
	db, e := persistence.Open(filepath.Join(root, "db"))
	if e != nil {
		t.Fatal(e)
	}
	mustAudioExec(t, db, `INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('owner','owner',X'00','profile',1)`)
	insertFixtureSession(t, db, "login", "owner", "profile", "owner", "2099-01-01T00:00:00Z")
	_, item, token := catalogFixture(t, db, "lib", root, compactcatalog.Movie, "item", "Movie", compactcatalog.Asset{Path: path, Size: info.Size(), ModifiedNS: info.ModTime().UnixNano(), Container: facts.Container, VideoCodec: facts.VideoCodec, AudioCodec: facts.AudioCodec, Width: facts.Width, Height: facts.Height, Duration: facts.Duration})
	gate, e := dbwork.Begin(ctx, db, dbwork.ClassPlaybackStart)
	if e != nil {
		t.Fatal(e)
	}
	if e = assets.PersistStreams(gate.Tx(), token, info.Size(), info.ModTime().UnixNano(), facts.Streams); e != nil {
		gate.Rollback()
		t.Fatal(e)
	}
	if e = gate.Commit(); e != nil {
		t.Fatal(e)
	}
	h, e := NewHLS(ctx, db, filepath.Join(root, "hls"), binary)
	if e != nil {
		t.Fatal(e)
	}
	cfg := DefaultDeliveryConfiguration()
	cfg.PlayedRetentionSeconds = 0
	h.ConfigureSettings(fixedSettings{cfg})
	s := New(db)
	s.ConfigureHLS(h)
	s.ConfigureDelivery(fixedSettings{cfg}, nil, nil, "")
	p := identity.Principal{Hash: "login", Viewer: identity.Viewer{AccountID: "owner", ProfileID: "profile", Authority: "local", Role: "owner"}, Epoch: 1}
	if mixed {
		raw, _ := json.Marshal(documentOf(appleTVProfile()))
		if _, err := s.PublishCapabilities(ctx, p, raw); err != nil {
			t.Fatal(err)
		}
	}
	session, e := s.Create(p, item, "automatic", "renditions")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		cancel()
		wait, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		_ = h.Shutdown(wait)
		db.Close()
	})
	return ctx, s, h, session, strings.TrimSuffix(strings.TrimPrefix(session.StreamURL, "/v1/media/"), "/master.m3u8")
}

func TestRenditionWindowsShareOneClock(t *testing.T) {
	ctx, s, h, session, grant := renditionFixture(t)
	if e := s.Ready(ctx, session); e != nil {
		t.Fatal(e)
	}
	video, e := s.HLSFileContext(ctx, grant, "segment-000003.ts")
	if e != nil {
		t.Fatal(e)
	}
	first, _ := videoSpan(t, video)
	if math.Abs(first-16) > .05 {
		t.Fatal("video", first)
	}
	audio, e := s.HLSFileContext(ctx, grant, "audio-1-000003.ts")
	if e != nil {
		t.Fatal(e)
	}
	out, e := exec.Command(decodertest.QualifiedFFprobe(t), "-v", "error", "-select_streams", "a:0", "-show_entries", "packet=pts_time", "-of", "json", audio).Output()
	if e != nil {
		t.Fatal(e)
	}
	var packets struct {
		Packets []struct {
			PTS string `json:"pts_time"`
		} `json:"packets"`
	}
	if json.Unmarshal(out, &packets) != nil || len(packets.Packets) == 0 {
		t.Fatal(string(out))
	}
	at, _ := strconv.ParseFloat(packets.Packets[0].PTS, 64)
	if math.Abs(at-18) > .05 {
		t.Fatalf("audio clock %f", at)
	}
	files, _ := filepath.Glob(filepath.Join(h.root, session.ID, "audio-2-*.ts"))
	if len(files) != 0 {
		t.Fatal("unselected audio produced", files)
	}
	h.mu.Lock()
	_, unselected := h.active[renditionKey(session.ID, 2)]
	h.mu.Unlock()
	if unselected {
		t.Fatal("unselected producer")
	}
}

func TestFragmentedSignallingFollowsActualDolbyVisionLayer(t *testing.T) {
	facts := matrixToolchain()
	qualifiedCopy := func(in *DeliveryInput) { in.FMP4Output = true; in.Server = facts }
	detail := hdr10Detail
	detail.DynamicRange = assets.RangeDolbyVision
	detail.DolbyVisionProfile = 8
	detail.DolbyVisionLevel = 6
	detail.DolbyVisionCompatibility = 1
	detail.CodecTag = "dvh1"
	source := videoSource("mkv", "hevc", detail, SourceAudio{Codec: "eac3", Channels: 6})
	p := decide(t, source, appleTVProfile(), qualifiedCopy)
	m := string(hlsMasterManifest(&p))
	if !strings.Contains(m, `CODECS="dvh1.08.06,ec-3"`) || !strings.Contains(m, `SUPPLEMENTAL-CODECS="dvh1.08.06/db1p"`) || !strings.Contains(m, "VIDEO-RANGE=PQ") {
		t.Fatal(m)
	}
	client := appleTVProfile()
	client.Video[1].DolbyVisionProfiles = nil
	client.Video[1].DynamicRanges = []string{"sdr", "hdr10", "hlg"}
	client.Display.DynamicRanges = []string{"sdr", "hdr10", "hlg"}
	// A device without Dolby Vision is sent the title's own HDR10 base layer,
	// copied, which needs a converter that can strip the Dolby Vision data.
	p = decide(t, source, client, qualifiedCopy)
	if p.VideoAction != "copy" {
		t.Fatalf("expected qualified base copy, got %+v", p)
	}
	if p.Strategy != DeliveryCopyRemux && p.Strategy != DeliveryAudioConversion {
		t.Fatal("the base layer was not copied", p.Strategy)
	}
	m = string(hlsMasterManifest(&p))
	if strings.Contains(m, "dvh1") || strings.Contains(m, "SUPPLEMENTAL") || !strings.Contains(m, "hvc1.2.4.L153.B0") || !strings.Contains(m, "VIDEO-RANGE=PQ") {
		t.Fatal(m)
	}
	source.Video.DolbyVisionCompatibility = 4
	p = decide(t, source, client, qualifiedCopy)
	if p.VideoAction != "copy" {
		t.Fatalf("expected qualified HLG base copy, got %+v", p)
	}
	if !strings.Contains(string(hlsMasterManifest(&p)), "VIDEO-RANGE=HLG") {
		t.Fatal(string(hlsMasterManifest(&p)))
	}
	p.OutputVideoCodec = "av1"
	p.Trace.Video = &TraceVideo{Level: 5, BitDepth: 8}
	if videoCodecString(&p) != "av01.0.05M.08" {
		t.Fatal(videoCodecString(&p))
	}
}

func TestRenditionFailureIsBoundToOneTrackAndGeneration(t *testing.T) {
	ctx, s, h, session, grant := renditionFixture(t)
	if e := s.Ready(ctx, session); e != nil {
		t.Fatal(e)
	}
	plan, e := loadDeliveryPlan(context.Background(), h.db, session.ID)
	if e != nil {
		t.Fatal(e)
	}
	h.renditionFailed(ctx, session.ID, session.Generation+1, plan.Renditions[1], FailureConverter)
	check, e := loadDeliveryPlan(context.Background(), h.db, session.ID)
	if e != nil || check.Renditions[1].Failure != "" {
		t.Fatal("stale producer changed plan", e)
	}
	h.renditionFailed(ctx, session.ID, session.Generation, plan.Renditions[1], FailureConverter)
	if _, e = s.HLSFileContext(ctx, grant, "audio-1-000000.ts"); !errors.Is(e, ErrAudioRenditionUnavailable) {
		t.Fatal(e)
	}
	if _, e = s.HLSFileContext(ctx, grant, "audio-0-000000.ts"); e != nil {
		t.Fatal("default audio stopped", e)
	}
	if _, e = s.HLSFileContext(ctx, grant, "segment-000000.ts"); e != nil {
		t.Fatal("video stopped", e)
	}
}
func TestRenditionsKeepChosenTrackBeyondCapAndDisambiguateTitles(t *testing.T) {
	tracks := make([]SourceAudio, 20)
	for i := range tracks {
		tracks[i] = SourceAudio{Codec: "aac", Channels: 2, Language: "eng", Title: "Same title"}
	}
	source := videoSource("mkv", "h264", avcDetail, tracks...)
	p := decide(t, source, chromeProfile(), func(in *DeliveryInput) { in.HasAudioChoice = true; in.AudioStream = 20 })
	if len(p.Renditions) != 16 || p.Renditions[15].StreamIndex != 20 || !p.Renditions[15].Default {
		t.Fatal(p.Renditions)
	}
	labels := map[string]bool{}
	for _, r := range p.Renditions {
		if labels[r.Label] {
			t.Fatal("duplicate label")
		}
		labels[r.Label] = true
	}
}

func TestRenditionPreservesAC3AndEAC3ChannelsAtSeek(t *testing.T) {
	ctx, s, h, session, grant := renditionFixtureAudio(t, true)
	if err := s.Ready(ctx, session); err != nil {
		t.Fatal(err)
	}
	plan, err := loadDeliveryPlan(context.Background(), h.db, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	for ordinal, codec := range map[int]string{1: "ac3", 2: "eac3"} {
		r := plan.Renditions[ordinal]
		if r.Action != "copy" || r.Codec != codec || r.Channels != 6 {
			t.Fatalf("%+v", r)
		}
		file, err := s.HLSFileContext(ctx, grant, fmt.Sprintf("audio-%d-000003.ts", ordinal))
		if err != nil {
			t.Fatal(err)
		}
		raw, err := exec.CommandContext(ctx, decodertest.QualifiedFFprobe(t), "-v", "error", "-select_streams", "a:0", "-show_entries", "stream=codec_name,channels:packet=pts_time", "-of", "json", file).Output()
		if err != nil {
			t.Fatal(err)
		}
		var probe struct {
			Streams []struct {
				Codec    string `json:"codec_name"`
				Channels int    `json:"channels"`
			}
			Packets []struct {
				PTS string `json:"pts_time"`
			}
		}
		if json.Unmarshal(raw, &probe) != nil || len(probe.Streams) != 1 || probe.Streams[0].Codec != codec || probe.Streams[0].Channels != 6 || len(probe.Packets) == 0 {
			t.Fatal(string(raw))
		}
		at, _ := strconv.ParseFloat(probe.Packets[0].PTS, 64)
		if math.Abs(at-18) > .05 {
			t.Fatal(codec, at)
		}
	}
}

func TestManifestPublicationWaitsForTheSessionGeneration(t *testing.T) {
	ctx, s, h, session, grant, _ := copyFixture(t, 96, 12)
	if err := s.Ready(ctx, session); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(h.root, session.ID, "generation")
	if err := os.WriteFile(marker, []byte("0"), 0600); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			list := func(path string) []string {
				entries, readErr := os.ReadDir(path)
				if readErr != nil {
					return []string{fmt.Sprintf("read error: %v", readErr)}
				}
				names := make([]string, 0, len(entries))
				for _, entry := range entries {
					names = append(names, entry.Name())
				}
				return names
			}
			var state, artifact string
			var generation int
			queryErr := h.db.QueryRow(`SELECT ps.state,COALESCE(a.status,''),ps.generation FROM playback_sessions ps LEFT JOIN playback_artifacts a ON a.session_id=ps.id WHERE ps.id=?`, session.ID).Scan(&state, &artifact, &generation)
			t.Fatalf("generation marker: %v; root=%v sessionDir=%v state=%q artifact=%q generation=%d queryErr=%v", err, list(h.root), list(filepath.Join(h.root, session.ID)), state, artifact, generation, queryErr)
		}
		t.Fatal(err)
	}
	if _, err := s.HLSFileContext(ctx, grant, "master.m3u8"); !errors.Is(err, ErrSegmentPreparing) {
		t.Fatalf("stale publication was visible: %v", err)
	}
	if err := os.WriteFile(marker, []byte(strconv.Itoa(session.Generation)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.HLSFileContext(ctx, grant, "master.m3u8"); err != nil {
		t.Fatal(err)
	}
}

func TestAudioRenditionLabelMatchesManifestContract(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	source := videoSource("mkv", "h264", assets.StreamDetail{Width: 320, Height: 180, BitDepth: 8, DynamicRange: "sdr"}, SourceAudio{Codec: "aac", Channels: 2, Language: "eng", Default: true, Title: "Director \"A\"\\B\n\x7f"}, SourceAudio{Codec: "aac", Channels: 2, Language: "eng", Commentary: true, Title: "Director \"A\"\\B\r"})
	p := decide(t, source, chromeProfile(), nil)
	if len(p.Renditions) != 2 {
		t.Fatalf("%+v", p)
	}
	doc, schema, err := apispec.Schema("AudioRenditionPlan")
	if err != nil {
		t.Fatal(err)
	}
	manifest := string(hlsMasterManifest(&p))
	for _, r := range p.Renditions {
		raw, _ := json.Marshal(r)
		if issues := doc.ValidateJSON(schema, raw); len(issues) > 0 {
			t.Fatal(issues)
		}
		if r.Title != "Director \"A\"\\B" || r.Commentary != source.Audio[r.Ordinal].Commentary {
			t.Fatalf("rendition facts lost: %+v", r)
		}
		if r.Label != manifestQuoted(r.Label) || !strings.Contains(manifest, `NAME="`+r.Label+`"`) {
			t.Fatalf("label %q differs from manifest %s", r.Label, manifest)
		}
	}
	if p.Renditions[0].Label == p.Renditions[1].Label {
		t.Fatal("lost track identities")
	}
}

func TestDolbyVisionManifestDoesNotInventMissingSourceLevel(t *testing.T) {
	detail := hdr10Detail
	detail.DynamicRange = assets.RangeDolbyVision
	detail.DolbyVisionProfile = 8
	detail.DolbyVisionCompatibility = 1
	detail.CodecTag = "dvh1"
	source := videoSource("mkv", "hevc", detail, SourceAudio{Codec: "eac3", Channels: 6})
	p := decide(t, source, appleTVProfile(), func(in *DeliveryInput) { in.FMP4Output = true; in.Server = matrixToolchain() })
	if p.Trace.Video.DolbyVisionLevel != 0 {
		t.Fatal("fixture unexpectedly has a DV level")
	}
	manifest := string(hlsMasterManifest(&p))
	if strings.Contains(manifest, "CODECS=") {
		t.Fatal("unknown DV level became a codec claim", manifest)
	}
	if !strings.Contains(manifest, "VIDEO-RANGE=PQ") {
		t.Fatal("actual compatible base range lost", manifest)
	}
}

// Apple's HLS authoring specification, exactly (VIDEO.md VS-1d): profile 5 is
// a dvh1 sample entry on its own, 8.4 keeps its HLG base and names the db4h
// brand in SUPPLEMENTAL-CODECS, and neither is signalled as anything else.
func TestAppleHLSReportsTheDeliveredDolbyVisionProfile(t *testing.T) {
	facts := matrixToolchain()
	qualified := func(in *DeliveryInput) { in.FMP4Output = true; in.Server = facts }
	eac3 := SourceAudio{Codec: "eac3", Channels: 6}

	p5 := hdr10Detail
	p5.DynamicRange, p5.DolbyVisionProfile, p5.DolbyVisionLevel, p5.CodecTag = assets.RangeDolbyVision, 5, 6, "dvh1"
	manifest := string(hlsMasterManifest(func() *DeliveryPlan {
		p := decide(t, videoSource("mkv", "hevc", p5, eac3), appleTVProfile(), qualified)
		return &p
	}()))
	if !strings.Contains(manifest, `CODECS="dvh1.05.06,ec-3"`) || strings.Contains(manifest, "SUPPLEMENTAL-CODECS") || !strings.Contains(manifest, "VIDEO-RANGE=PQ") {
		t.Fatal(manifest)
	}

	p84 := hdr10Detail
	p84.DynamicRange, p84.DolbyVisionProfile, p84.DolbyVisionCompatibility, p84.DolbyVisionLevel, p84.CodecTag = assets.RangeDolbyVision, 8, 4, 6, "dvh1"
	client := appleTVProfile()
	for i := range client.Video {
		if client.Video[i].Codec == "hevc" {
			client.Video[i].DolbyVisionProfile8Compatibility = []int{1, 4}
		}
	}
	manifest = string(hlsMasterManifest(func() *DeliveryPlan {
		p := decide(t, videoSource("mkv", "hevc", p84, eac3), client, qualified)
		return &p
	}()))
	if !strings.Contains(manifest, `CODECS="dvh1.08.06,ec-3"`) || !strings.Contains(manifest, `SUPPLEMENTAL-CODECS="dvh1.08.06/db4h"`) || !strings.Contains(manifest, "VIDEO-RANGE=HLG") {
		t.Fatal(manifest)
	}
}

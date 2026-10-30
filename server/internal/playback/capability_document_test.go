package playback

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func storeStreams(t *testing.T, s *Service, streams []assets.Stream) {
	t.Helper()
	var token string
	if err := s.db.QueryRow(`SELECT token FROM catalog_assets LIMIT 1`).Scan(&token); err != nil {
		t.Fatal(err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err = assets.PersistStreams(tx, token, 1, 1, streams); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// The same file is planned differently for different devices, the published
// document is what decides, and the decision is stored with its explanation.
func TestPublishedProfileDecidesDeliveryAndIsExplained(t *testing.T) {
	db, s, p, _, item, _ := activityFixture(t)
	ctx := context.Background()
	s.ConfigureHLS(&HLS{})
	asset := firstCatalogAssetToken(t, db)
	updateCatalogAsset(t, db, asset, func(a *compactcatalog.Asset) {
		a.Container, a.VideoCodec, a.AudioCodec, a.Width, a.Height = "mp4", "hevc", "eac3", 3840, 2160
	})
	storeStreams(t, s, []assets.Stream{
		{Index: 0, Type: "video", Codec: "hevc", Detail: assets.StreamDetail{Profile: "Main 10", Level: 153, BitDepth: 10, Width: 3840, Height: 2160, FrameRate: 23.976, DynamicRange: assets.RangeHDR10, CodecTag: "hvc1"}, ColorTransfer: "smpte2084"},
		{Index: 1, Type: "audio", Codec: "eac3", Channels: 6, Language: "eng", Default: true},
	})

	// Nothing published: the baseline cannot take HEVC, so the title converts.
	session, err := s.Create(p, item, "automatic", "baseline")
	if err != nil || session.Mode != "hls" {
		t.Fatal(session, err)
	}
	plan, err := loadDeliveryPlan(ctx, db, session.ID)
	if err != nil || plan.Strategy != DeliveryVideoConversion || !plan.ToneMap || plan.Trace == nil || plan.Trace.Client.Published {
		t.Fatal(plan, err)
	}

	// An Apple TV publishes its document and the same file plays as it is.
	raw, _ := json.Marshal(documentOf(appleTVProfile()))
	document, err := s.PublishCapabilities(ctx, p, raw)
	if err != nil || document.Revision == "" {
		t.Fatal(err)
	}
	if again, _ := s.PublishCapabilities(ctx, p, raw); again.Revision != document.Revision {
		t.Fatal("republishing the same document changed its revision")
	}
	var writes int
	if err = db.QueryRow(`SELECT revision FROM playback_device_capabilities`).Scan(&writes); err != nil || writes != 1 {
		t.Fatal("republishing an equal document wrote again", writes, err)
	}
	if held := s.CapabilityDocumentFor(ctx, p); held.Revision != document.Revision || held.Client.Family != "apple" {
		t.Fatal("the stored document was not read back", held.Profile(EngineNative).Summary())
	}
	session, err = s.Create(p, item, "automatic", "apple")
	if err != nil || session.Mode != "direct" {
		t.Fatal(session, err)
	}
	plan, err = loadDeliveryPlan(ctx, db, session.ID)
	if err != nil || plan.Strategy != DeliveryOriginal || plan.Engine != EngineNative || plan.AudioStream != 1 || plan.Trace.Client.Revision != document.Revision || plan.Trace.Client.Engine != EngineNative || plan.Trace.Video.DynamicRange != assets.RangeHDR10 {
		t.Fatal(plan, err)
	}

	// Another sign-in has published nothing and is unaffected.
	other := p
	other.Hash = "someone-else"
	if held := s.CapabilityDocumentFor(ctx, other); held.Engines.Native.Evidence != EvidenceBuiltin {
		t.Fatal("a document leaked across sign-ins", held.Client)
	}
	if _, err = s.PublishCapabilities(ctx, p, []byte(`{"version":9}`)); !errors.Is(err, ErrCapabilityDocumentVersion) {
		t.Fatal(err)
	}
}

// A device that says the original failed in its engine gets the next route up on
// its next attempt, and only that device, and only for that file.
func TestReportedRouteFailureEscalatesTheNextPlan(t *testing.T) {
	db, s, p, _, item, _ := activityFixture(t)
	ctx := context.Background()
	s.ConfigureHLS(&HLS{})
	first, err := s.Create(p, item, "automatic", "first")
	if err != nil || first.Mode != "direct" {
		t.Fatal(first, err)
	}
	out, err := s.RecordRouteFailure(ctx, p, first.ID, "decode_error", "MEDIA_ERR_DECODE at 12.4s")
	if err != nil || out.Route != string(DeliveryOriginal) || out.Engine != EngineNative || !out.Escalates {
		t.Fatal(out, err)
	}
	second, err := s.Create(p, item, "automatic", "second")
	if err != nil || second.Mode != "hls" {
		t.Fatal("the failed route was offered again", second, err)
	}
	plan, _ := loadDeliveryPlan(ctx, db, second.ID)
	if plan.Strategy != DeliveryCopyRemux || !rejected(*plan, DeliveryOriginal, RejectClientFailure) {
		t.Fatal(plan.Strategy, plan.Trace.Routes)
	}
	// Reports climb: repackaging fails too, then converting the audio.
	for _, id := range []string{second.ID} {
		if _, err = s.RecordRouteFailure(ctx, p, id, "source_not_supported", ""); err != nil {
			t.Fatal(err)
		}
	}
	third, err := s.Create(p, item, "automatic", "third")
	if err != nil {
		t.Fatal(err)
	}
	if plan, _ = loadDeliveryPlan(ctx, db, third.ID); plan.Strategy != DeliveryAudioConversion {
		t.Fatal(plan.Strategy)
	}
	if _, err = s.RecordRouteFailure(ctx, p, third.ID, "decode_error", ""); err != nil {
		t.Fatal(err)
	}
	fourth, err := s.Create(p, item, "automatic", "fourth")
	if err != nil {
		t.Fatal(err)
	}
	if plan, _ = loadDeliveryPlan(ctx, db, fourth.ID); plan.Strategy != DeliveryVideoConversion {
		t.Fatal(plan.Strategy)
	}
	// The last resort has nothing above it.
	if out, err = s.RecordRouteFailure(ctx, p, fourth.ID, "decode_error", ""); err != nil || out.Escalates {
		t.Fatal(out, err)
	}
	var finalFailures int
	if err = db.QueryRow(`SELECT count(*) FROM playback_route_failures WHERE route='video_conversion'`).Scan(&finalFailures); err != nil || finalFailures != 1 {
		t.Fatal("final conversion failure was not recorded", finalFailures, err)
	}

	// Somebody else's session cannot be reported on, and junk is refused.
	stranger := identity.Principal{Hash: "stranger", Viewer: identity.Viewer{AccountID: "other", ProfileID: "other", Authority: "local", Role: "member"}}
	if _, err = s.RecordRouteFailure(ctx, stranger, first.ID, "decode_error", ""); !errors.Is(err, identity.ErrUnauthorized) {
		t.Fatal("a stranger reported on this session", err)
	}
	if _, err = s.RecordRouteFailure(ctx, p, first.ID, "Not A Code!", ""); !errors.Is(err, ErrRouteFailure) {
		t.Fatal(err)
	}
}

// The viewer's language preference and an explicit track both reach the plan.
func TestAudioTrackChoiceReachesThePlan(t *testing.T) {
	db, s, p, _, item, _ := activityFixture(t)
	s.ConfigureHLS(&HLS{})
	asset := firstCatalogAssetToken(t, db)
	updateCatalogAsset(t, db, asset, func(a *compactcatalog.Asset) { a.Container = "mkv" })
	storeStreams(t, s, []assets.Stream{
		{Index: 0, Type: "video", Codec: "h264", Detail: assets.StreamDetail{Profile: "High", BitDepth: 8, Width: 640, Height: 360, DynamicRange: assets.RangeSDR}},
		{Index: 1, Type: "audio", Codec: "aac", Channels: 2, Language: "eng", Default: true},
		{Index: 2, Type: "audio", Codec: "aac", Channels: 2, Language: "jpn"},
		{Index: 3, Type: "audio", Codec: "aac", Channels: 2, Language: "eng", Title: "Commentary", Commentary: true},
	})
	s.ConfigureDelivery(nil, func(_ *sql.Tx, _ identity.Viewer, _ string) (DeliveryPreferenceReader, error) {
		return listPreferences{"playback.preferredAudioLanguages": {"ja", "en"}}, nil
	}, nil, "")
	session, err := s.Create(p, item, "automatic", "preferred")
	if err != nil {
		t.Fatal(err)
	}
	plan, _ := loadDeliveryPlan(context.Background(), db, session.ID)
	if plan.AudioStream != 2 || plan.Trace.Audio.ChosenBy != "preferred_language" || plan.Trace.Audio.Tracks != 3 {
		t.Fatal(plan.AudioStream, plan.Trace.Audio)
	}
	// An explicit track outranks the preference, commentary included.
	explicit := 3
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	policy := openPolicy()
	input := DeliveryInput{SourceID: asset, Policy: policy, Target: autoTarget(policy, 360), Config: DefaultDeliveryConfiguration(), TranscodingEnabled: true}
	if err = s.completeDeliveryInputTx(context.Background(), tx, p, &input, listPreferences{"playback.preferredAudioLanguages": {"ja"}}, true, &explicit); err != nil {
		t.Fatal(err)
	}
	chosen, err := planDelivery(input)
	if err != nil || chosen.AudioStream != 3 || chosen.Trace.Audio.ChosenBy != "explicit" {
		t.Fatal(chosen.AudioStream, err)
	}
}

// listPreferences is a preference reader that also answers the list accessor.
type listPreferences map[string][]string

func (listPreferences) Bool(string) bool           { return true }
func (listPreferences) Int(string) int             { return 0 }
func (listPreferences) Text(string) string         { return "" }
func (l listPreferences) List(key string) []string { return l[key] }

// A remote bitrate cap narrows every video codec's ceiling to it: an unlimited
// codec (0) and one above it become the ceiling, one below it is untouched. No
// ceiling leaves the profile equal, and the input's slice is never modified.
func TestWithVideoBitrateCeiling(t *testing.T) {
	in := BaselineClientProfile()
	in.Video = append(in.Video, ClientVideoCodec{Codec: "hevc", MaxBitrateBPS: 20_000_000}, ClientVideoCodec{Codec: "av1", MaxBitrateBPS: 1_000_000})
	got := WithVideoBitrateCeiling(in, 3_000_000)
	if len(got.Video) != 3 || got.Video[0].MaxBitrateBPS != 3_000_000 || got.Video[1].MaxBitrateBPS != 3_000_000 || got.Video[2].MaxBitrateBPS != 1_000_000 {
		t.Fatalf("narrowed codecs: %+v", got.Video)
	}
	if in.Video[0].MaxBitrateBPS != 0 || in.Video[1].MaxBitrateBPS != 20_000_000 || in.Video[2].MaxBitrateBPS != 1_000_000 {
		t.Fatalf("the input profile was modified: %+v", in.Video)
	}
	if equal := WithVideoBitrateCeiling(in, 0); !reflect.DeepEqual(equal, in) {
		t.Fatalf("ceiling 0 changed the profile: %+v", equal)
	}
}

// Turning conversion off does not turn repackaging off: a file whose streams
// are only copied into another container converts nothing and still plays.
// Anything that would be converted is refused.
func TestConversionOffStillRepackages(t *testing.T) {
	db, s, p, _, item, _ := activityFixture(t)
	s.ConfigureHLS(&HLS{})
	if _, err := db.Exec(`UPDATE playback_owner_policy SET transcoding_enabled=0 WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	asset := firstCatalogAssetToken(t, db)
	updateCatalogAsset(t, db, asset, func(a *compactcatalog.Asset) { a.Container = "mkv" })
	session, err := s.Create(p, item, "original", "repackage")
	if err != nil || session.Mode != "hls" {
		t.Fatal("a copy-only stream was refused with conversion off", session, err)
	}
	if plan, _ := loadDeliveryPlan(context.Background(), db, session.ID); plan.Strategy != DeliveryCopyRemux || plan.VideoAction != "copy" || plan.AudioAction != "copy" {
		t.Fatal(plan.Strategy, plan.VideoAction, plan.AudioAction)
	}
	updateCatalogAsset(t, db, asset, func(a *compactcatalog.Asset) { a.VideoCodec = "vp9" })
	if _, err = s.Create(p, item, "original", "convert"); err == nil {
		t.Fatal("a conversion was started with conversion off")
	}
}

// A report that names a fault of the stream closes its route for a week; one
// that only says something went wrong closes it for an evening.
func TestUncertainRouteFailuresLapseSooner(t *testing.T) {
	db, s, p, _, item, _ := activityFixture(t)
	ctx := context.Background()
	s.ConfigureHLS(&HLS{})
	first, err := s.Create(p, item, "automatic", "first")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.RecordRouteFailure(ctx, p, first.ID, "engine_error", ""); err != nil {
		t.Fatal(err)
	}
	modeOfNext := func(request string) string {
		t.Helper()
		next, err := s.Create(p, item, "automatic", request)
		if err != nil {
			t.Fatal(err)
		}
		return next.Mode
	}
	if modeOfNext("second") != "hls" {
		t.Fatal("a fresh failure did not close the route")
	}
	age := func(d time.Duration) {
		t.Helper()
		if _, err := db.Exec(`UPDATE playback_route_failures SET reported_at_ms=?`, time.Now().Add(-d).UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}
	age(7 * time.Hour)
	if modeOfNext("third") != "direct" {
		t.Fatal("an uncertain failure still closes the route after an evening")
	}
	if _, err = db.Exec(`UPDATE playback_route_failures SET code='decode_error'`); err != nil {
		t.Fatal(err)
	}
	if modeOfNext("fourth") != "hls" {
		t.Fatal("a decode failure lapsed with the uncertain ones")
	}
	age(8 * 24 * time.Hour)
	if modeOfNext("fifth") != "direct" {
		t.Fatal("a failure outlived its week")
	}
}

// Every DTS stream is scanned as the "dts" codec with its variant beside it, so
// a device that declares DTS-HD must fold onto that codec. The fold used to keep
// a "dts_hd" name no source ever carries, which quietly converted DTS-HD audio
// for devices that take it as it is.
func TestDTSHDDeclarationMatchesTheScannedDTSCodec(t *testing.T) {
	device := func(audio string) ClientProfile {
		t.Helper()
		doc, err := ParseCapabilityDocument([]byte(`{"version":2,"client":{"family":"shield"},"display":{"dynamicRanges":["sdr"]},"engines":{"native":{"name":"exoplayer","evidence":"declared","video":[{"codec":"h264","bitDepths":[8],"maxWidth":1920,"maxHeight":1080}],"audio":[` + audio + `],"transports":[{"transport":"direct","containers":["mkv"],"video":["h264"],"audio":["dts"]}]}}}`))
		if err != nil {
			t.Fatal(err)
		}
		return doc.Profile(EngineNative)
	}
	hdma := videoSource("mkv", "h264", avcDetail, SourceAudio{Codec: "dts", Channels: 8, StreamDetail: assets.StreamDetail{Profile: "DTS-HD MA"}})
	for _, declared := range []string{"dts-hd", "DTS-HD", "dtshd", "dts_hd", "dts-hd ma"} {
		client := device(`{"codec":"` + declared + `","variants":["dts_hd_ma"],"maxChannels":8}`)
		if client.Audio[0].Codec != "dts" {
			t.Fatalf("%s stayed %q instead of the scanned codec", declared, client.Audio[0].Codec)
		}
		if p := decide(t, hdma, client, nil); p.Strategy != DeliveryOriginal {
			t.Fatalf("%s did not play as it is: %+v", declared, p)
		}
	}
	// A family-only DTS declaration still does not promise DTS-HD: the route is
	// refused outright (there is no other track and no encoder in this fixture).
	source := hdma
	client := device(`{"codec":"dts","maxChannels":8}`)
	policy := openPolicy()
	if p, err := planDelivery(DeliveryInput{Source: source, Client: client, Policy: policy, Target: autoTarget(policy, 1080), Config: DefaultDeliveryConfiguration(), TranscodingEnabled: true}); err == nil && p.Strategy == DeliveryOriginal {
		t.Fatal("a bare DTS declaration claimed DTS-HD Master Audio")
	}
}

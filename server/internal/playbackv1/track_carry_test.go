package playbackv1

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
)

// These tests use real queue/session writes, delivery planning, persisted
// Settings and subtitle selection. Only readiness is supplied: no producer or
// media decoder is needed to establish which tracks a start actually selects.
type carryFixture struct {
	t       *testing.T
	catalog *catalogtest.Catalog
	ident   *identity.Service
	service *Service
	caller  Caller
	items   []catalogtest.Item
	tracks  []map[string]string
	prefs   *operations.Store
	prefRev int64
}

func newCarryFixture(t *testing.T, audio bool) *carryFixture {
	t.Helper()
	ctx := context.Background()
	catalog := catalogtest.Open(t)
	ident, err := identity.New(catalog.DB, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	catalog.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id) VALUES('owner','owner',x'00','primary')`)
	envelope, err := ident.Issue("owner", "primary", "local", "owner", 1)
	if err != nil {
		t.Fatal(err)
	}
	principal, err := ident.Authenticate(envelope.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	player := playback.New(catalog.DB)
	player.ConfigureHLS(&playback.HLS{})
	player.ConfigureDelivery(nil, func(tx *sql.Tx, viewer identity.Viewer, device string) (playback.DeliveryPreferenceReader, error) {
		values, _, _, err := operations.EffectivePreferences(tx, viewer, device)
		return values, err
	}, nil, "")
	caps := playback.BaselineCapabilityDocument()
	caps.Engines.Native.Evidence = playback.EvidenceDeclared
	caps.Engines.Native.EmbeddedAudioSwitching = true
	caps.AudioDecode = []playback.ClientAudioDecode{{Codec: "aac", Containers: []string{"mp4"}, MaxSampleRate: 96000, MaxChannels: 2}}
	if _, err = player.PutCapabilities(ctx, principal, principal.DeviceID, -1, caps); err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	subs, err := subtitles.New(subtitles.Options{DB: catalog.DB, Directory: t.TempDir(), Storage: storage.New(binary), HelperBinary: binary,
		Authorize: func(_ context.Context, _ *sql.Tx, p identity.Principal, _ string) (identity.Principal, error) {
			return p, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { subs.Close() })
	player.ConfigureSubtitleDelivery(subs)
	f := &carryFixture{t: t, catalog: catalog, ident: ident, service: New(catalog.DB, player, subs), caller: Caller{Principal: principal, DeviceID: principal.DeviceID, PlaybackProtocol: PlaybackProtocol}, prefs: operations.New(catalog.DB), prefRev: 1}
	f.service.ReadyCheck = func(context.Context, playback.Session) (bool, error) { return true, nil }
	root := t.TempDir()
	library := catalog.Library("carry-library", "Carry library", "movie", root)
	var album catalogtest.Item
	if audio {
		artist := catalog.Artist(library, "Fixture artist")
		album = catalog.Album(artist, "Fixture album", 2026)
	}
	for i := 0; i < 2; i++ {
		path := filepath.Join(root, fmt.Sprintf("file-%d.mp4", i))
		var item catalogtest.Item
		if audio {
			item = catalog.Song(album, i+1, path, fmt.Sprintf("Song %d", i))
			catalog.Exec(`UPDATE catalog_assets SET video_codec='',width=0,height=0 WHERE token=?`, item.Token)
			catalog.Exec(`INSERT INTO audio_render_facts(asset_id,size,modified_ns,container,codec,sample_rate,channels,bit_depth,raw_frames,duration_frames,start_frames,end_frames,trim_source,decoder_config,measured_ms)
 SELECT token,size,modified_ns,'mp4','aac',44100,2,0,238140000,238140000,0,0,'none','EhA=',1 FROM catalog_assets WHERE token=?`, item.Token)
		} else {
			item = catalog.Movie(library, path, fmt.Sprintf("Film %d", i), 2026)
		}
		en, ja := 1, 2
		if i == 1 {
			en, ja = 5, 7 // A continuation must carry language, never a track number.
		}
		streams := []assets.Stream{
			{Index: en, Type: "audio", Codec: "aac", Channels: 2, Language: "eng", Default: true},
			{Index: ja, Type: "audio", Codec: "aac", Channels: 2, Language: "jpn"},
		}
		if !audio {
			streams = append(streams, assets.Stream{Index: 0, Type: "video", Codec: "h264", Detail: assets.StreamDetail{Profile: "High", Level: 41, BitDepth: 8, Width: 1920, Height: 1080, FrameRate: 24, DynamicRange: assets.RangeSDR}})
		}
		catalog.Write(func(_ context.Context, tx *sql.Tx) error {
			return assets.PersistStreams(tx, item.Token, 1000, 1, streams)
		})
		f.items = append(f.items, item)
		tracks := map[string]string{}
		if !audio {
			for _, language := range []string{"en", "ja"} {
				content := "1\n00:00:00,000 --> 00:00:02,000\nFixture transcript\n"
				receipt, err := subs.Mutate(ctx, principal, item.Public, subtitles.Mutation{OperationID: fmt.Sprintf("carry-subtitle-%d-%s", i, language), SourceID: item.Token, Scope: "personal", Format: "srt", Language: language, Title: language, Rights: "Fixture transcript", OffsetUS: "0", Content: &content})
				if err != nil {
					t.Fatal(err)
				}
				tracks[language] = receipt.ResourceID
			}
		}
		f.tracks = append(f.tracks, tracks)
	}
	catalog.Drain()
	return f
}

func (f *carryFixture) settings(audio, subtitle, mode string) {
	f.t.Helper()
	_, err := f.prefs.ApplyPreferences(context.Background(), f.caller.Principal, func(context.Context, *sql.Tx, string) error { return nil }, operations.PreferenceChange{
		Scope: operations.ScopeProfileServer, DeviceClass: "web", ExpectedRevision: f.prefRev, IdempotencyKey: fmt.Sprintf("carry-settings-%d", f.prefRev),
		Values: operations.PreferencePatch{"playback.preferredAudioLanguages": []string{audio}, "playback.preferredSubtitleLanguages": []string{subtitle}, "playback.subtitleMode": mode},
	})
	if err != nil {
		f.t.Fatal(err)
	}
	f.prefRev++
}

func (f *carryFixture) queue() QueueReply {
	f.t.Helper()
	q, err := f.service.CreateQueue(context.Background(), f.caller, "carry-create-queue-0001", CreateQueueRequest{Segments: []SegmentInput{{Source: Selector{Items: &ItemList{IDs: []string{f.items[0].Public, f.items[1].Public}}}}}, StartPlayback: &StartPlayback{State: "playing"}})
	if err != nil || q.Session == nil || q.Queue.Current == nil {
		f.t.Fatalf("create: %+v %v", q, err)
	}
	return q
}

func (f *carryFixture) choose(session SessionView, index int) SessionView {
	f.t.Helper()
	track := "a2"
	if index == 1 {
		track = "a7"
	}
	chosen, err := f.service.Patch(context.Background(), f.caller, session.ID, session.Revision, Change{Audio: &AudioRequest{TrackID: track}, Subtitles: &SubtitleRequest{TrackID: TrackRef(f.tracks[index]["ja"])}})
	if err != nil {
		f.t.Fatal(err)
	}
	f.assertTracks(chosen, index, track, "ja")
	stored := f.stored(chosen.ID)
	if stored.Carry == nil || *stored.Carry != (TrackCarry{AudioLanguage: "ja", SubtitleMode: "on", SubtitleLanguage: "ja"}) {
		f.t.Fatalf("manual choice not carried as languages: %+v", stored.Carry)
	}
	return chosen
}

func (f *carryFixture) stored(session string) storedRequest {
	f.t.Helper()
	r, err := loadRow(context.Background(), f.catalog.DB, session)
	if err != nil {
		f.t.Fatal(err)
	}
	return r.stored()
}

func (f *carryFixture) assertTracks(session SessionView, index int, audio, subtitle string) {
	f.t.Helper()
	if session.ItemID != f.items[index].Public || session.Presentation.AudioTrackID != audio {
		f.t.Fatalf("item/audio: %+v", session)
	}
	if subtitle == "" {
		if session.Presentation.Subtitle != nil {
			f.t.Fatalf("subtitles should be off: %+v", session.Presentation.Subtitle)
		}
	} else if session.Presentation.Subtitle == nil || session.Presentation.Subtitle.TrackID != f.tracks[index][subtitle] {
		f.t.Fatalf("subtitle: %+v, want %s", session.Presentation.Subtitle, f.tracks[index][subtitle])
	}
}

func (f *carryFixture) start(key string, req StartRequest) SessionView {
	f.t.Helper()
	session, preparing, err := f.service.Start(context.Background(), f.caller, key, req)
	if err != nil || preparing {
		f.t.Fatalf("start: %+v preparing=%v %v", session, preparing, err)
	}
	return session
}

func TestQueueCarryFreshPausedRestoreUsesCurrentSettings(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, mode := range []string{"always", "off", "forced"} {
		t.Run(mode, func(t *testing.T) {
			f := newCarryFixture(t, false)
			f.settings("en", "en", "always")
			q := f.queue()
			chosen := f.choose(*q.Session, 0)
			if err := f.service.Stop(context.Background(), f.caller, chosen.ID, nil); err != nil {
				t.Fatal(err)
			}
			// Settings changed while the video was parked. The original queue,
			// original first occurrence and its old Carry are all still present.
			f.settings("en", "en", mode)
			// Quality already resets through the saved-default seam. Track carry
			// must not bypass the same fresh start's Settings resolution.
			_, err := f.prefs.ApplyPreferences(context.Background(), f.caller.Principal, func(context.Context, *sql.Tx, string) error { return nil }, operations.PreferenceChange{
				Scope: operations.ScopeDeviceClass, DeviceClass: "web", ExpectedRevision: 1, IdempotencyKey: "carry-saved-quality", Values: operations.PreferencePatch{"quality.home": "automatic"},
			})
			if err != nil {
				t.Fatal(err)
			}
			f.caller.SavedQuality = func(context.Context) (string, error) {
				values, _, _, err := operations.EffectivePreferences(f.catalog.DB, f.caller.Principal.Viewer, "web")
				return values.Text("quality.home"), err
			}
			if f.stored(chosen.ID).Carry == nil {
				t.Fatal("fixture lost the predecessor's Carry")
			}
			at := int64(42000)
			restored := f.start("carry-paused-restore-0001", StartRequest{Queue: &QueueRef{QueueID: q.Queue.ID, EntryID: q.Queue.Current.EntryID}, State: "paused", StartPositionMs: &at})
			wantSubtitle := "en"
			if mode != "always" {
				wantSubtitle = ""
			}
			f.assertTracks(restored, 0, "a1", wantSubtitle)
			if restored.State != "paused" || restored.Presentation.StartPositionMs != at || restored.Queue == nil || *restored.Queue != *chosen.Queue || f.stored(restored.ID).Carry != nil {
				t.Fatalf("fresh paused restore changed its occurrence/position or kept Carry: %+v", restored)
			}
			if restored.Presentation.Quality == nil || restored.Presentation.Quality.Mode != QualityAutomatic {
				t.Fatalf("fresh restore lost saved quality: %+v", restored.Presentation.Quality)
			}
			var header QueueView
			row, err := loadQueue(context.Background(), f.catalog.DB, q.Queue.ID)
			if err != nil {
				t.Fatal(err)
			}
			if err = f.service.withLayout(context.Background(), row, func(l *layout) error { header = f.service.header(l); return nil }); err != nil || header.Current == nil || header.Current.EntryID != q.Queue.Current.EntryID || row.session != restored.ID {
				t.Fatalf("restore lost the actual queue: %+v %+v %v", row, header, err)
			}
		})
	}
}

func TestQueueCarryFreshFirstTitleStartResetsWithOrWithoutOldSessionEnd(t *testing.T) {
	for _, end := range []bool{false, true} {
		t.Run(fmt.Sprintf("ended=%v", end), func(t *testing.T) {
			f := newCarryFixture(t, false)
			f.settings("en", "en", "always")
			q := f.queue()
			chosen := f.choose(*q.Session, 0)
			if end {
				if err := f.service.End(context.Background(), chosen.ID, "ended", ""); err != nil {
					t.Fatal(err)
				}
			}
			fresh := f.start("carry-fresh-first-title-01", StartRequest{Queue: chosen.Queue, StartFrom: "beginning"})
			f.assertTracks(fresh, 0, "a1", "en")
			if f.stored(fresh.ID).Carry != nil {
				t.Fatal("same first title borrowed old choices without a continuation")
			}
		})
	}
}

func TestQueueCarryFreshRestoreUsesPreferredLanguagesInsteadOfFileDefault(t *testing.T) {
	f := newCarryFixture(t, false)
	f.settings("ja", "ja", "always")
	q := f.queue()
	f.assertTracks(*q.Session, 0, "a2", "ja")
	chosen, err := f.service.Patch(context.Background(), f.caller, q.Session.ID, q.Session.Revision, Change{Audio: &AudioRequest{TrackID: "a1"}, Subtitles: &SubtitleRequest{TrackID: TrackRef(f.tracks[0]["en"])}})
	if err != nil {
		t.Fatal(err)
	}
	f.assertTracks(chosen, 0, "a1", "en")
	carry := f.stored(chosen.ID).Carry
	if carry == nil || *carry != (TrackCarry{AudioLanguage: "en", SubtitleMode: "on", SubtitleLanguage: "en"}) {
		t.Fatalf("fixture lost its English player choices: %+v", carry)
	}
	if err = f.service.Stop(context.Background(), f.caller, chosen.ID, nil); err != nil {
		t.Fatal(err)
	}
	at := int64(42000)
	restored := f.start("carry-preferred-restore-01", StartRequest{Queue: chosen.Queue, State: "paused", StartPositionMs: &at})
	f.assertTracks(restored, 0, "a2", "ja")
	if restored.State != "paused" || restored.Presentation.StartPositionMs != at || f.stored(restored.ID).Carry != nil {
		t.Fatalf("preferred-language restore kept a prior player choice: %+v", restored)
	}
}

func TestQueueCarryAdvanceRetainsLanguagesAcrossFilesAndEndedCompletion(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, end := range []bool{false, true} {
		t.Run(fmt.Sprintf("ended=%v", end), func(t *testing.T) {
			f := newCarryFixture(t, false)
			f.settings("en", "en", "always")
			q := f.queue()
			chosen := f.choose(*q.Session, 0)
			reason := "next"
			if end {
				reason = "completion"
				if err := newReporter(t, f.service, f.caller).report(chosen.ID, Observation{Generation: chosen.Presentation.Generation, State: "ended", PositionMs: 5400000}); err != nil {
					t.Fatal(err)
				}
			}
			next, err := f.service.Advance(context.Background(), f.caller, q.Queue.ID, q.Queue.Revision, AdvanceRequest{Reason: reason})
			if err != nil || next.Session == nil || next.Queue.Current == nil {
				t.Fatalf("advance: %+v %v", next, err)
			}
			f.assertTracks(*next.Session, 1, "a7", "ja")
			back, err := f.service.Advance(context.Background(), f.caller, q.Queue.ID, next.Queue.Revision, AdvanceRequest{Reason: "previous"})
			if err != nil || back.Session == nil {
				t.Fatalf("previous: %+v %v", back, err)
			}
			f.assertTracks(*back.Session, 0, "a2", "ja")
			jump, err := f.service.Advance(context.Background(), f.caller, q.Queue.ID, back.Queue.Revision, AdvanceRequest{Reason: "entry", EntryID: next.Queue.Current.EntryID})
			if err != nil || jump.Session == nil {
				t.Fatalf("entry jump: %+v %v", jump, err)
			}
			f.assertTracks(*jump.Session, 1, "a7", "ja")
		})
	}
}

func TestQueueCarryAdvanceFromAnotherDeviceKeepsContinuation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := newCarryFixture(t, false)
	f.settings("en", "en", "always")
	q := f.queue()
	chosen := f.choose(*q.Session, 0)
	envelope, err := f.ident.Issue("owner", "primary", "local", "owner", 1)
	if err != nil {
		t.Fatal(err)
	}
	principal, err := f.ident.Authenticate(envelope.AccessToken)
	if err != nil || principal.DeviceID == f.caller.DeviceID {
		t.Fatalf("second device identity: %+v %v", principal.Viewer, err)
	}
	controller := Caller{Principal: principal, DeviceID: principal.DeviceID, PlaybackProtocol: PlaybackProtocol}
	caps := f.service.deviceDocument(context.Background(), f.caller.DeviceID)
	if _, err = f.service.PutCapabilities(context.Background(), principal, controller.DeviceID, "", caps); err != nil {
		t.Fatal(err)
	}
	next, err := f.service.Advance(context.Background(), controller, q.Queue.ID, q.Queue.Revision, AdvanceRequest{Reason: "next"})
	if err != nil || next.Session == nil {
		t.Fatalf("controller advance: %+v %v", next, err)
	}
	f.assertTracks(*next.Session, 1, "a7", "ja")
	old, err := f.service.Get(context.Background(), f.caller, chosen.ID)
	if err != nil || old.State != "ended" || old.End == nil || old.End.Reason != "replaced" {
		t.Fatalf("controller did not retire predecessor: %+v %v", old, err)
	}
}

func TestQueueCarryExplicitReplacementRetainsReplayAndRecovery(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, reason := range []string{"", "ended", "lease_expired"} {
		// An empty name becomes #00 in TempDir; the current database opener
		// treats that unescaped # as a file URI fragment. Name the live case
		// explicitly while retaining its empty end reason and real Settings.
		name := reason
		if name == "" {
			name = "active"
		}
		t.Run(name, func(t *testing.T) {
			f := newCarryFixture(t, false)
			f.settings("en", "en", "always")
			q := f.queue()
			chosen := f.choose(*q.Session, 0)
			if reason != "" {
				if err := f.service.End(context.Background(), chosen.ID, reason, ""); err != nil {
					t.Fatal(err)
				}
			}
			replayed := f.start("carry-explicit-replay-001", StartRequest{Queue: chosen.Queue, ReplacesSessionID: chosen.ID, StartFrom: "beginning"})
			f.assertTracks(replayed, 0, "a2", "ja")
		})
	}
}

func TestQueueCarryFreshStartReplayAndLateSourceFences(t *testing.T) {
	f := newCarryFixture(t, false)
	f.settings("en", "en", "always")
	q := f.queue()
	chosen := f.choose(*q.Session, 0)
	if err := f.service.Stop(context.Background(), f.caller, chosen.ID, nil); err != nil {
		t.Fatal(err)
	}
	at := int64(42000)
	req := StartRequest{Queue: chosen.Queue, State: "paused", StartPositionMs: &at}
	const key = "carry-fresh-replay-key-001"
	fresh := f.start(key, req)
	// Replaying the same key after a manual change answers the same session;
	// it must neither reset its choices again nor create another source.
	changed := f.choose(fresh, 0)
	again := f.start(key, req)
	if again.ID != fresh.ID || again.Presentation.Generation != changed.Presentation.Generation {
		t.Fatalf("start replay made a different source: %+v %+v", again, changed)
	}
	f.assertTracks(again, 0, "a2", "ja")
	conflict := req
	conflict.State = "playing"
	if _, _, err := f.service.Start(context.Background(), f.caller, key, conflict); !errors.Is(err, ErrIdempotencyMismatch) {
		t.Fatalf("changed start replay: %v", err)
	}
	before, err := loadRow(context.Background(), f.catalog.DB, fresh.ID)
	if err != nil {
		t.Fatal(err)
	}
	r := newReporter(t, f.service, f.caller)
	if _, err = r.observe(chosen.ID, Observation{Generation: chosen.Presentation.Generation, State: "playing", PositionMs: 99999}); !errors.Is(err, ErrEnded) {
		t.Fatalf("late predecessor report: %v", err)
	}
	// Claim the current presentation before sending a late sample for its predecessor.
	if _, err = r.claim(fresh.ID, changed.Presentation.Generation); err != nil {
		t.Fatal(err)
	}
	if err = r.report(fresh.ID, Observation{Generation: 0, State: "playing", PositionMs: 99999}); err != nil {
		t.Fatal(err)
	}
	after, err := loadRow(context.Background(), f.catalog.DB, fresh.ID)
	if err != nil || before.request != after.request || before.presentation != after.presentation || before.position != after.position || before.lastSeq != after.lastSeq || before.state != after.state {
		t.Fatalf("late/stale source changed the current playback: before=%+v after=%+v %v", before, after, err)
	}
	other := f.caller
	other.Principal.ProfileID = "other-profile"
	if _, _, err = f.service.Start(context.Background(), other, key, req); !errors.Is(err, ErrNotFound) {
		t.Fatalf("replay crossed the profile fence: %v", err)
	}
	var count int
	if err = f.catalog.DB.QueryRow(`SELECT count(*) FROM playback_v1_sessions`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("replay/fence created sessions: %d %v", count, err)
	}
}

func TestQueueCarryDoesNotBorrowFromUnrelatedReplacement(t *testing.T) {
	f := newCarryFixture(t, false)
	f.settings("en", "en", "always")
	q := f.queue()
	chosen := f.choose(*q.Session, 0)
	unrelated := f.start("carry-unrelated-item-0001", StartRequest{ItemID: f.items[1].Public})
	fresh := f.start("carry-unrelated-replace-01", StartRequest{Queue: chosen.Queue, ReplacesSessionID: unrelated.ID})
	f.assertTracks(fresh, 0, "a1", "en")
	if f.stored(fresh.ID).Carry != nil {
		t.Fatal("an unrelated slot replacement continued the queue's old choices")
	}
}

func TestQueueCarryRefusedReplacementAndAdmissionKeepQueue(t *testing.T) {
	f := newCarryFixture(t, false)
	f.settings("en", "en", "always")
	q := f.queue()
	chosen := f.choose(*q.Session, 0)
	req := StartRequest{Queue: chosen.Queue, ReplacesSessionID: chosen.ID}
	otherDevice := f.caller
	otherDevice.DeviceID = "not-the-playing-device"
	var field *FieldError
	if _, _, err := f.service.Start(context.Background(), otherDevice, "carry-refused-device-0001", req); !errors.As(err, &field) || field.Path != "replacesSessionId" {
		t.Fatalf("replacement lost its device fence: %v", err)
	}
	denied := errors.New("fixture admission denied")
	caller := f.caller
	caller.Admit = func(context.Context, string) (int, error) { return 0, denied }
	if _, _, err := f.service.Start(context.Background(), caller, "carry-refused-admit-00001", req); !errors.Is(err, denied) {
		t.Fatalf("carry bypassed admission: %v", err)
	}
	queue, err := loadQueue(context.Background(), f.catalog.DB, q.Queue.ID)
	if err != nil || queue.session != chosen.ID {
		t.Fatalf("refusal changed queue session: %+v %v", queue, err)
	}
	current, err := f.service.Get(context.Background(), f.caller, chosen.ID)
	if err != nil || current.State != chosen.State || current.Presentation.Generation != chosen.Presentation.Generation {
		t.Fatalf("refusal changed predecessor: %+v %v", current, err)
	}
	f.assertTracks(current, 0, "a2", "ja")
	var count int
	if err = f.catalog.DB.QueryRow(`SELECT count(*) FROM playback_v1_sessions`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("refusal created a session: %d %v", count, err)
	}
}

func TestQueueCarryReplacementKeepsExplicitTrackPrecedence(t *testing.T) {
	f := newCarryFixture(t, false)
	f.settings("ja", "ja", "always")
	q := f.queue()
	chosen := f.choose(*q.Session, 0)
	replacement := f.start("carry-explicit-track-0001", StartRequest{Queue: chosen.Queue, ReplacesSessionID: chosen.ID, Audio: &AudioRequest{TrackID: "a1"}, Subtitles: &SubtitleRequest{TrackID: TrackOff}})
	f.assertTracks(replacement, 0, "a1", "")
	carry := f.stored(replacement.ID).Carry
	if carry == nil || *carry != (TrackCarry{AudioLanguage: "en", SubtitleMode: "off"}) {
		t.Fatalf("explicit choice was replaced by prior carry: %+v", carry)
	}
	next, err := f.service.Advance(context.Background(), f.caller, q.Queue.ID, q.Queue.Revision, AdvanceRequest{Reason: "next"})
	if err != nil || next.Session == nil {
		t.Fatalf("advance after explicit choice: %+v %v", next, err)
	}
	f.assertTracks(*next.Session, 1, "a5", "")
}

func TestQueueCarryOrdinaryAudioDoesNotAcquireVODChoices(t *testing.T) {
	f := newCarryFixture(t, true)
	q := f.queue()
	// A stored VOD choice must not affect an ordinary listening presentation,
	// even if one exists in an earlier request on this queue.
	raw, err := json.Marshal(storedRequest{Quality: Quality{Mode: QualityOriginal}, Carry: &TrackCarry{AudioLanguage: "ja", SubtitleMode: "on", SubtitleLanguage: "ja"}})
	if err != nil {
		t.Fatal(err)
	}
	f.catalog.Exec(`UPDATE playback_v1_sessions SET request=? WHERE id=?`, string(raw), q.Session.ID)
	at := int64(12000)
	fresh := f.start("carry-audio-paused-restore", StartRequest{Queue: q.Session.Queue, State: "paused", StartPositionMs: &at, ReplacesSessionID: q.Session.ID})
	if fresh.Kind != "audio" || fresh.State != "paused" || fresh.Presentation.StartPositionMs != at || fresh.Presentation.AudioRender == nil || fresh.Presentation.AudioRender.Mode != "direct" || f.stored(fresh.ID).Carry != nil {
		t.Fatalf("ordinary audio changed: %+v", fresh)
	}
}

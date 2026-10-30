package httpapi

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
)

func airPlayKnownStreams(t *testing.T, f *v1Fixture) {
	t.Helper()
	asset := f.records[0].Token
	if _, err := f.db.Exec(`INSERT INTO asset_stream_facts(asset_id,revision,size,modified_ns,fingerprint,detail_version) SELECT token,1,size,modified_ns,'receiver-facts',99 FROM catalog_assets WHERE token=?`, asset); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`INSERT INTO asset_streams(asset_id,stream_index,type,codec,language,title,channels,channel_layout,is_default,is_forced,detail_json) VALUES(?,0,'video','h264','','',0,'',1,0,'{"width":1920,"height":1080,"bitDepth":8,"profile":"High","level":40,"dynamicRange":"sdr"}'),(?,1,'audio','aac','eng','Default',2,'stereo',1,0,'{"sampleRate":48000,"profile":"LC"}'),(?,2,'audio','aac','fra','Selected',2,'stereo',0,0,'{"sampleRate":48000,"profile":"LC"}')`, asset, asset, asset); err != nil {
		t.Fatal(err)
	}
}

func TestAirPlayReceiverPreparesExactCurrentChoicesBeforeHandoff(t *testing.T) {
	f, drawn := v1BurnInFixture(t)
	if _, err := f.db.Exec(`UPDATE playback_owner_policy SET transcoding_enabled=1`); err != nil {
		t.Fatal(err)
	}
	airPlayKnownStreams(t, f)
	principal, err := f.id.Authenticate(f.owner.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	tracks := []string{}
	for i, lang := range []string{"en", "fr"} {
		content := "1\n00:00:00,000 --> 00:00:02,000\n" + lang + "\n"
		receipt, e := f.v1.Subtitles.Mutate(context.Background(), principal, f.items[0], subtitles.Mutation{OperationID: []string{"receiver-text-one", "receiver-text-two"}[i], SourceID: f.records[0].Token, Scope: "personal", Format: "srt", Language: lang, Title: lang, Rights: "Own text", OffsetUS: "500", Content: &content})
		if e != nil {
			t.Fatal(e)
		}
		tracks = append(tracks, receipt.ResourceID)
	}
	var local playbackv1.SessionView
	f.call("POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": "airplay-local-start-1"}, startBody(f.items[0], map[string]any{"subtitles": map[string]any{"trackId": tracks[0], "delivery": "sidecar"}}), 201, &local)
	if local.Presentation.Mode != "direct" {
		t.Fatalf("expected direct local text: %+v", local.Presentation)
	}
	var timed playbackv1.SessionView
	f.call("PATCH", "/v1/playback/sessions/"+local.ID, map[string]string{"If-Match": local.Revision}, map[string]any{"subtitles": map[string]any{"trackId": tracks[0], "delivery": "sidecar"}, "subtitleOffsetUs": "-500"}, 200, &timed)
	local = timed
	var lastURL string
	for index, track := range []string{tracks[0], "", tracks[1], drawn, "", tracks[0]} {
		if index > 0 {
			var value any = track
			delivery := "sidecar"
			if track == "" {
				value = nil
			}
			if track == drawn {
				delivery = "embeddedClient"
			}
			var next playbackv1.SessionView
			change := map[string]any{"subtitles": map[string]any{"trackId": value, "delivery": delivery}}
			if track == tracks[1] {
				change["audio"] = map[string]any{"trackId": "a2"}
			}
			f.call("PATCH", "/v1/playback/sessions/"+local.ID, map[string]string{"If-Match": local.Revision}, change, 200, &next)
			local = next
		}
		var prepared playbackv1.AirPlayPresentation
		endpoint := "/v1/playback/sessions/" + local.ID + "/receivers/airplay"
		body := playbackv1.AirPlayPrepare{Generation: local.Presentation.Generation, ChoiceRevision: local.Presentation.ChoiceRevision, PositionMs: 17000}
		f.call("POST", endpoint, nil, body, 200, &prepared)
		if prepared.Presentation.AudioTrackID != local.Presentation.AudioTrackID {
			t.Fatalf("receiver changed audio: %s -> %s", local.Presentation.AudioTrackID, prepared.Presentation.AudioTrackID)
		}
		if prepared.Presentation.Mode != "stream" || prepared.Presentation.URL == local.Presentation.URL || !strings.HasSuffix(prepared.Presentation.URL, "/master.m3u8") {
			t.Fatal(prepared)
		}
		if track == "" {
			if prepared.Presentation.Subtitle != nil || len(prepared.Presentation.Subtitles) != 0 {
				t.Fatal(prepared)
			}
		} else {
			if prepared.Presentation.Subtitle == nil || prepared.Presentation.Subtitle.TrackID != track {
				t.Fatal(prepared)
			}
			delivery := "sidecar"
			if track == drawn {
				delivery = "burn"
			}
			if prepared.Presentation.Subtitle.Delivery != delivery {
				t.Fatal(prepared)
			}
		}
		var again playbackv1.AirPlayPresentation
		f.call("POST", endpoint, nil, body, 200, &again)
		if again.Presentation.URL != prepared.Presentation.URL {
			t.Fatal("preparation was not idempotent")
		}
		var unchanged playbackv1.SessionView
		f.call("GET", "/v1/playback/sessions/"+local.ID, nil, nil, 200, &unchanged)
		if unchanged.Presentation.URL != local.Presentation.URL || unchanged.Presentation.Generation != local.Presentation.Generation || unchanged.Presentation.ChoiceRevision != local.Presentation.ChoiceRevision {
			t.Fatal("preparation changed the local picture")
		}
		grant := strings.TrimSuffix(strings.TrimPrefix(prepared.Presentation.URL, "/v1/media/"), "/master.m3u8")
		_, _, _, e := f.v1.Playback.ResolveGrantContext(context.Background(), grant)
		if e != nil {
			t.Fatalf("receiver grant: %v", e)
		}
		var media string
		if e = f.db.QueryRow(`SELECT media_session_id FROM playback_airplay_presentations WHERE session_id=?`, local.ID).Scan(&media); e != nil {
			t.Fatal(e)
		}
		plan, e := f.v1.Playback.SessionPlan(context.Background(), media)
		if e != nil || plan == nil || !plan.AirPlay || len(plan.Renditions) != 0 || plan.Mode != "hls" {
			t.Fatal(plan, e)
		}
		if track != "" {
			selected, e := f.v1.Subtitles.Plan(context.Background(), principal, f.items[0], media)
			if e != nil {
				t.Fatal(e)
			}
			ownerMedia := ""
			if e = f.db.QueryRow(`SELECT media_session_id FROM playback_v1_sessions WHERE id=?`, local.ID).Scan(&ownerMedia); e != nil {
				t.Fatal(e)
			}
			chosen, e := f.v1.Subtitles.Plan(context.Background(), principal, f.items[0], ownerMedia)
			if e != nil || selected.OffsetUS != chosen.OffsetUS {
				t.Fatal("receiver changed exact subtitle timing", selected.OffsetUS, chosen.OffsetUS, e)
			}
		}
		if track == drawn {
			if plan.BurnIn == nil || plan.BurnIn.ResourceID != drawn {
				t.Fatal("receiver lost drawn caption", plan)
			}
		} else if plan.BurnIn != nil {
			t.Fatal("Off/text inherited another track burn", plan)
		}
		if lastURL != "" {
			old := strings.TrimSuffix(strings.TrimPrefix(lastURL, "/v1/media/"), "/master.m3u8")
			if _, _, _, e = f.v1.Playback.ResolveGrantContext(context.Background(), old); e == nil {
				t.Fatal("old choices kept a receiver grant")
			}
		}
		stale := body
		stale.ChoiceRevision++
		w := f.raw("POST", endpoint, f.owner.AccessToken, nil, stale)
		if w.Code != 412 {
			t.Fatalf("stale prep %d %s", w.Code, w.Body.String())
		}
		lastURL = prepared.Presentation.URL
	}
	f.call("DELETE", "/v1/playback/sessions/"+local.ID, nil, nil, 204, nil)
	grant := strings.TrimSuffix(strings.TrimPrefix(lastURL, "/v1/media/"), "/master.m3u8")
	if _, _, _, e := f.v1.Playback.ResolveGrantContext(context.Background(), grant); e == nil {
		t.Fatal("receiver outlived session")
	}
}

func TestAirPlayReceiverBurnRefusalNeverPublishesAnUncaptionedAlternative(t *testing.T) {
	f, track := v1BurnInFixture(t)
	var local playbackv1.SessionView
	f.call("POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": "airplay-refusal-start"}, startBody(f.items[0], map[string]any{"subtitles": map[string]any{"trackId": track, "delivery": "embeddedClient"}}), 201, &local)
	if _, err := f.db.Exec(`UPDATE playback_owner_policy SET transcoding_enabled=0`); err != nil {
		t.Fatal(err)
	}
	w := f.raw("POST", "/v1/playback/sessions/"+local.ID+"/receivers/airplay", f.owner.AccessToken, nil, playbackv1.AirPlayPrepare{Generation: local.Presentation.Generation, ChoiceRevision: local.Presentation.ChoiceRevision})
	if w.Code < 400 {
		t.Fatalf("refused burn advertised receiver: %s", w.Body.String())
	}
	var count int
	if err := f.db.QueryRow(`SELECT count(*) FROM playback_airplay_presentations`).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	var after playbackv1.SessionView
	f.call("GET", "/v1/playback/sessions/"+local.ID, nil, nil, 200, &after)
	before, _ := json.Marshal(local.Presentation)
	now, _ := json.Marshal(after.Presentation)
	if string(before) != string(now) {
		t.Fatal("receiver refusal changed local captions")
	}
}

func TestAirPlayReceiverASSAsPlainTextDoesNotBurnWhenConversionIsDisabled(t *testing.T) {
	f, track := v1BurnInFixture(t)
	airPlayKnownStreams(t, f)
	if _, err := f.db.Exec(`UPDATE playback_owner_policy SET transcoding_enabled=0`); err != nil {
		t.Fatal(err)
	}
	var local playbackv1.SessionView
	f.call("POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": "airplay-ass-words-start"}, startBody(f.items[0], map[string]any{"subtitles": map[string]any{"trackId": track, "delivery": "sidecar"}}), 201, &local)
	var prepared playbackv1.AirPlayPresentation
	f.call("POST", "/v1/playback/sessions/"+local.ID+"/receivers/airplay", nil, playbackv1.AirPlayPrepare{Generation: local.Presentation.Generation, ChoiceRevision: local.Presentation.ChoiceRevision}, 200, &prepared)
	if prepared.Presentation.Subtitle == nil || prepared.Presentation.Subtitle.TrackID != track || prepared.Presentation.Subtitle.Delivery != "sidecar" {
		t.Fatal(prepared)
	}
	var media string
	if err := f.db.QueryRow(`SELECT media_session_id FROM playback_airplay_presentations WHERE session_id=?`, local.ID).Scan(&media); err != nil {
		t.Fatal(err)
	}
	plan, err := f.v1.Playback.SessionPlan(context.Background(), media)
	if err != nil || plan == nil || plan.BurnIn != nil || plan.VideoAction != "copy" || !plan.AirPlay {
		t.Fatal("plain ASS entered burn/conversion planning", plan, err)
	}
	principal, err := f.id.Authenticate(f.owner.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	selected, err := f.v1.Subtitles.Plan(context.Background(), principal, f.items[0], media)
	if err != nil || selected.Renderer != "external_text" || selected.Selected == nil || selected.Selected.ID != track {
		t.Fatal(selected, err)
	}
}

func TestAirPlayReceiverRejectsWrongAccountProfileAndDeviceBeforePreparing(t *testing.T) {
	f := newV1Fixture(t, 1)
	var local playbackv1.SessionView
	f.call("POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": "airplay-private-owner"}, startBody(f.items[0], nil), 201, &local)
	if _, err := f.db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('airplay-other','airplay-other',X'00','airplay-other-profile',1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE direct_memberships SET allowed_libraries=? WHERE account_id='airplay-other'`, `["`+f.library+`"]`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`INSERT INTO direct_profiles(id,account_id,name,is_primary) VALUES('airplay-secondary',?,'Secondary',0)`, f.owner.Viewer.AccountID); err != nil {
		t.Fatal(err)
	}
	other := f.deviceFor("airplay-other", "airplay-other-profile", "member", "airplay-other-device")
	profile := f.deviceFor(f.owner.Viewer.AccountID, "airplay-secondary", "member", "airplay-secondary-device")
	device := f.device("airplay-second-owner-device")
	for _, entry := range []struct {
		name, token string
		status      int
	}{{"account", other.AccessToken, 404}, {"profile", profile.AccessToken, 404}, {"device", device.AccessToken, 403}} {
		if w := f.raw("GET", "/v1/me", entry.token, nil, nil); w.Code != 200 {
			t.Fatalf("%s did not have a valid identity: %d", entry.name, w.Code)
		}
		w := f.raw("POST", "/v1/playback/sessions/"+local.ID+"/receivers/airplay", entry.token, nil, playbackv1.AirPlayPrepare{Generation: local.Presentation.Generation, ChoiceRevision: local.Presentation.ChoiceRevision})
		if w.Code != entry.status {
			t.Fatalf("%s: %d %s", entry.name, w.Code, w.Body.String())
		}
	}
	var count int
	if err := f.db.QueryRow(`SELECT count(*) FROM playback_airplay_presentations`).Scan(&count); err != nil || count != 0 {
		t.Fatal("an unrelated principal prepared a private receiver", count, err)
	}
}

func TestAirPlayReceiverRefusesHiddenTitleAndAbsentSession(t *testing.T) {
	airPlayRestrictionCheck(t)
}

// The restriction matrix calls this real route probe rather than exempting the
// opaque session handle: a title can become hidden after its session was issued.
func airPlayRestrictionCheck(t *testing.T) {
	f := newV1Fixture(t, 1)
	var local playbackv1.SessionView
	f.call("POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": "airplay-restricted-title"}, startBody(f.items[0], nil), 201, &local)
	var before int
	if err := f.db.QueryRow(`SELECT count(*) FROM playback_sessions`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`INSERT INTO profile_restrictions(profile_id,maximum_age,allow_unrated,revision) VALUES(?,12,0,2) ON CONFLICT(profile_id) DO UPDATE SET maximum_age=12,allow_unrated=0,revision=revision+1`, f.owner.Viewer.ProfileID); err != nil {
		t.Fatal(err)
	}
	f.handler = New(f.deps)
	input := playbackv1.AirPlayPrepare{Generation: local.Presentation.Generation, ChoiceRevision: local.Presentation.ChoiceRevision, PositionMs: 17000}
	hidden := f.raw("POST", "/v1/playback/sessions/"+local.ID+"/receivers/airplay", f.owner.AccessToken, nil, input)
	absent := f.raw("POST", "/v1/playback/sessions/"+local.ID+"-absent/receivers/airplay", f.owner.AccessToken, nil, input)
	if hidden.Code != 404 || absent.Code != hidden.Code || bodyErrorCode(hidden.Body.String()) != bodyErrorCode(absent.Body.String()) {
		t.Fatalf("hidden session/title differs from absent: %d %s / %d %s", hidden.Code, hidden.Body.String(), absent.Code, absent.Body.String())
	}
	if strings.Contains(hidden.Body.String(), f.items[0]) || strings.Contains(hidden.Body.String(), local.ID) {
		t.Fatal("refusal disclosed a hidden title or session")
	}
	var receivers, after int
	if err := f.db.QueryRow(`SELECT count(*) FROM playback_airplay_presentations`).Scan(&receivers); err != nil || receivers != 0 {
		t.Fatal("refused hidden title created a receiver", receivers, err)
	}
	if err := f.db.QueryRow(`SELECT count(*) FROM playback_sessions`).Scan(&after); err != nil || after != before {
		t.Fatal("refused hidden title created a media grant", before, after, err)
	}
}

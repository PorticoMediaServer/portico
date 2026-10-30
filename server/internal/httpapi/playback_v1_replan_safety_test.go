package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/playbackv1"
)

func TestPlaybackV1RejectedReplanKeepsWorkingMediaAndProgress(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, failure := range []string{"invalid audio", "revision race", "write failure"} {
		t.Run(failure, func(t *testing.T) {
			f := replanFixtureV1(t, 1)
			filmStreams(t, f, 0, 6_000_000)
			var initial playbackv1.SessionView
			f.call("POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": "replan-safe-start-01"}, startBody(f.items[0], nil), 201, &initial)
			var media, writer string
			if err := f.db.QueryRow(`SELECT media_session_id FROM playback_v1_sessions WHERE id=?`, initial.ID).Scan(&media); err != nil {
				t.Fatal(err)
			}
			if err := f.db.QueryRow(`SELECT playback_id FROM progress WHERE item_id=?`, f.records[0].ID).Scan(&writer); err != nil {
				t.Fatal(err)
			}
			body := map[string]any{"audio": map[string]any{"trackId": "a2"}}
			switch failure {
			case "invalid audio":
				body["audio"] = map[string]any{"trackId": "a99"}
			case "revision race":
				authority := f.deps.Playback.AuthorityTx
				f.deps.Playback.AuthorityTx = func(ctx context.Context, tx *sql.Tx, p identity.Principal, item string) (identity.Principal, error) {
					p, err := authority(ctx, tx, p, item)
					if err == nil {
						_, err = tx.ExecContext(ctx, `UPDATE playback_v1_sessions SET revision=revision+1 WHERE id=?`, initial.ID)
					}
					return p, err
				}
			case "write failure":
				if _, err := f.db.Exec(`CREATE TRIGGER refuse_replan BEFORE UPDATE OF media_session_id ON playback_v1_sessions BEGIN SELECT RAISE(ABORT,'write failed'); END`); err != nil {
					t.Fatal(err)
				}
			}
			w := f.raw("PATCH", "/v1/playback/sessions/"+initial.ID, f.owner.AccessToken, map[string]string{"If-Match": initial.Revision}, body)
			if w.Code < 400 {
				t.Fatalf("change should fail: %d %s", w.Code, w.Body.String())
			}
			var state, current, afterWriter string
			if err := f.db.QueryRow(`SELECT m.state,v.media_session_id FROM playback_v1_sessions v JOIN playback_sessions m ON m.id=? WHERE v.id=?`, media, initial.ID).Scan(&state, &current); err != nil {
				t.Fatal(err)
			}
			if err := f.db.QueryRow(`SELECT playback_id FROM progress WHERE item_id=?`, f.records[0].ID).Scan(&afterWriter); err != nil {
				t.Fatal(err)
			}
			if state == "stopped" || current != media || afterWriter != writer {
				t.Fatalf("refused change disturbed playback: state=%s media=%s writer=%s (was %s)", state, current, afterWriter, writer)
			}
			var active int
			if err := f.db.QueryRow(`SELECT COUNT(*) FROM playback_sessions WHERE state NOT IN ('stopped','ended','failed')`).Scan(&active); err != nil || active != 1 {
				t.Fatalf("orphan presentations: %d %v", active, err)
			}
		})
	}
}

func TestPlaybackV1ReadAndReplayRecheckVisibility(t *testing.T) {
	f := newV1Fixture(t, 1)
	var initial playbackv1.SessionView
	headers := map[string]string{"Idempotency-Key": "read-fence-start-001"}
	body := startBody(f.items[0], nil)
	f.call("POST", "/v1/playback/sessions", headers, body, 201, &initial)
	if _, err := f.db.Exec(`INSERT INTO profile_restrictions(profile_id,maximum_age,allow_unrated,revision) VALUES(?,12,0,2) ON CONFLICT(profile_id) DO UPDATE SET maximum_age=12,allow_unrated=0,revision=revision+1`, f.owner.Viewer.ProfileID); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"GET", "POST"} {
		path, requestHeaders, requestBody := "/v1/playback/sessions/"+initial.ID, map[string]string(nil), any(nil)
		if method == "POST" {
			path, requestHeaders, requestBody = "/v1/playback/sessions", headers, body
		}
		if w := f.raw(method, path, f.owner.AccessToken, requestHeaders, requestBody); w.Code != 404 {
			t.Errorf("%s leaked restricted session: %d %s", method, w.Code, w.Body.String())
		}
	}
}

func TestPlaybackV1ReplanRecordsCurrentLocality(t *testing.T) {
	f := replanFixtureV1(t, 1)
	// The viewer's home network is declared; 192.168.1.20 below is a home
	// request on any host, while 203.0.113.9 stays remote.
	v1HomeNetworks(t, f, "192.168.1.0/24")
	filmStreams(t, f, 0, 6_000_000)
	var initial playbackv1.SessionView
	w := f.rawFrom("192.168.1.20:5000", "POST", "/v1/playback/sessions", f.owner.AccessToken, map[string]string{"Idempotency-Key": "locality-replan-001"}, startBody(f.items[0], nil))
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &initial) != nil {
		t.Fatalf("start: %d %s", w.Code, w.Body.String())
	}
	for _, tc := range []struct{ peer, location, track string }{{"203.0.113.9:5000", "remote", "a2"}, {"192.168.1.20:5000", "local", "a1"}} {
		w := f.rawFrom(tc.peer, "PATCH", "/v1/playback/sessions/"+initial.ID, f.owner.AccessToken, map[string]string{"If-Match": initial.Revision}, map[string]any{"audio": map[string]any{"trackId": tc.track}})
		if w.Code != 200 {
			t.Fatalf("change: %d %s", w.Code, w.Body.String())
		}
		var location string
		if err := f.db.QueryRow(`SELECT location FROM playback_v1_sessions WHERE id=?`, initial.ID).Scan(&location); err != nil {
			t.Fatal(err)
		}
		if location != tc.location {
			t.Errorf("locality=%s want %s", location, tc.location)
		}
		if err := json.Unmarshal(w.Body.Bytes(), &initial); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPlaybackV1OriginalBandwidthCountsTowardUploadBudget(t *testing.T) {
	f := replanFixtureV1(t, 1)
	filmStreams(t, f, 0, 6_000_000)
	w := f.rawFrom("203.0.113.9:5000", "POST", "/v1/playback/sessions", f.owner.AccessToken, map[string]string{"Idempotency-Key": "upload-original-001"}, startBody(f.items[0], nil))
	var session playbackv1.SessionView
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &session) != nil {
		t.Fatalf("start: %d %s", w.Code, w.Body.String())
	}
	if session.Presentation.BitrateKbps != 6000 {
		t.Errorf("original rate=%d want 6000", session.Presentation.BitrateKbps)
	}
	tx, err := f.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	ceiling, _, err := uploadBudgetCeiling(context.Background(), tx, 20000, "", time.Now())
	if err != nil || ceiling != 9_808_000 {
		t.Fatalf("subsequent video budget=%d want 9808000: %v", ceiling, err)
	}
}

func TestPlaybackV1ControllerKeepsPlayingDeviceEngineAndLocality(t *testing.T) {
	f := replanFixtureV1(t, 1)
	filmStreams(t, f, 0, 6_000_000)
	caps := webCapabilities()
	native := caps["engines"].(map[string]any)["native"].(map[string]any)
	mpv := map[string]any{}
	for k, v := range native {
		mpv[k] = v
	}
	mpv["name"] = "mpv"
	caps["engines"].(map[string]any)["mpv"] = mpv
	caps["engineSetting"] = "mpv"
	f.call("PUT", "/v1/me/devices/current/capabilities", nil, caps, 204, nil)
	var initial playbackv1.SessionView
	w := f.rawFrom("203.0.113.9:5000", "POST", "/v1/playback/sessions", f.owner.AccessToken, map[string]string{"Idempotency-Key": "control-engine-001"}, startBody(f.items[0], nil))
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &initial) != nil {
		t.Fatalf("start: %d %s", w.Code, w.Body.String())
	}
	if initial.Presentation.Engine != "mpv" {
		t.Fatalf("initial engine %s", initial.Presentation.Engine)
	}
	controller := f.device("controller-device")
	w = f.rawFrom("192.168.1.20:5000", "PATCH", "/v1/playback/sessions/"+initial.ID, controller.AccessToken, map[string]string{"If-Match": initial.Revision}, map[string]any{"audio": map[string]any{"trackId": "a2"}})
	var changed playbackv1.SessionView
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &changed) != nil {
		t.Fatalf("controller change: %d %s", w.Code, w.Body.String())
	}
	if changed.Presentation.Engine != "mpv" {
		t.Errorf("controller changed playing engine to %s", changed.Presentation.Engine)
	}
	var location string
	if err := f.db.QueryRow(`SELECT location FROM playback_v1_sessions WHERE id=?`, initial.ID).Scan(&location); err != nil {
		t.Fatal(err)
	}
	if location != "remote" {
		t.Errorf("controller changed playing locality to %s", location)
	}
}

func TestPlaybackV1RejectedReplacementStartKeepsOldSession(t *testing.T) {
	for _, failure := range []string{"invalid audio", "readiness", "write failure"} {
		t.Run(failure, func(t *testing.T) {
			f := replanFixtureV1(t, 1)
			filmStreams(t, f, 0, 6_000_000)
			var initial playbackv1.SessionView
			f.call("POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": "replacement-start-01"}, startBody(f.items[0], nil), 201, &initial)
			body := startBody(f.items[0], map[string]any{"replacesSessionId": initial.ID})
			switch failure {
			case "invalid audio":
				body["audio"] = map[string]any{"trackId": "a99"}
			case "readiness":
				f.v1.ReadyCheck = func(context.Context, playback.Session) (bool, error) { return false, errors.New("readiness failed") }
			case "write failure":
				if _, err := f.db.Exec(`CREATE TRIGGER refuse_start BEFORE INSERT ON playback_v1_sessions BEGIN SELECT RAISE(ABORT,'write failed'); END`); err != nil {
					t.Fatal(err)
				}
			}
			w := f.raw("POST", "/v1/playback/sessions", f.owner.AccessToken, map[string]string{"Idempotency-Key": "replacement-start-02"}, body)
			if w.Code < 400 {
				t.Fatalf("replacement should fail: %d %s", w.Code, w.Body.String())
			}
			var mediaState, sessionState, media, writer string
			if err := f.db.QueryRow(`SELECT m.state,v.state,m.id FROM playback_v1_sessions v JOIN playback_sessions m ON m.id=v.media_session_id WHERE v.id=?`, initial.ID).Scan(&mediaState, &sessionState, &media); err != nil {
				t.Fatal(err)
			}
			if err := f.db.QueryRow(`SELECT playback_id FROM progress WHERE item_id=?`, f.records[0].ID).Scan(&writer); err != nil {
				t.Fatal(err)
			}
			if mediaState == "stopped" || sessionState != "playing" || media != writer {
				t.Fatalf("refused start disturbed old session: media=%s session=%s writer=%s", mediaState, sessionState, writer)
			}
		})
	}
}

type playbackReviewSettings struct {
	cfg playback.DeliveryConfiguration
}

func (s playbackReviewSettings) Delivery() playback.DeliveryConfiguration { return s.cfg }

func TestPlaybackV1RejectedNewStartKeepsExistingProgressWriter(t *testing.T) {
	f := replanFixtureV1(t, 1)
	filmStreams(t, f, 0, 6_000_000)
	var initial playbackv1.SessionView
	f.call("POST", "/v1/playback/sessions", map[string]string{"Idempotency-Key": "progress-writer-001"}, startBody(f.items[0], nil), 201, &initial)
	var media string
	if err := f.db.QueryRow(`SELECT media_session_id FROM playback_v1_sessions WHERE id=?`, initial.ID).Scan(&media); err != nil {
		t.Fatal(err)
	}
	w := f.raw("POST", "/v1/playback/sessions", f.owner.AccessToken, map[string]string{"Idempotency-Key": "progress-writer-002"}, startBody(f.items[0], map[string]any{"audio": map[string]any{"trackId": "a99"}}))
	if w.Code != 400 {
		t.Fatalf("invalid audio start: %d %s", w.Code, w.Body.String())
	}
	var writer string
	if err := f.db.QueryRow(`SELECT playback_id FROM progress WHERE item_id=?`, f.records[0].ID).Scan(&writer); err != nil {
		t.Fatal(err)
	}
	if writer != media {
		t.Fatalf("rejected start took progress authority: %s want %s", writer, media)
	}
}

func TestPlaybackV1PreparedProgressActivatesOnlyAtCommit(t *testing.T) {
	f, _ := renderingFixture(t, 2)
	q := playingQueue(t, f, "prepared-writer-001")
	prepared := prepareNext(t, f, q, "prepared-writer-002")
	var count int
	if err := f.db.QueryRow(`SELECT count(*) FROM progress WHERE item_id=?`, f.records[1].ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("preparation claimed personal progress")
	}
	var committed playbackv1.QueueReply
	f.call("POST", "/v1/queues/"+q.Queue.ID+":commit-next", nil, map[string]any{"token": prepared.Token}, 200, &committed)
	var media, writer string
	if err := f.db.QueryRow(`SELECT media_session_id FROM playback_v1_sessions WHERE id=?`, committed.Session.ID).Scan(&media); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRow(`SELECT playback_id FROM progress WHERE item_id=?`, f.records[1].ID).Scan(&writer); err != nil || writer != media {
		t.Fatalf("committed audio has no progress authority: writer=%s media=%s error=%v", writer, media, err)
	}
	var firstOrdinal, nextOrdinal int64
	if err := f.db.QueryRow(`SELECT accepted_ordinal FROM playback_personal_claims WHERE playback_id=(SELECT media_session_id FROM playback_v1_sessions WHERE id=?)`, q.Session.ID).Scan(&firstOrdinal); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRow(`SELECT accepted_ordinal FROM playback_personal_claims WHERE playback_id=?`, media).Scan(&nextOrdinal); err != nil {
		t.Fatal(err)
	}
	if nextOrdinal != firstOrdinal {
		t.Fatalf("automatic next got a new personal intent: %d want %d", nextOrdinal, firstOrdinal)
	}
}

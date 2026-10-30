package httpapi

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestConditionalStopRegisteredRouteRejectsPresentInvalidWithoutFallback(t *testing.T) {
	f, bearer, _, _, session := castOccurrenceFixture(t)
	for _, body := range []string{`{"expectedGeneration":null}`, `{"expectedGeneration":0}`, `{"expectedGeneration":-1}`, `{"expectedGeneration":1.5}`, `{"expectedGeneration":"1"}`, `{"expectedGeneration":9007199254740992}`, `{"expectedGeneration":1,"expectedGeneration":1}`, `{"expectedGeneration":1,"positionMs":5}`, `{"ExpectedGeneration":1}`} {
		r := httptest.NewRequest("DELETE", "/v1/playback/sessions/"+session.ID, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+bearer)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		f.h.ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatalf("invalid conditional %s: %d %s", body, w.Code, w.Body.String())
		}
		var ended int64
		if err := f.db.QueryRow(`SELECT ended_ms FROM playback_v1_sessions WHERE id=?`, session.ID).Scan(&ended); err != nil || ended != 0 {
			t.Fatalf("invalid request stopped resource: %v ended=%d", err, ended)
		}
	}
	if w := f.as(bearer, "DELETE", "/v1/playback/sessions/"+session.ID, map[string]any{"expectedGeneration": session.Presentation.Generation + 1}); w.Code != 412 {
		t.Fatalf("stale generation: %d %s", w.Code, w.Body.String())
	}
	f.callAs(bearer, "DELETE", "/v1/playback/sessions/"+session.ID, nil, map[string]any{"expectedGeneration": session.Presentation.Generation}, 204, nil)
	f.callAs(bearer, "DELETE", "/v1/playback/sessions/"+session.ID, nil, map[string]any{"expectedGeneration": session.Presentation.Generation}, 204, nil)
}

func TestConditionalStopOriginalDeviceAndOrdinaryOmission(t *testing.T) {
	f, bearer, _, _, session := castOccurrenceFixture(t)
	if w := f.as(f.token, "DELETE", "/v1/playback/sessions/"+session.ID, map[string]any{"expectedGeneration": session.Presentation.Generation}); w.Code == 204 {
		t.Fatal("same-profile other device stopped original")
	}
	f.callAs(bearer, "DELETE", "/v1/playback/sessions/"+session.ID, nil, nil, 204, nil)
	var ended int64
	if err := f.db.QueryRow(`SELECT ended_ms FROM playback_v1_sessions WHERE id=?`, session.ID).Scan(&ended); err != nil || ended == 0 {
		t.Fatal(fmt.Sprint("ordinary omission did not stop: ", err))
	}
}

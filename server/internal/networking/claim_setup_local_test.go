package networking

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSetupClaimOnLocalServerRequiresConfirmedMove(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, confirmed := range []bool{false, true} {
		t.Run(map[bool]string{false: "no move", true: "confirmed move"}[confirmed], func(t *testing.T) {
			f, h, tr := handlerFixture(t)
			if _, err := f.db.Exec(`INSERT INTO identity_state(singleton,sign_in_kind) VALUES(1,'local')`); err != nil {
				t.Fatal(err)
			}
			if confirmed {
				if _, err := f.db.Exec(`INSERT INTO identity_moves(id,owner_id,owner_revision,from_kind,to_kind,start_over,stage,revision,people,devices,profiles,server_name,created_ms,owner_confirmed) VALUES('confirmed-move','local_owner',1,'local','portico',0,'preview',1,1,1,1,'Fixture',1,1)`); err != nil {
					t.Fatal(err)
				}
			}
			before, code := handlerCall(t, h, http.MethodGet, "/v1/networking/claim", nil)
			if code != 200 {
				t.Fatal(code)
			}
			for _, purpose := range []string{"", "setup"} {
				raw, err := json.Marshal(ClaimPrepareRequest{AccountID: f.binding.AccountID, Expected: before.Identity, Purpose: purpose})
				if err != nil {
					t.Fatal(err)
				}
				r := httptest.NewRequest(http.MethodPost, "/v1/networking/claim/prepare", bytes.NewReader(raw))
				r.Header.Set("Authorization", "Bearer current")
				r.Header.Set("Origin", "https://web.example")
				r.Header.Set("Content-Type", "application/json")
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				var fault struct {
					Error struct {
						Code      string `json:"code"`
						Retryable bool   `json:"retryable"`
					} `json:"error"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &fault); err != nil {
					t.Fatal(err)
				}
				if w.Code != http.StatusConflict || fault.Error.Code != "claim_move_required" || fault.Error.Retryable {
					t.Fatalf("setup on Local must refuse before approval: HTTP %d %s", w.Code, w.Body.String())
				}
			}
			var intents int
			if err := f.db.QueryRow(`SELECT count(*) FROM networking_claim_intents`).Scan(&intents); err != nil || intents != 0 {
				t.Fatal("setup refusal persisted an intent", intents, err)
			}
			after, code := handlerCall(t, h, http.MethodGet, "/v1/networking/claim", nil)
			if code != 200 || after.Operation != nil || after.State != before.State || tr.finalizes != 0 || tr.retrieves != 0 {
				t.Fatal("refusal changed claim state or contacted Hosted", code, after)
			}
		})
	}
}

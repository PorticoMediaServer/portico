package httpapi

import (
	"bytes"
	"encoding/json"
	"golang.org/x/crypto/bcrypt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func TestRefreshRoundTripOverPlainHTTP(t *testing.T) {
	state := t.TempDir()
	db, err := persistence.Open(filepath.Join(state, "server.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ident, err := identity.New(db, state)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO identity_state(singleton,sign_in_kind) VALUES(1,'local')`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('account','owner',X'00','profile',1)`); err != nil {
		t.Fatal(err)
	}
	first, err := ident.Issue("account", "profile", "local", "owner", 1)
	if err != nil {
		t.Fatal(err)
	}
	var installation string
	if err = db.QueryRow(`SELECT installation_id FROM identity_devices WHERE id=?`, first.DeviceID).Scan(&installation); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(New(Dependencies{DB: db, Identity: ident, Catalog: catalog.New(db)}))
	defer server.Close()
	refresh := first.RefreshToken
	for range 2 {
		payload, _ := json.Marshal(map[string]string{"refreshToken": refresh, "installationId": installation, "requestId": identity.Token()})
		response, err := server.Client().Post(server.URL+"/v1/auth/refresh", "application/json", bytes.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}
		var out identity.Envelope
		decodeErr := json.NewDecoder(response.Body).Decode(&out)
		response.Body.Close()
		if response.StatusCode != http.StatusOK || decodeErr != nil || out.RefreshToken == "" || out.RefreshToken == refresh || out.DeviceID != first.DeviceID {
			t.Fatalf("plain HTTP refresh failed: status=%d envelope=%+v decode=%v", response.StatusCode, out, decodeErr)
		}
		refresh = out.RefreshToken
	}
}

func TestUnknownAndKnownNamesHaveTheSameSignInCooloff(t *testing.T) {
	d, owner := completedLocalFoundationFixture(t)
	db, h := d.DB, New(d)
	var knownCode, knownMessage string
	for _, name := range []string{"owner", "unlisted"} {
		for n := 0; n < 6; n++ {
			if n == 5 {
				// The fifth evaluated guess must genuinely establish the policy's
				// one-second backoff. Then keep this HTTP-admission fixture locked across
				// a wall-clock second or shared-runner scheduling gap; expiry is tested
				// separately by the identity backoff tests.
				table, predicate, args, buckets := "identity_credential_attempts", "account_id=?", []any{owner.Viewer.AccountID}, 1
				if name == "unlisted" {
					table, predicate, args, buckets = "identity_unknown_attempts", "1=1", nil, 2
				}
				var count, minAttempts, maxAttempts, minDelay, maxDelay int
				query := "SELECT count(*),MIN(failed_attempts),MAX(failed_attempts),MIN(locked_until-last_failure),MAX(locked_until-last_failure) FROM " + table + " WHERE " + predicate
				if err := db.QueryRow(query, args...).Scan(&count, &minAttempts, &maxAttempts, &minDelay, &maxDelay); err != nil || count != buckets || minAttempts != 5 || maxAttempts != 5 || minDelay != 1 || maxDelay != 1 {
					t.Fatalf("%s: fifth guess did not establish the real standard cool-off", name)
				}
				values := append([]any{time.Now().Unix() + 30}, args...)
				if _, err := db.Exec("UPDATE "+table+" SET locked_until=? WHERE "+predicate, values...); err != nil {
					t.Fatal(err)
				}
			}
			body, _ := json.Marshal(map[string]string{"username": name, "password": "wrong"})
			request := httptest.NewRequest("POST", "/v1/direct/sign-in", bytes.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, request)
			want := http.StatusUnauthorized
			if n == 5 {
				want = http.StatusTooManyRequests
				retry, err := strconv.ParseInt(w.Header().Get("Retry-After"), 10, 64)
				if err != nil || retry < 1 || retry > 60 {
					t.Fatalf("%s: cool-off has no bounded Retry-After", name)
				}
				var result struct {
					Error struct {
						Code    string `json:"code"`
						Message string `json:"message"`
					} `json:"error"`
				}
				if json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Error.Code != "account_locked" || result.Error.Message == "" {
					t.Fatalf("%s: cool-off response changed", name)
				}
				if name == "owner" {
					knownCode, knownMessage = result.Error.Code, result.Error.Message
				} else if result.Error.Code != knownCode || result.Error.Message != knownMessage {
					t.Fatal("unknown name disclosed a different cool-off response")
				}
			} else if w.Header().Get("Retry-After") != "" {
				t.Fatalf("%s: early guess received a different cool-off progression", name)
			}
			if w.Code != want {
				t.Fatalf("%s attempt %d: %d %s", name, n+1, w.Code, w.Body.String())
			}
		}
	}
}

func TestSignInWithoutInstallationHeaderReturnsRefreshBinding(t *testing.T) {
	state := t.TempDir()
	db, err := persistence.Open(filepath.Join(state, "server.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ident, err := identity.New(db, state)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO identity_state(singleton,sign_in_kind) VALUES(1,'local')`); err != nil {
		t.Fatal(err)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte("Testing1!"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id,epoch) VALUES('account','owner',?,'profile',1)`, hash); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(New(Dependencies{DB: db, Identity: ident, Catalog: catalog.New(db)}))
	defer server.Close()
	response, err := server.Client().Post(server.URL+"/v1/direct/sign-in", "application/json", bytes.NewBufferString(`{"username":"owner","password":"Testing1!"}`))
	if err != nil {
		t.Fatal(err)
	}
	raw, readErr := io.ReadAll(response.Body)
	response.Body.Close()
	var signed identity.DirectSignIn
	decodeErr := json.Unmarshal(raw, &signed)
	if response.StatusCode != 200 || decodeErr != nil || signed.InstallationID == "" || signed.Session == nil || signed.Session.RefreshToken == "" {
		t.Fatalf("anonymous sign-in did not return its installation binding: status=%d body=%+v decode=%v read=%v", response.StatusCode, signed, decodeErr, readErr)
	}
	if bytes.Contains(raw, []byte(`"accountToken"`)) || signed.Session.AccessToken == "" {
		t.Fatalf("sign-in exposed a separate account token: %s", raw)
	}
	request := func(method, path, body string, want int) []byte {
		t.Helper()
		r, e := http.NewRequest(method, server.URL+path, bytes.NewBufferString(body))
		if e != nil {
			t.Fatal(e)
		}
		r.Header.Set("Authorization", "Bearer "+signed.Session.AccessToken)
		r.Header.Set("Content-Type", "application/json")
		response, e := server.Client().Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer response.Body.Close()
		data, _ := io.ReadAll(response.Body)
		if response.StatusCode != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, response.StatusCode, want, data)
		}
		return data
	}
	var snapshot identity.DirectSnapshot
	if err := json.Unmarshal(request("POST", "/v1/direct/profiles", `{"name":"Child","art":"mint"}`, 201), &snapshot); err != nil {
		t.Fatal(err)
	}
	var child identity.DirectProfile
	for _, profile := range snapshot.Profiles {
		if !profile.Primary {
			child = profile
		}
	}
	if child.ID == "" {
		t.Fatal("profile creation did not return child")
	}
	request("PATCH", "/v1/direct/profiles/"+child.ID, `{"expectedRevision":`+itoa(child.Revision)+`,"pin":"1234"}`, 200)
	request("PUT", "/v1/direct/profiles/"+child.ID+"/restrictions", `{"expectedRevision":1,"ratingSystem":"mpaa","maximumAgeRating":"PG","allowUnrated":true}`, 200)
	request("POST", "/v1/direct/profiles/"+child.ID+"/pin-reset", `{"pin":""}`, 200)
	request("PATCH", "/v1/devices/"+signed.DeviceID, `{"name":"Living Room"}`, 200)
	wrong := request("POST", "/v1/direct/password", `{"newPassword":"Changed-password1!"}`, 403)
	if !bytes.Contains(wrong, []byte(`"code":"current_password_incorrect"`)) {
		t.Fatalf("wrong current password looked like an expired session: %s", wrong)
	}
	wrong = request("POST", "/v1/direct/two-factor/enrol", `{"password":"wrong"}`, 403)
	if !bytes.Contains(wrong, []byte(`"code":"current_password_incorrect"`)) {
		t.Fatalf("wrong factor-change password looked like an expired session: %s", wrong)
	}
	payload, _ := json.Marshal(map[string]string{"refreshToken": signed.Session.RefreshToken, "installationId": signed.InstallationID, "requestId": identity.Token()})
	response, err = server.Client().Post(server.URL+"/v1/auth/refresh", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	var refreshed identity.Envelope
	if err = json.NewDecoder(response.Body).Decode(&refreshed); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 || refreshed.AccessToken == "" {
		t.Fatalf("returned installation did not refresh over HTTP: %d", response.StatusCode)
	}
	signed.Session.AccessToken = refreshed.AccessToken
	request("POST", "/v1/direct/sessions/sign-out-everywhere", "", 204)
	request("GET", "/v1/direct", "", 401)
}

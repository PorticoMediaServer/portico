package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"github.com/PorticoMediaServer/portico/server/internal/servicelog"
	"strings"
	"testing"
)

func TestLiveConfigurationOwnerOnlyAndNeverEchoesCredentials(t *testing.T) {
	f := administrationFixtureNew(t)
	store, err := livechannels.New(f.deps.DB)
	if err != nil {
		t.Fatal(err)
	}
	auth := func(context.Context, *sql.Tx, bool) (string, func(string, string) bool, error) {
		return "owner", func(string, string) bool { return true }, nil
	}
	id := strings.Repeat("ab", 24)
	_, err = store.Save(context.Background(), auth, livechannels.SourceInput{ID: id, RequestID: strings.Repeat("cd", 24), Name: "Source", Playlist: "#EXTM3U\n#EXTINF:-1,One\nhttps://provider.invalid/stream\n"})
	if err != nil {
		t.Fatal(err)
	}
	raw := `{"draft":{"id":"` + id + `","kind":"xtream","guideUrl":"","username":"private-user-017","password":"private-pass-017","refreshMs":3600000},"approvals":[]}`
	if _, err = f.deps.DB.Exec(`INSERT INTO live_remote_configs(source_id,sealed,next_due_ms) VALUES(?,?,0)`, id, []byte(raw)); err != nil {
		t.Fatal(err)
	}
	path := "/v1/admin/live-sources/" + id + "/configuration"
	response := f.call("GET", path, f.owner, "")
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	var envelope struct {
		Result map[string]any `json:"result"`
	}
	if err = json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	settings := envelope.Result["settings"].(map[string]any)
	settings["remote"].(map[string]any)["password"] = "replacement-pass-017"
	change := map[string]any{"expectedRevision": envelope.Result["revision"], "operationId": "owner-configuration-write", "settings": settings}
	body, _ := json.Marshal(change)
	for _, token := range []string{f.member, f.admin} {
		for _, method := range []string{"GET", "PUT"} {
			payload := ""
			if method == "PUT" {
				payload = string(body)
			}
			w := f.call(method, path, token, payload)
			if w.Code != 403 {
				t.Fatalf("non-owner %s admitted: %d %s", method, w.Code, w.Body.String())
			}
		}
	}
	written := f.call("PUT", path, f.owner, string(body))
	if written.Code != 200 {
		t.Fatal("owner write refused", written.Code, written.Body.String())
	}
	change["operationId"] = "owner-configuration-stale"
	staleBody, _ := json.Marshal(change)
	stale := f.call("PUT", path, f.owner, string(staleBody))
	if stale.Code != 409 || !strings.Contains(stale.Body.String(), "channel_revision_conflict") {
		t.Fatal("stale write admitted", stale.Code, stale.Body.String())
	}
	logs, err := f.recorder.Read(servicelog.Query{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	logBody, _ := json.Marshal(logs)
	for _, w := range []string{response.Body.String(), written.Body.String(), stale.Body.String(), string(logBody)} {
		for _, secret := range []string{"private-user-017", "private-pass-017", "replacement-pass-017"} {
			if strings.Contains(w, secret) {
				t.Fatal("credential echoed")
			}
		}
	}
	// Readback proves the owner's update was accepted, not merely redacted from an error.
	var stored []byte
	if err = f.deps.DB.QueryRow(`SELECT sealed FROM live_remote_configs WHERE source_id=?`, id).Scan(&stored); err != nil || !strings.Contains(string(stored), "replacement-pass-017") {
		t.Fatal("owner write refused", err)
	}
}

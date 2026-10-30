package httpapi

import (
	"encoding/json"
	librarychannels "github.com/PorticoMediaServer/portico/server/internal/livechannels/library"
	"testing"
)

func TestLibraryChannelConfigWireContract(t *testing.T) {
	d, mux, owner, _ := notificationFixture(t)
	if _, err := d.DB.Exec(`INSERT INTO libraries(id,name,kind,root) VALUES('movies','Movies','movie','/movies'); INSERT OR IGNORE INTO library_revisions(library_id,revision) VALUES('movies',1)`); err != nil {
		t.Fatal(err)
	}
	settleCompactCatalogue(t, d.DB)
	var err error
	d.LibraryChannels, err = librarychannels.New(d.DB)
	if err != nil {
		t.Fatal(err)
	}
	d.libraryChannelRoutes(mux)
	defaults := notificationRequest(mux, "POST", owner.AccessToken, "/v1/admin/library-channels/defaults", `{"timezone":"UTC"}`)
	if defaults.Code != 200 {
		t.Fatalf("defaults %d %s", defaults.Code, defaults.Body.String())
	}
	assertSpecResponse(t, "POST", "/v1/admin/library-channels/defaults", defaults)
	var draft struct {
		Config librarychannels.Config `json:"config"`
	}
	if err = json.Unmarshal(defaults.Body.Bytes(), &draft); err != nil {
		t.Fatal(err)
	}
	draft.Config.Enabled = false
	input := map[string]any{"requestId": "config-contract", "expectedRevision": 0, "config": draft.Config}
	raw, _ := json.Marshal(input)
	// A different client omits blocks and weights. Every answer must still use arrays.
	var wire map[string]any
	json.Unmarshal(raw, &wire)
	cfg := wire["config"].(map[string]any)
	delete(cfg, "blocks")
	delete(cfg["rules"].([]any)[0].(map[string]any), "weights")
	raw, _ = json.Marshal(wire)
	for i := 0; i < 2; i++ {
		saved := notificationRequest(mux, "POST", owner.AccessToken, "/v1/admin/library-channels", string(raw))
		if saved.Code != 200 {
			t.Fatalf("save/replay %d %s", saved.Code, saved.Body.String())
		}
		assertSpecResponse(t, "POST", "/v1/admin/library-channels", saved)
	}
	listed := notificationRequest(mux, "GET", owner.AccessToken, "/v1/admin/library-channels", "")
	if listed.Code != 200 {
		t.Fatalf("list %d %s", listed.Code, listed.Body.String())
	}
	assertSpecResponse(t, "GET", "/v1/admin/library-channels", listed)
}

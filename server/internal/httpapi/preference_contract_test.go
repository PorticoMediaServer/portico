package httpapi

import (
	"encoding/json"
	"github.com/PorticoMediaServer/portico/apikit/contract"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"os"
	"testing"
)

// Reader and presentation fixtures use the active preference document rather
// than borrowing one member of the retired bootstrap aggregate.
func TestCurrentPreferencesContractForReaders(t *testing.T) {
	d, viewer := completedLocalFoundationFixture(t)
	w := logoutHTTPRequest(New(d), "GET", "/v1/preferences?deviceClass=web", viewer.AccessToken)
	var body struct {
		Data operations.PreferenceSnapshot `json:"data"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &body) != nil || body.Data.DeviceClass != "web" || len(body.Data.Registry.Fields) == 0 {
		t.Fatalf("current preferences: %d %s", w.Code, w.Body)
	}
	if os.Getenv("PORTICO_PREFERENCES_CONTRACT_UPDATE") == "1" {
		if err := contract.Record("../../testdata/contracts/get-preferences.json", "GetPreferencesResponse", "GET", "/v1/preferences?deviceClass=web", nil, w, "viewer"); err != nil {
			t.Fatal(err)
		}
	}
}

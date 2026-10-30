package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func TestAuthResponseCarriesRefreshCredentialOnHTTPAndHTTPS(t *testing.T) {
	value := map[string]any{"session": identity.Envelope{AccessToken: "short-access", RefreshToken: "durable-refresh", TokenGeneration: 12}}
	request := httptest.NewRequest("POST", "http://server.test/v1/direct/profiles/p/select", nil)
	for _, secure := range []bool{false, true} {
		if secure {
			request = httptest.NewRequest("POST", "https://server.test/v1/direct/profiles/p/select", nil)
		}
		response := httptest.NewRecorder()
		writeAuth(response, request, 200, value)
		var document map[string]map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &document); err != nil {
			t.Fatal(err)
		}
		if document["session"]["accessToken"] != "short-access" || document["session"]["refreshToken"] != "durable-refresh" || document["session"]["tokenGeneration"] != "12" {
			t.Fatalf("secure=%v credential policy: %s", secure, response.Body.String())
		}
	}
}

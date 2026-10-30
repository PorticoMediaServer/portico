package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func FuzzMCPMessages(f *testing.F) {
	for _, body := range []string{`{"jsonrpc":"2.0","id":1,"method":"ping"}`, `{"jsonrpc":"2.0","id":{},"method":"tools/call","params":{"name":"search","arguments":null}}`, `{"jsonrpc":"2.0","method":"notifications/initialized"}`, `{"jsonrpc":"2.0","id":"x","method":"initialize","params":{"protocolVersion":"2025-06-18"}}`} {
		f.Add([]byte(body))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > 64<<10 {
			return
		}
		var request mcpRequest
		if json.Unmarshal(raw, &request) != nil {
			return
		}
		// No routes or granted scopes: fuzzing can never dispatch a tool action.
		d := Dependencies{}
		answer, wanted := d.mcpHandle(httptest.NewRequest("POST", mcpPath, nil), identity.APICredential{}, identity.TierMember, request)
		if wanted {
			if _, err := json.Marshal(answer); err != nil {
				t.Fatalf("invalid JSON-RPC answer: %v", err)
			}
		}
	})
}

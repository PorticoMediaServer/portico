package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/remotemedia"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

func TestRemoteSourceCapacityIsNotProviderThrottle(t *testing.T) {
	for _, err := range []error{storage.ErrBusy, remotemedia.ErrBusy, fmt.Errorf("owned context: %w", storage.ErrBusy)} {
		response := httptest.NewRecorder()
		remoteSourceFailure(response, err)
		var body struct {
			Error struct {
				Code      string
				Retryable bool
			}
		}
		if e := json.Unmarshal(response.Body.Bytes(), &body); e != nil {
			t.Fatal(e)
		}
		if response.Code != 503 || body.Error.Code != "remote_busy" || !body.Error.Retryable || response.Header().Get("Retry-After") != "" {
			t.Fatal("local capacity became provider status or deadline", response.Code, body, response.Header())
		}
	}
}

package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/downloads"
)

func TestDownloadOptionsMatrixPreservesIDInsideActualDevice(t *testing.T) {
	// The same 32-byte/base64url shape used by identity.Token, deterministically
	// containing the matrix placeholder inside a legitimate device identifier.
	raw := make([]byte, 32)
	raw[1], raw[2] = 2, 3
	device := base64.RawURLEncoding.EncodeToString(raw)
	if !strings.HasPrefix(device, "AAID") {
		t.Fatal("fixture lost its ID token")
	}
	m := &restrictionEnv{device: device, hiddenEntities: map[string][]string{
		"show": {"hidden-show"}, "season": {"hidden-season"}, "album": {"hidden-album"}, "book": {"hidden-book"},
	}}
	probes := restrictionWriteRoutes()["POST /v1/downloads/options"].probes(m)
	if len(probes) != 4 {
		t.Fatalf("missing container restriction probes: %d", len(probes))
	}
	for _, p := range probes {
		for _, probe := range []idProbe{p, p.with(absentTwin(p.hidden))} {
			var body downloads.ContainerOptionsRequest
			if err := json.Unmarshal([]byte(probe.body), &body); err != nil {
				t.Fatal(err)
			}
			if body.DeviceID != device {
				t.Errorf("%s: target substitution changed actual device", body.Target.Kind)
			}
			if body.Target.ID != probe.hidden || body.Policy.Episodes != "all" || probe.path != "/v1/downloads/options" || probe.method != "POST" {
				t.Fatalf("hidden/absent probe lost its target or policy: %+v", body)
			}
		}
	}
}

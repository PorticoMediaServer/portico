package httpapi

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime/debug"
	"testing"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/apikit/contract"
)

// The concurrency diagnostic must not publish Go's unbounded memory sentinel as
// a wide JSON number. Exercise the actual owner route with an unbounded limit,
// a representable 1 GiB limit and a finite limit beyond the shared safe-integer
// bound, restoring the process setting afterwards.
func TestConcurrencyDiagnosticsMemoryLimitWire(t *testing.T) {
	d, session, _ := hostedAdministrationFixture(t, "owner")
	mux := http.NewServeMux()
	d.foundationRoutes(mux)
	d.concurrencyRoutes(mux)
	original := debug.SetMemoryLimit(-1)
	t.Cleanup(func() { debug.SetMemoryLimit(original) })
	recordingDir := os.Getenv("PORTICO_CONCURRENCY_DIAGNOSTICS_FIXTURE_DIR")

	type processReply struct {
		Process struct {
			MemoryLimitBytes      *int64 `json:"memoryLimitBytes"`
			MemoryLimitBytesExact string `json:"memoryLimitBytesExact"`
		} `json:"process"`
	}
	read := func() (*httptest.ResponseRecorder, processReply) {
		w := supportRequest(mux, session.AccessToken, "/v1/admin/diagnostics/concurrency")
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var reply processReply
		if err := json.Unmarshal(w.Body.Bytes(), &reply); err != nil {
			t.Fatal(err)
		}
		return w, reply
	}
	for _, vector := range []struct {
		name, fixture string
		limit         int64
		want          *int64
		wantExact     string
	}{
		{"unlimited", "get-admin-diagnostics-concurrency-unlimited.json", math.MaxInt64, nil, ""},
		{"one gibibyte", "get-admin-diagnostics-concurrency-finite.json", 1 << 30, boundedInt64(1 << 30), ""},
		{"unsafe finite", "get-admin-diagnostics-concurrency-unsafe-finite.json", 1 << 60, nil, "1152921504606846976"},
	} {
		debug.SetMemoryLimit(vector.limit)
		w, reply := read()
		if vector.want == nil {
			if reply.Process.MemoryLimitBytes != nil {
				t.Fatalf("%s: published %d instead of null", vector.name, *reply.Process.MemoryLimitBytes)
			}
		} else if reply.Process.MemoryLimitBytes == nil || *reply.Process.MemoryLimitBytes != *vector.want {
			t.Fatalf("%s: got %v, want %d", vector.name, reply.Process.MemoryLimitBytes, *vector.want)
		}
		if reply.Process.MemoryLimitBytesExact != vector.wantExact {
			t.Fatalf("%s: exact string %q, want %q", vector.name, reply.Process.MemoryLimitBytesExact, vector.wantExact)
		}
		if recordingDir != "" {
			if err := os.MkdirAll(recordingDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := contract.Record(filepath.Join(recordingDir, vector.fixture), "GetAdminDiagnosticsConcurrencyResponse", "GET", "/v1/admin/diagnostics/concurrency", nil, w, "owner"); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func boundedInt64(v int64) *int64 { return &v }

// The projection boundaries are pure: Go permits a finite zero limit, and only
// the unbounded sentinel or an out-of-range finite value is null on the wire.
func TestMemoryLimitProjectionBoundaries(t *testing.T) {
	for _, vector := range []struct {
		name      string
		limit     int64
		want      *int64
		wantExact string
	}{
		{"zero", 0, boundedInt64(0), ""},
		{"max safe", diagnosticsSafeInteger, boundedInt64(diagnosticsSafeInteger), ""},
		{"max safe plus one", diagnosticsSafeInteger + 1, nil, "9007199254740992"},
		{"max int64 minus one", math.MaxInt64 - 1, nil, "9223372036854775806"},
		{"max int64 sentinel", math.MaxInt64, nil, ""},
		{"negative invalid", -1, nil, ""},
	} {
		got, exact := memoryLimitBytes(vector.limit), memoryLimitBytesExact(vector.limit)
		if vector.want == nil {
			if got != nil {
				t.Fatalf("%s: projected %d instead of null", vector.name, *got)
			}
		} else if got == nil || *got != *vector.want {
			t.Fatalf("%s: projected %v, want %d", vector.name, got, *vector.want)
		}
		if exact != vector.wantExact {
			t.Fatalf("%s: exact %q, want %q", vector.name, exact, vector.wantExact)
		}
	}
}

// The published schema bounds the required integer to the shared safe range and
// accepts the optional exact decimal companion only as canonical positive
// decimal text.
func TestConcurrencyDiagnosticsMemoryLimitSchema(t *testing.T) {
	shape, err := apikit.Schema(reflect.TypeFor[ProcessDiagnostics]())
	if err != nil {
		t.Fatal(err)
	}
	properties := shape["properties"].(map[string]any)
	limit := properties["memoryLimitBytes"].(map[string]any)
	if limit["type"] != "integer" || limit["nullable"] != true || limit["maximum"] != diagnosticsSafeInteger {
		t.Fatalf("memoryLimitBytes is not a bounded nullable integer: %v", limit)
	}
	exact := properties["memoryLimitBytesExact"].(map[string]any)
	if exact["type"] != "string" || exact["pattern"] != "^[1-9][0-9]*$" {
		t.Fatalf("memoryLimitBytesExact is not canonical positive decimal: %v", exact)
	}
	required := map[string]bool{}
	for _, name := range shape["required"].([]string) {
		required[name] = true
	}
	if !required["memoryLimitBytes"] || required["memoryLimitBytesExact"] {
		t.Fatalf("memory limit requiredness changed: %v", shape["required"])
	}
}

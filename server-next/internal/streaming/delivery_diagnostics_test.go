package streaming

import (
	"encoding/json"
	"strings"
	"testing"
)

func assertRecentFailuresArray(t *testing.T, d DeliveryDiagnostics) {
	t.Helper()
	if d.RecentFailures == nil {
		t.Fatal("successful diagnostics left recentFailures nil")
	}
	encoded, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"recentFailures":[]`) {
		t.Fatalf("successful diagnostics did not encode an empty failure array: %s", encoded)
	}
}

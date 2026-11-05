package sqliteconn

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestZeroReadLimitsJSONMatchesPreviousShapes(t *testing.T) {
	// Exact pre-ReadLimits Config/Settings shapes. Existing HN plan goldens
	// must retain identical bytes when the optional policy is not configured.
	type previousConfig struct {
		Role        Role
		Connections int
		CacheKiB    int32
		Synchronous SyncMode
	}
	type previousSettings struct {
		Engine         string
		JournalMode    string
		ForeignKeys    int
		Synchronous    int
		AutoCheckpoint int
		CacheSize      int
		BusyTimeout    int
		MmapSize       int64
		QueryOnly      int
		TempStore      int
	}
	for _, pair := range [][2]any{
		{Config{}, previousConfig{}},
		{Config{Role: Reader, Connections: 3, CacheKiB: 1024, Synchronous: Normal}, previousConfig{Reader, 3, 1024, Normal}},
		{Settings{}, previousSettings{}},
		{Settings{Engine: "3.53.4", JournalMode: "wal", ForeignKeys: 1, Synchronous: 1, CacheSize: -1024, QueryOnly: 1, TempStore: 1}, previousSettings{"3.53.4", "wal", 1, 1, 0, -1024, 0, 0, 1, 1}},
	} {
		actual, err := json.Marshal(pair[0])
		if err != nil {
			t.Fatal(err)
		}
		previous, err := json.Marshal(pair[1])
		if err != nil || !bytes.Equal(actual, previous) {
			t.Fatalf("zero policy changed existing JSON: got %s want %s: %v", actual, previous, err)
		}
	}
	limits := ReadLimits{LengthBytes: 65536, Columns: 64}
	for _, value := range []any{Config{ReadLimits: limits}, Settings{ReadLimits: limits}} {
		encoded, err := json.Marshal(value)
		if err != nil || !bytes.Contains(encoded, []byte(`"ReadLimits":{"LengthBytes":65536,"Columns":64}`)) {
			t.Fatalf("configured native limits missing from evidence: %s %v", encoded, err)
		}
	}
}

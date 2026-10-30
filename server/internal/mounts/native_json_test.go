package mounts

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

func TestNativeJSONBoundedElementsAndMalformedTail(t *testing.T) {
	count := 0
	visit := func(raw []byte) error {
		var row map[string]any
		if err := json.Unmarshal(raw, &row); err != nil {
			return storage.ErrRemoteConfig
		}
		count++
		return nil
	}
	if err := eachNativeJSON(strings.NewReader(`[{"Path":"one","quoted":"a}b\\\"c"},{"Path":"two"}]`), visit); err != nil || count != 2 {
		t.Fatal(count, err)
	}
	for _, raw := range []string{`[`, `[{},]`, `[{}] trailing`, `[1]`, `[{} {}]`, `[{"x":[}]`, `[{}] []`} {
		if err := eachNativeJSON(strings.NewReader(raw), visit); err == nil {
			t.Fatalf("accepted malformed list %q", raw)
		}
	}
	for _, raw := range []string{`[{"x":"` + strings.Repeat("a", 64<<10) + `"}]`, `[{"x":` + strings.Repeat("[", 25) + `0` + strings.Repeat("]", 25) + `}]`} {
		if err := eachNativeJSON(strings.NewReader(raw), visit); !errors.Is(err, storage.ErrRemoteLimit) {
			t.Fatal("missing pre-allocation limit", err)
		}
	}
	stop := errors.New("callback cancelled")
	if err := eachNativeJSON(strings.NewReader(`[{},{}]`), func([]byte) error { return stop }); !errors.Is(err, stop) {
		t.Fatal(err)
	}
}

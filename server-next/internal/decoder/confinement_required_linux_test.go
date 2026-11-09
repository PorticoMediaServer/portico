//go:build linux

package decoder

import (
	"errors"
	"sync"
	"testing"
)

func TestRequiredDecoderRejectsUnavailableBridge(t *testing.T) {
	t.Setenv("PORTICO_DECODER_SANDBOX", "required")
	bridgeProbe.once = sync.Once{}
	bridgeProbe.ok = false
	bridgeProbe.once.Do(func() {})
	t.Cleanup(func() { bridgeProbe.once = sync.Once{}; bridgeProbe.ok = false })
	if _, err := (decoderJob{executable: realTool(t), endpoint: "127.0.0.1:19502"}).command(); !errors.Is(err, ErrConfinementUnavailable) {
		t.Fatalf("required decoder used host-network fallback: %v", err)
	}
}

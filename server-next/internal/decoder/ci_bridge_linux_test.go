//go:build linux

package decoder

import (
	"context"
	"os"
	"testing"
)

// The hosted sandbox job first proves that bubblewrap can create all required
// namespaces. A cached false bridge result must fail that job, never turn the
// real decoder coverage into successful skips or a host-network fallback.
func TestHostedSandboxRequiresDecoderBridge(t *testing.T) {
	if os.Getenv("PORTICO_REQUIRE_DECODER_SANDBOX") != "1" {
		t.Skip("required by the hosted decoder sandbox job")
	}
	if !bridgeSandbox() {
		t.Fatalf("namespace-capable CI must use the decoder bridge: %v", ProbeConfinement(context.Background()))
	}
}

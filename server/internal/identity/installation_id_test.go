package identity

import (
	"strings"
	"testing"
)

func TestPublicInstallationValidatorUsesDeviceRegistryGrammar(t *testing.T) {
	for _, value := range []string{"33333333-3333-4333-8333-333333333333", Token(), strings.Repeat("a", 32), strings.Repeat("Z", 128)} {
		if !ValidInstallationID(value) || !validInstallationID(value) {
			t.Fatalf("existing installation refused: %q", value)
		}
	}
	for _, value := range []string{"", strings.Repeat("a", 31), strings.Repeat("a", 129), strings.Repeat("a", 32) + "/", strings.Repeat("a", 32) + "=", strings.Repeat("a", 32) + "\n"} {
		if ValidInstallationID(value) || validInstallationID(value) {
			t.Fatalf("invalid installation accepted: %q", value)
		}
	}
}

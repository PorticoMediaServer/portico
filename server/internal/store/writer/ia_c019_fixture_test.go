package writer

import (
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"testing"
)

// C fixtures use the complete adopted fresh schema, never an ALTER/CREATE overlay.
func requireC019Fixture(t *testing.T) {
	t.Helper()
	if schema.SHA256 != "81d6971a2eeb98f07304cf9e889af5f0bf94572c9c8f3fa8539a099d7f98d46b" {
		t.Fatal("IA fixtures require exact adopted C019 schema", schema.SHA256)
	}
}

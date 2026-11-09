package read

import (
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"testing"
)

func requireC019ReadFixture(t *testing.T) {
	t.Helper()
	if schema.SHA256 != "81d6971a2eeb98f07304cf9e889af5f0bf94572c9c8f3fa8539a099d7f98d46b" {
		t.Fatal("IA read fixtures require exact adopted C019 schema", schema.SHA256)
	}
}

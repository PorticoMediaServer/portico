package search_test

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/searchmanifest"
	"github.com/PorticoMediaServer/portico/server/internal/store/search"
)

// These assignments deliberately require aliases, preserving the exact native
// types when preparation adapters hand data to the shared portable protocol.
var (
	_ searchmanifest.Input      = search.ManifestInput{}
	_ searchmanifest.Row        = search.ManifestRow{}
	_ searchmanifest.Checkpoint = search.ManifestCheckpoint{}
	_ searchmanifest.Manifest   = search.Manifest{}
	_ searchmanifest.Subject    = search.ManifestEntity
)

func TestManifestPublicProtocolAliases(t *testing.T) {
	if search.ErrManifest != searchmanifest.ErrManifest || search.ManifestAlgorithm != searchmanifest.Algorithm ||
		search.ManifestEntity != searchmanifest.Entity || search.ManifestPerson != searchmanifest.Person {
		t.Fatal("preparation API differs from the shared portable protocol")
	}
}

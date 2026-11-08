package writer

import (
	"bytes"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/storage/nativeinventory"
)

func TestInventoryBatchRefusesUnstablePageWithEntries(t *testing.T) {
	p, in, page := inventoryPreparationObservation(t)
	f := in.Files[0]
	f.RelativePath, f.AssetPath, f.ObjectIdentity = []byte("Film 2.mkv"), []byte("/media/Film 2.mkv"), "dev:7:ino:32"
	in.Files = append(in.Files, f)
	in.Directory.RawNames = 2
	page.Entries = append(page.Entries, nativeinventory.Entry{Path: bytes.Clone(f.AssetPath), Name: bytes.Clone(f.RelativePath), ObjectIdentity: f.ObjectIdentity, ChangeToken: f.ChangeToken, Size: f.Size, ModifiedNS: f.ModifiedNS})
	if !p.matchesInventoryObservation(in, page) {
		t.Fatal("stable batch control refused")
	}
	page.Unstable = true
	for _, complete := range []bool{false, true} {
		page.Complete = complete
		if p.matchesInventoryObservation(in, page) {
			t.Fatal("unstable page entered the batch purpose", complete)
		}
	}
}

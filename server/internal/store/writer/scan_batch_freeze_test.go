package writer

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/storage/nativeinventory"
)

func TestInventoryBatchRefusesUnacceptedPageSuffix(t *testing.T) {
	for _, width := range []int{2, maxInventoryBatchFiles} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			p, in, page := inventoryPreparationObservation(t)
			for i := 1; i < width; i++ {
				f := in.Files[0]
				f.RelativePath, f.AssetPath = []byte(fmt.Sprintf("Film %d.mkv", i)), []byte(fmt.Sprintf("/media/Film %d.mkv", i))
				f.SourceKey, f.ObjectIdentity = fmt.Sprintf("source-key:%d", i+1), fmt.Sprintf("file-native:%d", 31+i)
				in.Files = append(in.Files, f)
				page.Entries = append(page.Entries, nativeinventory.Entry{Path: bytes.Clone(f.AssetPath), Name: bytes.Clone(f.RelativePath), ObjectIdentity: f.ObjectIdentity, ChangeToken: f.ChangeToken, Size: f.Size, ModifiedNS: f.ModifiedNS})
			}
			in.Directory.RawNames = int64(width)
			if !p.matchesInventoryObservation(in, page) {
				t.Fatal("exact page control refused")
			}
			// This cookie is after a filename absent from the proposed input.
			// Accepting its prefix would permanently skip that unaccepted file.
			page.NextCursor, in.Directory.NextCursor = "123456", "123456"
			page.Entries = append(page.Entries, nativeinventory.Entry{Path: []byte("/media/Unaccepted.mkv"), Name: []byte("Unaccepted.mkv"), ObjectIdentity: "file-native:99", Size: 1, ModifiedNS: 1})
			if p.matchesInventoryObservation(in, page) {
				t.Fatal("cursor advanced past an unaccepted observed filename")
			}
		})
	}
}

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

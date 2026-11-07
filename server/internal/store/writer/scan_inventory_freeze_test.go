package writer

import (
	"bytes"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/storage/nativeinventory"
)

func inventoryPreparationObservation(t *testing.T) (*N1Preparation, InventoryAcceptInput, nativeinventory.Page) {
	t.Helper()
	in, _ := inventoryPublicInput(t)
	in.Directory.RawNames = 1
	in.Directory.Complete, in.Directory.Unstable = false, false
	f := in.Files[0]
	page := nativeinventory.Page{Version: 1, RootIdentity: in.Source.RootIdentity,
		DirectoryIdentity: in.Directory.Identity, DirectoryRevision: in.Directory.Revision,
		NextCursor: in.Directory.NextCursor, Entries: []nativeinventory.Entry{{
			Path: bytes.Clone(f.AssetPath), Name: bytes.Clone(f.RelativePath),
			ObjectIdentity: f.ObjectIdentity, ChangeToken: f.ChangeToken, Size: f.Size, ModifiedNS: f.ModifiedNS}}}
	template := in
	template.Source.ResolvedRoot = bytes.Clone(in.Source.ResolvedRoot)
	template.Directory.Path = bytes.Clone(in.Directory.Path)
	return &N1Preparation{template: template}, in, page
}

func TestN1PreparationBindsTheOwnedRawObservation(t *testing.T) {
	p, in, page := inventoryPreparationObservation(t)
	if !p.matchesInventoryObservation(in, page) {
		t.Fatal("exact owned observation refused")
	}
	for _, change := range []func(*InventoryAcceptInput, *nativeinventory.Page){
		func(in *InventoryAcceptInput, _ *nativeinventory.Page) {
			in.Files[0].AssetPath = []byte("/other/Film.mkv")
		},
		func(_ *InventoryAcceptInput, page *nativeinventory.Page) { page.RootIdentity = "replacement-root" },
		func(_ *InventoryAcceptInput, page *nativeinventory.Page) { page.NextCursor += "1" },
	} {
		p, in, page := inventoryPreparationObservation(t)
		change(&in, &page)
		if p.matchesInventoryObservation(in, page) {
			t.Fatal("producer value replaced the owned native observation")
		}
	}
	page.Complete = true
	if p.matchesInventoryObservation(in, page) {
		t.Fatal("directory EOF entered the ordinary N1 purpose")
	}
}

func TestN1PreparationForecastPrecedesDerivedAllocation(t *testing.T) {
	_, in, page := inventoryPreparationObservation(t)
	_, id := inventoryPublicInput(t)
	command, err := NewInventoryAcceptCommand(id, in)
	if err != nil {
		t.Fatal(err)
	}
	domain, err := inventoryFreshRequiredBytes(command.(*inventoryAcceptCommand))
	if err != nil {
		t.Fatal(err)
	}
	whole, err := inventoryPreparationRequiredBytes(in, page, 64)
	if err != nil || whole <= domain+32*64 {
		t.Fatal("whole preparation omitted retained original/page copies", domain, whole, err)
	}
	larger, err := inventoryPreparationRequiredBytes(in, page, 65)
	if err != nil || larger != whole+32 {
		t.Fatal("logical identity root escaped the retained envelope", whole, larger, err)
	}
	if allocations := testing.AllocsPerRun(10, func() {
		if _, err := inventoryPreparationRequiredBytes(in, page, 64); err != nil {
			panic(err)
		}
	}); allocations != 0 {
		t.Fatal("forecast allocated before admission", allocations)
	}
	in.Files[0].Title = strings.Repeat("x", maxCanonicalRequestBytes)
	if n, err := inventoryPreparationRequiredBytes(in, page, 64); err == nil || n != 0 {
		t.Fatal("oversized original produced a usable preparation forecast", n, err)
	}
}

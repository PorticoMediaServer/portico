package writer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/mediasource"
	"github.com/PorticoMediaServer/portico/server/internal/storage/nativeinventory"
	"github.com/PorticoMediaServer/portico/server/internal/storage/objectidentity"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// Constructor/retirement component only. Native source values are read from
// the existing fixture on its real opening/registry/governor; the observer's
// transport is an explicit fault fixture. This issues no run/lease or effects.
func n1FreezeFixture(t *testing.T) (*N1Preparation, InventoryAcceptInput) {
	t.Helper()
	f := newScanDriverFixture(t, scanIssuerStatementFixture)
	turn := f.turn(t, InventoryCreate, f.create.scope.runID)
	source, err := captureScanSource(t.Context(), turn, 7, "lifecycle-library", "lifecycle-new-source")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	b := f.preparation(t, InventoryAccept, f.create.scope.runID)
	b.source = &source
	in, id := inventoryPublicInput(t)
	s := source.source
	in.Scope = InventoryScopeInput{f.create.scope.runID, source.libraryKey, source.libraryID}
	in.Source = InventorySourceInput{s.id, s.incarnation, s.rootIdentity, bytes.Clone(s.resolvedRoot), s.generation, s.followSymlinks}
	in.Directory.Path, in.Directory.RawNames = []byte("."), 1
	in.Directory.Identity, in.Directory.Revision, in.Directory.NextCursor = "actual-directory", "directory-revision", "42"
	in.Directory.Complete, in.Directory.Unstable = false, false
	file := &in.Files[0]
	file.RelativePath, file.AssetPath = []byte("The Film (2020).mkv"), []byte("/media-new/The Film (2020).mkv")
	file.ObjectIdentity, file.ChangeToken = "dev:7:ino:31", "native-change"
	projection, err := ProjectOrdinaryFilename(InventoryOrdinaryFilenameInput{source.identityRoot, file.AssetPath, file.RelativePath, "", "en-US"})
	if err != nil {
		t.Fatal(err)
	}
	m := projection.Metadata
	file.SourceKey, file.Year = projection.SourceKey, projection.Year
	file.Title, file.SortTitle, file.SortKey, file.SortHead = m.Title, m.SortTitle, m.SortKey, m.SortHead
	file.SortKeyKeepArticles, file.SortHeadKeepArticles, file.MetadataLanguage = m.SortKeyKeepArticles, m.SortHeadKeepArticles, m.MetadataLanguage
	evidence := mediasource.InventoryEvidence{Kind: "local", Scope: s.id + ":" + s.incarnation,
		Object: objectidentity.StableObjectIdentity(file.ObjectIdentity, s.rootIdentity), Size: file.Size, ModifiedNS: file.ModifiedNS, ChangeToken: file.ChangeToken}
	file.Revision, err = evidence.Revision()
	if err != nil {
		t.Fatal(err)
	}
	page := nativeinventory.Page{Version: 1, RootIdentity: s.rootIdentity, DirectoryIdentity: in.Directory.Identity,
		DirectoryRevision: in.Directory.Revision, NextCursor: in.Directory.NextCursor,
		Entries: []nativeinventory.Entry{{Path: file.AssetPath, Name: file.RelativePath, ObjectIdentity: file.ObjectIdentity, ChangeToken: file.ChangeToken, Size: file.Size, ModifiedNS: file.ModifiedNS}}}
	raw, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	b.observer = nativeinventory.NewObserver(&scanObservationFaultTransport{page: string(raw)})
	b.request.RootIdentity, b.request.DirectoryRevision = s.rootIdentity, in.Directory.Revision
	n := &N1Preparation{driver: f.driver, preparation: b, identity: id, template: in}
	n.self, b.inventory = n, n
	if _, err = n.ObserveInventory(t.Context()); err != nil {
		t.Fatal(err)
	}
	return n, in
}

func TestN1FreezeUsesOwnedObservationAndRetainsUntilClose(t *testing.T) {
	n, in := n1FreezeFixture(t)
	bad := in
	bad.Files = append([]InventoryFilenameInput(nil), in.Files...)
	bad.Files[0].Revision = strings.Repeat("x", 1<<20)
	if command, err := n.Freeze(t.Context(), bad); command != nil || !isN1CapacityRefusal(err) {
		t.Fatal("derived construction began without its full same-grant envelope", command, err)
	}
	command, err := n.Freeze(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	c := command.(*inventoryAcceptCommand)
	ref, err := InventoryAcceptReference(c)
	if err != nil || ref != n.reference {
		t.Fatal("frozen original diverged", ref, err)
	}
	in.Files[0].AssetPath[0] = 'X'
	if c.request.files[0].assetPath[0] == 'X' {
		t.Fatal("retained caller buffer")
	}
	a := n.driver.writer.effective.authority
	if a.Usage() == ([4]governor.Amounts{}) {
		t.Fatal("published command lost original preparation custody")
	}
	if err = n.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c.valid || len(c.request.files) != 0 || n.command != nil || n.preparation != nil || n.driver.preparation != nil || a.Usage() != ([4]governor.Amounts{}) {
		t.Fatal("closed/refunded tombstone retained command buffers")
	}
}

func TestN1FreezeFinalCancellationDiscardsCommandWithoutRefund(t *testing.T) {
	n, in := n1FreezeFixture(t)
	base, cancel := context.WithCancel(t.Context())
	defer cancel()
	ctx := &scanIssuerPublicationContext{Context: base, cancel: cancel, installed: func() bool { return n.command != nil }}
	if command, err := n.Freeze(ctx, in); command != nil || !errors.Is(err, context.Canceled) || n.command != nil || n.reference != (ScanReceiptRequest{}) {
		t.Fatal("discarded constructor remained published", command, err)
	}
	if n.preparation == nil || n.driver.writer.effective.authority.Usage() == ([4]governor.Amounts{}) {
		t.Fatal("cancellation refunded retained observation custody")
	}
	if err := n.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func isN1CapacityRefusal(err error) bool {
	var refusal *Refusal
	return errors.As(err, &refusal) && refusal.Code == RefusalCapacity
}

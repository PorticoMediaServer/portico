package remotesources

import (
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/mediasource"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"testing"
)

func TestInventorySnapshotNamespacesGenerationAndIdentity(t *testing.T) {
	b := binding{ID: "remote-one", Root: "/virtual/one"}
	raw := storage.Snapshot{Path: "/virtual/one/movie.mkv", ObjectID: "object", Revision: "etag", Size: 10}
	first := inventorySnapshot(b, "1", raw)
	second := inventorySnapshot(b, "2", raw)
	if first.Revision != "1:etag" || first.ChangeToken != first.Revision {
		t.Fatalf("missing generation evidence: %+v", first)
	}
	if first.ObjectIdentity == "" || first.ObjectIdentity == second.ObjectIdentity || first.ChangeToken == second.ChangeToken {
		t.Fatal("identity/evidence conflated")
	}
	other := inventorySnapshot(binding{ID: "remote-two"}, "1", raw)
	if first.ObjectIdentity == other.ObjectIdentity {
		t.Fatal("identities escaped adapter namespace")
	}
	raw.ObjectID = ""
	if got := inventorySnapshot(b, "1", raw); got.ObjectIdentity != "" {
		t.Fatal("invented strong object identity")
	}
	raw.Directory = true
	root1 := inventoryRootSnapshot(inventorySnapshot(b, "1", raw))
	root2 := inventoryRootSnapshot(inventorySnapshot(b, "2", raw))
	if root1.ObjectIdentity == root2.ObjectIdentity {
		t.Fatal("configuration replacement reused root admission")
	}
	if got := inventorySnapshot(b, "1", raw); got.ObjectIdentity != storage.RemoteDirectoryIdentity(b.ID, raw.Path) {
		t.Fatal("directory identities disagree")
	}
}

func TestListingClaimDoesNotSerializeUnrelatedSources(t *testing.T) {
	s := &Service{}
	release, err := s.claimListing("source-one")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.claimListing("source-one"); !errors.Is(err, storage.ErrBusy) {
		t.Fatal("duplicate listing accepted", err)
	}
	other, err := s.claimListing("source-two")
	if err != nil {
		t.Fatal("unrelated listing blocked", err)
	}
	other()
	release()
	release, err = s.claimListing("source-one")
	if err != nil {
		t.Fatal("claim not released", err)
	}
	release()
}

func TestStrongETagCannotAliasAcrossRemoteConfiguration(t *testing.T) {
	b := binding{ID: "source"}
	first, err := mediasource.NewVersion(mediasource.Evidence{Kind: mediasource.StrongETag, Scope: remoteVersionScope(b, "1"), Object: "path", Revision: `"same-etag"`, Size: 10})
	if err != nil {
		t.Fatal(err)
	}
	second, err := mediasource.NewVersion(mediasource.Evidence{Kind: mediasource.StrongETag, Scope: remoteVersionScope(b, "2"), Object: "path", Revision: `"same-etag"`, Size: 10})
	if err != nil {
		t.Fatal(err)
	}
	if first.ID() == second.ID() {
		t.Fatal("provider generation escaped the immutable version key")
	}
}

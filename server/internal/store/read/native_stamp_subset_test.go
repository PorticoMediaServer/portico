package read

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"unsafe"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/search"
)

// Metadata controls only; these do not manufacture a runtime read certificate.
func nativeSubsetFixture(n int) (*FullSearchReadStamp, []FullSearchHit) {
	s := &FullSearchReadStamp{pool: new(governor.Snapshots), output: new(governor.Grant), policies: make([]nativePolicyWitness, 0, n), checked: make([]nativePolicyWitness, 0, n), hits: make([]nativeStampedHit, 0, n)}
	s.fence.permission.libraryID = 1
	var hits []FullSearchHit
	for i := 1; i <= n; i++ {
		p := nativePolicyWitness{valid: true, id: int64(i), kind: 1, library: 1, witness: fmt.Sprintf("%04d", i) + strings.Repeat("x", 4092)}
		h := FullSearchHit{Subject: search.ManifestEntity, ID: int64(i), Kind: 1, Library: 1, Item: BrowseItem{ID: int64(i), Title: "Source"}, permission: p}
		hits = append(hits, h)
		s.checked = append(s.checked, p)
		s.policies = append(s.policies, p)
		s.hits = append(s.hits, nativeStampedHit{nativeHitIdentity(h), p})
	}
	s.ownWitnesses()
	return s, hits
}

func TestNativeStampNarrowingKeepsOnlyIssuedHits(t *testing.T) {
	s, hits := nativeSubsetFixture(2)
	keep, err := s.RetainHits(hits[:1])
	if err != nil || len(keep.policies) != 1 || len(keep.hits) != 1 || keep.MetadataBytes() >= s.MetadataBytes() {
		t.Fatal("subset", keep, err)
	}
	if _, err = keep.RetainHits(hits); !errors.Is(err, ErrStale) {
		t.Fatal("narrowed stamp widened", err)
	}
	for _, alter := range []func(*FullSearchHit){func(h *FullSearchHit) { h.ID++ }, func(h *FullSearchHit) { h.SortKey = "forged" }, func(h *FullSearchHit) { h.Item.Title = "forged" }} {
		h := hits[0]
		alter(&h)
		if _, err = s.RetainHits([]FullSearchHit{h}); !errors.Is(err, ErrStale) {
			t.Fatal("unissued public values inherited proof", err)
		}
	}
	if _, err = (&FullSearchReadStamp{}).RetainHits(hits[:1]); !errors.Is(err, ErrInput) {
		t.Fatal("zero stamp narrowed", err)
	}
}

func TestNativeStamp128ProofsAndMetadataCap(t *testing.T) {
	s, hits := nativeSubsetFixture(MaxNativeRetained)
	if s.MetadataBytes() > MaxNativeStampBytes {
		t.Fatalf("bounded128proof fixture bytes=%d witness=%d hit=%d caps=%d/%d/%d", s.MetadataBytes(), unsafe.Sizeof(nativePolicyWitness{}), unsafe.Sizeof(nativeStampedHit{}), cap(s.checked), cap(s.policies), cap(s.hits))
	}
	keep, err := s.RetainHits(hits)
	if err != nil || len(keep.policies) != MaxNativeRetained {
		t.Fatal("normal preview envelope", err)
	}
	if _, err = s.RetainHits(append(hits, hits[0])); !errors.Is(err, ErrInput) {
		t.Fatal("retained count cap", err)
	}
	s.query = strings.Repeat("x", MaxNativeStampBytes)
	if _, err = s.RetainHits(hits[:1]); !errors.Is(err, ErrInput) {
		t.Fatal("metadata cap", err)
	}
}

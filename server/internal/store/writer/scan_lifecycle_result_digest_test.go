package writer

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

func scanLifecycleResultFixture(claim bool) (CommandIdentity, RequestDigest, scanRunLifecycleResult) {
	var i CommandIdentity
	var q RequestDigest
	i.Sequence = 1
	for n := range i.Incarnation {
		i.Incarnation[n], i.OperationID[n] = byte(n), byte(n+16)
	}
	for n := range q {
		q[n] = byte(n + 32)
	}
	v := scanRunLifecycleResult{
		Original: scanRunOriginalScope{RunID: "run-é", LibraryKey: "library-public", LibraryID: 13,
			SourceID: "scan-root", SourceIncarnation: "inc-3", SourceGeneration: 7, RootIdentity: "source-root",
			ResolvedRoot: []byte("/media/é"), FollowSymlinks: true, PolicyRevision: 11, ScanMode: "first-import",
			CreateOperationID: i.OperationID, CreateRequestDigest: q},
		AcceptedRunRevision: 1, AppliedMS: 1000, RootIdentity: "dir-native", RootRevision: "rev:\x00opaque",
	}
	if claim {
		i.Sequence = 4
		for n := range i.OperationID {
			i.OperationID[n] = byte(n + 64)
		}
		for n := range q {
			q[n] = byte(n + 80)
		}
		v.AcceptedRunRevision, v.AppliedMS = 4, 2000
		v.LeaseOwner, v.LeaseGeneration, v.LeaseExpiresMS = 23, 2, 9000
		v.AcknowledgedSequence, v.Discovered, v.Processed = 3, 31, 29
		v.RootCursor, v.RootPages, v.RootSkipped = "cookie:9", 5, 1
	}
	return i, q, v
}

func TestScanLifecycleClosedPurposeAndKnownWire(t *testing.T) {
	// Independent Python struct.pack('>Q')/UTF-8-LP construction fixes the wire;
	// these answers are not recorded by the production encoder under test.
	for _, tc := range []struct {
		claim bool
		size  int
		sha   string
	}{
		{false, 511, "d410cf32dee7f13ee6ba80e961c7783838b0fc8d45849d1b5f10120d1f9b6c93"},
		{true, 518, "0fc3fc5851f329bed77b5a379d5002080194bb276f30a362c9e3b8a092c060bf"},
	} {
		i, q, v := scanLifecycleResultFixture(tc.claim)
		kind := InventoryCreate
		var value any = inventoryCreateResult{v}
		if tc.claim {
			kind, value = InventoryClaim, inventoryClaimResult{v}
		}
		descriptor, err := Describe(kind)
		class, classErr := commandClass(kind)
		if err != nil || classErr != nil || descriptor.Durability != Normal || class != governor.ScanMinimum {
			t.Fatal(descriptor, class, err, classErr)
		}
		h := &recordedResultHash{Hash: sha256.New()}
		e := requestEncoder{h: h}
		if err := encodeCanonicalResult(&e, i, q, value); err != nil || h.bytes.Len() != tc.size || hex.EncodeToString(h.Sum(nil)) != tc.sha {
			t.Fatalf("wire: bytes=%d digest=%x err=%v", h.bytes.Len(), h.Sum(nil), err)
		}
		d, err := canonicalResultDigest(i, q, value)
		if err != nil || hex.EncodeToString(d[:]) != tc.sha {
			t.Fatal(d, err)
		}
		payload := len(v.Original.RunID) + len(v.Original.LibraryKey) + len(v.Original.SourceID) + len(v.Original.SourceIncarnation) +
			len(v.Original.RootIdentity) + len(v.Original.ResolvedRoot) + len(v.Original.ScanMode) +
			len(v.RootIdentity) + len(v.RootRevision) + len(v.RootCursor)
		if int(e.written) != scanLifecycleResultFixedBytes+len(descriptor.Name)+payload {
			t.Fatal("preflight envelope disagrees with complete wire", e.written)
		}
	}
}

func TestScanLifecycleRefusesWrongVariantAndBindingBeforeHash(t *testing.T) {
	for _, claim := range []bool{false, true} {
		for _, mutate := range []func(*CommandIdentity, *RequestDigest, *scanRunLifecycleResult){
			func(i *CommandIdentity, _ *RequestDigest, _ *scanRunLifecycleResult) { i.Sequence = 0 },
			func(_ *CommandIdentity, _ *RequestDigest, v *scanRunLifecycleResult) { v.Original.SourceGeneration = 0 },
			func(_ *CommandIdentity, _ *RequestDigest, v *scanRunLifecycleResult) {
				v.Original.ResolvedRoot = []byte{'/', 0}
			},
			func(_ *CommandIdentity, _ *RequestDigest, v *scanRunLifecycleResult) { v.Original.ScanMode = "future" },
			func(_ *CommandIdentity, _ *RequestDigest, v *scanRunLifecycleResult) { v.RootRevision = "\xff" },
			func(_ *CommandIdentity, _ *RequestDigest, v *scanRunLifecycleResult) { v.RootSkipped = v.RootPages + 1 },
			func(_ *CommandIdentity, _ *RequestDigest, v *scanRunLifecycleResult) { v.AcknowledgedSequence++ },
		} {
			i, q, v := scanLifecycleResultFixture(claim)
			mutate(&i, &q, &v)
			var value any = inventoryCreateResult{v}
			if claim {
				value = inventoryClaimResult{v}
			}
			h := &countedResultHash{Hash: sha256.New()}
			e := requestEncoder{h: h}
			if err := encodeCanonicalResult(&e, i, q, value); !errors.Is(err, ErrInvalid) || h.writes != 0 || e.written != 0 {
				t.Fatal("invalid scalar reached hash", err, h.writes, e.written)
			}
		}
	}
	i, q, v := scanLifecycleResultFixture(false)
	wrong := q
	wrong[0]++
	if d, err := inventoryCreateResultDigest(i, wrong, inventoryCreateResult{v}); d != (ResultDigest{}) || !errors.Is(err, ErrInvalid) {
		t.Fatal("create original digest mismatch", d, err)
	}
	i.OperationID[0]++
	if d, err := inventoryCreateResultDigest(i, q, inventoryCreateResult{v}); d != (ResultDigest{}) || !errors.Is(err, ErrInvalid) {
		t.Fatal("create original operation mismatch", d, err)
	}
	i, q, v = scanLifecycleResultFixture(true)
	if d, err := inventoryCreateResultDigest(i, q, inventoryCreateResult{v}); d != (ResultDigest{}) || !errors.Is(err, ErrInvalid) {
		t.Fatal("claim relabeled create", d, err)
	}
	v.LeaseExpiresMS = v.AppliedMS
	if d, err := inventoryClaimResultDigest(i, q, inventoryClaimResult{v}); d != (ResultDigest{}) || !errors.Is(err, ErrInvalid) {
		t.Fatal("claim expiry equality", d, err)
	}
}

func TestScanLifecycleCompleteEnvelopePrecedesHash(t *testing.T) {
	i, q, v := scanLifecycleResultFixture(true)
	v.RootCursor = strings.Repeat("x", maxCanonicalResultBytes)
	h := &countedResultHash{Hash: sha256.New()}
	e := requestEncoder{h: h}
	if err := encodeCanonicalResult(&e, i, q, inventoryClaimResult{v}); !errors.Is(err, ErrInvalid) || h.writes != 0 || e.written != 0 {
		t.Fatal("oversized late cursor reached hash", err, h.writes, e.written)
	}
	// Zero-member lifecycle schemas must not loosen ordinary N1's cohort rule.
	if d, err := inventoryAcceptResultDigest(i, q, inventoryAcceptResult{}); d != (ResultDigest{}) || !errors.Is(err, ErrInvalid) {
		t.Fatal("N1 accepted a lifecycle-shaped empty result", d, err)
	}
}

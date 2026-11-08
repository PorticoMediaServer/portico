package writer

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"hash"
	"strings"
	"testing"
)

// enrichAcceptWire assembles the expected v1 bytes with plain binary/string
// primitives. It deliberately does not use requestEncoder or any writer codec.
func enrichAcceptWire(identity CommandIdentity, request RequestDigest, v enrichAcceptResult) []byte {
	var b bytes.Buffer
	number := func(n uint64) {
		var raw [8]byte
		binary.BigEndian.PutUint64(raw[:], n)
		b.Write(raw[:])
	}
	text := func(s string) {
		number(uint64(len(s)))
		b.WriteString(s)
	}
	b.WriteString("portico-writer-result-sha256-v1\x00")
	b.Write([]byte{0, 1})
	text("enrich_accept")
	text("accept")
	text("applied")
	text(v.ProviderOperationID)
	b.Write(identity.Incarnation[:])
	number(uint64(identity.Sequence))
	b.Write(identity.OperationID[:])
	b.Write(request[:])
	number(uint64(v.EntityID))
	number(uint64(v.AcceptedCatalogRevision))
	number(uint64(v.AppliedMS))
	text(v.AfterItem)
	number(uint64(v.ImageID))
	number(uint64(v.ImageRevision))
	number(uint64(v.RetainedCount))
	return b.Bytes()
}

type enrichAcceptRecordingHash struct {
	hash.Hash
	buf bytes.Buffer
}

func (h *enrichAcceptRecordingHash) Write(p []byte) (int, error) {
	h.buf.Write(p)
	return h.Hash.Write(p)
}

func enrichAcceptIdentity(seed byte, sequence int64) (CommandIdentity, RequestDigest) {
	var identity CommandIdentity
	var request RequestDigest
	for i := range identity.Incarnation {
		identity.Incarnation[i] = seed + byte(i)
	}
	identity.Sequence = sequence
	for i := range identity.OperationID {
		identity.OperationID[i] = seed ^ byte(0xa0+i)
	}
	for i := range request {
		request[i] = seed + byte(0x40+i)
	}
	return identity, request
}

type enrichAcceptCase struct {
	name     string
	identity CommandIdentity
	request  RequestDigest
	result   enrichAcceptResult
}

func enrichAcceptCases() []enrichAcceptCase {
	asciiIdentity, asciiRequest := enrichAcceptIdentity(0x00, 1)
	utf8Identity, utf8Request := enrichAcceptIdentity(0x3d, 987654321)
	return []enrichAcceptCase{
		{
			name:     "ascii",
			identity: asciiIdentity,
			request:  asciiRequest,
			result: enrichAcceptResult{
				ProviderOperationID:     "provider-operation-1",
				EntityID:                41,
				AcceptedCatalogRevision: 7,
				AppliedMS:               0,
				AfterItem:               "after:item-0",
				ImageID:                 51,
				ImageRevision:           3,
				RetainedCount:           0,
			},
		},
		{
			name:     "utf8",
			identity: utf8Identity,
			request:  utf8Request,
			result: enrichAcceptResult{
				ProviderOperationID:     "opération-2",
				EntityID:                9001,
				AcceptedCatalogRevision: 12,
				AppliedMS:               1762500000123,
				AfterItem:               "café ✓ 日本",
				ImageID:                 77,
				ImageRevision:           5,
				RetainedCount:           19,
			},
		},
	}
}

func TestEnrichAcceptResultIndependentKnownAnswers(t *testing.T) {
	for _, c := range enrichAcceptCases() {
		t.Run(c.name, func(t *testing.T) {
			wantWire := enrichAcceptWire(c.identity, c.request, c.result)
			if got, want := len(wantWire), enrichAcceptFixedBytes+len(c.result.ProviderOperationID)+len(c.result.AfterItem); got != want {
				t.Fatalf("independent wire width %d want %d", got, want)
			}
			wantDigest := ResultDigest(sha256.Sum256(wantWire))

			h := &enrichAcceptRecordingHash{Hash: sha256.New()}
			e := requestEncoder{h: h}
			if err := encodeCanonicalResult(&e, c.identity, c.request, c.result); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(h.buf.Bytes(), wantWire) {
				t.Fatalf("canonical bytes differ: %x want %x", h.buf.Bytes(), wantWire)
			}
			d, err := enrichAcceptResultDigest(c.identity, c.request, c.result)
			if err != nil || d != wantDigest {
				t.Fatalf("digest %x want %x err %v", d, wantDigest, err)
			}
			common, err := canonicalResultDigest(c.identity, c.request, c.result)
			if err != nil || common != d {
				t.Fatal("typed helper diverges from common codec", err)
			}
		})
	}
}

func TestEnrichAcceptResultIdentityRequestAndFieldBinding(t *testing.T) {
	base := enrichAcceptCases()[0]
	originalWire := enrichAcceptWire(base.identity, base.request, base.result)
	original := ResultDigest(sha256.Sum256(originalWire))
	changes := map[string]func(*CommandIdentity, *RequestDigest, *enrichAcceptResult){
		"Incarnation":             func(i *CommandIdentity, _ *RequestDigest, _ *enrichAcceptResult) { i.Incarnation[0] ^= 1 },
		"Sequence":                func(i *CommandIdentity, _ *RequestDigest, _ *enrichAcceptResult) { i.Sequence++ },
		"OperationID":             func(i *CommandIdentity, _ *RequestDigest, _ *enrichAcceptResult) { i.OperationID[0] ^= 1 },
		"RequestDigest":           func(_ *CommandIdentity, q *RequestDigest, _ *enrichAcceptResult) { q[0] ^= 1 },
		"ProviderOperationID":     func(_ *CommandIdentity, _ *RequestDigest, v *enrichAcceptResult) { v.ProviderOperationID += "x" },
		"EntityID":                func(_ *CommandIdentity, _ *RequestDigest, v *enrichAcceptResult) { v.EntityID++ },
		"AcceptedCatalogRevision": func(_ *CommandIdentity, _ *RequestDigest, v *enrichAcceptResult) { v.AcceptedCatalogRevision++ },
		"AppliedMS":               func(_ *CommandIdentity, _ *RequestDigest, v *enrichAcceptResult) { v.AppliedMS++ },
		"AfterItem":               func(_ *CommandIdentity, _ *RequestDigest, v *enrichAcceptResult) { v.AfterItem += "x" },
		"ImageID":                 func(_ *CommandIdentity, _ *RequestDigest, v *enrichAcceptResult) { v.ImageID++ },
		"ImageRevision":           func(_ *CommandIdentity, _ *RequestDigest, v *enrichAcceptResult) { v.ImageRevision++ },
		"RetainedCount":           func(_ *CommandIdentity, _ *RequestDigest, v *enrichAcceptResult) { v.RetainedCount++ },
	}
	if len(changes) != 4+8 {
		t.Fatal("identity/request/native coverage drift")
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			i, q, v := base.identity, base.request, base.result
			change(&i, &q, &v)
			if wire := enrichAcceptWire(i, q, v); bytes.Equal(wire, originalWire) {
				t.Fatal("change did not alter fixed wire")
			}
			d, err := enrichAcceptResultDigest(i, q, v)
			if err != nil || d == original {
				t.Fatalf("unbound field digest %x err %v", d, err)
			}
		})
	}
}

func TestEnrichAcceptResultRefusesMalformedShapes(t *testing.T) {
	base := enrichAcceptCases()[0]
	changes := map[string]func(*enrichAcceptResult){
		"emptyOwner":            func(v *enrichAcceptResult) { v.ProviderOperationID = "" },
		"ownerUTF8":             func(v *enrichAcceptResult) { v.ProviderOperationID = string([]byte{0xff}) },
		"afterUTF8":             func(v *enrichAcceptResult) { v.AfterItem = string([]byte{0xff}) },
		"entityZero":            func(v *enrichAcceptResult) { v.EntityID = 0 },
		"entityNegative":        func(v *enrichAcceptResult) { v.EntityID = -1 },
		"revisionZero":          func(v *enrichAcceptResult) { v.AcceptedCatalogRevision = 0 },
		"revisionNegative":      func(v *enrichAcceptResult) { v.AcceptedCatalogRevision = -1 },
		"appliedNegative":       func(v *enrichAcceptResult) { v.AppliedMS = -1 },
		"imageIDZero":           func(v *enrichAcceptResult) { v.ImageID = 0 },
		"imageIDNegative":       func(v *enrichAcceptResult) { v.ImageID = -1 },
		"imageRevisionZero":     func(v *enrichAcceptResult) { v.ImageRevision = 0 },
		"imageRevisionNegative": func(v *enrichAcceptResult) { v.ImageRevision = -1 },
		"retainedCountNegative": func(v *enrichAcceptResult) { v.RetainedCount = -1 },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			v := base.result
			change(&v)
			if d, err := enrichAcceptResultDigest(base.identity, base.request, v); !errors.Is(err, ErrInvalid) || d != (ResultDigest{}) {
				t.Fatalf("malformed result accepted: %x %v", d, err)
			}
		})
	}
	for _, sequence := range []int64{-1, 0} {
		identity := base.identity
		identity.Sequence = sequence
		if d, err := enrichAcceptResultDigest(identity, base.request, base.result); !errors.Is(err, ErrInvalid) || d != (ResultDigest{}) {
			t.Fatalf("invalid sequence accepted: %x %v", d, err)
		}
	}
	for _, unknown := range []any{nil, &base.result, struct{}{}, RequestDigest{}} {
		if _, err := canonicalResultDigest(base.identity, base.request, unknown); !errors.Is(err, ErrInvalid) {
			t.Fatal("unknown result schema accepted", err)
		}
	}
}

func TestEnrichAcceptResultPreflightBeforeAnyHashWrite(t *testing.T) {
	base := enrichAcceptCases()[0]
	const ceiling = 32 << 20
	changes := map[string]func(*enrichAcceptResult){
		"ownerOverCeiling": func(v *enrichAcceptResult) {
			v.ProviderOperationID = strings.Repeat("a", ceiling-enrichAcceptFixedBytes+1)
		},
		"bothOverCeiling": func(v *enrichAcceptResult) {
			v.ProviderOperationID = strings.Repeat("a", 16<<20)
			v.AfterItem = strings.Repeat("b", 16<<20)
		},
		"emptyOwner": func(v *enrichAcceptResult) { v.ProviderOperationID = "" },
		"afterUTF8":  func(v *enrichAcceptResult) { v.AfterItem = string([]byte{0xff}) },
		"shape":      func(v *enrichAcceptResult) { v.RetainedCount = -1 },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			v := base.result
			change(&v)
			h := &enrichAcceptRecordingHash{Hash: sha256.New()}
			e := requestEncoder{h: h}
			if err := encodeCanonicalResult(&e, base.identity, base.request, v); !errors.Is(err, ErrInvalid) || h.buf.Len() != 0 || e.written != 0 {
				t.Fatalf("invalid result fed hash: bytes%d written%d err%v", h.buf.Len(), e.written, err)
			}
		})
	}

	// The exact literal ceiling is accepted; one more byte is not.
	atCeiling := base.result
	atCeiling.ProviderOperationID = "p"
	atCeiling.AfterItem = strings.Repeat("c", ceiling-enrichAcceptFixedBytes-1)
	d, err := enrichAcceptResultDigest(base.identity, base.request, atCeiling)
	if err != nil || d != ResultDigest(sha256.Sum256(enrichAcceptWire(base.identity, base.request, atCeiling))) {
		t.Fatalf("literal ceiling rejected: %x %v", d, err)
	}
	overCeiling := atCeiling
	overCeiling.AfterItem += "c"
	if d, err := enrichAcceptResultDigest(base.identity, base.request, overCeiling); !errors.Is(err, ErrInvalid) || d != (ResultDigest{}) {
		t.Fatalf("literal ceiling+1 accepted: %x %v", d, err)
	}
}

func TestEnrichAcceptResultDistinctFromBeginVariant(t *testing.T) {
	base := enrichAcceptCases()[0]
	begin := effectiveBeginResult{
		ProviderOperationID:     base.result.ProviderOperationID,
		EntityID:                base.result.EntityID,
		ObservedCatalogRevision: base.result.AcceptedCatalogRevision,
		AppliedMS:               base.result.AppliedMS,
		AfterItem:               base.result.AfterItem,
		ImageID:                 base.result.ImageID,
		ImageRevision:           base.result.ImageRevision,
		ResultCount:             base.result.RetainedCount,
	}
	acceptDigest, err := enrichAcceptResultDigest(base.identity, base.request, base.result)
	if err != nil {
		t.Fatal(err)
	}
	beginDigest, err := effectiveBeginResultDigest(base.identity, base.request, begin)
	if err != nil {
		t.Fatal(err)
	}
	if acceptDigest == beginDigest {
		t.Fatal("accept result aliases the BEGIN variant")
	}
	acceptWire := enrichAcceptWire(base.identity, base.request, base.result)
	beginHash := &enrichAcceptRecordingHash{Hash: sha256.New()}
	beginEncoder := requestEncoder{h: beginHash}
	if err := encodeCanonicalResult(&beginEncoder, base.identity, base.request, begin); err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(acceptWire, beginHash.buf.Bytes()) {
		t.Fatal("accept wire aliases the BEGIN wire")
	}
}

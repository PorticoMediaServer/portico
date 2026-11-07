package writer

import (
	"crypto/sha256"
	"errors"
	"testing"
)

func iaConfigurationHistoryFixture(t *testing.T, source bool) (iaConfigurationReference, iaConfigurationOwner, iaConfigurationReceipt) {
	t.Helper()
	id, request, result := iaConfigResultFixture()
	if source {
		result.original.purpose = IAConfigureSource
		result.original.sourceID = result.sources[0].id
		result.libraryName, result.libraryKind = "", ""
	}
	q := iaConfigurationReference{original: result.original, identity: id, request: request}
	value := any(iaConfigureLibraryResult{result})
	if source {
		value = iaConfigureSourceResult{result}
	}
	digest, err := canonicalResultDigest(id, request, value)
	if err != nil {
		t.Fatal(err)
	}
	headerBytes, err := iaConfigurationHeaderRetainedBytes(result.original.purpose, result)
	if err != nil {
		t.Fatal(err)
	}
	var rows []int64
	total := headerBytes
	for _, s := range result.sources {
		n, e := iaConfigurationSourceRetainedBytes(result.original.purpose, s)
		if e != nil {
			t.Fatal(e)
		}
		rows = append(rows, n)
		total += n
	}
	o := iaConfigurationOwner{exists: true, original: q.original, incarnation: id.Incarnation, last: 1, cursor: iaReceiptCursor{0, -1}, rows: 1 + result.resultCount, bytes: total}
	h := iaConfigurationReceipt{exists: true, identity: receiptIdentity{id, request}, purpose: q.original.purpose, version: 1, resultCount: result.resultCount, digest: digest, headerBytes: headerBytes, result: result, sourceBytes: rows}
	return q, o, h
}

func TestIAConfigurationDispatchUsesClosedVariants(t *testing.T) {
	for _, source := range []bool{false, true} {
		q, _, h := iaConfigurationHistoryFixture(t, source)
		name := "configure_library"
		var value any = iaConfigureLibraryResult{h.result}
		var wrong any = iaConfigureSourceResult{h.result}
		if source {
			name = "configure_source"
			value, wrong = wrong, value
		}
		got, err := canonicalResultDigest(q.identity, q.request, value)
		if err != nil || got != sha256.Sum256(iaConfigResultWire(q.identity, q.request, h.result, name)) {
			t.Fatal("closed dispatch differs from independent wire", err)
		}
		if _, err := canonicalResultDigest(q.identity, q.request, wrong); !errors.Is(err, ErrInvalid) {
			t.Fatal("configuration result was relabeled", err)
		}
	}
}

func TestIAConfigurationHistoryReplayAndRetiredBodies(t *testing.T) {
	for _, source := range []bool{false, true} {
		q, o, h := iaConfigurationHistoryFixture(t, source)
		got, err := classifyIAConfigurationHistory(q, o, h)
		if err != nil || got.decision != receiptReplay || got.digest != h.digest || len(got.result.sources) != 1 {
			t.Fatal("saved original did not replay", got, err)
		}
		h.result.sources[0].resolvedRoot = "/replacement" // decoded input mutation cannot alter saved output
		if got.result.sources[0].resolvedRoot != "/b" {
			t.Fatal("replay retained a mutable cohort slice")
		}
		// Later GC advances ACK and partially/fully reclaims source history. Do
		// not decode/reconstruct it, even if the old body is malformed or gone.
		o.last, o.acknowledged, o.rows, o.bytes = 4, 4, 0, 0
		o.cursor = iaReceiptCursor{1, -1}
		h.result, h.sourceBytes, h.version, h.resultCount, h.headerBytes = iaConfigurationResult{}, nil, 0, -1, -1
		for _, saved := range []iaConfigurationReceipt{h, {}} {
			got, err = classifyIAConfigurationHistory(q, o, saved)
			if err != nil || got.decision != receiptRetired || len(got.result.sources) != 0 || got.digest != (ResultDigest{}) {
				t.Fatal("ACKed body was decoded", got, err)
			}
		}
		wrong := q
		wrong.original.requestDigest[0]++
		wrong.request = wrong.original.requestDigest
		if _, err := classifyIAConfigurationHistory(wrong, o, iaConfigurationReceipt{}); !errors.Is(err, ErrConflict) {
			t.Fatal("reclaimed configuration lost original request anchor", err)
		}
	}
}

func TestIAConfigurationHistoryRejectsMissingCohortAndNativeDebt(t *testing.T) {
	q, o, h := iaConfigurationHistoryFixture(t, false)
	for _, change := range []func(*iaConfigurationReceipt){
		func(h *iaConfigurationReceipt) { h.exists = false },
		func(h *iaConfigurationReceipt) { h.version++ },
		func(h *iaConfigurationReceipt) { h.resultCount++ },
		func(h *iaConfigurationReceipt) { h.headerBytes-- },
		func(h *iaConfigurationReceipt) { h.sourceBytes = []int64{h.sourceBytes[0] - 1} },
		func(h *iaConfigurationReceipt) { h.sourceBytes = nil },
		func(h *iaConfigurationReceipt) { h.digest[0]++ },
		func(h *iaConfigurationReceipt) { h.result.sources = nil },
		func(h *iaConfigurationReceipt) { h.result.original.requestDigest[0]++ },
	} {
		bad := h
		change(&bad)
		got, err := classifyIAConfigurationHistory(q, o, bad)
		if !errors.Is(err, ErrResolveRequired) || got.decision != 0 || len(got.result.sources) != 0 {
			t.Fatal("incomplete saved proof escaped", got, err)
		}
	}
	for _, change := range []func(*iaConfigurationOwner){
		func(o *iaConfigurationOwner) { o.exists = false },
		func(o *iaConfigurationOwner) { o.incarnation[0]++ },
		func(o *iaConfigurationOwner) { o.rows++ },
		func(o *iaConfigurationOwner) { o.bytes++ },
		func(o *iaConfigurationOwner) { o.cursor.ordinal = 0 },
	} {
		bad := o
		change(&bad)
		got, err := classifyIAConfigurationHistory(q, bad, h)
		if !errors.Is(err, ErrResolveRequired) || got.decision != 0 {
			t.Fatal("owner drift admitted", got, err)
		}
	}
	bad := o
	bad.original.libraryID++
	if _, err := classifyIAConfigurationHistory(q, bad, h); !errors.Is(err, ErrDisclosureDenied) {
		t.Fatal("cross-library history disclosed", err)
	}
	badHeader := h
	badHeader.identity.digest[0]++
	o.acknowledged = 1
	if _, err := classifyIAConfigurationHistory(q, o, badHeader); !errors.Is(err, ErrConflict) {
		t.Fatal("ACK floor hid retained request conflict", err)
	}
}

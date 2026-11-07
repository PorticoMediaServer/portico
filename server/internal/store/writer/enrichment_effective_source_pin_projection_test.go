package writer

import (
	"errors"
	"reflect"
	"testing"
)

// Pure projections only: no DB, registry, Capture or qualification. Reuse the
// existing independent saved-result vectors, with no second digest codec.
func effectivePinTestProjection(t *testing.T, values []any, layout string) (effectivePinProjection, []any) {
	t.Helper()
	meta := make([]any, len(values))
	var lengths, fixed []any
	for i, v := range values {
		switch v.(type) {
		case nil:
			meta[i] = "null"
		case string:
			meta[i] = "text"
		case []byte:
			meta[i] = "blob"
		case int64:
			meta[i] = "integer"
		default:
			t.Fatalf("foreign native type %T", v)
		}
		if layout[i] == 'T' || layout[i] == 't' || layout[i] == 'B' || layout[i] == 'b' {
			switch v := v.(type) {
			case nil:
				lengths = append(lengths, nil)
			case string:
				lengths = append(lengths, int64(len(v)))
			case []byte:
				lengths = append(lengths, int64(len(v)))
			default:
				t.Fatal("non-byte fixture slot")
			}
		} else {
			fixed = append(fixed, v)
		}
	}
	meta = append(meta, lengths...)
	p, err := decodeEffectivePinLengths(meta, layout)
	if err != nil {
		t.Fatal(err)
	}
	p, err = effectivePinFixed(fixed, layout, p)
	if err != nil {
		t.Fatal(err)
	}
	return p, meta
}

func TestEffectiveSourcePinSavedProjection(t *testing.T) {
	for _, answer := range sealResultAnswers(t) {
		t.Run(answer.Kind, func(t *testing.T) {
			values := append(savedSealAnswerRow(t, answer), int64(7), int64(2), int64(123), int64(9))
			p, echo := effectivePinTestProjection(t, values, effectiveReceiptPinLayout)
			identity, _, _ := sealResultInputs(t, answer)
			kind := EvidenceSeal
			if answer.Kind == "credit_source_seal" {
				kind = CreditSourceSeal
			}
			pre := effectiveReceiptPinLengths{ref: effectiveSourceSealRef{answer.Facts.ProviderOperationID, identity.Incarnation, identity.Sequence, kind}, projection: p}
			got, err := decodeEffectiveSourcePinnedReceipt(pre, values, echo)
			if err != nil || got.saved.facts != answer.Facts || got.proof != (effectiveSealProofState{7, 2, EvidenceInteger{Value: 123, Present: true}, 9}) {
				t.Fatal(got, err)
			}
			// Lifecycle is separate: zero pins is data, never acceptance or Applied.
			values[45] = int64(0)
			p, echo = effectivePinTestProjection(t, values, effectiveReceiptPinLayout)
			pre.projection = p
			unpinned, err := decodeEffectiveSourcePinnedReceipt(pre, values, echo)
			if err != nil || unpinned.saved != got.saved || unpinned.proof.pinCount != 0 {
				t.Fatal("lifecycle folded into history", unpinned, err)
			}
			for name, mutate := range map[string]func(){
				"exact_pk":             func() { pre.ref.sequence++ },
				"exact_incarnation":    func() { pre.ref.incarnation[0] ^= 1 },
				"same_projection_echo": func() { echo[len(echo)-1] = int64(1) },
			} {
				t.Run(name, func(t *testing.T) {
					savedPre, savedEcho := pre, append([]any(nil), echo...)
					mutate()
					bad, err := decodeEffectiveSourcePinnedReceipt(pre, values, echo)
					if err == nil || bad != (effectivePinnedSealReceipt{}) {
						t.Fatal("native mismatch escaped", bad, err)
					}
					pre, echo = savedPre, savedEcho
				})
			}
			values[savedSealResultDigest].([]byte)[0] ^= 1
			bad, err := decodeEffectiveSourcePinnedReceipt(pre, values, echo)
			if !errors.Is(err, ErrResolveRequired) || bad != (effectivePinnedSealReceipt{}) {
				t.Fatal(bad, err)
			}
		})
	}
}

func TestEffectiveSourcePinNativeBodies(t *testing.T) {
	inc := make([]byte, 16)
	inc[0] = 19
	e := []any{"original-e", int64(3), "parser", "provider", int64(1), "older-owner", inc, int64(5), "evidence_seal"}
	p, echo := effectivePinTestProjection(t, e, effectiveEvidencePinLayout)
	got, err := decodeEffectiveSourceEvidencePin(effectiveEvidencePinLengths{evidence: "original-e", projection: p}, e, echo)
	if err != nil || got.ref.owner != "older-owner" || got.ref.kind != EvidenceSeal || got.ref.incarnation[0] != 19 {
		t.Fatal(got, err)
	}
	s := []any{"00112233445566778899aabbccddeeff", int64(101), "provider", int64(0), int64(1), "current-owner", inc, int64(8), "credit_source_seal"}
	sp, se := effectivePinTestProjection(t, s, effectivePublicationPinLayout)
	source, err := decodeEffectiveSourcePublicationPin(effectivePublicationPinLengths{publication: s[0].(string), projection: sp}, s, se)
	if err != nil || source.ref.owner == got.ref.owner || source.ref.kind != CreditSourceSeal {
		t.Fatal(source, err)
	}
	e[8] = "credit_source_seal"
	bad, err := decodeEffectiveSourceEvidencePin(effectiveEvidencePinLengths{evidence: "original-e", projection: p}, e, echo)
	if err == nil || bad != (effectiveEvidenceBodyPin{}) {
		t.Fatal(bad, err)
	}
	for _, change := range []func([]any){func(m []any) { m[6] = "text" }, func(m []any) { m[len(m)-2] = int64(32) }} {
		m := append([]any(nil), echo...)
		change(m)
		q, err := decodeEffectivePinLengths(m, effectiveEvidencePinLayout)
		if err == nil {
			if q.cells[6].octets == 16 {
				t.Fatal("bad native pin length accepted")
			}
		} else if !reflect.DeepEqual(q, effectivePinProjection{}) {
			t.Fatal("partial lengths escaped", q, err)
		}
	}
}

func TestEffectiveSourcePinControlTotals(t *testing.T) {
	values := []any{"e", int64(1), "p", "v", int64(1), "o", make([]byte, 16), int64(1), "evidence_seal"}
	_, echo := effectivePinTestProjection(t, values, effectiveEvidencePinLayout)
	// Metadata slot 12 is owner octets: three other stored TEXT occurrences
	// each have one octet. The generated purpose marker is fixed payload only.
	echo[12] = int64(MaxSealControlStringBytes - 3)
	p, err := decodeEffectivePinLengths(echo, effectiveEvidencePinLayout)
	if err != nil || p.octets.control != MaxSealControlStringBytes || p.octets.text != MaxSealControlStringBytes+uint64(len("evidence_seal")) || p.octets.payload != p.octets.text+16 {
		t.Fatal(p.octets, err)
	}
	echo[12] = int64(MaxSealControlStringBytes - 2)
	p, err = decodeEffectivePinLengths(echo, effectiveEvidencePinLayout)
	if !errors.Is(err, ErrCap) || p != (effectivePinProjection{}) {
		t.Fatal("over-limit native lengths escaped", p, err)
	}
}

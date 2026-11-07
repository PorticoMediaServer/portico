package writer

import (
	"context"
	"errors"
	"math"
	"testing"
)

func TestEffectiveSourceCopyForecastCountsRetainedAndNativeOccurrences(t *testing.T) {
	refs := effectiveSourceReferenceInput()
	c := &effectiveStageBeginCommand{valid: true, sourceReferences: &refs}
	c.request.objectID = "café"
	n, err := effectiveSourceRequestTextBytes(c)
	want := uint64(len("café") + len(refs.evidence.owner) + len(refs.source.owner))
	if err != nil || n != want {
		t.Fatal("retained original/reference TEXT not counted exactly", n, want, err)
	}
	var e, s effectivePinProjection
	e.octets.payload = 17
	s.octets.payload = 31
	got, err := effectiveSourceCopyBytes(n, e, s)
	if err != nil || got != effectiveSourceFixedWorkspace+32*(want+17+31) {
		t.Fatal("native copy occurrences escaped actual-byte model", got, err)
	}
	if got, err := effectiveSourceCopyBytes(math.MaxUint64, e); !errors.Is(err, ErrCap) || got != 0 {
		t.Fatal("overflow returned a partial budget", got, err)
	}
	c.request.producer.Capture.Path = "\xff"
	if n, err = effectiveSourceRequestTextBytes(c); err == nil || n != 0 {
		t.Fatal("invalid frozen TEXT produced a usable forecast", n, err)
	}
}

func TestEffectiveSourceCopyCoverageCannotChangeInputOrSnapshot(t *testing.T) {
	refs := effectiveSourceReferenceInput()
	input := &effectiveStageBeginCommand{valid: true, sourceReferences: &refs}
	turn := &commandTurn{transaction: &transaction{}}
	c := &effectiveSourceByteCoverage{turn: turn, tx: turn.transaction, input: input, refs: refs}
	c.self = c
	other := *input
	if err := c.consumeEvidencePinCopy(t.Context(), turn, &other, c.evidence); !errors.Is(err, ErrInvalid) {
		t.Fatal("copied command used another original's reservation", err)
	}
	stale := c.evidence
	stale.evidence = "another-native-key"
	if err := c.consumeEvidencePinCopy(t.Context(), turn, input, stale); !errors.Is(err, ErrInvalid) {
		t.Fatal("different native preflight used original coverage", err)
	}
	copied := *c
	if err := copied.consumeEvidencePinCopy(t.Context(), turn, input, c.evidence); !errors.Is(err, ErrInvalid) {
		t.Fatal("copied reservation became another copy allowance", err)
	}
}

func TestEffectiveNativeSourceVerifierRefusesWithoutLiveTurn(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	out, err := verifyEffectiveSource(ctx, nil, nil)
	if out != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled native verifier returned proof", out, err)
	}
	out, err = verifyEffectiveSource(t.Context(), nil, nil)
	if out != nil || err == nil {
		t.Fatal("no native turn produced proof", out, err)
	}
}

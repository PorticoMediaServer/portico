package writer

import (
	"context"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"math"
	"reflect"
	"unicode/utf8"
)

const effectiveSourceFixedWorkspace = uint64(64 << 10) // fixed bounded point/row/codec bookkeeping

// This is a one-use copy reservation inside ONE live Effective turn, not a
// grant or source proof. Only preflightEffectiveSourcePins creates it, after
// the real same-purpose memory check. It cannot survive another command step.
type effectiveSourceByteCoverage struct {
	self        *effectiveSourceByteCoverage
	turn        *commandTurn
	tx          *transaction
	input       *effectiveStageBeginCommand
	digest      RequestDigest
	refs        effectiveBeginSourceReferences
	evidence    effectiveEvidencePinLengths
	publication effectivePublicationPinLengths
	receipts    [2]effectiveReceiptPinLengths
	remaining   Work
	required    uint64
	next        uint8
}

// Count retained request TEXT without cloning or encoding it. The frozen
// request is a finite value tree; additions of a dynamic/opaque kind refuse.
// This is memory accounting only, never a second request codec.
func effectiveSourceRequestTextBytes(c *effectiveStageBeginCommand) (uint64, error) {
	if c == nil || !c.valid || c.sourceReferences == nil {
		return 0, ErrInvalid
	}
	var total uint64
	var visit func(reflect.Value) error
	visit = func(v reflect.Value) error {
		switch v.Kind() {
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				if err := visit(v.Field(i)); err != nil {
					return err
				}
			}
		case reflect.String:
			s := v.String()
			if !utf8.ValidString(s) || uint64(len(s)) > maxCanonicalRequestBytes-total {
				return ErrCap
			}
			total += uint64(len(s))
		case reflect.Array:
			if v.Type().Elem().Kind() != reflect.Uint8 {
				return ErrInvalid
			}
		case reflect.Int64, reflect.Bool, reflect.Float64:
		default:
			return ErrInvalid
		}
		return nil
	}
	if err := visit(reflect.ValueOf(c.request)); err != nil {
		return 0, err
	}
	for _, s := range []string{c.sourceReferences.evidence.owner, c.sourceReferences.source.owner} {
		if !utf8.ValidString(s) || uint64(len(s)) > maxCanonicalRequestBytes-total {
			return 0, ErrCap
		}
		total += uint64(len(s))
	}
	return total, nil
}

// The existing actual-byte model covers retained input, driver/bind/row copies,
// strict result decoding and streaming codec scratch. This is a forecast to be
// checked against the already installed private partition, never a new quota.
func effectiveSourceCopyBytes(inputText uint64, projections ...effectivePinProjection) (uint64, error) {
	total := inputText
	for _, p := range projections {
		if p.octets.payload > math.MaxUint64-total {
			return 0, ErrCap
		}
		total += p.octets.payload
	}
	if total > (math.MaxUint64-effectiveSourceFixedWorkspace)/32 {
		return 0, ErrCap
	}
	return effectiveSourceFixedWorkspace + 32*total, nil
}

func preflightEffectiveSourcePins(ctx context.Context, turn *commandTurn, input *effectiveStageBeginCommand) (out *effectiveSourceByteCoverage, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if err = effectivePinLive(ctx, turn); err != nil {
		return nil, err
	}
	if !effectiveSourceProofPurpose(turn.transaction.descriptor.ID) || input == nil || !input.valid || input.sourceReferences == nil {
		return nil, ErrInvalid
	}
	if err = validateEffectiveSourceReferences(*input.sourceReferences); err != nil {
		return nil, err
	}
	text, err := effectiveSourceRequestTextBytes(input)
	if err != nil {
		return nil, err
	}
	fixed, err := effectiveSourceCopyBytes(text)
	if err != nil {
		return nil, err
	}
	if err = turn.requireEffectiveSourceMemory(ctx, fixed); err != nil {
		return nil, err
	}
	e, err := readEffectiveSourceEvidencePinLengths(ctx, turn, input.request.evidenceID)
	if err != nil {
		return nil, err
	}
	s, err := readEffectiveSourcePublicationPinLengths(ctx, turn, input.request.publicationID)
	if err != nil {
		return nil, err
	}
	refs := *input.sourceReferences
	er, err := readEffectiveSourceReceiptPinLengths(ctx, turn, effectiveSourceSealRef{refs.evidence.owner, refs.evidence.receipt.Identity.Incarnation, refs.evidence.receipt.Identity.Sequence, EvidenceSeal})
	if err != nil {
		return nil, err
	}
	sr, err := readEffectiveSourceReceiptPinLengths(ctx, turn, effectiveSourceSealRef{refs.source.owner, refs.source.receipt.Identity.Incarnation, refs.source.receipt.Identity.Sequence, CreditSourceSeal})
	if err != nil {
		return nil, err
	}
	// These are selected occurrences within each proof, not a replacement for
	// the full birth-time retained-control census or retained byte charge.
	if e.projection.octets.control > MaxSealControlStringBytes-er.projection.octets.control || s.projection.octets.control > MaxSealControlStringBytes-sr.projection.octets.control {
		return nil, ErrCap
	}
	required, err := effectiveSourceCopyBytes(text, e.projection, s.projection, er.projection, sr.projection)
	if err != nil {
		return nil, err
	}
	if err = turn.requireEffectiveSourceMemory(ctx, required); err != nil {
		return nil, err
	}
	out = &effectiveSourceByteCoverage{turn: turn, tx: turn.transaction, input: input, digest: input.digest, refs: refs, evidence: e, publication: s, receipts: [2]effectiveReceiptPinLengths{er, sr}, remaining: turn.transaction.quantum.Remaining(), required: required}
	out.self = out
	return out, nil
}

// Only the four exact reads below may run between preflight and the last copy.
// The quantum state rejects any intervening charged statement, including a
// same-transaction write that could change equal-length text. Failed copies
// consume their slot and require the outer command to roll back.
func (c *effectiveSourceByteCoverage) consume(ctx context.Context, turn *commandTurn, input *effectiveStageBeginCommand, next uint8) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if c == nil || c.self != c || c.turn != turn || turn == nil || c.tx != turn.transaction || c.input != input || input == nil || input.sourceReferences == nil || c.digest != input.digest || c.refs != *input.sourceReferences || c.next != next {
		return ErrInvalid
	}
	if err = effectivePinLive(ctx, turn); err != nil {
		return err
	}
	if turn.transaction.quantum == nil || turn.transaction.quantum.Remaining() != c.remaining {
		return ErrConflict
	}
	ids := [4]stmt.ID{effectiveSourcePinEvidenceCopy, effectiveSourcePinPublicationCopy, effectiveSourcePinReceiptCopy, effectiveSourcePinReceiptCopy}
	if next >= uint8(len(ids)) {
		return ErrInvalid
	}
	step, ok := turn.steps[ids[next]]
	if !ok || !fits(step.work, c.remaining) {
		return ErrInvalid
	}
	if err = turn.requireEffectiveSourceMemory(ctx, c.required); err != nil {
		return err
	}
	c.remaining = subtract(c.remaining, step.work)
	c.next++
	return nil
}

func (c *effectiveSourceByteCoverage) consumeEvidencePinCopy(ctx context.Context, t *commandTurn, input *effectiveStageBeginCommand, p effectiveEvidencePinLengths) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(t), struct{}{}, err) }()
	if c == nil || p != c.evidence {
		return ErrInvalid
	}
	return c.consume(ctx, t, input, 0)
}
func (c *effectiveSourceByteCoverage) consumePublicationPinCopy(ctx context.Context, t *commandTurn, input *effectiveStageBeginCommand, p effectivePublicationPinLengths) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(t), struct{}{}, err) }()
	if c == nil || p != c.publication {
		return ErrInvalid
	}
	return c.consume(ctx, t, input, 1)
}
func (c *effectiveSourceByteCoverage) consumeReceiptPinCopy(ctx context.Context, t *commandTurn, input *effectiveStageBeginCommand, p effectiveReceiptPinLengths) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(t), struct{}{}, err) }()
	if c == nil || c.next < 2 || c.next > 3 || p != c.receipts[c.next-2] {
		return ErrInvalid
	}
	return c.consume(ctx, t, input, c.next)
}

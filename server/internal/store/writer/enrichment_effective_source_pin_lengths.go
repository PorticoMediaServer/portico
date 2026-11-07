package writer

import (
	"context"
	"errors"
	"fmt"
)

// Required schema type contracts in literal SELECT order, not observed SQL types.
// Adopted CHECKs constrain scalar slots. Upper case requires a
// non-NULL value; lower case permits NULL. No table/SQL selector is accepted.
const (
	effectiveEvidencePinLayout    = "TITTITBIT"
	effectivePublicationPinLayout = "TITIITBIT"
	effectiveReceiptPinLayout     = "TBIBBTIIITItiiIBITITTTTTTTTItiITBIIItbitbittIIiI"
	effectivePinMaxColumns        = 48 // saved 44 + separately observed lifecycle 4
)

type effectivePinCell struct {
	kind    byte // T/B/I or zero for actual native NULL
	octets  int64
	integer int64
}

// Amounts describe only these actual stored occurrences. They are NOT admitted
// memory/work and NOT the complete retained closure. Parent must sum E+S+both
// receipts AND every other retained occurrence (including repeated controls),
// enforce the ONE 64KiB control census, and reserve bookkeeping/copy/codec work.
// No caller bool, limit, refund or helper-issued capability authorizes copying.
type effectivePinOctets struct{ text, control, blob, payload uint64 }

type effectivePinProjection struct {
	cells  [effectivePinMaxColumns]effectivePinCell
	octets effectivePinOctets
}

type effectiveSourceSealRef struct {
	owner       string
	incarnation [16]byte
	sequence    int64
	kind        CommandID // exactly EvidenceSeal or CreditSourceSeal; never stage aliases
}

// Snapshot identities below bind inert native facts to their original live turn
// and transaction. They do not establish frozen-request binding or admission.
// The parent-owned copy boundary must consume this SAME value with the same
// frozen input/proof tuple; a fresh turn or a caller-created value is not enough.
type effectiveEvidencePinLengths struct {
	turn       *commandTurn
	tx         *transaction
	evidence   string
	projection effectivePinProjection
}
type effectivePublicationPinLengths struct {
	turn        *commandTurn
	tx          *transaction
	publication string
	projection  effectivePinProjection
}
type effectiveReceiptPinLengths struct {
	turn       *commandTurn
	tx         *transaction
	ref        effectiveSourceSealRef
	projection effectivePinProjection
}

func effectivePinMalformed() error {
	return fmt.Errorf("%w: native pinned seal projection", ErrResolveRequired)
}

// Each preflight cell is native octet_length for TEXT/BLOB, or the actual
// fixed INTEGER/NULL scalar. No variable payload is copied in this phase.
func decodeEffectivePinLengths(raw []any, layout string) (out effectivePinProjection, err error) {
	if len(layout) > effectivePinMaxColumns || len(raw) != len(layout) {
		return out, effectivePinMalformed()
	}
	for i := range layout {
		expected := layout[i]
		nullable := expected >= 'a' && expected <= 'z'
		if nullable {
			expected -= 'a' - 'A'
		}
		if expected != 'T' && expected != 'B' && expected != 'I' {
			return effectivePinProjection{}, effectivePinMalformed()
		}
		cell := effectivePinCell{}
		if raw[i] != nil {
			cell.kind = expected
		} else if !nullable {
			return effectivePinProjection{}, effectivePinMalformed()
		}
		if expected == 'I' && cell.kind != 0 {
			n, ok := raw[i].(int64)
			if !ok {
				return effectivePinProjection{}, effectivePinMalformed()
			}
			cell.integer = n
		}

		if expected == 'T' || expected == 'B' {
			n, ok := raw[i].(int64)
			if cell.kind == 0 {
				if raw[i] != nil {
					return effectivePinProjection{}, effectivePinMalformed()
				}
			} else {
				if !ok || n < 0 {
					return effectivePinProjection{}, effectivePinMalformed()
				}
				cell.octets = n
				// Every stored TEXT occurrence is control here. The generated
				// body purpose marker is fixed-width payload, not stored control.
				if expected == 'T' {
					marker := i == 8 && (layout == effectiveEvidencePinLayout || layout == effectivePublicationPinLayout)
					if marker {
						width := len("evidence_seal")
						if layout == effectivePublicationPinLayout {
							width = len("credit_source_seal")
						}
						if n != int64(width) {
							return effectivePinProjection{}, effectivePinMalformed()
						}
					} else {
						if uint64(n) > MaxSealControlStringBytes-out.octets.control {
							return effectivePinProjection{}, ErrCap
						}
						out.octets.control += uint64(n)
					}
					out.octets.text += uint64(n)
				} else {
					// All BLOBs in these three closed projections are 16 or 32 bytes.
					if n != 16 && n != 32 {
						return effectivePinProjection{}, effectivePinMalformed()
					}
					out.octets.blob += uint64(n)
				}
			}
		}
		out.cells[i] = cell
	}
	// Explicit total: sum EACH TEXT occurrence + sum EACH fixed BLOB occurrence.
	// Stored controls <=64KiB + one fixed purpose marker <=18; BLOBs <=48*32.
	// This addition cannot overflow uint64. Native allocation overhead is separate.
	out.octets.payload = out.octets.text + out.octets.blob
	return out, nil
}

// Cardinality, error and close handling shared by the three literal readers;
// accepts protected rows only, never SQL, a connector or an execution callback.
func effectivePinScanPoint(rows *commandRows, dest []any) (err error) {
	if rows == nil {
		return ErrInvalid
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	if !rows.Next() {
		if err = rows.Err(); err == nil {
			err = ErrResolveRequired
		}
		return err
	}
	if err = rows.Scan(dest...); err != nil {
		return err
	}
	if rows.Next() {
		return effectivePinMalformed()
	}
	return rows.Err()
}

func effectivePinDest(raw []any) []any {
	dest := make([]any, len(raw))
	for i := range raw {
		dest[i] = &raw[i]
	}
	return dest
}

func effectivePinLive(ctx context.Context, turn *commandTurn) error {
	if turn == nil || turn.transaction == nil || turn.transaction.done || !turn.transaction.begun {
		return ErrInvalid
	}
	return turn.checkLifetimes(ctx)
}

func readEffectiveSourceEvidencePinLengths(ctx context.Context, turn *commandTurn, evidence string) (out effectiveEvidencePinLengths, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if err = effectivePinLive(ctx, turn); err != nil {
		return out, err
	}
	if evidence == "" {
		return out, ErrInvalid
	}
	step, err := sealStep(turn, effectiveSourcePinEvidenceLengths)
	if err != nil {
		return out, err
	}
	rows, err := turn.query(ctx, step, evidence)
	if err != nil {
		return out, err
	}
	var raw [9]any // six lengths + three scalars
	if err = effectivePinScanPoint(rows, effectivePinDest(raw[:])); err != nil {
		return out, err
	}
	p, err := decodeEffectivePinLengths(raw[:], effectiveEvidencePinLayout)
	if err != nil {
		return out, err
	}
	if p.cells[6].octets != 16 {
		return out, effectivePinMalformed()
	}
	if p.cells[1].integer <= 0 || p.cells[4].integer != 1 || p.cells[7].integer <= 0 {
		return out, effectivePinMalformed()
	}
	out = effectiveEvidencePinLengths{turn, turn.transaction, evidence, p}
	return out, effectivePinLive(ctx, turn)
}

func readEffectiveSourcePublicationPinLengths(ctx context.Context, turn *commandTurn, publication string) (out effectivePublicationPinLengths, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if err = effectivePinLive(ctx, turn); err != nil {
		return out, err
	}
	if publication == "" {
		return out, ErrInvalid
	}
	step, err := sealStep(turn, effectiveSourcePinPublicationLengths)
	if err != nil {
		return out, err
	}
	rows, err := turn.query(ctx, step, publication)
	if err != nil {
		return out, err
	}
	var raw [9]any // five lengths + four scalars
	if err = effectivePinScanPoint(rows, effectivePinDest(raw[:])); err != nil {
		return out, err
	}
	p, err := decodeEffectivePinLengths(raw[:], effectivePublicationPinLayout)
	if err != nil {
		return out, err
	}
	if p.cells[6].octets != 16 {
		return out, effectivePinMalformed()
	}
	if p.cells[1].integer <= 0 || p.cells[3].integer < 0 || p.cells[3].integer > MaxCastCredits+MaxCrewCredits || p.cells[4].integer != 1 || p.cells[7].integer <= 0 {
		return out, effectivePinMalformed()
	}
	out = effectivePublicationPinLengths{turn, turn.transaction, publication, p}
	return out, effectivePinLive(ctx, turn)
}

// Private dependency lookup uses the EXACT receipt PK obtained from the native
// body pin. It deliberately does not classifyReceipt: ACKed historical proof
// may remain pinned. This returns neither Outcome nor an Applied public result.
func readEffectiveSourceReceiptPinLengths(ctx context.Context, turn *commandTurn, ref effectiveSourceSealRef) (out effectiveReceiptPinLengths, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if err = effectivePinLive(ctx, turn); err != nil {
		return out, err
	}
	if ref.owner == "" || ref.sequence <= 0 || (ref.kind != EvidenceSeal && ref.kind != CreditSourceSeal) {
		return out, ErrInvalid
	}
	step, err := sealStep(turn, effectiveSourcePinReceiptLengths)
	if err != nil {
		return out, err
	}
	rows, err := turn.query(ctx, step, ref.owner, ref.incarnation[:], ref.sequence)
	if err != nil {
		return out, err
	}
	var raw [48]any // 26 lengths + 22 scalars
	if err = effectivePinScanPoint(rows, effectivePinDest(raw[:])); err != nil {
		return out, err
	}
	p, err := decodeEffectivePinLengths(raw[:], effectiveReceiptPinLayout)
	if err != nil {
		return out, err
	}
	if err = effectiveReceiptPinShape(p, ref.kind); err != nil {
		return out, err
	}
	if err = effectiveReceiptPinFixedShape(p, ref); err != nil {
		return out, err
	}
	out = effectiveReceiptPinLengths{turn, turn.transaction, ref, p}
	return out, effectivePinLive(ctx, turn)
}

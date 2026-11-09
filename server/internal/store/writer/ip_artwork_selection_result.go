package writer

import "math"

// W result wire for 226/227: shared identity/request prefix, the native purpose
// and the actual command variant, then the exact owner choice and its history
// pointer. Source, blob and material rows are never relabelled as a result.
func preflightIPArtworkSelectionResult(v ipArtworkSelectionSavedResult) error {
	r := v.Value
	if !ipArtworkSelectionKind(v.Kind) || r.OwnerID <= 0 || !ipArtworkSelectionRole(r.Role) ||
		r.Choice != "locked" || r.SelectionRevision <= 0 || r.SelectionRevision == math.MaxInt64 ||
		r.Count < 0 || r.Count > 1 || r.AppliedMS < 0 ||
		!ipArtworkSelectionMaybeDigest(r.Previous) || !ipArtworkSelectionMaybeDigest(r.Selected) ||
		(v.Kind == IPArtworkClear && r.Selected != "") ||
		(r.Count == 0 && r.Selected != r.Previous) ||
		r.History.Present != (r.Count == 1) ||
		(r.History.Present && (r.History.Value <= 0 || r.History.Value == math.MaxInt64)) {
		return ErrInvalid
	}
	return nil
}

func ipArtworkSelectionResultDigest(kind CommandID, identity CommandIdentity, request RequestDigest, value ipArtworkSelectionResult) (ResultDigest, error) {
	return canonicalResultDigest(identity, request, ipArtworkSelectionSavedResult{kind, value})
}

func writeIPArtworkSelectionResult(e *requestEncoder, i CommandIdentity, q RequestDigest, v ipArtworkSelectionSavedResult) error {
	e.bytes([]byte(canonicalResultCodec))
	e.bytes([]byte{0, 1})
	e.text("ip_artwork_selection")
	e.text(ipArtworkSelectionCommandKind(v.Kind))
	e.text("applied")
	e.bytes(i.Incarnation[:])
	e.number(uint64(i.Sequence))
	e.bytes(i.OperationID[:])
	e.bytes(q[:])
	r := v.Value
	e.number(uint64(r.OwnerID))
	e.text(r.Role)
	e.text(r.Choice)
	e.number(uint64(r.SelectionRevision))
	e.number(uint64(r.Count))
	e.text(r.Previous)
	e.text(r.Selected)
	e.flag(r.History.Present)
	e.number(uint64(r.History.Value))
	e.number(uint64(r.AppliedMS))
	return e.err
}

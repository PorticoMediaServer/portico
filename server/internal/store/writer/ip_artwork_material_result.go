package writer

import "unicode/utf8"

// W result wire: shared identity/request prefix, the native purpose and the
// actual command variant, then the durable intent head. The manifest slots stay
// in their own table and are never relabeled as a result or proof.
func preflightIPArtworkMaterialResult(v ipArtworkMaterialSavedResult) error {
	r := v.Value
	if !ipArtworkMaterialKind(v.Kind) || len(r.IntentID) == 0 ||
		len(r.IntentID) > ipArtworkMaterialMaxIntent || !utf8.ValidString(r.IntentID) ||
		r.Revision != ipArtworkMaterialRevision(v.Kind) || (r.Slots != 3 && r.Slots != 4) ||
		r.JobGeneration <= 0 || r.Bytes < 0 || r.Bytes > ipArtworkMaterialMaxBytes {
		return ErrInvalid
	}
	if v.Kind == IPArtworkMaterialIntent && r.Bytes != 0 {
		return ErrInvalid
	}
	if v.Kind == IPArtworkMaterialInstalled && r.Bytes == 0 {
		return ErrInvalid
	}
	return nil
}

func ipArtworkMaterialResultDigest(kind CommandID, identity CommandIdentity, request RequestDigest, value ipArtworkMaterialResult) (ResultDigest, error) {
	return canonicalResultDigest(identity, request, ipArtworkMaterialSavedResult{kind, value})
}

func writeIPArtworkMaterialResult(e *requestEncoder, i CommandIdentity, q RequestDigest, v ipArtworkMaterialSavedResult) error {
	e.bytes([]byte(canonicalResultCodec))
	e.bytes([]byte{0, 1})
	e.text("ip_artwork_material")
	e.text(ipArtworkMaterialKindName(v.Kind))
	e.text("applied")
	e.bytes(i.Incarnation[:])
	e.number(uint64(i.Sequence))
	e.bytes(i.OperationID[:])
	e.bytes(q[:])
	r := v.Value
	e.text(r.IntentID)
	for _, n := range []int64{r.Revision, r.Slots, r.Bytes, r.JobGeneration} {
		e.number(uint64(n))
	}
	return e.err
}

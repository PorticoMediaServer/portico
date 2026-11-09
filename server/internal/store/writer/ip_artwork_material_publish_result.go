package writer

import "unicode/utf8"

// W result wire: shared identity/request prefix, the native purpose and the
// actual command variant, then the published intent head. The blobs and source
// stay in their own tables and are never relabelled as a result or proof.
func preflightIPArtworkMaterialPublishResult(v ipArtworkMaterialPublishSavedResult) error {
	r := v.Value
	if v.Kind != IPArtworkMaterialPublish || len(r.IntentID) == 0 ||
		len(r.IntentID) > ipArtworkMaterialMaxIntent || !utf8.ValidString(r.IntentID) ||
		r.Revision != ipArtworkMaterialPublishRevision || (r.Slots != 3 && r.Slots != 4) ||
		r.Bytes < 1 || r.Bytes > ipArtworkMaterialMaxBytes || r.JobGeneration <= 0 {
		return ErrInvalid
	}
	return nil
}

func writeIPArtworkMaterialPublishResult(e *requestEncoder, i CommandIdentity, q RequestDigest, v ipArtworkMaterialPublishSavedResult) error {
	e.bytes([]byte(canonicalResultCodec))
	e.bytes([]byte{0, 1})
	e.text("ip_artwork_material")
	e.text("publish")
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

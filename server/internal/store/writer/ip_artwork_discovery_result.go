package writer

// W result wire: shared identity/request prefix, the native purpose and the
// actual command variant, then the POST-CAS stage head as unsigned64 fields.
// The source manifest and original request keep their independent domains.
func preflightIPArtworkDiscoveryResult(v ipArtworkDiscoverySavedResult) error {
	r := v.Value
	if !ipArtworkDiscoveryKind(v.Kind) || r.StageID <= 0 || r.Revision <= 0 ||
		r.Count < 0 || r.Count > ipArtworkDiscoveryMaxCandidates || r.AfterOrdinal != r.Count {
		return ErrInvalid
	}
	if v.Kind == IPArtworkDiscoveryClaim && (r.Revision != 1 || r.Count != 0) {
		return ErrInvalid
	}
	return nil
}

func ipArtworkDiscoveryKindName(kind CommandID) string {
	switch kind {
	case IPArtworkDiscoveryClaim:
		return "claim"
	case IPArtworkDiscoveryPage:
		return "page"
	case IPArtworkDiscoveryFinish:
		return "finish"
	}
	return ""
}

func writeIPArtworkDiscoveryResult(e *requestEncoder, i CommandIdentity, q RequestDigest, v ipArtworkDiscoverySavedResult) error {
	e.bytes([]byte(canonicalResultCodec))
	e.bytes([]byte{0, 1})
	e.text("ip_artwork_discovery")
	e.text(ipArtworkDiscoveryKindName(v.Kind))
	e.text("applied")
	e.bytes(i.Incarnation[:])
	e.number(uint64(i.Sequence))
	e.bytes(i.OperationID[:])
	e.bytes(q[:])
	r := v.Value
	for _, n := range []int64{r.StageID, r.Revision, r.Count, r.AfterOrdinal} {
		e.number(uint64(n))
	}
	return e.err
}

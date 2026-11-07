package writer

// W result wire: shared identity prefix, purpose ip_artwork_discovery, actual
// command uint16, stage/revision/count/afterOrdinal as four unsigned64 fields.
// The source manifest and original request retain their independent domains.
func preflightIPArtworkDiscoveryResult(v ipArtworkDiscoverySavedResult) error {
	r := v.Value
	if (v.Kind != IPArtworkDiscoveryClaim && v.Kind != IPArtworkDiscoveryPage && v.Kind != IPArtworkDiscoveryFinish) ||
		r.stageID <= 0 || r.revision <= 0 || r.count < 0 || r.afterOrdinal != r.count {
		return ErrInvalid
	}
	return nil
}

func writeIPArtworkDiscoveryResult(e *requestEncoder, i CommandIdentity, q RequestDigest, v ipArtworkDiscoverySavedResult) error {
	writeIPResultIdentity(e, "ip_artwork_discovery", i, q)
	e.bytes([]byte{byte(v.Kind >> 8), byte(v.Kind)})
	r := v.Value
	for _, n := range []int64{r.stageID, r.revision, r.count, r.afterOrdinal} {
		e.number(uint64(n))
	}
	return e.err
}

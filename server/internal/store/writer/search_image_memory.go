package writer

// searchImageFreshRequiredBytes is an allocation-free prospective Go peak,
// not a grant or a native/index/idle allowance. Four complete wire envelopes
// cover the retained immutable packet, request rehash TEXT conversions, bound
// values/native TEXT transport and copied native observations/results. The
// fixed 64KiB covers the finite argument/row/encoder/hash bookkeeping. Fresh
// text observations are SQL-filtered to the captured values before crossing
// into Go; raw source titles are not fetched again. W must reserve its separate
// native/common partition in the SAME grant, in addition to this amount.
func searchImageFreshRequiredBytes(c *searchImageStageCommand) (uint64, error) {
	if c == nil || !c.valid {
		return 0, ErrInvalid
	}
	r := c.request
	texts := [...]string{r.variant, r.bank.key, r.bank.role, r.bank.profile.RecipeVersion,
		r.bank.profile.EngineVersion, r.bank.profile.UnicodeFingerprint, r.source.title,
		r.owner.recipe, r.owner.phase, r.owner.contentDigest, r.header.titleFold,
		r.header.titlePrefixTokens, r.header.initialPrefixTokens}
	wire := uint64(searchImageStageFixedBytes)
	for _, value := range texts {
		if uint64(len(value)) > maxCanonicalRequestBytes-wire {
			return 0, invalid("search_image_text_bytes")
		}
		wire += uint64(len(value))
	}
	// The 32MiB complete-wire cap independently prevents arithmetic overflow.
	return 65536 + 4*wire, nil
}

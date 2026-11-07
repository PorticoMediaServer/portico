package writer

// Version745/749 belongs to prepared_media_versions, not its mutable job or
// a generic media receipt. W obtains this original scalar projection in the
// same fresh command turn for W's sole result codec; replay uses the saved W payload.
// This commitment is a typed draft control, never the persisted codec digest.
type mpVersionResult struct {
	cleanupRequest, cleanupExecution                                        string
	cleanupGeneration, cleanupCursor                                        int64
	cleanupFinished, physicalExited, cleaned                                bool
	version, job, sourceRevision, transformation, digest, state, retirement string
	revision, size                                                          int64
	physicalPending                                                         bool
}
type mpVersionReceipt struct {
	header mpPrepReceiptHeader
	result mpVersionResult
}

func mpVersionResultDigest(v mpVersionReceipt) (ResultDigest, error) {
	h, r := v.header, v.result
	if !mpPrepTexts(r.cleanupRequest) || !mpPrepText(r.cleanupExecution, true) || r.cleanupGeneration < 0 || r.cleanupCursor < 0 || (r.cleanupGeneration == 0) != (r.cleanupExecution == "") || r.cleaned && !r.physicalExited || r.cleanupFinished && (!r.cleaned || r.cleanupGeneration == 0) || r.physicalPending == r.cleanupFinished {
		return ResultDigest{}, ErrResolveRequired
	}
	if h.kind != 745 && h.kind != 749 || !mpPrepTexts(r.version, r.job) || !lowerHex(r.sourceRevision, 64) || !lowerHex(r.transformation, 64) || !lowerHex(r.digest, 64) || !mpPrepRevision(r.revision) || r.size <= 0 || r.retirement != "live" && r.retirement != "draining" && r.retirement != "retired" {
		return ResultDigest{}, ErrResolveRequired
	}
	if h.kind == 745 && (r.state != "deleting" || !r.physicalPending || r.cleanupGeneration != 0 || r.cleanupCursor != 0 || !r.physicalExited || !r.cleaned) || h.kind == 749 && (r.state != "deleting" && r.state != "deleted" || r.cleanupFinished != (r.state == "deleted")) {
		return ResultDigest{}, ErrResolveRequired
	}
	e, err := mpPrepReceiptEncoder(h, "mp_prepared_version")
	if err != nil {
		return ResultDigest{}, err
	}
	for _, s := range []string{r.version, r.job, r.sourceRevision, r.transformation, r.digest, r.state, r.retirement, r.cleanupRequest, r.cleanupExecution} {
		e.text(s)
	}
	e.number(uint64(r.revision))
	e.number(uint64(r.size))
	e.number(uint64(r.cleanupGeneration))
	e.number(uint64(r.cleanupCursor))
	for _, b := range []bool{r.cleanupFinished, r.physicalExited, r.cleaned} {
		if b {
			e.number(1)
		} else {
			e.number(0)
		}
	}
	if r.physicalPending {
		e.number(1)
	} else {
		e.number(0)
	}
	if e.err != nil {
		return ResultDigest{}, e.err
	}
	var d ResultDigest
	copy(d[:], e.h.Sum(nil))
	return d, nil
}

package writer

// Data-only contracts for native MP worker and UO parent results through W's sole codec.
// These routines neither serialize nor hash results, choose a receipt owner,
// admit memory, persist a header/payload, advance sequence/floor/debt or ACK.
// W must preflight the actual versioned encoded payload within each role cap.
const (
	mpSessionResultPayloadCap      int64 = 64 << 10
	mpSourcePinResultPayloadCap    int64 = 64 << 10
	mpHLSResultPayloadCap          int64 = 64 << 10
	mpInstallationResultPayloadCap int64 = 64 << 10
)

type mpMediaResultDataBounds struct{ textBytes, binaryBytes, scalarBytes, payloadByteCap int64 }

func (b *mpMediaResultDataBounds) texts(values ...string) error {
	for _, v := range values {
		if !mpPrepText(v, true) || int64(len(v)) > b.payloadByteCap-b.textBytes-b.binaryBytes-b.scalarBytes {
			return ErrInvalid
		}
		b.textBytes += int64(len(v))
	}
	return nil
}
func mpPhysicalExitTexts(x mpPhysicalExitFacts) []string {
	return []string{x.retirementRequest, x.exitRequest}
}
func mpSessionResultDataBounds(r mpSessionEffect) (b mpMediaResultDataBounds, err error) {
	b = mpMediaResultDataBounds{binaryBytes: 96, scalarBytes: 23*8 + 3, payloadByteCap: mpSessionResultPayloadCap}
	if !mpPrepTexts(r.session, r.media, r.asset, r.state) || r.sessionIncarnation == ([16]byte{}) || r.mediaIncarnation == ([16]byte{}) || r.item <= 0 || r.part < 0 || !mpPrepRevision(r.revision) || r.generation <= 0 || r.mediaGeneration <= 0 || r.presentationGeneration <= 0 || r.physicalRevision <= 0 || r.leaseMS <= 0 || r.grantExpiresMS <= 0 || r.positionMS < 0 || r.startMS < 0 || r.transportSequence < 0 || r.endedMS < 0 || r.cleanupCursor < 0 || !mpPresentationURL(r.presentationURL) || (r.engine != "native" && r.engine != "mpv") || !r.physicalExit.valid(r.mediaGeneration, r.physicalPending) {
		return b, ErrInvalid
	}
	if err = b.texts(r.session, r.media, r.asset, r.state, r.mode, r.engine, r.presentationURL, r.endReason, r.stopExecution, r.queue, r.entry); err != nil {
		return
	}
	if err = b.texts(mpPhysicalExitTexts(r.physicalExit)...); err != nil {
		return
	}
	old := r.retiredMedia
	if old.media == "" {
		if old != (mpSessionRetireProjection{}) {
			return b, ErrInvalid
		}
	} else {
		if !mpPrepTexts(old.media, old.execution, old.sourceRequest, old.asset) || old.incarnation == ([16]byte{}) || old.generation <= 0 || old.item <= 0 || !old.exit.valid(old.generation, old.physicalPending) {
			return b, ErrInvalid
		}
	}
	if err = b.texts(old.media, old.execution, old.sourceRequest, old.asset); err != nil {
		return
	}
	err = b.texts(mpPhysicalExitTexts(old.exit)...)
	return
}
func mpSourcePinResultDataBounds(r mpSourcePinEffect) (b mpMediaResultDataBounds, err error) {
	b = mpMediaResultDataBounds{binaryBytes: 32, scalarBytes: 9*8 + 1 + 3, payloadByteCap: mpSourcePinResultPayloadCap}
	if !mpPrepTexts(r.media, r.execution, r.asset, r.state) || r.mediaIncarnation == ([16]byte{}) || r.revision <= 0 || r.generation <= 0 || r.kind < mpInventoryPin || r.kind > mpGrantPin || r.resourceRevision < 0 || r.size < 0 || r.configurationGeneration < 0 || r.expiresMS < 0 || !r.physicalExit.valid(r.generation, r.physicalPending) {
		return b, ErrInvalid
	}
	if err = b.texts(r.media, r.execution, r.asset, r.resource, r.version, r.source, r.incarnation, r.digest, r.grantHash, r.grantToken, r.expiresAt, r.ownerSession, r.originVersion, r.state, r.mode); err != nil {
		return
	}
	err = b.texts(mpPhysicalExitTexts(r.physicalExit)...)
	return
}
func mpHLSResultDataBounds(r mpHLSEffect) (b mpMediaResultDataBounds, err error) {
	b = mpMediaResultDataBounds{binaryBytes: 32, scalarBytes: 18*8 + 1 + 5, payloadByteCap: mpHLSResultPayloadCap}
	if !mpPrepTexts(r.media, r.execution, r.asset, r.state) || r.incarnation == ([16]byte{}) || r.revision <= 0 || r.generation <= 0 || r.windowGeneration < 0 || r.windowRevision < 0 || r.reservedBytes < 0 || r.size < 0 || r.cleanupCursor < 0 || r.highestProduced < -1 || r.highestServed < -1 || !r.physicalExit.valid(r.generation, r.physicalPending) {
		return b, ErrInvalid
	}
	if r.windowExecution != "" && (r.windowGeneration <= 0 || r.windowRevision <= 0 || r.windowStart < 0 || r.windowEnd < r.windowStart || r.windowEnd-r.windowStart >= 900 || !lowerHex(r.planDigest, 64)) {
		return b, ErrInvalid
	}
	if err = b.texts(r.media, r.execution, r.windowExecution, r.planDigest, r.artifactDigest, r.artifactName, r.rendition, r.backend, r.status, r.exitDigest, r.readPin, r.sourceVersion, r.state, r.asset, r.mode); err != nil {
		return
	}
	err = b.texts(mpPhysicalExitTexts(r.physicalExit)...)
	return
}
func mpInstallationResultDataBounds(r mpSessionDeliveryProjection) (b mpMediaResultDataBounds, err error) {
	b.payloadByteCap = mpInstallationResultPayloadCap
	add := func(x mpMediaResultDataBounds) error {
		if x.textBytes+x.binaryBytes+x.scalarBytes > b.payloadByteCap-b.textBytes-b.binaryBytes-b.scalarBytes {
			return ErrInvalid
		}
		b.textBytes += x.textBytes
		b.binaryBytes += x.binaryBytes
		b.scalarBytes += x.scalarBytes
		return nil
	}
	// Fixed role slots, each at most ONE. This is no generic response/command list.
	for _, p := range []mpSourcePinEffect{r.prepared, r.subtitle, r.disc, r.receiver, r.remote} {
		if p.media == "" {
			if p != (mpSourcePinEffect{}) {
				return b, ErrInvalid
			}
			continue
		}
		x, e := mpSourcePinResultDataBounds(p)
		if e != nil {
			return b, e
		}
		if err = add(x); err != nil {
			return
		}
	}
	for _, p := range []mpHLSEffect{r.delivery, r.reservation} {
		if p.media == "" {
			if p != (mpHLSEffect{}) {
				return b, ErrInvalid
			}
			continue
		}
		x, e := mpHLSResultDataBounds(p)
		if e != nil {
			return b, e
		}
		if err = add(x); err != nil {
			return
		}
	}
	if r.physicalRevision <= 0 {
		return b, ErrInvalid
	}
	if b.textBytes+b.binaryBytes+b.scalarBytes > b.payloadByteCap-8 {
		return b, ErrInvalid
	}
	b.scalarBytes += 8
	return
}

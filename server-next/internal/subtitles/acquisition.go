package subtitles

type sidecarBytes struct{ Data, Companion []byte }

func SidecarFingerprint(raw, companion []byte) string {
	if len(companion) == 0 {
		return digestBytes(raw)
	}
	return requestDigest([]any{digestBytes(raw), digestBytes(companion)})
}

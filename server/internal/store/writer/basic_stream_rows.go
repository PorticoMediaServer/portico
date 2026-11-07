package writer

import (
	"encoding/json"
	"strings"
)

type basicStreamRow struct {
	index, channels                                                                          int64
	streamType, codec, language, title, channelLayout, colorTransfer, colorPrimaries, detail string
	isDefault, isForced                                                                      bool
}
type basicStreamBreadth struct {
	index, fpsNumerator, fpsDenominator                                                                             int64
	audioDescription, telecine, doviRPU, doviBase, doviKnown                                                        bool
	audioVariant, chroma, frameMode, interlace, doviEnhancement, doviCM, playbackFramesRevision, packetPeakRevision string
}

func (basicStreamRow) isBasicMediaRow()     {}
func (basicStreamBreadth) isBasicMediaRow() {}
func basicStreamClass(t string) StreamClass {
	switch t {
	case "audio":
		return AudioStream
	case "video":
		return VideoStream
	case "subtitle":
		return SubtitleStream
	}
	return 0
}
func validateBasicStreamRow(r basicStreamRow) error {
	if r.index < 0 || r.channels < 0 || basicStreamClass(r.streamType) == 0 || (r.detail != "" && !json.Valid([]byte(r.detail))) {
		return ErrInvalid
	}
	return nil
}
func validateBasicStreamBreadth(r basicStreamBreadth) error {
	if r.index < 0 || r.fpsNumerator < 0 || r.fpsDenominator < 0 || (r.frameMode != "" && r.frameMode != "constant" && r.frameMode != "variable") || (r.doviEnhancement != "" && r.doviEnhancement != "mel" && r.doviEnhancement != "fel") {
		return ErrInvalid
	}
	return nil
}
func cloneBasicStreamRow(r basicStreamRow) basicStreamRow {
	r.streamType = strings.Clone(r.streamType)
	r.codec = strings.Clone(r.codec)
	r.language = strings.Clone(r.language)
	r.title = strings.Clone(r.title)
	r.channelLayout = strings.Clone(r.channelLayout)
	r.colorTransfer = strings.Clone(r.colorTransfer)
	r.colorPrimaries = strings.Clone(r.colorPrimaries)
	r.detail = strings.Clone(r.detail)
	return r
}
func cloneBasicStreamBreadth(r basicStreamBreadth) basicStreamBreadth {
	r.audioVariant = strings.Clone(r.audioVariant)
	r.chroma = strings.Clone(r.chroma)
	r.frameMode = strings.Clone(r.frameMode)
	r.interlace = strings.Clone(r.interlace)
	r.doviEnhancement = strings.Clone(r.doviEnhancement)
	r.doviCM = strings.Clone(r.doviCM)
	r.playbackFramesRevision = strings.Clone(r.playbackFramesRevision)
	r.packetPeakRevision = strings.Clone(r.packetPeakRevision)
	return r
}

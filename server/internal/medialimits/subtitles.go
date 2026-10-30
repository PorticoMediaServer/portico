// Package medialimits projects owner resource limits for media readers.
package medialimits

import "sync/atomic"

const DefaultSubtitleTracks = 128
const DefaultSubtitleFileMiB = 128
const MaxSubtitleTracks = 4096
const MaxSubtitleFileMiB = 512

var tracks atomic.Int64
var fileMiB atomic.Int64

func ConfigureSubtitles(trackLimit, sizeMiB int) {
	if trackLimit < 32 || trackLimit > MaxSubtitleTracks {
		trackLimit = DefaultSubtitleTracks
	}
	if sizeMiB < 32 || sizeMiB > MaxSubtitleFileMiB {
		sizeMiB = DefaultSubtitleFileMiB
	}
	tracks.Store(int64(trackLimit))
	fileMiB.Store(int64(sizeMiB))
}
func SubtitleTracks() int {
	n := tracks.Load()
	if n == 0 {
		n = DefaultSubtitleTracks
	}
	return int(n)
}
func SubtitleFileBytes() int {
	n := fileMiB.Load()
	if n == 0 {
		n = DefaultSubtitleFileMiB
	}
	return int(n) << 20
}
func SubtitleAssetBytes() int { return SubtitleFileBytes() * 3 / 2 }

package preparedmedia

import (
	"math"
	"github.com/PorticoMediaServer/portico/server/internal/audiotags"
)

// Keep WavPack's observed header mode with the very same retained source
// passed to the encoder. A codec name or a text tag cannot establish it.
func musicSourceFacts(original probed, tags audiotags.Tags, size int64) probed {
	if original.AudioCodec == "wavpack" {
		original.AudioLossless = tags.Lossless
		// Like download selection, use measured source byte-rate when the
		// single audio stream has no codec bitrate. This keeps hybrid-mode
		// planning and encoding consistent without asserting a stream rate.
		if original.AudioBitrateBPS <= 0 && original.AudioTracks == 1 && original.VideoStreams == 0 && size > 0 && original.Duration > 0 {
			rate := float64(size) * 8 / original.Duration
			if !math.IsNaN(rate) && !math.IsInf(rate, 0) && rate > 0 && rate < float64(int(^uint(0)>>1)) {
				original.AudioBitrateBPS = int(rate)
			}
		}
	}
	return original
}

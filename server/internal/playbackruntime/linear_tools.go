package playbackruntime

import "github.com/PorticoMediaServer/portico/server/internal/decoder"

// decoderLibraries lists the sandbox's exact dependency files, but only when
// the sandbox is in use: with the baseline the loader finds the libraries
// itself, so a resolution failure must not disable Live TV.
func decoderLibraries(ffmpeg, ffprobe string, extra []string) ([]string, error) {
	if !linearSandboxInUse(ffmpeg) {
		return append([]string{}, extra...), nil
	}
	return decoder.ResolveLibraries(ffmpeg, ffprobe, extra)
}

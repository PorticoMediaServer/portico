package localmetadata

import (
	"strconv"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
)

func artworkExtensionPriority(extension string) int64 {
	for i, value := range []string{".jpg", ".jpeg", ".png", ".tbn", ".webp"} {
		if extension == value {
			return int64(i)
		}
	}
	return -1
}

// Canonical aliases retain the existing primary discovery precedence. Numeric
// suffixes sort by their actual integer, independently of lexicographic names.
// Letters and unrepresentably large suffixes remain accepted at rest rank.
func artworkNameRank(name, extension string, bases []string, letter bool, canonicalBase int64) (int64, bool) {
	name = strings.ToLower(name)
	ext := artworkExtensionPriority(extension)
	for index, base := range bases {
		if name == strings.ToLower(base) {
			return canonicalBase + int64(index)*8 + ext, true
		}
	}
	best, matched := assets.LocalArtworkUnknownRank, false
	for _, base := range bases {
		if !numberedArtName(name, base, letter) {
			continue
		}
		matched = true
		tail := strings.TrimPrefix(name, strings.ToLower(base))
		if !strings.HasPrefix(tail, "-") {
			continue
		}
		number, err := strconv.ParseUint(tail[1:], 10, 63)
		if err != nil || number > uint64(assets.LocalArtworkUnknownRank-assets.LocalArtworkNumberedRankBase-1) {
			continue
		}
		rank := assets.LocalArtworkNumberedRankBase + int64(number)
		if rank < best {
			best = rank
		}
	}
	return best, matched
}

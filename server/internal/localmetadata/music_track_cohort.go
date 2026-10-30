package localmetadata

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/text/unicode/norm"
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/naming"
)

// MusicTrackListing is a complete, revision-checked directory observation from
// the scan. It permits filename evidence, not reads of the named files.
type MusicTrackListing func(directory string) ([]string, bool)
type musicTrackListingKey struct{}

func WithMusicTrackListing(ctx context.Context, listed MusicTrackListing) context.Context {
	return context.WithValue(ctx, musicTrackListingKey{}, listed)
}

func musicPackedTrackEvidence(ctx context.Context, path string, f assets.Facts) assets.Facts {
	// Any explicit tag or cue is stronger evidence than a cohort inference.
	if strings.TrimSpace(f.Tags["disc"]) != "" || strings.TrimSpace(f.Tags["track"]) != "" || len(f.MusicCueTracks) != 0 {
		return f
	}
	if _, disc := naming.DiscFolder(filepath.Base(filepath.Dir(path))); disc {
		return f
	}
	listed, ok := ctx.Value(musicTrackListingKey{}).(MusicTrackListing)
	if !ok || listed == nil {
		return f
	}
	names, known := listed(filepath.Dir(path))
	if !known || len(names) > naming.MaxPackedTrackCohort {
		return f
	}
	current := strings.ToLower(norm.NFC.String(filepath.Base(path)))
	numbers := make([]int, 0, len(names))
	own := 0
	for _, name := range names {
		if filepath.Base(name) != name {
			return f
		}
		reading := naming.Read(name, "music")
		if len(reading.TrackNumbers) != 1 || len(reading.DiscNumbers) != 0 {
			return f
		}
		number := reading.TrackNumbers[0].Value
		numbers = append(numbers, number)
		if strings.ToLower(norm.NFC.String(name)) == current {
			own = number
		}
	}
	if own == 0 {
		return f
	}
	cohort, clear := naming.PackedTrackCohort(numbers, false)
	if !clear {
		return f
	}
	number := cohort[own]
	f = withEvidence(f, "disc", strconv.Itoa(number.Disc), "filename")
	return withEvidence(f, "track", strconv.Itoa(number.Track), "filename")
}

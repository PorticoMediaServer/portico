package streaming

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/libraryfile"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediaexec"
	"github.com/PorticoMediaServer/portico/server-next/internal/subtitles"
)

// JoinedPart is one file of a title in several files that plays as one (an audiobook in
// parts), as the scanner recorded it.
type JoinedPart struct {
	Path     string
	Size     int64
	Modified time.Time
	// DurationUS is the part's packet length. Converted parts fill the encoder
	// padding with silence so that the clock is the same whichever route plays.
	DurationUS int64
	// Shape is the codec, sample rate, channels and layout of the part's sound. Parts whose
	// shapes are all the same are joined as they are; parts that differ are converted to one
	// stream, since no player can be handed one stream that changes codec half way.
	Shape string
}

// PartsDiffer says a title's parts are not all encoded alike, so they cannot be copied into
// one stream.
func PartsDiffer(parts []JoinedPart) bool {
	for _, part := range parts[1:] {
		if part.Shape != parts[0].Shape {
			return true
		}
	}
	return false
}

// JoinedInput is one part of a title as the converter reads it: its file, and where it sits
// in the whole title, in seconds.
type JoinedInput struct {
	Path             string
	Offset, Duration float64
}

// joinedBatch is the most parts one converter run is handed. The sandbox gives a tool at most
// 32 files, and a book in one file per chapter can have hundreds: a run converts a batch and
// ends on a segment boundary inside its last part, and the next segment's request starts the
// next run there. The files held stay the same however long the book is.
const joinedBatch = 24

// joinedBatchEnd is where a run from position over parts first.. ends in the whole title, in
// seconds, and the part after its last: 0 when the batch reaches the end of the title, else
// the last segment boundary inside the batch's last part, so the next run starts on the grid.
func joinedBatchEnd(parts []JoinedPart, first int, offset, position float64) (until float64, last int) {
	end := offset
	last = first
	for last < len(parts) && last-first < joinedBatch {
		end += float64(parts[last].DurationUS) / 1e6
		last++
	}
	for last < len(parts) {
		until = math.Floor(end/HLSSegmentSeconds) * HLSSegmentSeconds
		if until > position {
			return until, last
		}
		// Parts so short that the batch does not reach the next boundary: one more, as far as
		// the sandbox allows.
		if last-first >= 31 {
			return end, last
		}
		end += float64(parts[last].DurationUS) / 1e6
		last++
	}
	return 0, last
}

// joinedInput opens one bounded batch, on the whole book's clock.
func joinedInput(parts []JoinedPart, roots func() []string) inputOpener {
	return func(ctx context.Context, position float64) (*PlaybackInput, subtitles.WindowInput, error) {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		// The part holding the run's start, and where in it the start is.
		first, offset := 0, 0.0
		for first+1 < len(parts) && offset+float64(parts[first].DurationUS)/1e6 <= position {
			offset += float64(parts[first].DurationUS) / 1e6
			first++
		}
		until, last := joinedBatchEnd(parts, first, offset, position)
		if !PartsDiffer(parts) {
			// HLS cuts relative to the run's first packet. Stop at the very next
			// grid line so a restart cannot shift a later cut by one frame.
			whole := 0.0
			for _, part := range parts {
				whole += float64(part.DurationUS) / 1e6
			}
			cut := position + HLSSegmentSeconds
			if whole-cut < hlsShortestTail {
				cut = whole // Keep a folded last tail in its last listed slot.
			}
			until = cut
			if cut == whole {
				until = 0
			}
			// Open just the parts needed for that slot, within the existing bound.
			end, needed := offset, first
			for needed < last && end < cut {
				end += float64(parts[needed].DurationUS) / 1e6
				needed++
			}
			last = needed
		}
		var files []*os.File
		closeAll := func() {
			for _, f := range files {
				f.Close()
			}
		}
		for _, part := range parts[first:last] {
			file, err := libraryfile.Open(part.Path, roots())
			if err != nil {
				closeAll()
				return nil, nil, ErrSourceUnavailable
			}
			files = append(files, file)
			if info, err := file.Stat(); err != nil || part.Size > 0 && info.Size() != part.Size ||
				!part.Modified.IsZero() && !info.ModTime().Equal(part.Modified) {
				closeAll()
				return nil, nil, ErrSourceChanged
			}
		}
		if PartsDiffer(parts) {
			// Parts not encoded alike: each part of the batch an input of its own (descriptor
			// 3 on), from the part holding the start; the converter seeks within that one,
			// decoding, and joins them.
			var joined []JoinedInput
			at := offset
			for n, part := range parts[first:last] {
				name := fmt.Sprintf("/dev/fd/%d", 3+n)
				if runtime.GOOS == "windows" {
					name = files[n].Name()
				}
				joined = append(joined, JoinedInput{Path: name, Offset: at, Duration: float64(part.DurationUS) / 1e6})
				at += float64(part.DurationUS) / 1e6
			}
			return &PlaybackInput{File: files[0], Argument: joined[0].Path, Inherit: runtime.GOOS != "windows",
				Options: []string{"-format_whitelist", mediaexec.LocalInputFormats}, Extra: files[1:], Joined: joined, Positioned: true,
				Until: until}, nil, nil
		}
		// Parts alike: one list for FFmpeg's concat demuxer, from the start of the part holding
		// the run's start, each part with its stored length. The run is cut at its start after
		// the list is read (Skip), not by seeking the demuxer: a copied demuxer cannot be sought
		// (it starts from the beginning whatever it is asked), and an in-point keeps the frame
		// that straddles it, which the run before has already played (26 ms heard twice). Cut
		// after, the run's first frame is the one after the last frame of the run before.
		var list strings.Builder
		list.WriteString("ffconcat version 1.0\n")
		for n, part := range parts[first:last] {
			// The list is descriptor 3, so the parts are 4 on.
			path := fmt.Sprintf("/dev/fd/%d", 4+n)
			if runtime.GOOS == "windows" {
				path = files[n].Name()
			}
			fmt.Fprintf(&list, "file '%s'\nduration %.6f\n", strings.ReplaceAll(path, "'", `'\''`), float64(part.DurationUS)/1e6)
		}
		listFile, err := os.CreateTemp("", "portico-parts-*.ffconcat")
		if err == nil && runtime.GOOS != "windows" {
			// Unlinked at once: the converter reads it through its descriptor.
			os.Remove(listFile.Name())
		}
		if err == nil {
			if _, err = listFile.WriteString(list.String()); err == nil {
				_, err = listFile.Seek(0, 0)
			}
		}
		if err != nil {
			if listFile != nil {
				listFile.Close()
				if runtime.GOOS == "windows" {
					os.Remove(listFile.Name())
				}
			}
			closeAll()
			return nil, nil, err
		}
		// The concat demuxer, and under it only the single-file formats a library file may be.
		options := []string{"-f", "concat", "-safe", "0", "-format_whitelist", mediaexec.LocalInputFormats + ",concat"}
		input := &PlaybackInput{File: listFile, Argument: "/dev/fd/3", Inherit: true, Options: options, Extra: files, Positioned: true,
			Skip: position - offset, Until: until}
		if runtime.GOOS == "windows" {
			input.Argument, input.Inherit = listFile.Name(), false
			input.ReleaseSource = func() error { return errors.Join(listFile.Close(), os.Remove(listFile.Name())) }
		}
		return input, nil, nil
	}
}

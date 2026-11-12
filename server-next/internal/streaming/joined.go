package streaming

import (
	"context"
	"fmt"
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
	// DurationMs is the part's length as its probe stored it: where the next part begins in
	// the whole title, the same place the title's page shows for it.
	DurationMs int64
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

// joinedInput is the converter's input for a title in several files: FFmpeg's concat
// demuxer reading the parts in order, as one title whose clock runs across all of them. Each
// part is opened by the one rule for library files and given to the converter as a
// descriptor of its own (4, 5, …), so it never opens a library path itself; the list naming
// them is descriptor 3. Each part's length is the stored one, so a place in the stream is the
// same place the title's page shows.
func joinedInput(parts []JoinedPart, roots func() []string) inputOpener {
	return func(ctx context.Context, position float64) (*PlaybackInput, subtitles.WindowInput, error) {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		// The part holding the run's start, and where in it the start is.
		first, offset := 0, 0.0
		for first+1 < len(parts) && offset+float64(parts[first].DurationMs)/1000 <= position {
			offset += float64(parts[first].DurationMs) / 1000
			first++
		}
		var files []*os.File
		closeAll := func() {
			for _, f := range files {
				f.Close()
			}
		}
		for _, part := range parts[first:] {
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
		name := func(n int) string {
			if runtime.GOOS == "windows" {
				return files[n].Name()
			}
			return fmt.Sprintf("/dev/fd/%d", 3+n)
		}
		if PartsDiffer(parts) {
			// Parts not encoded alike: each part an input of its own (descriptor 3 on), from the
			// part holding the start; the converter seeks within that one and joins them.
			var joined []JoinedInput
			at := offset
			for n, part := range parts[first:] {
				joined = append(joined, JoinedInput{Path: name(n), Offset: at, Duration: float64(part.DurationMs) / 1000})
				at += float64(part.DurationMs) / 1000
			}
			return &PlaybackInput{File: files[0], Argument: joined[0].Path, Inherit: runtime.GOOS != "windows",
				Options: []string{"-format_whitelist", mediaexec.LocalInputFormats}, Extra: files[1:], Joined: joined, Positioned: true}, nil, nil
		}
		// Parts alike: one list for FFmpeg's concat demuxer, from the part holding the start,
		// entered at its place in that part. The demuxer cannot itself be sought when its
		// packets are copied (it starts from the beginning whatever it is asked), so the list
		// starts where the run does. A part's duration after its in-point is what is left of
		// it, which is what places the next part at its place in the whole title.
		var list strings.Builder
		list.WriteString("ffconcat version 1.0\n")
		for n, part := range parts[first:] {
			// The list is descriptor 3, so the parts are 4 on.
			path := fmt.Sprintf("/dev/fd/%d", 4+n)
			if runtime.GOOS == "windows" {
				path = files[n].Name()
			}
			fmt.Fprintf(&list, "file '%s'\n", strings.ReplaceAll(path, "'", `'\''`))
			length := float64(part.DurationMs) / 1000
			if n == 0 && position > offset {
				fmt.Fprintf(&list, "inpoint %.3f\n", position-offset)
				length -= position - offset
			}
			fmt.Fprintf(&list, "duration %.3f\n", length)
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
			}
			closeAll()
			return nil, nil, err
		}
		// The concat demuxer, and under it only the single-file formats a library file may be.
		options := []string{"-f", "concat", "-safe", "0", "-format_whitelist", mediaexec.LocalInputFormats + ",concat"}
		input := &PlaybackInput{File: listFile, Argument: "/dev/fd/3", Inherit: true, Options: options, Extra: files, Positioned: true}
		if runtime.GOOS == "windows" {
			input.Argument, input.Inherit = listFile.Name(), false
		}
		return input, nil, nil
	}
}

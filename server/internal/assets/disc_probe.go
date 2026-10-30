package assets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/disc"
)

// InspectDiscScan probes one exact observed title through FFmpeg's disc reader.
// It grants no source access: the caller retains its inventory invocation and
// source observation fences, just as for InspectScan. An inventory context must
// use the confined helper; unsupported input hand-offs fail rather than falling
// back to opening the library pathname. Folder/root admission is separate.
func (p Probe) InspectDiscScan(ctx context.Context, path string, structure disc.Probe, title disc.Title) (Facts, error) {
	raw, err := json.Marshal(structure)
	if err != nil {
		return Facts{}, disc.ErrProtocol
	}
	validated, err := disc.DecodeProbe(raw)
	if err != nil {
		return Facts{}, err
	}
	input, err := validated.FFmpegInput(path, title, 0, nil)
	if err != nil {
		return Facts{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	options := append([]string{"-probesize", "8388608", "-analyzeduration", "5000000"}, input.Options...)
	data, err := p.probeOutputMode(ctx, path, input.URL, options, structure.Packaging == "folder")
	if err != nil {
		return Facts{}, err
	}
	if err = ctx.Err(); err != nil {
		return Facts{}, err
	}
	if p.ReadGuard != nil {
		if err = p.ReadGuard(ctx, path); err != nil {
			return Facts{}, err
		}
	}
	return decodeDiscMedia(data, title)
}

func decodeDiscMedia(data []byte, title disc.Title) (Facts, error) {
	f, err := decodeMediaProbe(data)
	if err != nil {
		return Facts{}, err
	}
	if f.Streams == nil || f.VideoCodec == "" && f.AudioCodec == "" {
		return Facts{}, errors.New("disc title has no indexed timed media streams")
	}
	// FFmpeg indexes streams; the disc structure supplies languages by transport
	// identity. Both TrueHD and its AC-3 presentation can have the same PID. Never
	// zip stream order, infer a neighbour PID or retain a contradictory clip tag.
	var ids struct {
		Streams []struct {
			Index *int   `json:"index"`
			ID    string `json:"id"`
		} `json:"streams"`
	}
	if err = json.Unmarshal(data, &ids); err != nil {
		return Facts{}, err
	}
	languages := make(map[int]string, len(ids.Streams))
	for _, stream := range ids.Streams {
		if stream.Index == nil {
			continue
		}
		text, base := stream.ID, 10
		if strings.HasPrefix(text, "0x") {
			text, base = text[2:], 16
		}
		pid, err := strconv.ParseUint(text, base, 13)
		if err == nil {
			languages[*stream.Index] = title.LanguageForPID(int(pid))
		}
	}
	for i := range f.Streams {
		f.Streams[i].Language = languages[f.Streams[i].Index]
	}
	// These are the selected reader title's duration and ordered chapter starts,
	// not the first clip's MPEG-TS duration or FFmpeg's incomplete chapter view.
	f.Duration = float64(title.DurationMs) / 1000
	f.Chapters = make([]Chapter, 0, len(title.Chapters))
	for i, chapter := range title.Chapters {
		end := title.DurationMs
		if i+1 < len(title.Chapters) {
			end = title.Chapters[i+1].StartMs
		}
		f.Chapters = append(f.Chapters, Chapter{Title: fmt.Sprintf("Chapter %d", i+1), Start: float64(chapter.StartMs) / 1000, End: float64(end) / 1000})
	}
	f.ChapterStatus = "known"
	return f, nil
}

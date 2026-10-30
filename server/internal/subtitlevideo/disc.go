package subtitlevideo

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
	"strconv"
	"strings"
	"time"
)

func (r *Runtime) DiscSubtitleAvailable() bool {
	return r != nil && r.ffmpeg != "" && r.ffprobe != "" && r.DiscInputs != nil && r.DiscAvailable != nil && r.DiscAvailable()
}
func (r *Runtime) ExtractDisc(ctx context.Context, item, source string, index int, format string) ([]byte, error) {
	invocation, ok := subtitles.DiscExtractionInvocationFromContext(ctx)
	if !ok || !r.DiscSubtitleAvailable() {
		return nil, subtitles.ErrUnavailable
	}
	facts := invocation.Facts()
	if facts.ItemID != item || facts.AssetID != source || facts.StreamIndex != index || index < 0 || index > 65535 {
		return nil, subtitles.ErrConflict
	}
	input, err := r.DiscInputs(ctx, item, source, facts.SessionID)
	if err != nil {
		return nil, err
	}
	defer input.Close()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Hour)
	defer cancel()
	codec, mux := "copy", format
	switch format {
	case "srt":
		codec = "srt"
	case "vtt":
		codec, mux = "webvtt", "webvtt"
	case "ass", "ssa":
		mux = "ass"
	case "pgs":
		mux = "sup"
	case "vobsub", "dvb":
		mux = "matroska"
	default:
		return nil, subtitles.ErrUnsupported
	}
	before := []string{"-hide_banner", "-nostdin", "-v", "error", "-threads", "1", "-copyts", "-start_at_zero"}
	after := []string{"-map", "0:" + strconv.Itoa(index), "-vn", "-an", "-dn", "-c:s", codec, "-map_metadata", "-1", "-f", mux, "pipe:1"}
	limit := int64(subtitles.BinaryLimitBytes())
	if format == "srt" || format == "vtt" || format == "ass" || format == "ssa" {
		limit = min(limit, 4<<20)
	}
	raw, err := input.Run(ctx, r.ffmpeg, before, after, limit)
	if err != nil {
		return nil, err
	}
	var canonical []byte
	switch format {
	case "vobsub":
		idx, sub, e := subtitles.VobSubOriginal(raw)
		if e != nil {
			return nil, e
		}
		canonical, err = subtitles.CanonicalAsset(idx, sub, "vobsub", 0)
	case "dvb":
		canonical, err = json.Marshal(subtitles.RenderAsset{Version: 2, TimeDomain: "source-relative", Renderer: "burn_in", Format: "mks", Data: raw})
	default:
		canonical, err = subtitles.Canonical(raw, format, 0)
	}
	if err != nil {
		return nil, err
	}
	if format == "ass" || format == "ssa" {
		var asset subtitles.RenderAsset
		if err = json.Unmarshal(canonical, &asset); err != nil || asset.Version != 2 {
			return nil, subtitles.ErrInput
		}
		asset.Fonts, asset.SkippedFonts, err = r.discFonts(ctx, input)
		if err != nil {
			return nil, err
		}
		canonical, err = json.Marshal(asset)
	}
	if err != nil {
		return nil, err
	}
	if err = input.Validate(ctx); err != nil {
		return nil, err
	}
	return canonical, nil
}
func (r *Runtime) discFonts(ctx context.Context, input subtitles.DiscInput) ([]subtitles.FontAttachment, int, error) {
	raw, err := input.Run(ctx, r.ffprobe, []string{"-v", "error", "-show_streams", "-of", "json"}, nil, 1<<20)
	if err != nil {
		return nil, 0, err
	}
	var result struct {
		Streams []probeStream `json:"streams"`
	}
	if json.Unmarshal(raw, &result) != nil || len(result.Streams) > 256 {
		return nil, 0, subtitles.ErrInput
	}
	out := []subtitles.FontAttachment{}
	seen := map[int]bool{}
	total, attempts, skipped := 0, 0, 0
	for _, st := range result.Streams {
		name := filepath.Base(st.Tags["filename"])
		ext := strings.ToLower(filepath.Ext(name))
		if st.Type != "attachment" || (ext != ".ttf" && ext != ".otf") {
			continue
		}
		if st.Index < 0 || st.Index > 65535 || seen[st.Index] {
			skipped++
			continue
		}
		seen[st.Index] = true
		if attempts >= 16 {
			skipped++
			continue
		}
		attempts++
		before := []string{"-hide_banner", "-nostdin", "-v", "error", "-threads", "1", "-dump_attachment:" + strconv.Itoa(st.Index), "pipe:1"}
		data, e := input.Run(ctx, r.ffmpeg, before, []string{"-map", "0:v:0?", "-t", "0", "-an", "-sn", "-f", "null", "pipe:1"}, 8<<20)
		if e != nil {
			if ctx.Err() != nil {
				return nil, 0, ctx.Err()
			}
			skipped++
			continue
		}
		if len(data) < 12 || !(binary.BigEndian.Uint32(data[:4]) == 0x10000 || string(data[:4]) == "OTTO" || string(data[:4]) == "ttcf" || string(data[:4]) == "true") {
			skipped++
			continue
		}
		if total+len(data) > 16<<20 {
			skipped++
			continue
		}
		total += len(data)
		name = strings.Map(func(r rune) rune {
			if r < 32 || r == 127 {
				return -1
			}
			return r
		}, name)
		if name == "" || len(name) > 160 {
			name = fmt.Sprintf("font%d%s", attempts, ext)
		}
		out = append(out, subtitles.FontAttachment{Name: name, Data: data})
	}
	if err = input.Validate(ctx); err != nil {
		return nil, 0, err
	}
	return out, skipped, nil
}

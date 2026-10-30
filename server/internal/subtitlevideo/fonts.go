package subtitlevideo

import (
	"context"
	"encoding/binary"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
	"strings"
)

func (r *Runtime) extractFonts(ctx context.Context, input subtitles.RenderInput) ([]subtitles.FontAttachment, int, error) {
	_, streams, e := r.probe(ctx, input, &extentLedger{})
	if e != nil {
		return nil, 0, e
	}
	var out []subtitles.FontAttachment
	total, attempts, skipped := 0, 0, 0
	for _, s := range streams {
		name := filepath.Base(s.Tags["filename"])
		ext := strings.ToLower(filepath.Ext(name))
		if s.Type != "attachment" || (ext != ".ttf" && ext != ".otf") {
			continue
		}
		if attempts >= 16 {
			skipped++
			continue
		}
		attempts++
		bridge, e := openBridge(ctx, input, &extentLedger{})
		if e != nil {
			return nil, 0, e
		}
		data, e := decoder.RunSubtitleFontExtract(ctx, r.storage.Supervisor, identity.Token(), r.ffmpeg, bridge.url, bridge.reservation, s.Index, r.libraries...)
		bridge.Close()
		if e != nil {
			if ctx.Err() != nil {
				return nil, 0, ctx.Err()
			}
			skipped++
			continue
		}
		if len(data) < 12 || !(binary.BigEndian.Uint32(data[:4]) == 0x00010000 || string(data[:4]) == "OTTO" || string(data[:4]) == "ttcf" || string(data[:4]) == "true") {
			skipped++
			continue
		}
		total += len(data)
		if total > 16<<20 {
			total -= len(data)
			skipped++
			continue
		}
		name = strings.Map(func(r rune) rune {
			if r < 32 || r == 127 {
				return -1
			}
			return r
		}, name)
		if len(name) > 160 || name == "" {
			name = "font" + ext
		}
		out = append(out, subtitles.FontAttachment{Name: name, Data: data})
	}
	if e = input.Validate(ctx); e != nil {
		return nil, 0, e
	}
	return out, skipped, nil
}

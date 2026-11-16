package streaming

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
	"github.com/PorticoMediaServer/portico/server-next/internal/subtitles"
)

// subtitleFonts is shared by the styled tracks in one file. At most 32 fonts,
// 8 MiB apiece and 64 MiB in total, are kept until the stream ends.
type subtitleFonts struct {
	once  sync.Once
	files []subtitles.FontAttachment
	err   error
}

// serveSubtitleOriginal answers only server-inventoried tracks of a live grant.
// Neither resource ids nor attachment names become filesystem paths.
func (s *Streams) serveSubtitleOriginal(w http.ResponseWriter, r *http.Request, file string) {
	parts := strings.Split(file, "/")
	if len(parts) != 4 && len(parts) != 5 {
		mediaError(w, ErrHLSArtifactNotFound)
		return
	}
	st, err := s.grant(parts[0])
	if err != nil {
		mediaError(w, err)
		return
	}
	var track *TextRenditionPlan
	for i := range st.originals {
		candidate := &st.originals[i]
		if candidate.Resource == parts[1] && strconv.FormatInt(candidate.Revision, 10) == parts[2] {
			track = candidate
			break
		}
	}
	if track == nil || track.Format != "ass" && track.Format != "ssa" {
		mediaError(w, ErrHLSArtifactNotFound)
		return
	}
	original := len(parts) == 4 && parts[3] == "original."+track.Format
	font := parts[3] == "fonts" && (len(parts) == 4 || len(parts) == 5)
	if !original && !font {
		mediaError(w, ErrHLSArtifactNotFound)
		return
	}
	ctx, cancel := s.subtitleContext(st)
	defer cancel()
	var body []byte
	contentType := "text/x-ssa; charset=utf-8"
	if original {
		if track.Path != "" {
			source := track.Source
			if source == nil {
				source = libraryFile{path: track.Path, size: track.Size, modified: track.Modified, roots: s.libraries}
			}
			body, err = readSmallSource(ctx, source)
			if err == nil {
				body, err = subtitles.DecodeSubtitleText(body, track.Language)
			}
		} else if source, ok := st.source.(remoteFile); ok {
			body, err = s.remoteSubtitleOriginal(ctx, source, track.Track, track.Format)
		} else {
			body, err = s.extractTrack(ctx, st.source, track.Track, subtitles.MaxInputBytes, "-c:s", "copy", "-f", "ass")
		}
	} else {
		st.fonts.once.Do(func() {
			st.fonts.files, st.fonts.err = s.readSubtitleFonts(ctx, st)
		})
		err = st.fonts.err
		if err == nil && len(parts) == 4 {
			list := subtitles.FontFiles{Fonts: []subtitles.FontFile{}}
			base := subtitles.MediaBaseURL(st.grant, track.Resource, track.Revision)
			for i, font := range st.fonts.files {
				list.Fonts = append(list.Fonts, subtitles.FontFile{Name: font.Name, URL: base + "/fonts/" + strconv.Itoa(i)})
			}
			body, err = json.Marshal(list)
			contentType = "application/json"
		} else if err == nil {
			n, parseErr := strconv.Atoi(parts[4])
			if parseErr != nil || n < 0 || n >= len(st.fonts.files) || strconv.Itoa(n) != parts[4] {
				err = ErrHLSArtifactNotFound
			} else {
				body = st.fonts.files[n].Data
				contentType = "application/octet-stream"
			}
		}
	}
	if _, grantErr := s.grant(st.grant); err != nil || grantErr != nil {
		mediaError(w, ErrHLSArtifactNotFound)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-store")
	http.ServeContent(w, r, "", time.Time{}, &guardedReader{ReadSeeker: bytes.NewReader(body), check: func() error { _, err := s.grant(st.grant); return err }})
}

// Extraction belongs to the stream, rather than the first reader. Ending the
// stream cancels it, and every response checks the grant again before serving.
func (s *Streams) subtitleContext(st *stream) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(s.hls.ctx, 2*time.Minute)
	go func() {
		tick := time.NewTicker(500 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				if _, err := s.grant(st.grant); err != nil {
					cancel()
					return
				}
			}
		}
	}()
	return ctx, cancel
}

func (s *Streams) readSubtitleFonts(ctx context.Context, st *stream) ([]subtitles.FontAttachment, error) {
	// Only Matroska carries these attachments. Sidecar ASS beside an MP4
	// still works on a server without ffmpeg or any attached fonts.
	if st.plan.SourceContainer != "matroska" && st.plan.SourceContainer != "mkv" {
		return []subtitles.FontAttachment{}, nil
	}
	input, remote, err := s.hls.openWindowInput(ctx, st.id, st.plan, 0)
	if err != nil {
		return nil, err
	}
	defer input.Close()
	if remote != nil {
		defer remote.Close()
	}
	dir, err := os.MkdirTemp(s.hls.root, "subtitle-fonts-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	fonts, err := s.hls.extractBurnFonts(ctx, input, remote, dir)
	if err != nil {
		return nil, err
	}
	out := []subtitles.FontAttachment{}
	for i := 0; i < 32; i++ {
		name := fmt.Sprintf("font-%02d.ttf", i)
		raw, err := os.ReadFile(filepath.Join(fonts, name))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, subtitles.FontAttachment{Name: name, Data: raw})
	}
	return out, nil
}

// Remote originals use the existing isolated input bridge, never a source URL
// in ffmpeg's host network namespace.
func (s *Streams) remoteSubtitleOriginal(ctx context.Context, source remoteFile, track int, format string) ([]byte, error) {
	input, bridge, err := source.openBridge(ctx, s.hls.binary, s.hls.ffprobe, s.processes)
	if err != nil {
		return nil, err
	}
	defer input.Close()
	defer bridge.Close()
	reservation, err := bridge.bridge.Reserve()
	if err != nil {
		return nil, err
	}
	return decoder.RunSubtitleExtract(ctx, s.processes, token(), s.hls.binary, bridge.bridge.URL(), reservation, track, format, bridge.libraries...)
}

package streaming

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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

// subtitleFonts keeps only generated names; font bytes stay in stream output.
type subtitleFonts struct {
	mu    sync.Mutex
	names []string
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
	var ordinal int
	for i := range st.originals {
		candidate := &st.originals[i]
		if candidate.Resource == parts[1] && strconv.FormatInt(candidate.Revision, 10) == parts[2] {
			track, ordinal = candidate, i
			break
		}
	}
	if track == nil {
		mediaError(w, ErrHLSArtifactNotFound)
		return
	}
	if kind := subtitles.RenderingFormat(track.Format); kind == "pgs" || kind == "vobsub" || kind == "dvb" {
		if len(parts) != 4 {
			mediaError(w, ErrHLSArtifactNotFound)
			return
		}
		s.servePictureOriginal(w, r, st, *track, ordinal, parts[3])
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
	var fontFile *os.File
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
		var names []string
		names, err = s.subtitleFontNames(ctx, st)
		if err == nil && len(parts) == 4 {
			list := subtitles.FontFiles{Fonts: []subtitles.FontFile{}}
			base := subtitles.MediaBaseURL(st.grant, track.Resource, track.Revision)
			for i, name := range names {
				list.Fonts = append(list.Fonts, subtitles.FontFile{Name: name, URL: base + "/fonts/" + strconv.Itoa(i)})
			}
			body, err = json.Marshal(list)
			contentType = "application/json"
		} else if err == nil {
			n, parseErr := strconv.Atoi(parts[4])
			if parseErr != nil || n < 0 || n >= len(names) || strconv.Itoa(n) != parts[4] {
				err = ErrHLSArtifactNotFound
			} else {
				dir, release := s.hls.retainOutput(st.id)
				if release == nil {
					err = ErrSegmentPreparing
				} else {
					fontFile, err = os.Open(filepath.Join(dir, "subtitle-fonts", "burn-fonts", names[n]))
					release()
					if err == nil {
						defer fontFile.Close()
					}
				}
				contentType = "application/octet-stream"
			}
		}
	}
	if err != nil {
		mediaError(w, err)
		return
	}
	if _, err := s.grant(st.grant); err != nil {
		mediaError(w, ErrHLSArtifactNotFound)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-store")
	var reader io.ReadSeeker = bytes.NewReader(body)
	if fontFile != nil {
		reader = fontFile
	}
	http.ServeContent(w, r, "", time.Time{}, &guardedReader{ReadSeeker: reader, check: func() error { _, err := s.grant(st.grant); return err }})
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

func (s *Streams) subtitleFontNames(ctx context.Context, st *stream) ([]string, error) {
	st.fonts.mu.Lock()
	defer st.fonts.mu.Unlock()
	if st.fonts.names != nil {
		return st.fonts.names, nil
	}
	// Only Matroska carries attachments. An ASS sidecar beside an MP4 needs
	// neither extraction nor ffmpeg to return an empty list.
	if st.plan.SourceContainer != "matroska" && st.plan.SourceContainer != "mkv" {
		st.fonts.names = []string{}
		return st.fonts.names, nil
	}
	input, remote, err := s.hls.openWindowInput(ctx, st.id, st.plan, 0)
	if err != nil {
		return nil, err
	}
	defer input.Close()
	if remote != nil {
		defer remote.Close()
	}
	if _, err := s.hls.createStreamingOutput(ctx, st.id, st.plan); err != nil {
		return nil, err
	}
	dir, release := s.hls.retainOutput(st.id)
	if release == nil {
		return nil, ErrSegmentPreparing
	}
	defer release()
	if _, err := s.grant(st.grant); err != nil {
		return nil, err
	}
	dir = filepath.Join(dir, "subtitle-fonts")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	fonts, err := s.hls.extractBurnFonts(ctx, input, remote, dir)
	if err != nil {
		return nil, err
	}
	names := []string{}
	for i := 0; i < 32; i++ {
		name := fmt.Sprintf("font-%02d.ttf", i)
		if _, err := os.Stat(filepath.Join(fonts, name)); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	st.fonts.names = names
	return names, nil
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

package streaming

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/subtitles"
)

func (s *Streams) servePictureOriginal(w http.ResponseWriter, r *http.Request, st *stream, track TextRenditionPlan, ordinal int, name string) {
	kind := subtitles.RenderingFormat(track.Format)
	ext := map[string]string{"pgs": "sup", "vobsub": "idx", "dvb": "mks"}[kind]
	suffix := ext
	switch {
	case name == "original."+ext:
	case kind == "vobsub" && name == "original.sub":
		suffix = "sub"
	case (kind == "pgs" || kind == "vobsub") && name == "lines":
		suffix = "lines"
	default:
		mediaError(w, ErrHLSArtifactNotFound)
		return
	}
	ctx, cancel := s.subtitleContext(st)
	defer cancel()
	file, err := s.pictureOriginalFile(ctx, st, track, ordinal, suffix)
	if err != nil {
		mediaError(w, err)
		return
	}
	defer file.Close()
	if _, err := s.grant(st.grant); err != nil {
		mediaError(w, err)
		return
	}
	contentType := "application/octet-stream"
	if suffix == "lines" {
		contentType = "application/json"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-store")
	http.ServeContent(w, r, "", time.Time{}, &guardedReader{ReadSeeker: file, check: func() error { _, err := s.grant(st.grant); return err }})
}

// A stream serializes initial extraction. Only bounded decoder output is held
// while writing its private files; later reads serve disk, not retained bytes.
// The existing output lease guards a move/cleanup until the file is open.
func (s *Streams) pictureOriginalFile(ctx context.Context, st *stream, track TextRenditionPlan, ordinal int, suffix string) (*os.File, error) {
	st.pictures.Lock()
	defer st.pictures.Unlock()
	if _, err := s.grant(st.grant); err != nil {
		return nil, err
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
	dir = filepath.Join(dir, "subtitle-originals")
	prefix := filepath.Join(dir, fmt.Sprintf("picture-%d", ordinal))
	if ready, err := os.ReadFile(prefix + ".ready"); err != nil || string(ready) != "1" {
		data, companion, err := s.readPictureOriginal(ctx, st, track)
		if err != nil {
			return nil, err
		}
		if len(data)+len(companion) > subtitles.BinaryLimitBytes() {
			return nil, subtitles.ErrCapacity
		}
		kind := subtitles.RenderingFormat(track.Format)
		ext := map[string]string{"pgs": "sup", "vobsub": "idx", "dvb": "mks"}[kind]
		files := map[string][]byte{ext: data}
		if kind == "vobsub" {
			files["sub"] = companion
		}
		if kind == "vobsub" || kind == "pgs" {
			files["lines"], err = json.Marshal(subtitles.ImageLines(kind, data, companion))
			if err != nil {
				return nil, err
			}
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := os.MkdirAll(dir, 0700); err != nil {
			return nil, err
		}
		for ext, body := range files {
			if err := os.WriteFile(prefix+"."+ext, body, 0600); err != nil {
				return nil, err
			}
		}
		if err := os.WriteFile(prefix+".ready", []byte("1"), 0600); err != nil {
			return nil, err
		}
	}
	return os.Open(prefix + "." + suffix)
}

package streaming

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/childprocess"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediaexec"
	"github.com/PorticoMediaServer/portico/server-next/internal/subtitles"
)

// A text subtitle is served beside a stream as WebVTT: whole, for an app that
// draws subtitles itself, and in one-minute pieces for the playlist of a player
// that follows HLS. It is read the first time somebody asks for it (a file
// beside the video is parsed; a track inside the video is taken out with ffmpeg
// first) and kept in memory until the stream ends. Nothing is written to disk.

// textCues is one subtitle's cues once read.
type textCues struct {
	once     sync.Once
	document []byte
	err      error
}

// TextURL is the address of a stream's text subtitle as one WebVTT file.
func TextURL(grant, resource string, revision int64) string {
	return subtitles.MediaBaseURL(grant, resource, revision) + ".vtt"
}

// serveText answers /v1/media/subtitles/{grant}/{resource}/{revision}.vtt,
// with ?segment=N for one minute of it. Anything it does not know is the same
// hidden 404 as any other media address.
func (s *Streams) serveText(w http.ResponseWriter, r *http.Request, file string) {
	parts := strings.Split(file, "/")
	if len(parts) != 3 || !strings.HasSuffix(parts[2], ".vtt") {
		mediaError(w, ErrHLSArtifactNotFound)
		return
	}
	st, err := s.grant(parts[0])
	if err != nil {
		mediaError(w, err)
		return
	}
	revision, _ := strconv.ParseInt(strings.TrimSuffix(parts[2], ".vtt"), 10, 64)
	segment := -1
	if raw := r.URL.Query().Get("segment"); raw != "" {
		if segment, err = strconv.Atoi(raw); err != nil || segment < 0 {
			mediaError(w, ErrHLSArtifactNotFound)
			return
		}
	}
	s.hls.mu.Lock()
	var track *TextRenditionPlan
	for i := range st.text {
		if st.text[i].Resource == parts[1] && st.text[i].Revision == revision {
			track = &st.text[i]
		}
	}
	var cues *textCues
	if track != nil {
		if st.cues == nil {
			st.cues = map[string]*textCues{}
		}
		if cues = st.cues[track.Resource]; cues == nil {
			cues = &textCues{}
			st.cues[track.Resource] = cues
		}
	}
	s.hls.mu.Unlock()
	if track == nil {
		mediaError(w, ErrHLSArtifactNotFound)
		return
	}
	cues.once.Do(func() {
		// The first reader's request does not own the work: others wait on it too.
		ctx, cancel := context.WithTimeout(s.hls.ctx, 2*time.Minute)
		defer cancel()
		cues.document, cues.err = s.readText(ctx, st, *track)
	})
	if cues.err != nil {
		mediaError(w, ErrHLSArtifactNotFound)
		return
	}
	body, err := subtitles.ManifestWebVTT(bytes.NewReader(cues.document), 0, segment)
	if err != nil {
		mediaError(w, ErrHLSArtifactNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
	w.Header().Set("Cache-Control", "private, no-store")
	_, _ = w.Write(body)
}

// readText reads one text subtitle into cues.
func (s *Streams) readText(ctx context.Context, st *stream, track TextRenditionPlan) ([]byte, error) {
	var raw []byte
	var err error
	format := track.Format
	if track.Path != "" {
		raw, err = readSmallSource(ctx, libraryFile{path: track.Path, size: track.Size, modified: track.Modified, roots: s.libraries})
	} else {
		// ffmpeg writes every text track as SRT, and as ASS where that is what it is.
		if format != "ass" && format != "ssa" {
			format = "srt"
		}
		raw, err = s.extractText(ctx, st, track.Track, format)
	}
	if err != nil {
		return nil, err
	}
	return subtitles.TextDocument(raw, format, int64(st.duration*1e6))
}

func readSmallSource(ctx context.Context, source Source) ([]byte, error) {
	file, _, _, err := source.Open(ctx)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, subtitles.MaxInputBytes+1))
	if err == nil && len(raw) > subtitles.MaxInputBytes {
		err = subtitles.ErrCapacity
	}
	return raw, err
}

// extractText takes one subtitle track out of the stream's file with ffmpeg in
// its sandbox, which is handed the open file and nothing else.
func (s *Streams) extractText(ctx context.Context, st *stream, track int, format string) ([]byte, error) {
	if s.hls.binary == "" {
		return nil, ErrNoConverter
	}
	input, err := converterInput(ctx, st.source)
	if err != nil {
		return nil, err
	}
	defer input.Close()
	ctx, release := input.BorrowContext(ctx)
	defer release()
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-protocol_whitelist", "file,pipe"}
	args = append(args, input.Options...)
	args = append(args, "-i", input.Argument, "-map", "0:"+strconv.Itoa(track), "-f", format, "pipe:1")
	cmd, err := mediaexec.CommandContext(ctx, mediaexec.Job{Executable: s.hls.binary, Args: args, Files: input.ExtraFiles(),
		InputFD: input.MountFD, InputMount: input.MountPath, ReadPaths: input.ReadPaths()})
	if err != nil {
		return nil, err
	}
	cmd.WaitDelay = time.Second
	var out bytes.Buffer
	cmd.Stdout = &limitedWriter{buffer: &out, limit: subtitles.MaxInputBytes}
	if err := childprocess.Run(ctx, cmd); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

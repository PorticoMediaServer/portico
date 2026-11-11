package streaming

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/audiofacts"
	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
	"github.com/PorticoMediaServer/portico/server-next/internal/inputbridge"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediaexec"
)

// MeasureAudio reads the exact facts PlanAudio needs from a music file in a
// library folder: its codec and decoder configuration, how many frames it
// really decodes to, and what a gapless player must trim from each end. It
// decodes the whole file once with ffprobe in its sandbox, which reaches the
// file through a private loopback address and nothing else. For a file with no
// seek table of its own (MP3, ADTS, Ogg) it also makes the seek index.
func (s *Streams) MeasureAudio(ctx context.Context, path string, size int64, modified time.Time) (audiofacts.Facts, *audiofacts.SeekIndex, error) {
	var facts audiofacts.Facts
	if s.hls.ffprobe == "" {
		return facts, nil, ErrNoConverter
	}
	body, size, _, err := libraryFile{path: path, size: size, modified: modified, roots: s.libraries}.Open(ctx)
	if err != nil {
		return facts, nil, err
	}
	defer body.Close()
	source, ok := body.(io.ReaderAt)
	if !ok {
		return facts, nil, errors.New("this kind of source cannot be measured yet")
	}
	binary, err := exec.LookPath(s.hls.ffprobe)
	if err == nil {
		binary, err = filepath.Abs(binary)
	}
	if err != nil {
		return facts, nil, err
	}
	libraries, err := mediaexec.ResolveLibraries(binary, binary, nil)
	if err != nil {
		return facts, nil, err
	}
	bridge, err := inputbridge.Open(ctx, source, size)
	if err != nil {
		return facts, nil, err
	}
	defer bridge.Close()
	reservation, err := bridge.Reserve()
	if err != nil {
		return facts, nil, err
	}
	var key [8]byte
	rand.Read(key[:])
	err = decoder.RunStreamingProbe(ctx, s.processes, hex.EncodeToString(key[:]), binary, bridge.URL(), audiofacts.Args(bridge.URL()), reservation,
		func(r io.Reader) error {
			var e error
			facts, e = audiofacts.Parse(r)
			return e
		}, libraries...)
	if err != nil {
		return facts, nil, err
	}
	index := seekIndex(ctx, source, size, &facts)
	return facts, index, nil
}

// AudioSeekIndex makes the seek index of a music file that was measured before (the scan
// keeps the measurement, not the index, which is quick to make): one read through the
// file's frame headers. It is nil for a format that has a seek table of its own.
func (s *Streams) AudioSeekIndex(ctx context.Context, path string, size int64, modified time.Time, facts *audiofacts.Facts) (*audiofacts.SeekIndex, error) {
	body, size, _, err := libraryFile{path: path, size: size, modified: modified, roots: s.libraries}.Open(ctx)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	source, ok := body.(io.ReaderAt)
	if !ok {
		return nil, errors.New("this kind of source cannot be measured yet")
	}
	return seekIndex(ctx, source, size, facts), nil
}

// seekIndex makes a file's seek index and notes in facts whether it has one. A file that
// cannot be indexed exactly just has no index: it still plays.
func seekIndex(ctx context.Context, source io.ReaderAt, size int64, facts *audiofacts.Facts) *audiofacts.SeekIndex {
	if !audiofacts.Indexed(*facts) {
		return nil
	}
	index, _ := audiofacts.BuildSeekIndex(ctx, func(_ context.Context, offset, length int64) ([]byte, error) {
		extent := make([]byte, length)
		n, err := source.ReadAt(extent, offset)
		if err == io.EOF && n > 0 {
			err = nil
		}
		return extent[:n], err
	}, size, *facts)
	facts.HasSeekIndex = index != nil
	return index
}

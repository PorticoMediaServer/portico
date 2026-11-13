package mediaengine

import (
	"context"
	"fmt"
	"io"
	"os/exec"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
	"github.com/PorticoMediaServer/portico/server-next/internal/inputbridge"
	"github.com/PorticoMediaServer/portico/server-next/internal/libraryfile"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediafacts"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediatools"
	"github.com/PorticoMediaServer/portico/server-next/internal/streaming"
)

// HEVC mastering metadata can live on decoded frames, absent from the header
// probe. Read one frame only when an SDR conversion needs the distinction.
// A completed or failed attempt shares the existing file-version facts cache,
// so an unreadable first frame delays only the first Play of that version.
func (e *Engine) observeToneMappingSource(ctx context.Context, path string, place db.FileOnDisk, remoteID int64, facts mediafacts.Facts) mediafacts.Facts {
	video := streaming.SourceFromFacts(streaming.FileFacts{Streams: facts.Streams}).Video
	if video == nil || video.MasterDisplay != "" {
		return facts
	}
	stream := -1
	for i, s := range facts.Streams {
		if s.Index == video.Index && s.Type == "video" && mediafacts.NeedsFrameHDR(s) {
			stream = i
			break
		}
	}
	if stream < 0 {
		return facts
	}
	key := fmt.Sprint(remoteID, "\x00", path, "\x00", place.Size, "\x00", place.ModifiedNS)
	e.mu.Lock()
	if known := e.facts[key]; known.FrameHDRChecked {
		e.mu.Unlock()
		return known
	}
	// Publish the attempt before reading. Another Play can use the processor
	// immediately; success below replaces this with the measured metadata.
	facts.FrameHDRChecked = true
	e.facts[key] = facts
	e.mu.Unlock()
	file, closeFile, err := e.toneMappingInput(ctx, place, remoteID)
	if err != nil {
		return facts
	}
	defer closeFile()
	bridge, err := inputbridge.Open(ctx, file, place.Size)
	if err != nil {
		return facts
	}
	defer bridge.Close()
	reservation, err := bridge.Reserve()
	if err != nil {
		return facts
	}
	defer reservation.Close()
	binary := e.probe.Binary
	if binary == "" {
		_, binary = mediatools.ResolvePair()
	}
	binary, err = exec.LookPath(binary)
	if err != nil {
		return facts
	}
	raw, err := decoder.RunAnalysisProbe(ctx, e.processes, "playback-hdr:"+key, binary, bridge.URL(), reservation,
		decoder.AnalysisProbeSpec{Kind: decoder.ProbeFrameHDR, StreamIndex: &video.Index})
	if err != nil {
		return facts
	}
	// The cached slice may also be in use by another Play. Change a private copy.
	facts.Streams = append([]mediafacts.Stream(nil), facts.Streams...)
	if mediafacts.ApplyFrameHDR(raw, &facts.Streams[stream].Detail) != nil {
		return facts
	}
	e.mu.Lock()
	e.facts[key] = facts
	e.mu.Unlock()
	return facts
}

func (e *Engine) toneMappingInput(ctx context.Context, place db.FileOnDisk, remoteID int64) (io.ReaderAt, func(), error) {
	if remoteID != 0 {
		storage, _, err := e.remote(ctx, remoteID)
		if err != nil {
			return nil, nil, err
		}
		file, err := storage.Open(ctx, place.Path)
		if err != nil {
			return nil, nil, err
		}
		if file.Size() != place.Size {
			file.Close()
			return nil, nil, streaming.ErrSourceChanged
		}
		return file, func() { file.Close() }, nil
	}
	file, err := libraryfile.Open(place.Path, e.folders())
	if err != nil {
		return nil, nil, err
	}
	info, err := file.Stat()
	if err != nil || info.Size() != place.Size || info.ModTime().UnixNano() != place.ModifiedNS {
		file.Close()
		return nil, nil, streaming.ErrSourceChanged
	}
	return file, func() { file.Close() }, nil
}

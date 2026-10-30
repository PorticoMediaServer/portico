package subtitles

import (
	"context"
)

// These are server-private composition contracts, not public locator or decoder
// arguments. Acquisition remains in playback/source adapters; the renderer never
// receives an origin credential and never opens a client-supplied URL.
type RenderInput interface {
	Evidence() string
	Size() int64
	ReadExtent(context.Context, int64, int64) ([]byte, error)
	Validate(context.Context) error
	Close() error
}
type SourceInputs interface {
	OpenSubtitleInput(context.Context, string, string, string) (RenderInput, error)
}
type SubtitleExtractor interface {
	Extract(context.Context, string, string, int, string) ([]byte, string, error)
	Probe(context.Context, string, string) (*Inventory, string, error)
}

type Presentation struct {
	SessionID  string `json:"sessionId"`
	Generation int    `json:"generation"`
	StreamURL  string `json:"streamUrl"`
	Mode       string `json:"mode"`
	PositionUS string `json:"positionUs"`
}

// SourceMediaInfo carries facts from the same retained source that produced the
// evidence, never facts probed through a separately reopened remote locator.
type SourceMediaInfo struct {
	Evidence, DescriptorDigest, VideoCodec, AudioCodec, Container string
	NetworkPolicyRevision                                         int64
	DurationUS, OriginUS                                          int64
	Width, Height                                                 int
	// Probe is the confined ffprobe JSON (format and streams) of the same
	// retained version, for planning a remote source like a local one.
	Probe []byte
}
type SourceInspector interface {
	InspectSubtitleSource(context.Context, string, string) (SourceMediaInfo, error)
}

// SidecarInputs handles server-inventoried adapter sidecars. The bool distinguishes
// an unhandled local file from a failed handled remote acquisition; no fallback
// from an unavailable remote object to a local path is permitted.
type SidecarInputs interface {
	AcquireSubtitleSidecar(context.Context, string, string, string, int64, int64, string) ([]byte, []byte, bool, error)
}

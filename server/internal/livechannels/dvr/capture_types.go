package dvr

import (
	"context"
	"os"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"github.com/PorticoMediaServer/portico/server/internal/mediaartifact"
	"time"
)

// CaptureDriver is an internal adapter into confined media work. Success means
// validated closed bytes, not publication. The scheduler owns every DB fence.
type CaptureDriver interface {
	Available() bool
	RetirementAvailable() bool
	Capture(context.Context, CaptureRequest, func(CaptureCheckpoint) error) (CaptureResult, error)
	Recover(context.Context, CaptureRequest, CaptureCheckpoint) (CaptureResult, error)
	Remove(context.Context, livechannels.Owner, string, mediaartifact.Object) error
}
type CaptureRequest struct {
	Checkpoint     func(CaptureCheckpoint) error
	Recording      Recording
	Owner          livechannels.Owner
	Input          livechannels.Input
	Allocation     livechannels.Allocation
	Generation     int64
	EstimatedBytes int64
	ArtifactKey    string
	PhysicalLock   *os.File
	OnRetired      func()
}
type CaptureCheckpoint struct {
	// SharedReady is stamped by the scheduler after immutable validation. It
	// lets a withdrawn leader finish for other owners without publishing its item.
	SharedReady       bool `json:"sharedReady,omitempty"`
	Object            mediaartifact.Object
	First, Last       time.Time
	Gaps              int
	WriterRetired     bool
	ProducerSucceeded bool
	BoundaryReached   bool
	SourceFence       string `json:"sourceFence"`
	SourceRevision    int64  // stamped by the scheduler, never accepted from a client
}
type CaptureResult struct {
	// LayoutReceipt binds a real external filename to this exact sealed source.
	// Catalog playback retains its original occurrence-private asset authority.
	LayoutReceipt *RecordingLayoutReceipt `json:"-"`
	CaptureCheckpoint
	Path                              string // canonical private object path; never serialized to a viewer
	Root                              string // this owner's managed Recorded TV root
	Container, VideoCodec, AudioCodec string
	Width, Height                     int
	Duration                          float64
	ModifiedNS                        int64
	Reason                            string
	Complete                          bool
}

// SharedCaptureDriver publishes independent profile-owned paths for one closed
// capture without copying its media bytes.
type SharedCaptureDriver interface {
	Share(context.Context, CaptureRequest, CaptureRequest, CaptureCheckpoint) (CaptureResult, error)
}

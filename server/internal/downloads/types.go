// Package downloads owns offline downloads: the preparations a viewer asks for,
// the transfer grants that move the bytes, the signed receipts that let a client
// play those bytes with no server in reach, and the deferred progress a client
// recorded while it was away.
//
// It produces no media of its own and keeps no copy of any. A preparation for
// `original` points at the library file itself, read in place; a preparation
// for a quality ladder rung points at a published prepared_media_versions row
// from internal/preparedmedia. This package adds the viewer's claim on those
// bytes, the verification hash, the storage accounting and the offline
// authorization — nothing else.
//
// Two things are kept apart throughout. A preparation is the server's promise
// to hand over bytes: retention, a replaced file or a removed title end it. A
// receipt is the viewer's permission to play a copy already on a device: only
// a change to what that viewer may do ends it. Tidying the first never touches
// the second.
package downloads

import (
	"encoding/json"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/qualityladder"
	"regexp"
	"time"
)

var (
	// ErrInput is a malformed request: an unknown quality, an empty target set,
	// more targets than the batch limit, a bad identifier.
	ErrInput = errors.New("invalid download request")
	// ErrNotFound is an unknown preparation, grant or receipt for this viewer.
	ErrNotFound = errors.New("download not found")
	// ErrConflict is a fence failure: expectedRevision did not match, or the
	// action is not legal from the preparation's current state.
	ErrConflict = errors.New("download revision changed")
	// ErrStorageFull is the owner's downloads.maxPreparedBytes refusing new work.
	ErrStorageFull = errors.New("the prepared download store is full")
	// ErrPolicy is the profile losing allowDownloads.
	ErrPolicy = errors.New("this profile may not download")
	// ErrNotReady is a grant or receipt asked for a preparation that is not ready.
	ErrNotReady = errors.New("the download is not ready")
	// ErrGrant is an expired, revoked, replayed or unknown transfer grant.
	ErrGrant = errors.New("the download grant is no longer valid")
	// ErrReceipt is an unreadable or unverifiable offline receipt envelope.
	ErrReceipt = errors.New("the offline receipt could not be read")
	// ErrCapacity bounds simultaneous container requests awaiting admission.
	ErrCapacity = errors.New("too many active download requests")
)

// Published state names. They travel to clients and into stored rows, so they
// are stable strings rather than an enum a refactor can renumber.
const (
	StateQueued      = "queued"
	StateRunning     = "running"
	StateReady       = "ready"
	StatePaused      = "paused"
	StateFailed      = "failed"
	StateUnavailable = "unavailable"
	StateCancelled   = "cancelled"
	StateExpired     = "expired"
)

// Published action names.
const (
	ActionPause  = "pause"
	ActionResume = "resume"
	ActionCancel = "cancel"
	ActionRetry  = "retry"
	ActionRemove = "remove"
)

// Reason codes. A client renders its own sentence from these; the server never
// promises a translated message. They are a closed set: an unexpected internal
// failure collapses to ReasonPreparationFailed rather than leaking a cause.
const (
	ReasonNotAllowed      = "downloads_not_allowed"
	ReasonItemDeleted     = "item_deleted"
	ReasonSourceMissing   = "source_unavailable"
	ReasonSourceChanged   = "source_changed"
	ReasonNotOptimized    = "optimized_version_unavailable"
	ReasonOptimizeFailed  = "optimization_failed"
	ReasonStorageFull     = "storage_full"
	ReasonArtifactChanged = "artifact_changed"
	ReasonVerifyFailed    = "verification_failed"
	ReasonRetention       = "retention_expired"
	ReasonCancelled       = "cancelled"
	ReasonFailed          = "preparation_failed"
	ReasonAccountDisabled = "account_disabled"
	ReasonRevoked         = "revoked"
	// ReasonCacheReleased ends a server-side prepared copy at the owner's
	// request. Offline permissions stay as they were.
	ReasonCacheReleased  = "cache_released"
	ReasonUnknownReceipt = "unknown_receipt"
	// ReasonUnknownPreparation answers a receipt asked for a claim this server
	// has no record of, which a client recovers from by asking again.
	ReasonUnknownPreparation = "unknown_preparation"
	// ReasonSeveralFiles is a title stored as more than one file. One claim is
	// one file, so it is refused plainly rather than as an unreachable source.
	ReasonSeveralFiles = "several_files"
)

// publishedReasons is the closed set a client is promised. Anything stored or
// accepted as a reason is one of these.
var publishedReasons = map[string]bool{
	ReasonNotAllowed: true, ReasonItemDeleted: true, ReasonSourceMissing: true, ReasonSourceChanged: true,
	ReasonNotOptimized: true, ReasonOptimizeFailed: true, ReasonStorageFull: true, ReasonArtifactChanged: true,
	ReasonVerifyFailed: true, ReasonRetention: true, ReasonCancelled: true, ReasonFailed: true,
	ReasonAccountDisabled: true, ReasonRevoked: true, ReasonCacheReleased: true, ReasonUnknownReceipt: true, ReasonUnknownPreparation: true,
	ReasonSeveralFiles: true, ReasonRemoved: true,
}

// Origins record how a preparation was asked for, so a client can tell an
// auto-next-episode claim from one the viewer tapped.
const (
	OriginItem      = "item"
	OriginItems     = "items"
	OriginContainer = "container" // a durable container request's members
	OriginNext      = "next"
)

// QualityOriginal names the untouched source file. Every other accepted quality
// is a quality ladder rung id published by internal/playback.
const QualityOriginal = "original"

// MaxLivePerViewer bounds running work, not queued or ready claims.
// MaxBatchTargets bounds synchronous explicit-item requests.
const (
	MaxLivePerViewer = 200
	MaxBatchTargets  = 100
	// GrantIdle is how long a transfer address stays good with nobody using or
	// renewing it. A phone's own downloader pauses a transfer for hours (no
	// Wi-Fi, a suspended app) and then asks for the same address again, so the
	// address has to outlive that. It is kept alive by use and by renewal, both
	// of which recheck that the viewer may still have the file, and it ends
	// with the claim.
	GrantIdle = 24 * time.Hour
	// ReceiptTTL is how long a client may play downloaded bytes with no server
	// in reach before it has to revalidate.
	ReceiptTTL = 30 * 24 * time.Hour
)

// hashChunk bounds one worker pass over an original file so a large movie
// cannot monopolize the worker; the SHA-256 state is checkpointed between
// passes, which is also what makes pause and resume mean something. It is a
// variable so a test can shrink the window and exercise the checkpoint without
// writing a gigabyte to disk.
var hashChunk int64 = 32 << 20

var validID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,160}$`)
var validDigest = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Artifact is the concrete object a ready preparation points at.
type Artifact struct {
	Kind        string `json:"kind"`
	Ref         string `json:"ref,omitempty"`
	SHA256      string `json:"sha256,omitempty"`
	Bytes       int64  `json:"bytes"`
	Estimated   bool   `json:"estimated"`
	Container   string `json:"container,omitempty"`
	ContentType string `json:"contentType,omitempty"`
	FileName    string `json:"fileName,omitempty"`
}

// Progress is byte-accurate while a preparation runs. bytesTotal is an estimate
// until the artifact exists, and estimated says so rather than making a client
// guess which number it is looking at.
type Progress struct {
	BytesDone  int64   `json:"bytesDone"`
	BytesTotal int64   `json:"bytesTotal"`
	Estimated  bool    `json:"bytesTotalEstimated"`
	Percent    float64 `json:"percent"`
	ETAMs      *int64  `json:"etaMs"`
}

// Preparation is the published view of one viewer's claim. Title is what the
// claim was for, kept with it, so a list of downloads reads as titles even
// after one has left the library.
type Preparation struct {
	VersionID string   `json:"versionId"`
	ID        string   `json:"id"`
	ItemID    string   `json:"itemId"`
	LibraryID string   `json:"libraryId"`
	ProfileID string   `json:"profileId"`
	Title     Title    `json:"title"`
	Quality   string   `json:"quality"`
	Origin    string   `json:"origin"`
	BatchID   string   `json:"batchId,omitempty"`
	State     string   `json:"state"`
	Reason    string   `json:"reason,omitempty"`
	Artifact  Artifact `json:"artifact"`
	Progress  Progress `json:"progress"`
	Actions   []string `json:"actions"`
	Revision  int64    `json:"revision"`
	CreatedAt string   `json:"createdAt"`
	UpdatedAt string   `json:"updatedAt"`
	ReadyAt   string   `json:"readyAt,omitempty"`
	ExpiresAt string   `json:"expiresAt,omitempty"`
}

// RequestQuality is optional only at input. Its zero value means omission;
// any explicitly supplied value must be a canonical quality, including Original.
type RequestQuality string

func (q *RequestQuality) UnmarshalJSON(raw []byte) error {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	if err := preparationQuality(value); err != nil {
		return err
	}
	*q = RequestQuality(value)
	return nil
}
func (RequestQuality) JSONSchema() map[string]any {
	ids := append([]string{}, qualityladder.IDs()...)
	for _, id := range qualityladder.MusicIDs() {
		if id != "original" {
			ids = append(ids, id)
		}
	}
	return map[string]any{"type": "string", "enum": ids}
}

// Request is one preparation request of explicit items. Exactly one target form
// is accepted; containers are durable requests (ContainerRequest).
type Request struct {
	VersionID        string         `json:"versionId,omitempty"`
	OperationID      string         `json:"operationId"`
	MediaID          string         `json:"mediaId,omitempty"`
	MediaIDs         []string       `json:"mediaIds,omitempty"`
	NextAfterMediaID string         `json:"nextAfterMediaId,omitempty"`
	Quality          RequestQuality `json:"quality,omitempty"`
}

// Rejection explains one target a batch could not accept. The rest of the batch
// still applies: a season with one unavailable episode still prepares the other
// episodes rather than failing whole.
type Rejection struct {
	ItemID string `json:"itemId"`
	Code   string `json:"code"`
}

// Batch is the answer to one preparation request.
type Batch struct {
	BatchID   string        `json:"batchId"`
	Items     []Preparation `json:"items"`
	Rejected  []Rejection   `json:"rejected"`
	Accepted  int           `json:"accepted"`
	Duplicate bool          `json:"duplicate"`
}

// Command carries the caller's fence for every state-changing action.
type Command struct {
	OperationID      string `json:"operationId"`
	Action           string `json:"action"`
	ExpectedRevision int64  `json:"expectedRevision"`
}

// Page is one cursor page of preparations.
type Page struct {
	Items      []Preparation `json:"items"`
	NextCursor string        `json:"nextCursor"`
	Closed     *bool         `json:"closed,omitempty"` // only operation/device-scoped actual transfer inventory
}

// stamp renders a stored millisecond instant the way every other Portico API
// renders time. Zero means "not yet", and is published as an absent field.
func stamp(ms int64) string {
	if ms <= 0 {
		return ""
	}
	return time.UnixMilli(ms).UTC().Format(time.RFC3339)
}

// actionsFor publishes exactly the actions the current state accepts, so a
// client renders live controls instead of guessing and being refused.
func actionsFor(state string) []string {
	switch state {
	case StateQueued, StateRunning:
		return []string{ActionPause, ActionCancel}
	case StatePaused:
		return []string{ActionResume, ActionCancel}
	case StateReady:
		return []string{ActionRemove}
	case StateFailed, StateUnavailable:
		return []string{ActionRetry, ActionRemove}
	default:
		return []string{}
	}
}

package dvr

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/mediaartifact"
)

// RecordingLayoutReceipt is server-private immutable publication custody. It
// confers no actor, library, recording or source authority and is never put in
// a viewer DTO, media URL or capture checkpoint.
type RecordingLayoutReceipt struct {
	OwnerKey, RecordingID, ArtifactKey                     string
	LayoutRevision                                         int64
	RootPath, RootIdentity, SourceIdentity, TargetIdentity string
	BaseTarget                                             string
	Collision                                              int
	Source, Target                                         string
	Object                                                 mediaartifact.Object
	Phase                                                  string
}

// RecordingLayoutDriver extends capture with exact prepared receipt IO. The
// scheduler must reserve a prepared receipt in its guarded transaction before
// LinkRecordingLayout and retain it across failed/cancelled/crashed IO.
type RecordingLayoutDriver interface {
	PrepareRecordingLayout(context.Context, CaptureRequest, CaptureResult, string, int64) (RecordingLayoutReceipt, error)
	LinkRecordingLayout(context.Context, RecordingLayoutReceipt, bool) (RecordingLayoutReceipt, error)
	RemoveRecordingLayout(context.Context, RecordingLayoutReceipt) error
}

func layoutRelativePath(path string) bool {
	if path == "" || len(path) > 4096 || !utf8.ValidString(path) || !filepath.IsLocal(path) || filepath.ToSlash(filepath.Clean(path)) != path {
		return false
	}
	for _, component := range strings.Split(path, "/") {
		if component == "" || component == "." || component == ".." || len(component) > 255 || strings.ContainsAny(component, "\\\x00") {
			return false
		}
		for _, r := range component {
			if unicode.IsControl(r) {
				return false
			}
		}
	}
	return true
}
func (r RecordingLayoutReceipt) Valid() bool {
	if !canonicalID.MatchString(r.OwnerKey) || !canonicalID.MatchString(r.RecordingID) || !canonicalID.MatchString(r.Object.Digest) || r.Object.Size <= 0 || r.LayoutRevision < 1 || r.RootIdentity == "" || r.SourceIdentity == "" {
		return false
	}
	if !filepath.IsAbs(r.RootPath) || filepath.Clean(r.RootPath) != r.RootPath {
		return false
	}
	if r.TargetIdentity != "" && r.TargetIdentity != r.SourceIdentity {
		return false
	}
	if r.Phase != "prepared" && r.Phase != "linked" && r.Phase != "retired" {
		return false
	}
	if r.Phase == "linked" && r.TargetIdentity == "" {
		return false
	}
	id, raw, found := strings.Cut(r.ArtifactKey, ".")
	generation, err := strconv.ParseInt(raw, 10, 64)
	if !found || id != r.RecordingID || err != nil || generation < 1 || strconv.FormatInt(generation, 10) != raw {
		return false
	}
	if r.Source != r.OwnerKey+"/captures/"+r.RecordingID+"/objects/"+r.Object.Digest || !layoutRelativePath(r.Target) {
		return false
	}
	if !layoutRelativePath(r.BaseTarget) || r.Collision < 1 {
		return false
	}
	planned, err := layoutCollisionCandidate(r.BaseTarget, r.Collision)
	if err != nil || planned != r.Target {
		return false
	}
	parts := strings.Split(r.Target, "/")
	return len(parts) >= 1 && !(canonicalID.MatchString(parts[0]) && len(parts) > 1 && (parts[1] == "captures" || parts[1] == "objects")) && r.Source != r.Target
}

var ErrRecordingLayoutCollision = errors.New("recording layout target already exists")

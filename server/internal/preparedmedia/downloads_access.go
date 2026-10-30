package preparedmedia

import (
	"context"
	"database/sql"
	"errors"
	"io"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/mediaartifact"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
)

// OpenDownloadArtifact opens a published prepared version for an offline
// download transfer.
//
// It is the offline-download counterpart of OpenSession: a download is not a
// playback session and pins no session row, but it needs exactly the same
// custody — a reader lease that holds the physical object while bytes are being
// copied, so the collector cannot remove a file a transfer is halfway through.
//
// The caller supplies the digest and size it expects. A version whose bytes
// changed underneath the caller is refused rather than served, because the
// download receipt the viewer holds vouches for that exact digest.
func (s *Service) OpenDownloadArtifact(ctx context.Context, versionID, digest string, size int64) (io.ReadSeekCloser, error) {
	if !validID.MatchString(versionID) || !validDigest.MatchString(digest) || size <= 0 {
		return nil, ErrInput
	}
	release, e := s.readerLease(digest)
	if e != nil {
		return nil, e
	}
	// Recheck under custody: a collector may have run between the caller's read
	// and this lease. A version already deleting still serves an open transfer,
	// exactly as it serves an already admitted playback session.
	v, e := readVersion(s.db.QueryRowContext(ctx, `SELECT `+versionColumns+` FROM `+versionSource+` WHERE v.id=? AND v.digest=? AND v.size=? AND v.state IN('published','deleting')`, versionID, digest, size))
	if e != nil {
		release()
		if errors.Is(e, sql.ErrNoRows) {
			return nil, ErrUnavailable
		}
		return nil, e
	}
	reader, e := s.artifacts.Open(ctx, mediaartifact.Object{Digest: digest, Size: size})
	if e != nil {
		release()
		return nil, e
	}
	return &Reader{SectionReader: io.NewSectionReader(reader, 0, size), reader: reader, release: release, Version: v}, nil
}

// RequestOptimization asks this producer for the version an offline download
// needs. It is the Optimizer seam internal/downloads calls after it admits a
// preparation for a quality ladder rung.
//
// Authority is unchanged: Submit still requires the server owner, so a viewer
// who cannot order conversions simply gets no job and their preparation
// publishes optimized_version_unavailable. The source revision is read here
// rather than asked of the caller, because downloads has no business knowing
// how a prepared-media selection is fenced.
func (s *Service) RequestOptimization(ctx context.Context, p identity.Principal, itemID, assetID, profileID, operationID string) error {
	view, e := s.View(ctx, p, itemID)
	if e != nil {
		return e
	}
	if !view.CanManage || !view.Configured {
		return ErrOwner
	}
	for _, v := range view.Versions {
		if v.ProfileID == profileID && v.Selectable {
			return nil
		}
	}
	for _, j := range view.Jobs {
		if j.ProfileID == profileID && (j.State == "queued" || j.State == "running" || j.State == "cancelling") {
			return nil
		}
	}
	for _, c := range view.Sources {
		if c.ID != assetID || !c.Available {
			continue
		}
		_, e = s.Submit(ctx, p, Request{IdempotencyKey: operations.Hash("downloads:" + operationID), ItemID: itemID, SourceID: assetID, ExpectedSourceRevision: c.Revision, ProfileID: profileID, TargetID: TargetID})
		return e
	}
	return ErrUnavailable
}

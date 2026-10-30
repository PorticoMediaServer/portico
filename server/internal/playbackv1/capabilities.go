// Package playbackv1 is the resource layer of Playback Protocol v1 (Spec — Playback
// Protocol v1 §3–§16): device capabilities, playback options, sessions, timeline,
// queues, device commands and transfers, groups and the playback event topics. It
// owns resources, revisions, leases and idempotency; bytes are produced by the
// existing delivery machinery in package playback, one presentation per session
// generation.
package playbackv1

import (
	"context"
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
)

// CapabilitiesDocument is a device's stored capability document and its
// revision. There is one capability document (playback.CapabilityDocument): it
// is what a device publishes, what is stored, and what planning reads.
type CapabilitiesDocument = playback.StoredCapabilities

// Capabilities reads the device's current document.
func (s *Service) Capabilities(ctx context.Context, device string) (CapabilitiesDocument, error) {
	out, err := s.Playback.Capabilities(ctx, device)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrNotFound
	}
	return out, err
}

// CapabilitiesRevision is the revision of the device's current document.
func (s *Service) CapabilitiesRevision(ctx context.Context, device string) (int64, error) {
	revision, err := s.Playback.CapabilitiesRevision(ctx, device)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return revision, err
}

// PutCapabilities replaces the device's whole document (spec §17.17). The
// precondition is checked and the document written in one gated transaction,
// so a refused write changes nothing. The document belongs to the device, so
// it survives token rotation and pruning.
func (s *Service) PutCapabilities(ctx context.Context, p identity.Principal, device, ifMatch string, document playback.CapabilityDocument) (int64, error) {
	want := int64(-1)
	if ifMatch != "" {
		n, ok := ParseRevision(ifMatch)
		if !ok {
			return 0, &FieldError{Path: "If-Match"}
		}
		want = n
	}
	revision, err := s.Playback.PutCapabilities(ctx, p, device, want, document)
	var field *playback.CapabilityFieldError
	var stale *playback.ErrCapabilityRevision
	switch {
	case errors.As(err, &field):
		return 0, &FieldError{Path: field.Path}
	case errors.Is(err, playback.ErrCapabilityDocumentVersion):
		return 0, &FieldError{Path: "version"}
	case errors.As(err, &stale):
		return 0, &RevisionError{Current: stale.Current, Revision: stale.Current.Revision}
	}
	return revision, err
}

// deviceDocument is the capability document planning uses for a device: what
// it published, or the baseline.
func (s *Service) deviceDocument(ctx context.Context, device string) playback.CapabilityDocument {
	if s.Playback == nil || device == "" {
		return playback.BaselineCapabilityDocument()
	}
	return s.Playback.DeviceCapabilityDocument(ctx, device)
}

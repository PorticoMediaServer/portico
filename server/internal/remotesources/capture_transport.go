package remotesources

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// captureReadMode describes this adapter's actual acquisition, not a provider
// range classification or an operation grant. Only the owned-capture producer
// issues it after resolving the original finite object and full reservation.
type captureReadMode string

const (
	captureClassifiedSequential captureReadMode = "classified_sequential"
	captureManagedUnversioned   captureReadMode = "managed_no_conditional_v1"
)

type captureReadPlan struct {
	mode             captureReadMode
	sourceSequential bool
}

// A from-start outer request does not qualify an imported wrapper's internal
// reopens or cache/spool. Restrict this owned acquisition to a direct supported
// backend. The existing graph/egress/approval checks still run independently.
func checkCaptureServeConfiguration(raw []byte, remote string) error {
	sections, err := parseServeConfiguration(raw)
	if err != nil {
		return err
	}
	name, _, ok := strings.Cut(remote, ":")
	if !ok || sections[name] == nil {
		return storage.ErrRemoteConfig
	}
	if _, err = sourceBackendCapabilities(sections[name]["type"], ""); err != nil {
		return storage.ErrRemoteRange
	}
	return nil
}

// captureReadPlanTx records an implemented-adapter limitation. Managed source
// acquisitions expose no retained conditional Version (acquisition.go); a
// loopback range, provider hash or ETag does not supply that missing port. The
// direct DAV conditional adapter remains range-first. No provider request or
// classification/settings write occurs here.
func captureReadPlanTx(ctx context.Context, tx *sql.Tx, source, path string) (captureReadPlan, error) {
	if ctx == nil || tx == nil || source == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return captureReadPlan{}, storage.ErrRemoteConfig
	}
	sequential, err := sourceObjectSequentialTx(ctx, tx, source, path)
	if err != nil {
		return captureReadPlan{}, err
	}
	if sequential {
		return captureReadPlan{mode: captureClassifiedSequential, sourceSequential: true}, nil
	}
	var kind string
	if err = tx.QueryRowContext(ctx, `SELECT kind FROM remote_sources WHERE id=? AND removed=0`, source).Scan(&kind); err != nil {
		return captureReadPlan{}, err
	}
	if !managedSourceKind(kind) {
		return captureReadPlan{}, storage.ErrRemoteRange
	}
	return captureReadPlan{mode: captureManagedUnversioned}, nil
}

// captureOwnedReadSourceTx is private to the source producer. Public lease
// admission has no force-capture flag; it keeps the observed range capability.
// This constructor independently derives the original object's acquisition
// mode in the accepting transaction and retains it in the lease.
func (s *Service) captureOwnedReadSourceTx(ctx context.Context, tx *sql.Tx, admission SourceSessionAdmission) (*SourceReadLease, error) {
	if s == nil || s.db == nil || tx == nil || admission.Purpose != SourcePlayback {
		return nil, storage.ErrRemoteConfig
	}
	var root string
	if err := tx.QueryRowContext(ctx, `SELECT root FROM remote_sources WHERE id=? AND removed=0`, admission.Observation.SourceID).Scan(&root); err != nil {
		return nil, err
	}
	// The common constructor validates and constructs the actual path. Derive
	// the same path here only after the observation's relative path is checked.
	if !validObservationObject(admission.Observation.Object) {
		return nil, storage.ErrRemoteConfig
	}
	plan, err := captureReadPlanTx(ctx, tx, admission.Observation.SourceID, filepath.Join(root, filepath.FromSlash(admission.Observation.Object)))
	if err != nil {
		return nil, err
	}
	return s.captureReadSourceTx(ctx, tx, admission, plan.mode)
}

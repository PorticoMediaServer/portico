package mounts

import (
	"context"
	"database/sql"
	"path/filepath"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/rcloneprogram"
)

// RcloneConnectionInput is an unsaved imported configuration. It cannot name
// an existing source, choose an executable, or issue a lifecycle command.
type RcloneConnectionInput struct {
	Remote string `json:"remote"`
	Config string `json:"config"`
}

type RcloneConnectionResult struct {
	Version string `json:"version"`
}

// TestRcloneConnection reuses the exact owner-save candidate admission and its
// exclusive bounded serve test, without calling its publication callback.
// The returned fact is not a saved source, approval, receipt or browse grant.
func (s *Service) TestRcloneConnection(ctx context.Context, input RcloneConnectionInput, authorize func(*sql.Tx) error) (RcloneConnectionResult, error) {
	var out RcloneConnectionResult
	if s == nil || authorize == nil {
		return out, identity.ErrUnauthorized
	}
	checkOwner := func() error {
		snapshot, err := dbwork.BeginSnapshot(ctx, s.db)
		if err != nil {
			return err
		}
		defer snapshot.Rollback()
		return authorize(snapshot.Tx())
	}
	if err := checkOwner(); err != nil {
		return out, err
	}
	if err := validateConfig(input.Config, input.Remote); err != nil {
		return out, ErrConfigInvalid
	}
	select {
	case s.validation <- struct{}{}:
		defer func() { <-s.validation }()
	default:
		return out, ErrCommandCapacity
	}
	program, err := rcloneprogram.Resolve()
	if err != nil {
		return out, ErrExecutableInvalid
	}
	executable, digest, version, err := s.approveExecutable(ctx, program.Path)
	if err != nil || version != "rclone v"+rcloneprogram.BundledVersion {
		return out, ErrExecutableInvalid
	}
	// Keep the same absolute selected-path and version check as saved sources.
	program.Path = filepath.Clean(executable)
	raw := []byte(input.Config)
	defer clear(raw)
	candidate, err := s.sourceCandidate(ctx, SourceCandidateInput{Configuration: raw, Remote: input.Remote}, authorize)
	if err != nil {
		return out, err
	}
	if err := candidate.Test(ctx, program, digest); err != nil {
		return out, err
	}
	if err := candidate.CheckAfterTest(ctx); err != nil {
		return out, err
	}
	if err := checkOwner(); err != nil {
		return out, err
	}
	return RcloneConnectionResult{Version: rcloneprogram.BundledVersion}, nil
}

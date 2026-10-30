package mounts

import (
	"context"
	"database/sql"
	"github.com/PorticoMediaServer/portico/server/internal/rcloneprogram"
)

// SourceCandidatePublication belongs to one exact owner save and candidate.
// Mounts calls CheckAfterTest only after its real connection test succeeds,
// then CommitTx in the same authorized transaction as config and receipt.
// Neither callback grants content/analysis access or runs provider I/O in SQL.
type SourceCandidateInput struct {
	Configuration []byte
	Remote        string
	Current       *Mount
}

type SourceCandidatePublication struct {
	Test           func(context.Context, rcloneprogram.Program, string) error
	CheckAfterTest func(context.Context) error
	CommitTx       func(context.Context, *sql.Tx, Mount) error
}

func (s *Service) sourceCandidate(ctx context.Context, input SourceCandidateInput, authorize func(*sql.Tx) error) (*SourceCandidatePublication, error) {
	if s.PrepareSourceCandidate == nil {
		return nil, ErrConfigInvalid
	}
	publication, err := s.PrepareSourceCandidate(ctx, input, authorize)
	if err == nil && (publication == nil || publication.Test == nil || publication.CheckAfterTest == nil || publication.CommitTx == nil) {
		return nil, ErrConfigInvalid
	}
	return publication, err
}

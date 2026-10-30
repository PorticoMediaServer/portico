package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"

	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
)

type RestoreJob struct {
	JobID      string       `json:"jobId"`
	Phase      string       `json:"phase"`
	BytesDone  int64        `json:"bytesDone"`
	BytesTotal int64        `json:"bytesTotal"`
	Result     *StageResult `json:"result,omitempty"`
	ErrorCode  string       `json:"errorCode,omitempty"`
}

// A worker keeps the accepted identity for its entire lifetime. Progress and
// journal writes must not borrow the service's mutable latest-job fields.
type restoreIdentity struct {
	jobID, operation string
}

// StartRestore returns immediately. Copy and integrity work keep their own
// lifetime, so an HTTP timeout or a disconnected browser cannot interrupt them.
func (s *Service) StartRestore(ctx context.Context, source Source, operation string) (string, error) {
	return s.StartRestoreWithPassphrase(ctx, source, operation, "")
}

func (s *Service) StartRestoreWithPassphrase(ctx context.Context, source Source, operation, passphrase string) (string, error) {
	if len(passphrase) > 1024 {
		return "", ErrInvalid
	}
	if !validOperationID(operation) {
		return "", ErrInvalid
	}
	if s.restoreUnavailable {
		return "", ErrRestoreLockUnavailable
	}
	last, err := s.LastRestore()
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	if s.stopping {
		s.mu.Unlock()
		return "", context.Canceled
	}
	if s.restoreJob != nil && s.restoreJob.Phase != "failed" {
		if operation != s.restoreOperation {
			s.mu.Unlock()
			return "", ErrRestorePending
		}
		if source != s.restoreSource {
			s.mu.Unlock()
			return "", ErrInvalid
		}
		id := s.restoreJob.JobID
		s.mu.Unlock()
		return id, nil
	}
	if last != nil && last.OperationID == operation && last.JobID != "" && last.Outcome == "restored" {
		if last.SourceDigest != restoreSourceDigest(source) {
			s.mu.Unlock()
			return "", ErrInvalid
		}
		s.restoreOperation, s.restoreSource = operation, source
		s.restoreJob = &RestoreJob{JobID: last.JobID, Phase: "staged", Result: &StageResult{Staged: true, RestartRequired: true, Validation: validationOf(last.SchemaVersion, last.Recovery)}}
		s.mu.Unlock()
		return last.JobID, nil
	}
	if operation == s.restoreOperation && s.restoreJob != nil {
		if source != s.restoreSource {
			s.mu.Unlock()
			return "", ErrInvalid
		}
	}
	id := newToken()
	s.restoreOperation, s.restoreSource = operation, source
	s.restoreJob = &RestoreJob{JobID: id, Phase: "queued"}
	s.admitLocked()
	s.mu.Unlock()
	work := restoreIdentity{jobID: id, operation: operation}
	supervise.Go("backup.restore", func() {
		s.runRestore(context.WithoutCancel(ctx), source, passphrase, work)
	})
	return id, nil
}

func (s *Service) runRestore(ctx context.Context, source Source, passphrase string, work restoreIdentity) {
	defer s.leave()
	var result StageResult
	var err error
	returned := false
	defer func() {
		panicked := recover()
		if !returned {
			err = errors.New("backup: restore worker stopped before completion")
			// A restart callback may panic after the durable staged marker was
			// published. That restore is pending, not a failed job to supersede.
			if staged, ok := stagedResult(s.state, source, work); ok {
				result, err = staged, nil
			}
		}
		s.mu.Lock()
		if s.ownsRestore(work) {
			if err != nil {
				s.restoreJob.Phase = "failed"
				s.restoreJob.ErrorCode = restoreCode(err)
			} else {
				s.restoreJob.Phase = "staged"
				s.restoreJob.Result = &result
			}
		}
		s.mu.Unlock()
		if panicked != nil {
			panic(panicked) // retain the supervisor's panic reporting
		}
	}()
	result, err = s.stageWithPassphrase(ctx, source, passphrase, work)
	returned = true
}

func stagedResult(state string, source Source, work restoreIdentity) (StageResult, bool) {
	raw, err := os.ReadFile(filepath.Join(state, RestoreStagedDir, RestoreMarkerFile))
	if err != nil {
		return StageResult{}, false
	}
	var marker restoreMarker
	if json.Unmarshal(raw, &marker) != nil || marker.JobID != work.jobID || marker.OperationID != work.operation || marker.Source != source || marker.State != markerStaged {
		return StageResult{}, false
	}
	return StageResult{Staged: true, RestartRequired: true, Validation: validationOf(marker.SchemaVersion, marker.Recovery)}, true
}

func validationOf(schemaVersion int, recovery *persistence.Recovery) Validation {
	out := Validation{SchemaVersion: schemaVersion, Integrity: "ok", Recovery: recovery}
	if recovery != nil {
		out.Integrity = "recovered"
	}
	return out
}

// Caller holds s.mu.
func (s *Service) ownsRestore(work restoreIdentity) bool {
	return work.jobID != "" && s.restoreJob != nil && s.restoreJob.JobID == work.jobID && s.restoreOperation == work.operation
}

func (s *Service) RestoreProgress() *RestoreJob {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.restoreJob == nil {
		return nil
	}
	copy := *s.restoreJob
	return &copy
}

func (s *Service) restoreProgress(work restoreIdentity, phase string, done, total int64) {
	s.mu.Lock()
	if !s.ownsRestore(work) {
		s.mu.Unlock()
		return
	}
	changed := s.restoreJob.Phase != phase
	id := s.restoreJob.JobID
	s.restoreJob.Phase = phase
	s.restoreJob.BytesDone, s.restoreJob.BytesTotal = done, total
	s.mu.Unlock()
	if changed {
		log.Printf("debug: [server] Restore %s entered %s", id, phase)
	}
}

func restoreCode(err error) string {
	switch {
	case errors.Is(err, ErrSourceInvalid):
		return "restore_source_invalid"
	case errors.Is(err, ErrInsufficientDisk), errors.Is(err, persistence.ErrRecoveryNoRoom):
		return "insufficient_disk"

	case errors.Is(err, persistence.ErrCandidatePreRelease):
		return "restore_pre_release"
	case errors.Is(err, persistence.ErrCandidateNotPortico):
		return "restore_not_portico"
	case errors.Is(err, persistence.ErrCandidateIntegrity), errors.Is(err, ErrIntegrity):
		return "restore_integrity_failed"
	default:
		return "internal"
	}
}

func restoreSourceDigest(source Source) string {
	raw, _ := json.Marshal(source)
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

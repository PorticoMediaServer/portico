package remotesources

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/remotemedia"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// SavedSourceTestResult is one completed saved-source connection test. A
// reachable=false result with a closed code is still a real test outcome, never
// a saved change or a grant. A successful test saves nothing and grants nothing.
type SavedSourceTestResult struct {
	SourceID     string `json:"sourceId"`
	Generation   string `json:"generation"`
	Reachable    bool   `json:"reachable"`
	RangeSupport string `json:"rangeSupport,omitempty"`
	ErrorCode    string `json:"errorCode,omitempty"`
}

// TestSavedSource tests the exact saved owner/source/profile association with the
// existing exclusive test and physical-stop custody. It reads the saved config
// and never writes source configuration, credentials or a receipt.
func (s *Service) TestSavedSource(ctx context.Context, source string, authorize func(*sql.Tx) error) (SavedSourceTestResult, error) {
	result := SavedSourceTestResult{}
	if s == nil || s.db == nil || s.mounts == nil || source == "" || authorize == nil {
		return result, storage.ErrRemoteConfig
	}
	// Serialize with saved-source configuration changes; this test never writes
	// a saved row, but it must not race an edit of the association it tests.
	s.commands.Lock()
	defer s.commands.Unlock()
	snapshot, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return result, err
	}
	if err = authorize(snapshot.Tx()); err != nil {
		snapshot.Rollback()
		return result, err
	}
	row, err := sourceRowTx(ctx, snapshot.Tx(), source)
	if err != nil {
		snapshot.Rollback()
		return result, err
	}
	// The raw source-row generation and the effective observation generation
	// (the mount generation for managed kinds) are both part of the association.
	effective, err := observationGenerationTx(ctx, snapshot.Tx(), source)
	if err != nil {
		snapshot.Rollback()
		return result, err
	}
	profile, err := SourceReadProfileTx(ctx, snapshot.Tx(), source)
	if err != nil {
		snapshot.Rollback()
		return result, err
	}
	var config []byte
	if err = snapshot.Tx().QueryRowContext(ctx, `SELECT config FROM remote_sources WHERE id=? AND removed=0`, source).Scan(&config); err != nil {
		snapshot.Rollback()
		return result, err
	}
	snapshot.Rollback()
	result.SourceID = row.ID
	result.Generation = effective

	var support string
	switch row.Kind {
	case SourceWebDAV:
		support, err = s.testSavedDAV(ctx, row, effective, config, authorize)
	default:
		err = s.testSavedMount(ctx, row, profile, effective, authorize)
	}
	if err != nil {
		// Only a failure marked at the candidate's provider-I/O boundary is a
		// completed test outcome. Setup, program, custody and local errors stay
		// errors and record nothing.
		var observed *providerTestFailure
		if errors.As(err, &observed) {
			// One completed test observation: checked time and the closed
			// provider failure merge atomically under one timestamp.
			if code := s.noteSourceTest(row.ID, row.MountID, effective, time.Now(), err); code != "" {
				result.ErrorCode = code
				return result, nil
			}
		}
		return result, err
	}
	// One completed test observation: checked time and success merge atomically.
	s.noteSourceTest(row.ID, row.MountID, effective, time.Now(), nil)
	result.Reachable = true
	result.RangeSupport = support
	return result, nil
}

// testSavedDAV reuses the save-time DAV candidate test with the saved config,
// the current profile and an exclusive test hold. It passes the captured
// effective generation explicitly; no row is written and no pre-save
// generation is impersonated.
func (s *Service) testSavedDAV(ctx context.Context, row Source, effective string, stored []byte, authorize func(*sql.Tx) error) (string, error) {
	expected, err := strconv.ParseInt(effective, 10, 64)
	if err != nil || expected < 1 {
		return "", storage.ErrRemoteConfig
	}
	config, err := s.privateDAVConfig(stored)
	if err != nil {
		return "", err
	}
	support, _, err := s.testDAVSetupAt(ctx, row.ID, expected, row, config, authorize)
	if err != nil {
		return "", err
	}
	return string(support), nil
}

// testSavedMount reuses the saved material and the candidate serve under the
// exclusive test hold. The per-step check pins owner, source row, mount
// association and profile, then the lease's own transport/approval custody.
func (s *Service) testSavedMount(ctx context.Context, row Source, profile SourceReadProfile, generation string, authorize func(*sql.Tx) error) error {
	life, cancel := context.WithCancel(ctx)
	defer cancel()
	slot := &sourceProcessLease{service: s, source: row.ID, generation: generation, root: row.RootPath, profile: profile, life: life, cancel: cancel}
	snapshot, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return err
	}
	var policy remotemedia.Policy
	slot.transport, err = TransportTx(ctx, snapshot.Tx(), row.ID)
	if err == nil {
		network, e := s.sourceNetworkTx(ctx, snapshot.Tx(), row.ID, row.RootPath)
		if e == nil {
			slot.networkFingerprint = network.Fingerprint
			policy = network.Policy
			slot.sequential, e = sourceSequentialTx(ctx, snapshot.Tx(), row.ID, row.RootPath)
		}
		err = e
	}
	snapshot.Rollback()
	if err != nil {
		return err
	}
	material, err := s.serveMaterial(ctx, slot)
	if err != nil {
		return err
	}
	defer clear(material.Configuration)
	check := func(ctx context.Context) error {
		snap, err := dbwork.BeginSnapshot(ctx, s.db)
		if err != nil {
			return err
		}
		if err = authorize(snap.Tx()); err != nil {
			snap.Rollback()
			return err
		}
		current, err := sourceRowTx(ctx, snap.Tx(), row.ID)
		if err != nil {
			snap.Rollback()
			return err
		}
		profileNow, err := SourceReadProfileTx(ctx, snap.Tx(), row.ID)
		if err == nil {
			var effectiveNow string
			effectiveNow, err = observationGenerationTx(ctx, snap.Tx(), row.ID)
			if err == nil && effectiveNow != generation {
				err = storage.ErrRemoteChanged
			}
		}
		snap.Rollback()
		if err != nil {
			return err
		}
		if current.Generation != row.Generation || current.Kind != row.Kind || current.RootPath != row.RootPath || current.MountID != row.MountID || profileNow != profile {
			return storage.ErrRemoteChanged
		}
		return slot.Check(ctx)
	}
	// runCandidateServe holds the source's shared operation budget exclusively
	// and stops plus physically waits for its temporary process before returning.
	return s.runCandidateServe(ctx, row.ID, generation, profile, material.Configuration, material.Remote, material.Program, material.ApprovedProgramDigest, policy, check, nil)
}

package remotesources

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/rcloneprogram"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/mounts"
	"github.com/PorticoMediaServer/portico/server/internal/remotemedia"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// prepareSourceCandidate captures approval facts for one real owner Save.
// They remain provisional through the retained serve connection test and fresh
// actor/DNS checks; publication shares the existing config/receipt transaction.
func (s *Service) prepareSourceCandidate(life context.Context, input mounts.SourceCandidateInput, authorize func(*sql.Tx) error) (*mounts.SourceCandidatePublication, error) {
	if s == nil || s.mounts == nil || life == nil || authorize == nil {
		return nil, storage.ErrRemoteConfig
	}
	if err := s.authorize(life, authorize); err != nil {
		return nil, err
	}
	profile, err := s.candidateProfile(life, input.Current)
	if err != nil {
		return nil, err
	}
	raw := append([]byte(nil), input.Configuration...)
	leaves, err := configuredBackends(raw, input.Remote)
	if err != nil {
		clear(raw)
		return nil, err
	}
	policy := remotemedia.Policy{Resolver: s.resolver}
	approvals := []remotemedia.Approval{}
	seen := map[string]bool{}
	for _, leaf := range leaves {
		if leaf.Native.Scheme != "" {
			approval, err := policy.PrepareNativeApproval(life, leaf.Native)
			if err != nil {
				clear(raw)
				return nil, err
			}
			if !seen[approval.Root] {
				seen[approval.Root] = true
				approvals = append(approvals, approval)
			}
		}
		for _, endpoint := range leaf.Origins {
			approval, err := policy.PrepareEndpointApproval(life, endpoint)
			if err != nil {
				clear(raw)
				return nil, err
			}
			if !seen[approval.Root] {
				seen[approval.Root] = true
				approvals = append(approvals, approval)
			}
		}
	}
	webdavLeaves := webDAVLeafCount(leaves)
	digest := hex.EncodeToString(sha256Sum(raw))
	var configuration any = importedSourceConfiguration{ConfigurationDigest: digest, Approvals: approvals}
	backend, origin := "rclone", ""
	if len(leaves) == 1 && leaves[0].Native.Scheme != "" {
		endpoint := leaves[0].Native
		backend = endpoint.Scheme
		configuration = nativeSourceConfiguration{Host: endpoint.Host, Port: endpoint.Port, ConfigurationDigest: digest, Approval: approvals[0]}
	}
	if len(approvals) == 1 {
		origin = approvals[0].Root
	}
	remote := input.Remote
	input.Configuration = nil
	if input.Current != nil {
		current := *input.Current
		input.Current = &current
	}
	var testing, tested atomic.Bool
	var setupObservations []webDAVSetupObservation

	var checked, used atomic.Bool
	return &mounts.SourceCandidatePublication{
		Test: func(ctx context.Context, program rcloneprogram.Program, approved string) error {
			if !testing.CompareAndSwap(false, true) {
				return storage.ErrRemoteConfig
			}
			defer clear(raw)
			err := s.testCandidate(ctx, input, raw, program, approved, approvals, profile, authorize, &setupObservations)
			if err == nil {
				tested.Store(true)
			}
			return err
		},
		CheckAfterTest: func(ctx context.Context) error {
			if !tested.Load() {
				return storage.ErrRemoteConfig
			}
			if err := life.Err(); err != nil {
				return err
			}
			if err := s.authorize(ctx, authorize); err != nil {
				return err
			}
			if err := s.checkImportedApprovals(ctx, importedSourceConfiguration{Approvals: approvals}); err != nil {
				return err
			}
			if err := s.authorize(ctx, authorize); err != nil {
				return err
			}
			if input.Current != nil {
				snapshot, err := dbwork.BeginSnapshot(ctx, s.db)
				if err != nil {
					return err
				}
				actual, err := SourceReadProfileTx(ctx, snapshot.Tx(), input.Current.ID)
				snapshot.Rollback()
				if err != nil {
					return err
				}
				if actual != profile {
					return storage.ErrRemoteChanged
				}
			}
			checked.Store(true)
			return nil
		},
		CommitTx: func(ctx context.Context, tx *sql.Tx, mount mounts.Mount) error {
			if tx == nil || !checked.Load() || !used.CompareAndSwap(false, true) || mount.ID == "" || mount.BackendRevision < 1 || !filepath.IsAbs(mount.MountPath) || filepath.Clean(mount.MountPath) != mount.MountPath {
				return storage.ErrRemoteConfig
			}
			if err := life.Err(); err != nil {
				return err
			}
			if err := authorize(tx); err != nil {
				return err
			}
			if input.Current != nil {
				actual, err := SourceReadProfileTx(ctx, tx, input.Current.ID)
				if err != nil {
					return err
				}
				if actual != profile {
					return storage.ErrRemoteChanged
				}
			}
			var generation int64
			var root, selectedRemote string
			if err := tx.QueryRowContext(ctx, `SELECT b.generation,m.mount_path,m.remote FROM mount_backend_configs b JOIN managed_mounts m ON m.id=b.mount_id WHERE m.id=?`, mount.ID).Scan(&generation, &root, &selectedRemote); err != nil {
				return err
			}
			if generation != mount.BackendRevision || root != mount.MountPath || selectedRemote != remote {
				return storage.ErrRemoteChanged
			}
			encoded, err := json.Marshal(configuration)
			if err != nil {
				return err
			}
			sealed, err := s.mounts.Seal(encoded)
			clear(encoded)
			if err != nil {
				return err
			}
			if len(setupObservations) != webdavLeaves {
				return storage.ErrRemoteChanged
			}
			support := aggregateWebDAVSetup(setupObservations)
			result, err := tx.ExecContext(ctx, `INSERT INTO remote_sources(id,kind,name,root,generation,config,state,error_code,range_support,origin,credential_present,mount_id,webdav_leaf_count)
 VALUES(?,?,?,?,?,?,'ready','',?,?,1,?,?) ON CONFLICT(id) DO UPDATE SET
 kind=excluded.kind,name=excluded.name,generation=excluded.generation,config=excluded.config,state='ready',error_code='',range_support=excluded.range_support,origin=excluded.origin,credential_present=1,webdav_leaf_count=excluded.webdav_leaf_count
 WHERE remote_sources.root=excluded.root AND remote_sources.mount_id=excluded.mount_id AND remote_sources.removed=0 AND remote_sources.generation<=excluded.generation AND remote_sources.kind IN('rclone','sftp','smb')`, mount.ID, backend, mount.Name, root, generation, sealed, support, origin, mount.ID, webdavLeaves)
			if err != nil {
				return err
			}
			count, err := result.RowsAffected()
			if err == nil && count != 1 {
				return storage.ErrRemoteChanged
			}
			if err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `DELETE FROM remote_source_webdav_tests WHERE source_id=?`, mount.ID); err != nil {
				return err
			}
			for _, row := range setupObservations {
				if row.Observed == 0 {
					row.Observed = time.Now().UnixMilli()
				}
				if _, err = tx.ExecContext(ctx, `INSERT INTO remote_source_webdav_tests(source_id,source_generation,config_digest,leaf_remote,object_path,object_size,range_support,observed_ms) VALUES(?,?,?,?,?,?,?,?)`, mount.ID, strconv.FormatInt(generation, 10), digest, row.Remote, row.Object, row.Size, row.Support, row.Observed); err != nil {
					return err
				}
			}
			return nil
		},
	}, nil
}

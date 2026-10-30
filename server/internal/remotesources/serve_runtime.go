package remotesources

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/rcloneprogram"
	"github.com/PorticoMediaServer/portico/server/internal/remotemedia"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

type sourceNetwork struct {
	Policy       remotemedia.Policy
	Fingerprint  string
	Native       *nativeSourceAuthority
	Imported     *importedSourceConfiguration
	WebDAVLeaves sql.NullInt64
}

// sourceNetworkTx reuses durable owner network approvals. An absent record is
// the existing public-only policy, never content/actor or decode permission.
// Only enabled library roots belonging to this exact source contribute rules.
func (s *Service) sourceNetworkTx(ctx context.Context, tx *sql.Tx, id, root string) (sourceNetwork, error) {
	out := sourceNetwork{Policy: remotemedia.Policy{Resolver: s.resolver}}
	var kind string
	var config []byte
	if err := tx.QueryRowContext(ctx, `SELECT kind,config,webdav_leaf_count FROM remote_sources WHERE id=? AND root=? AND removed=0`, id, root).Scan(&kind, &config, &out.WebDAVLeaves); err != nil {
		return out, err
	}
	if len(config) > 64<<10 {
		return out, storage.ErrRemoteConfig
	}
	evidence := []any{kind, hex.EncodeToString(sha256Sum(config)), out.WebDAVLeaves}
	if kind == "webdav" {
		c, err := s.privateDAVConfig(config)
		if err != nil {
			return out, err
		}
		out.Policy.Approvals = append(out.Policy.Approvals, c.Approval)
		out.Policy.SourceEndpoints = true
		out.Policy.SourcePrivateOnly = strings.HasPrefix(c.Root, "http://")
	}
	if kind == "sftp" || kind == "smb" {
		var err error
		out.Native, err = s.readNativeSourceAuthority(kind, config)
		if err != nil {
			return out, err
		}
		out.Policy.Approvals = append(out.Policy.Approvals, out.Native.config.Approval)
	}
	if kind == "rclone" {
		config, err := s.importedSourceConfig(config)
		if err != nil {
			return out, err
		}
		out.Imported = &config
		out.Policy.SourceEndpoints = true
		out.Policy.Approvals = append(out.Policy.Approvals, config.Approvals...)
	}
	rows, err := tx.QueryContext(ctx, `SELECT s.id,s.root,COALESCE(n.revision,0),COALESCE(n.approvals_json,'[]') FROM library_sources s LEFT JOIN library_network_policy n ON n.library_id=s.library_id WHERE s.kind='remote' AND s.enabled=1 ORDER BY s.id`)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	total := 0
	for rows.Next() {
		var id, directory, raw string
		var revision int64
		if err = rows.Scan(&id, &directory, &revision, &raw); err != nil {
			return out, err
		}
		relative, e := filepath.Rel(root, directory)
		if e != nil || !filepath.IsLocal(relative) {
			continue
		}
		total += len(raw)
		if total > 1<<20 || revision < 0 {
			return out, storage.ErrRemoteConfig
		}
		var approvals []remotemedia.Approval
		if json.Unmarshal([]byte(raw), &approvals) != nil || len(approvals) > 256 {
			return out, storage.ErrRemoteConfig
		}

		evidence = append(evidence, []any{id, directory, revision, raw})
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	raw, err := json.Marshal(evidence)
	if err != nil {
		return out, err
	}
	out.Fingerprint = hex.EncodeToString(sha256Sum(raw))
	return out, nil
}
func sha256Sum(raw []byte) []byte { h := sha256.Sum256(raw); return h[:] }

// Unknown WebDAV capability is not permission to trust rclone's loopback206.
// The leaf count is produced with the saved graph, never inferred from an HTTP
// approval or a library policy. A missing imported classification fails closed.
func unqualifiedWebDAVTx(ctx context.Context, tx *sql.Tx, source string) (bool, error) {
	var kind, support string
	var leaves sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT kind,range_support,webdav_leaf_count FROM remote_sources WHERE id=? AND removed=0`, source).Scan(&kind, &support, &leaves); err != nil {
		return false, err
	}
	return unqualifiedWebDAV(kind, support, leaves)
}

func unqualifiedWebDAV(kind, support string, leaves sql.NullInt64) (bool, error) {
	switch kind {
	case "webdav":
		return support != "conditional", nil
	case "rclone":
		if !leaves.Valid || leaves.Int64 < 0 || leaves.Int64 > 32 {
			return false, storage.ErrRemoteConfig
		}
		return leaves.Int64 > 0 && support != "conditional", nil
	case "sftp", "smb":
		return false, nil
	default:
		return false, storage.ErrRemoteConfig
	}
}

type unknownWebDAVRange struct{}

func (*unknownWebDAVRange) Error() string { return storage.ErrRemoteRange.Error() }
func (*unknownWebDAVRange) Unwrap() error { return storage.ErrRemoteRange }

var errUnknownWebDAVRange error = &unknownWebDAVRange{}

// The existing limitation is shared, but diagnostics must not turn missing
// qualification into an observed unsupported fact.
func (s *Service) requireWebDAVRandom(ctx context.Context, source string) error {
	snapshot, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return err
	}
	defer snapshot.Rollback()
	unqualified, err := unqualifiedWebDAVTx(ctx, snapshot.Tx(), source)
	if err != nil || !unqualified {
		return err
	}
	var support string
	if err = snapshot.Tx().QueryRowContext(ctx, `SELECT range_support FROM remote_sources WHERE id=? AND removed=0`, source).Scan(&support); err != nil {
		return err
	}
	if support == "unsupported" {
		return storage.ErrRemoteRange
	}
	return errUnknownWebDAVRange
}

// Imported/wrapped graphs have no established end-to-end from-zero proof.
// Classification still selects sequential mode, but that alone must not admit
// a wrapper which could reopen the origin at nonzero offsets.
func (s *Service) requireWebDAVSequential(ctx context.Context, source string) error {
	view, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return err
	}
	defer view.Rollback()
	var kind, support string
	var leaves sql.NullInt64
	if err = view.Tx().QueryRowContext(ctx, `SELECT kind,range_support,webdav_leaf_count FROM remote_sources WHERE id=? AND removed=0`, source).Scan(&kind, &support, &leaves); err != nil {
		return err
	}
	unqualified, err := unqualifiedWebDAV(kind, support, leaves)
	if err != nil || !unqualified || kind != "rclone" {
		return err
	}
	if support == "unsupported" {
		return storage.ErrRemoteRange
	}
	return errUnknownWebDAVRange
}

func sourceObjectSequentialTx(ctx context.Context, tx *sql.Tx, source, path string) (bool, error) {
	unqualified, err := unqualifiedWebDAVTx(ctx, tx, source)
	if err != nil {
		return false, err
	}
	var sequential bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM remote_sources WHERE id=? AND (range_support='unsupported' OR error_code='range_unsupported')) OR EXISTS(SELECT 1 FROM remote_analysis_status WHERE path=? AND code='range_unsupported')`, source, path).Scan(&sequential)
	return unqualified || sequential, err
}
func sourceSequentialTx(ctx context.Context, tx *sql.Tx, source, root string) (bool, error) {
	unqualified, err := unqualifiedWebDAVTx(ctx, tx, source)
	if err != nil {
		return false, err
	}
	var sequential bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM remote_sources WHERE id=? AND (range_support='unsupported' OR error_code='range_unsupported')) OR EXISTS(SELECT 1 FROM remote_analysis_status WHERE code='range_unsupported' AND substr(path,1,length(?)+1)=?||'/')`, source, root, root).Scan(&sequential)
	return unqualified || sequential, err
}

func (s *Service) startReadRuntime(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.serveMu.Lock()
	defer s.serveMu.Unlock()
	if s.serveClosed {
		return storage.ErrRemoteOffline
	}
	if s.serveLife != nil {
		return storage.ErrBusy
	}
	s.serveLife = ctx
	return nil
}

// ensureSourceServe starts only the one upstream-rclone path from retained
// config. Caller transactions must be closed before this method is reached.
// The caller's real purpose/target check runs before and after this method.
func (s *Service) ensureSourceServe(ctx context.Context, binding SourceReadBinding, expectedEpoch string) (*rcloneprogram.ServeProcess, error) {
	if waited, err := s.waitSourceTest(ctx, binding.Observation.SourceID); waited {
		if err != nil {
			return nil, err
		}
		return s.ensureSourceServe(ctx, binding, expectedEpoch)
	}
	s.serveMu.Lock()
	life := s.serveLife
	if s.sourceRemovals[binding.Observation.SourceID] {
		s.serveMu.Unlock()
		return nil, storage.ErrBusy
	}
	slot := s.serveLeases[binding.Observation.SourceID]
	if slot != nil {
		if slot.retiring {
			// The idle cut already chose this slot and keeps it registered until
			// its supervisor finishes the physical Wait. A new caller waits for
			// the next published start instead of sharing a dying process.
			s.serveMu.Unlock()
			return nil, storage.ErrBusy
		}
		process := slot.process
		same := slot.generation == binding.Observation.Generation && slot.profile == binding.Profile && slot.root == binding.Root
		if same && slot.captureMode != binding.captureMode {
			if expectedEpoch != "" {
				s.serveMu.Unlock()
				return nil, storage.ErrRemoteChanged
			}
			// A VFS-backed reader cannot supply an original owned-capture body.
			// Do not replace an active reader/operation or start a second process.
			// An idle slot can retire, but it remains reserved until physical Wait.
			idle := process != nil && process.OperationCounts().Active == 0
			for id := range s.readLeases[binding.Observation.SourceID] {
				if id != binding.LeaseID {
					idle = false
				}
			}
			if idle {
				slot.retiring = true
				slot.cancel()
			}
			s.serveMu.Unlock()
			if idle {
				// No body has opened and no physical epoch was lent to this
				// caller. Join the idle process before reserving the same slot
				// in the new mode, within the original caller's deadline.
				if err := process.Wait(ctx); err != nil && !errors.Is(err, rcloneprogram.ErrServeStopped) {
					return nil, err
				}
				return s.ensureSourceServe(ctx, binding, expectedEpoch)
			}
			return nil, storage.ErrBusy
		}
		if !same {
			s.serveMu.Unlock()
			slot.cancel()
			if expectedEpoch == "" {
				return nil, storage.ErrBusy
			}
			return nil, storage.ErrRemoteChanged
		}
		if process == nil {
			s.serveMu.Unlock()
			return nil, storage.ErrBusy
		}
		if expectedEpoch != "" && process.Generation() != expectedEpoch {
			s.serveMu.Unlock()
			return nil, storage.ErrRemoteOffline
		}
		// Admission is registered under serveMu before the lock is dropped. The
		// idle cut decides under the same lock, so it either observes this use
		// or wins first and the recheck below refuses the retired slot.
		slot.touch()
		s.serveMu.Unlock()
		if err := process.Check(ctx); err != nil {
			// A new metadata/reader admission has not consumed this physical
			// epoch. A committed policy/root/profile change must retire the old
			// process, then let that new caller's normal bounded turn try again.
			// Existing readers retain their exact epoch and fail terminally.
			if expectedEpoch == "" && ctx.Err() == nil && (errors.Is(err, storage.ErrRemoteChanged) || (err == context.Canceled || err == rcloneprogram.ErrServeStopped) && slot.life.Err() != nil) {
				return nil, storage.ErrBusy
			}
			return nil, err
		}
		s.serveMu.Lock()
		admitted := s.serveLeases[binding.Observation.SourceID] == slot && !slot.retiring && slot.life.Err() == nil
		s.serveMu.Unlock()
		if !admitted {
			return nil, storage.ErrBusy
		}
		return process, nil
	}
	closed := s.serveClosed
	s.serveMu.Unlock()
	if closed || life == nil || life.Err() != nil {
		return nil, storage.ErrRemoteOffline
	}
	if expectedEpoch != "" {
		return nil, storage.ErrRemoteOffline
	}
	slot, err := s.reserveSourceProcessMode(life, binding.Observation.SourceID, binding.Observation.Generation, binding.Profile.Revision, binding.captureMode)
	if err != nil {
		if errors.Is(err, storage.ErrBusy) {
			if waited, e := s.waitSourceTest(ctx, binding.Observation.SourceID); waited {
				if e != nil {
					return nil, e
				}
				return s.ensureSourceServe(ctx, binding, expectedEpoch)
			}
		}
		return nil, err
	}
	transferred := false
	defer func() {
		if !transferred {
			slot.Release()
		}
	}()
	snapshot, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return nil, err
	}
	network, err := s.sourceNetworkTx(ctx, snapshot.Tx(), slot.source, slot.root)
	if err == nil {
		slot.sequential, err = sourceSequentialTx(ctx, snapshot.Tx(), slot.source, slot.root)
	}
	snapshot.Rollback()
	if err != nil {
		return nil, err
	}
	slot.networkFingerprint = network.Fingerprint
	material, err := s.serveMaterial(ctx, slot)
	if err != nil {
		return nil, err
	}
	defer clear(material.Configuration)
	if slot.captureMode != "" {
		if err = checkCaptureServeConfiguration(material.Configuration, material.Remote); err != nil {
			return nil, err
		}
	}
	backend, host, port, err := serveTransport(material.Configuration, material.Remote)
	if err != nil {
		return nil, err
	}
	slot.capabilities, err = configuredCapabilities(material.Configuration, material.Remote)
	if err != nil {
		return nil, err
	}
	if network.Imported != nil {
		if err = s.checkImportedMaterial(ctx, *network.Imported, material.Configuration, material.Remote); err != nil {
			return nil, err
		}
		leaves, err := configuredBackends(material.Configuration, material.Remote)
		if err != nil {
			return nil, err
		}
		if !network.WebDAVLeaves.Valid || network.WebDAVLeaves.Int64 != int64(webDAVLeafCount(leaves)) {
			return nil, storage.ErrRemoteChanged
		}
	}
	if backend == "sftp" || backend == "smb" {
		// Check retained native configuration before selecting its route.
		if network.Imported == nil {
			if err = s.checkNativeMaterial(ctx, network.Native, backend, host, port, material.Configuration); err != nil {
				return nil, err
			}
		}
	}
	egress, err := s.startRcloneEgress(slot.life, material.Configuration, material.Remote, network.Policy, slot.Check)
	if err != nil {
		return nil, err
	}
	slot.egress = egress
	endpoint, err := privateServePort()
	if err != nil {
		return nil, err
	}
	metadata, err := privateServePort()
	if err != nil {
		return nil, err
	}
	if metadata == endpoint {
		return nil, storage.ErrBusy
	}
	// The serve process shares this source's one operation budget with direct
	// reads, folder lists and existing-source tests. StartServe requires the
	// budget capacity to equal the profile limit it was created from.
	ops, err := s.sourceOperations(ctx, slot.source, slot.generation)
	if err != nil {
		return nil, err
	}
	if ops.budget.Counts().Capacity != slot.profile.Limits.Connections {
		return nil, storage.ErrRemoteChanged
	}
	spec := rcloneprogram.ServeCommandSpec{SourceID: slot.source, SourceGeneration: slot.generation, ApprovedProgramDigest: material.ApprovedProgramDigest, PrivateDirectory: s.private, Configuration: egress.TransientConfiguration(material.Configuration), Remote: material.Remote, Port: endpoint, MetadataPort: metadata, Username: identity.Token(), Password: identity.Token(), Connections: slot.profile.Limits.Connections, RequestsPerSecond: slot.profile.Limits.RequestsPerSecond, BytesPerSecond: slot.profile.Limits.BytesPerSecond, SharedOperations: ops.budget, SequentialReads: slot.sequential || slot.captureMode != "",
		DirCacheSeconds: slot.transport.DirCacheSeconds, ReadBufferBytes: slot.transport.ReadBufferBytes,
		ConnectTimeoutSeconds: slot.transport.ConnectTimeoutSeconds, IdleTimeoutSeconds: slot.transport.IdleTimeoutSeconds,
		Retries: slot.transport.Retries, LowLevelRetries: slot.transport.LowLevelRetries,
		Enforce: func(cmd *exec.Cmd) error {
			if err := slot.Check(ctx); err != nil {
				return err
			}
			cmd.Env = append(cmd.Env, "RCLONE_HTTP_PROXY="+egress.HTTPProxy, "RCLONE_SFTP_HTTP_PROXY="+egress.SFTPProxy)
			return nil
		}}
	// Cache controls are existing source facts. Start-only mode always uses the
	// upstream no-cache path; neither mode creates Portico whole-file spools.
	if material.CacheBytes > 0 && !slot.sequential && slot.captureMode == "" {
		cache, err := os.MkdirTemp(s.private, "serve-cache-")
		if err != nil {
			return nil, err
		}
		slot.cache = cache
		spec.CacheDirectory, spec.CacheBytes, spec.CacheFloor = cache, material.CacheBytes, material.CacheFloor
	}
	process, err := rcloneprogram.StartServe(life, material.Program, spec, slot)
	// StartServe consumes/releases custody on every physical success/failure.
	transferred = true
	if err != nil {
		return nil, mapError(err)
	}
	if err = slot.Check(ctx); err != nil {
		process.Stop()
		return nil, err
	}
	s.serveMu.Lock()
	if s.serveLeases[slot.source] != slot || slot.CheckLocal() != nil {
		s.serveMu.Unlock()
		process.Stop()
		return nil, storage.ErrRemoteOffline
	}
	slot.process = process
	slot.touch()
	s.serveMu.Unlock()
	// Publish the custom program's observed version for the source page, fenced
	// to this exact mount generation. A best-effort projection; it never changes
	// source identity or read authority.
	if version := process.ProgramVersion(); version != "" && material.MountID != "" {
		// Bind the exact mount captured for this start, not whatever mount the
		// source points at now: a replacement association with the same numeric
		// generation must not receive the old program's observation.
		_, _ = dbwork.ExecWrite(ctx, s.db, dbwork.ClassBackgroundMedia, `UPDATE mount_backend_configs SET version=? WHERE mount_id=? AND generation=? AND EXISTS(SELECT 1 FROM remote_sources r WHERE r.id=? AND r.removed=0 AND r.mount_id=?) AND version<>?`, version, material.MountID, slot.generation, slot.source, material.MountID, version)
	}
	return process, nil
}
func privateServePort() (int, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}

type serveMaterial struct {
	Program                rcloneprogram.Program
	MountID                string
	Remote                 string
	Configuration          []byte
	ApprovedProgramDigest  string
	CacheBytes, CacheFloor int64
}

func (s *Service) serveMaterial(ctx context.Context, slot *sourceProcessLease) (serveMaterial, error) {
	var out serveMaterial
	var kind, mount string
	var raw []byte
	if err := s.db.QueryRowContext(ctx, `SELECT kind,mount_id,config FROM remote_sources WHERE id=? AND removed=0`, slot.source).Scan(&kind, &mount, &raw); err != nil {
		return out, err
	}
	if managedSourceKind(kind) {
		if s.mounts == nil || mount == "" {
			return out, storage.ErrRemoteConfig
		}
		captured, err := s.mounts.CaptureServeSource(ctx, mount, slot.generation)
		if err != nil {
			return out, err
		}
		return serveMaterial{Program: captured.Program, MountID: captured.MountID, ApprovedProgramDigest: captured.ApprovedProgramDigest, Remote: captured.Remote, Configuration: captured.Configuration, CacheBytes: captured.CacheBytes, CacheFloor: captured.CacheFloor}, nil
	}
	if kind != "webdav" || len(raw) > 64<<10 {
		return out, storage.ErrRemoteConfig
	}
	c, err := s.privateDAVConfig(raw)
	if err != nil {
		return out, err
	}
	program, err := rcloneprogram.Resolve()
	if err != nil {
		return out, err
	}
	return serveMaterial{Program: program, Remote: "portico:", Configuration: davServeConfiguration(c)}, nil
}

// privateDAVConfig uses the existing bounded private-format opener. Its current
// format is plain configuration; it creates no key or replacement auth store.
func (s *Service) privateDAVConfig(stored []byte) (remotemedia.DAVConfig, error) {
	var c remotemedia.DAVConfig
	if s.mounts == nil {
		return c, storage.ErrRemoteConfig
	}
	raw, err := s.mounts.Open(stored)
	if err != nil {
		return c, storage.ErrRemoteCredentials
	}
	defer clear(raw)
	if json.Unmarshal(raw, &c) != nil {
		return c, storage.ErrRemoteConfig
	}
	_, err = remotemedia.NewSourceDAV(c, s.resolver)
	return c, err
}

// serveTransport classifies the complete selected graph, never a native
// provider replacement. Mixed graphs retain HTTP proxy and direct native checks.
func serveTransport(raw []byte, remote string) (backend, host, port string, err error) {
	leaves, err := configuredBackends(raw, remote)
	if err != nil {
		return "", "", "", err
	}
	if len(leaves) == 1 {
		leaf := leaves[0]
		if leaf.Native.Scheme != "" {
			return leaf.Kind, leaf.Native.Host, strconv.Itoa(leaf.Native.Port), nil
		}
		return leaf.Kind, "", "", nil
	}
	return "compound", "", "", nil
}

func configuredCapabilities(raw []byte, remote string) (backendCapabilities, error) {
	leaves, err := configuredBackends(raw, remote)
	if err != nil {
		return backendCapabilities{}, err
	}
	if len(leaves) == 1 {
		return sourceBackendCapabilities(leaves[0].Kind, leaves[0].Native.Host)
	}
	facts := backendCapabilities{Backend: "compound", AdmittedOperationsBounded: true, HiddenRetriesWithinOperation: true, ThrottleReportsAsynchronous: true}
	for _, leaf := range leaves {
		child, err := sourceBackendCapabilities(leaf.Kind, leaf.Native.Host)
		if err != nil {
			return backendCapabilities{}, err
		}
		facts.HTTPProxyConfigured = facts.HTTPProxyConfigured || child.HTTPProxyConfigured
		facts.HTTPSOpaque = facts.HTTPSOpaque || child.HTTPSOpaque
		facts.NativeDirect = facts.NativeDirect || child.NativeDirect
		facts.NativeHostnameCheckConnectGap = facts.NativeHostnameCheckConnectGap || child.NativeHostnameCheckConnectGap
		facts.SFTPConnectionPoolBounded = facts.SFTPConnectionPoolBounded || child.SFTPConnectionPoolBounded
		facts.SMBIdlePoolMayExceedOperations = facts.SMBIdlePoolMayExceedOperations || child.SMBIdlePoolMayExceedOperations
	}
	return facts, nil
}

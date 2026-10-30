package mounts

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/authorityfiles"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/rcloneprogram"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"github.com/PorticoMediaServer/portico/server/internal/usermutation"
)

// S3Setup is a bounded managed form, not a generic config/command passthrough.
// OAuth-only backends remain available through imported owner-authorized config.
type S3Setup struct {
	Endpoint  string `json:"endpoint"`
	Region    string `json:"region"`
	AccessKey string `json:"accessKey"`
	SecretKey string `json:"secretKey"`
	Bucket    string `json:"bucket"`
	Prefix    string `json:"prefix"`
}

func managedS3(v S3Setup) (string, string, error) {
	for _, s := range []string{v.Endpoint, v.Region, v.AccessKey, v.SecretKey, v.Bucket, v.Prefix} {
		if len(s) > 4096 || strings.ContainsAny(s, "\x00\r\n") {
			return "", "", ErrConfigInvalid
		}
	}
	if v.AccessKey == "" || v.SecretKey == "" || v.Bucket == "" || strings.ContainsAny(v.Bucket, "/:\\") || !nativeRelative(v.Prefix) {
		return "", "", ErrConfigInvalid
	}
	if v.Endpoint != "" && !strings.HasPrefix(v.Endpoint, "https://") {
		return "", "", ErrConfigInvalid
	}
	config := "[portico]\ntype = s3\nprovider = Other\nenv_auth = false\naccess_key_id = " + v.AccessKey + "\nsecret_access_key = " + v.SecretKey + "\nregion = " + v.Region + "\n"
	if v.Endpoint != "" {
		config += "endpoint = " + v.Endpoint + "\n"
	}
	remote := "portico:" + v.Bucket
	if v.Prefix != "" {
		remote += "/" + v.Prefix
	}
	return config, remote, nil
}

func (s *Service) configure(ctx context.Context, actor, id, hash string, c Command, authorize func(*sql.Tx) error, readCacheAccepted func(context.Context, *sql.Tx, Mount, Mount) error) (Receipt, error) {
	defer authorityfiles.Process.Admit()()
	var zero Receipt
	if id == "" || c.ExpectedRevision < 1 {
		return zero, ErrInvalid
	}
	m, err := s.get(id)
	if err != nil {
		return zero, err
	}
	if m.ControlRevision != c.ExpectedRevision || m.RemovalPending {
		return zero, ErrCommandConflict
	}
	if c.readCacheGeneration != 0 && m.BackendRevision != c.readCacheGeneration {
		return zero, ErrCommandConflict
	}
	before := m
	if c.sourceGeneration != 0 && m.BackendRevision != c.sourceGeneration {
		return zero, ErrCommandConflict
	}
	b, err := s.Native(ctx, id)
	if err != nil {
		return zero, err
	}
	// A changed binary can be explicitly reapproved; Native only checks the
	// configuration generation, not the binary digest, until a lifecycle starts.
	if c.Name == "" {
		c.Name = m.Name
	}
	// The program choice is preserved before any executable fallback: a request
	// with neither a path nor an included intent (Advanced/cache edits) keeps
	// the saved selection instead of silently demoting it to custom. An explicit
	// non-empty executable means custom; explicit included=true resolves the
	// bundled program even when the saved selection was custom.
	included := m.ProgramIncluded
	switch {
	case c.UseIncluded != nil && *c.UseIncluded:
		program, e := rcloneprogram.Resolve()
		if e != nil || c.Executable != "" && c.Executable != program.Path {
			return zero, ErrExecutableInvalid
		}
		c.Executable = program.Path
		included = true
	case c.UseIncluded != nil && !*c.UseIncluded:
		if c.Executable == "" {
			return zero, ErrExecutableInvalid
		}
		included = false
	case c.Executable != "":
		included = false
	default:
		c.Executable = m.Executable
	}
	if c.Remote == "" {
		c.Remote = b.remote
	}
	if strings.TrimSpace(c.Name) == "" || len(c.Name) > 100 {
		return zero, ErrInvalid
	}
	raw := []byte(c.Config)
	if len(raw) == 0 {
		stored, e := os.ReadFile(b.config)
		if e != nil {
			return zero, ErrConfigInvalid
		}
		if strings.HasPrefix(string(stored), sealedConfigPrefix) {
			// Sealed-era file the migration could not convert: re-enter the
			// configuration instead of refusing to start.
			return zero, ErrConfigInvalid
		}
		raw = stored
	}
	if err = validateConfig(string(raw), c.Remote); err != nil {
		return zero, ErrConfigInvalid
	}
	publication, err := s.sourceCandidate(ctx, SourceCandidateInput{Configuration: raw, Remote: c.Remote, Current: &m}, authorize)
	if err != nil {
		clear(raw)
		return zero, err
	}
	executable, digest, version, err := s.approveExecutable(ctx, c.Executable)
	if err != nil {
		return zero, err
	}
	candidate, err := s.candidateFile(raw)
	clear(raw)
	if err != nil {
		return zero, err
	}
	defer os.Remove(candidate)
	if err = publication.Test(ctx, rcloneprogram.Program{Path: executable}, digest); err != nil {
		return zero, err
	}
	if publication != nil {
		if err = publication.CheckAfterTest(ctx); err != nil {
			return zero, err
		}
	}
	_, _, cache, floor, err := s.BackendInfo(ctx, id)
	if err != nil {
		return zero, err
	}
	if c.CacheBytes != nil {
		cache = *c.CacheBytes
	}
	if c.CacheFloor != nil {
		floor = *c.CacheFloor
	}
	if cache < 0 || cache > 64<<30 || cache != 0 && cache < 64<<20 || floor < 1<<30 || floor > 1<<40 {
		return zero, ErrInvalid
	}
	// Immutable installation first, active pointer/receipt atomically second.
	// An abandoned generation is recoverable private state, never an active config.
	file := id + ".g-" + identity.Token() + ".conf"
	installed := filepath.Join(s.private, file)
	if err = os.Rename(candidate, installed); err != nil {
		return zero, err
	}
	if err = syncDirectory(s.private); err != nil {
		return zero, err
	}
	committed := false
	defer func() {
		if !committed {
			os.Remove(installed)
		}
	}()
	s.mu.Lock()
	defer s.mu.Unlock()
	defer s.publish()
	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassFrom(ctx, dbwork.ClassInteractive))
	if err != nil {
		return zero, err
	}
	tx := gated.Tx()
	defer gated.Rollback()
	if c.sourceProgram != nil {
		prior, err := s.checkSourceProgram(ctx, tx, id, c, authorize)
		if err != nil {
			return zero, err
		}
		if prior != nil {
			gated.Rollback()
			return *prior, s.reloadSourceProgramLocked(id)
		}
		if err = c.sourceProgram.fresh(tx); err != nil {
			return zero, err
		}
	} else if err = authorize(tx); err != nil {
		return zero, err
	}
	if c.sourceGeneration != 0 {
		control, err := importedSourceControlTx(ctx, tx, id, c.sourceGeneration)
		if err != nil {
			return zero, err
		}
		if control != c.ExpectedRevision {
			return zero, ErrCommandConflict
		}
	}
	result, err := tx.Exec(`UPDATE mount_controls SET revision=revision+1,restart_generation=restart_generation+1 WHERE mount_id=? AND revision=? AND removal_pending=0`, id, c.ExpectedRevision)
	if err != nil {
		return zero, err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return zero, ErrCommandConflict
	}
	if _, err = tx.Exec(`UPDATE mount_backend_configs SET generation=generation+1,config_file=?,version=?,cache_bytes=?,cache_floor=? WHERE mount_id=?`, file, version, cache, floor, id); err != nil {
		return zero, err
	}
	if _, err = tx.Exec(`UPDATE managed_mounts SET name=?,executable=?,digest=?,program_included=?,remote=?,error='' WHERE id=?`, c.Name, executable, digest, included, c.Remote, id); err != nil {
		return zero, err
	}
	m.Name = c.Name
	m.Executable = executable
	m.ExecutableDigest = digest
	m.ProgramIncluded = included
	m.ExecutableVersion = version
	m.ControlRevision++
	m.BackendRevision++
	m.CacheBytes, m.CacheFloor = cache, floor
	m.Error = ""
	if publication != nil {
		if err = publication.CommitTx(ctx, tx, m); err != nil {
			return zero, err
		}
	}
	// The source publication and its exact accepted child are still inside
	// this same transaction. A private cache continuation cannot adopt latest.
	if readCacheAccepted != nil {
		if err = readCacheAccepted(ctx, tx, before, m); err != nil {
			return zero, err
		}
	}
	receipt := Receipt{c.OperationID, id, m.ControlRevision, true, false, &m}
	if c.sourceProgram != nil {
		err = usermutation.SaveContext(ctx, tx, usermutation.Result{Status: "applied", TargetID: c.sourceProgram.source, Revision: m.ControlRevision}, time.Now().UnixMilli())
	} else {
		err = s.record(tx, actor, hash, receipt)
	}
	if err != nil {
		return zero, err
	}
	// Once COMMIT is attempted its outcome can be ambiguous. Retain this synced
	// private generation on error; recovery removes unreferenced files later.
	// Deleting it here could break an acceptance discovered by same-key replay.
	if c.sourceProgram != nil {
		committed = true
	}
	if err = gated.Commit(); err != nil {
		return zero, err
	}
	committed = true
	if s.NativeInvalidated != nil {
		s.NativeInvalidated(id)
	}
	if c.sourceProgram != nil {
		return receipt, s.reloadSourceProgramLocked(id)
	}
	s.available[id] = false
	if child := s.active[id]; child != nil && !child.stopping {
		child.invalidateRuntime()
		child.stopping = true
		child.stopAt = time.Now()
		_ = child.control.Close()
	}
	delete(s.retry, id)
	return receipt, nil
}

// Cleanup only private, unreferenced candidate/generation files. No remote data
// or mount path is traversed. Retain old generations for a day for late helpers.
// Called only by mounts.New before backup service creation.
func (s *Service) recoverNativeFiles() {
	entries, err := os.ReadDir(s.private)
	if err != nil {
		return
	}
	used := map[string]bool{}
	rows, err := s.db.Query(`SELECT config_file FROM mount_backend_configs`)
	if err != nil {
		return
	}
	for rows.Next() {
		var name string
		if rows.Scan(&name) == nil {
			used[name] = true
		}
	}
	rows.Close()
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || used[name] || !(strings.HasSuffix(name, ".candidate") || strings.Contains(name, ".g-") && strings.HasSuffix(name, ".conf")) {
			continue
		}
		info, err := e.Info()
		if err == nil && time.Since(info.ModTime()) > 24*time.Hour {
			_ = os.Remove(filepath.Join(s.private, name))
		}
	}
}

var _ = errors.Is
var _ = storage.ErrRemoteOffline

func (s *Service) PrivateDigest(raw []byte) string {
	return s.fingerprint("remote-sources", "", Command{Config: string(raw)})
}

// CacheSpaceAvailable checks the real host free-space floor for private native
// acquisition caches as well as optional compatibility mounts.
func CacheSpaceAvailable(path string, floor int64) bool { return cacheSpaceAvailable(path, floor) }

// CacheSpaceFor checks that growth more bytes can still be written while
// keeping the host free-space floor. It is the growth-aware form used before a
// cache copy, not just a snapshot of free space.
func CacheSpaceFor(path string, floor, growth int64) bool {
	if growth < 0 {
		return false
	}
	return cacheSpaceFor(path, floor, growth)
}

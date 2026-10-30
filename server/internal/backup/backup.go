// Package backup creates consistent server snapshots with optional authenticated
// encryption and crash-resumable, verified restore.
package backup

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/authorityfiles"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
)

// Layout names.
const (
	// BackupsDir holds one folder per backup under the state directory.
	BackupsDir = "Backups"
	// DatabaseName is the database copy inside every backup folder.
	DatabaseName = "Database/server.sqlite"
	// ManifestName describes a backup folder. Names inside use "/".
	ManifestName = "manifest.json"
	// RestoreStagedDir holds a validated restore waiting for a restart.
	RestoreStagedDir = "Backups/restore-staged"
	// RestoreMarkerFile is the staged-restore marker inside RestoreStagedDir.
	RestoreMarkerFile = "restore.json"
	// RestoreResultFile records the last restore's outcome.
	RestoreResultFile = "Backups/restore-result.json"
	// Format and Version identify the manifest shape.
	Format        = "portico-backup"
	Version       = 3
	HashedVersion = 2
	// LegacyVersion predates mandatory database hashes. It remains readable for recovery.
	LegacyVersion = 1
	// DefaultKeepCount is the shipped scheduled-backup retention, matching the
	// maintenance settings default.
	DefaultKeepCount = 7
)

// timestampLayout names a backup folder, in UTC.
const timestampLayout = "2006-01-02T150405Z"

// In-process polling/idempotency receipts have a bounded replay lifetime.
// Backup files and the last-result status have their own independent lifetimes.
const backupReplayWindow = 24 * time.Hour

// Kinds of backup. The keep count applies to scheduled backups;
// pre-restore copies are the automatic copy taken before a restore applies.
type Kind string

const (
	KindScheduled  Kind = "scheduled"
	KindManual     Kind = "manual"
	KindPreRestore Kind = "pre-restore"
)

// FileEntry is one file in a backup folder, named with "/" separators.
type FileEntry struct {
	Name   string `json:"name"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

// Manifest is manifest.json: identity plus integrity hashes, not security.
type Manifest struct {
	SignInsRemoved bool        `json:"signInsRemoved,omitempty"`
	Format         string      `json:"format"`
	Version        int         `json:"version"`
	ServerID       string      `json:"serverId"`
	ServerVersion  string      `json:"serverVersion"`
	SchemaVersion  int         `json:"schemaVersion"`
	CreatedAt      string      `json:"createdAt"`
	Kind           Kind        `json:"kind"`
	Files          []FileEntry `json:"files"`
	BackupID       string      `json:"backupId,omitempty"`
	BaselineDigest string      `json:"baselineDigest,omitempty"`
	streamPath     string
	streamHash     string
}

// Info is one listed backup.
type Info struct {
	ID            string `json:"id"`
	CreatedAt     string `json:"createdAt"`
	Kind          Kind   `json:"kind"`
	SchemaVersion int    `json:"schemaVersion"`
	ServerVersion string `json:"serverVersion"`
	Bytes         int64  `json:"bytes"`
	Path          string `json:"path"`
	Encrypted     bool   `json:"encrypted,omitempty"`
}

// BackupResult is the current process's last completed asynchronous job. Errors
// are stable codes; filesystem paths and secret values never enter status.
type BackupResult struct {
	JobID     string `json:"jobId"`
	BackupID  string `json:"backupId,omitempty"`
	Outcome   string `json:"outcome"`
	ErrorCode string `json:"errorCode,omitempty"`
}

// RunningJob is the in-progress backup the status endpoint reports.
type RunningJob struct {
	JobID string `json:"jobId"`
	// Kind is manual for a backup the owner started: it shares turns with
	// people's changes, so the server may pause briefly while it runs.
	Kind       Kind   `json:"kind"`
	Phase      string `json:"phase"`
	BytesDone  int64  `json:"bytesDone"`
	BytesTotal int64  `json:"bytesTotal"`
}

// RestoreResult is the last restore's recorded outcome.
type RestoreResult struct {
	OperationID   string `json:"operationId,omitempty"`
	SourceDigest  string `json:"sourceDigest,omitempty"`
	JobID         string `json:"jobId,omitempty"`
	SchemaVersion int    `json:"schemaVersion,omitempty"`
	At            string `json:"at"`
	Outcome       string `json:"outcome"`
	Reason        string `json:"reason,omitempty"`
	// Recovery is set when the restored database was damaged: what restore
	// repaired, and what it could not read.
	Recovery *persistence.Recovery `json:"recovery,omitempty"`
}

var (
	ErrRestoreLockUnavailable = errors.New("restore needs a filesystem with server locking")
	// ErrInsufficientDisk refuses a backup (or a staged restore) when free
	// space is below the estimate. The API reports insufficient_disk.
	ErrInsufficientDisk = errors.New("backup needs more free disk space than is available")
	// ErrNotFound is an unknown backup id. The API reports not_found.
	ErrNotFound = errors.New("no such backup")
	// ErrInvalid is a malformed id, operation id or source. The API reports
	// invalid_request or restore_source_invalid.
	ErrInvalid = errors.New("invalid backup request")
	// ErrSourceInvalid is a restore source that names nothing usable. The API
	// reports restore_source_invalid.
	ErrSourceInvalid = errors.New("restore source is not a usable backup")
	// ErrIntegrity is a backup whose files fail their manifest hashes. The API
	// reports restore_integrity_failed.
	ErrIntegrity = errors.New("backup files do not match their manifest")
	// ErrRestorePending: a restore stopped part-way and must finish (the next
	// start resumes it) before another can be staged.
	ErrBackupRunning  = errors.New("another backup is running")
	ErrRestorePending = errors.New("a restore is still finishing; restart the server to complete it first")
)

// validID guards Delete and restore-by-id against traversal: a timestamp the
// service minted, with an optional clash suffix or pre-restore marker.
var validID = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{6}Z(-\d+)?(-pre-restore)?$`)

func checkID(id string) error {
	if !validID.MatchString(id) || len(id) > 64 {
		return ErrInvalid
	}
	return nil
}

// validOperationID mirrors the administration idempotency rule: the same
// operation id twice is one backup.
func validOperationID(v string) bool {
	if len(v) < 8 || len(v) > 128 {
		return false
	}
	for _, r := range v {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
			return false
		}
	}
	return true
}

func newToken() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return hex.EncodeToString([]byte(time.Now().UTC().Format(time.RFC3339Nano)))[:32]
	}
	return hex.EncodeToString(raw[:])
}

// Job is one backup run. Progress is safe for concurrent readers.
type Job struct {
	id          string
	kind        Kind
	operationID string
	done        chan struct{}
	mu          sync.Mutex
	phase       string
	bytesDone   int64
	bytesTotal  int64
	err         error
	finishedAt  time.Time
}

// ID identifies the job for polling.
func (j *Job) ID() string { return j.id }

// Done closes when the run finishes, successfully or not.
func (j *Job) Done() <-chan struct{} { return j.done }

// Result reports the run's outcome after Done closes.
func (j *Job) Result() error {
	<-j.done
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.err
}

// Progress snapshots the run for the status endpoint.
func (j *Job) Progress() RunningJob {
	j.mu.Lock()
	defer j.mu.Unlock()
	return RunningJob{JobID: j.id, Kind: j.kind, Phase: j.phase, BytesDone: j.bytesDone, BytesTotal: j.bytesTotal}
}

func (j *Job) setTotal(total int64) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.bytesTotal = total
}

func (j *Job) report(phase string, done, total int64) {
	j.mu.Lock()
	changed := j.phase != phase
	j.phase = phase
	j.bytesDone = done
	if total > j.bytesTotal {
		j.bytesTotal = total
	}
	j.mu.Unlock()
	if changed {
		log.Printf("debug: [server] Backup %s entered %s", j.id, phase)
	}
}

func (j *Job) finish(err error, at time.Time) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.err = err
	j.finishedAt = at
	close(j.done)
}

// Service creates, lists and deletes Plex-model backups in one state folder.
// The zero value is unusable; construct with New.
type Service struct {
	state         string
	policy        func(context.Context) (Snapshot, error)
	settingsStore *PolicyStore
	directory     string
	db            *sql.DB
	sourceGuard   sourceFileGuard
	serverID      string
	serverVersion string
	schemaVersion int
	now           func() time.Time
	// onStaged runs after a restore is staged and answered, to restart the
	// server. It is injected by the composition root; nil skips the restart
	// (tests).
	onStaged           func()
	restoreUnavailable bool
	restoreOperation   string
	restoreSource      Source
	restoreJob         *RestoreJob

	// OnFinished is told how each backup went: its kind, its id when it
	// succeeded, and the error when it did not. It runs on the backup's own
	// goroutine and must not block for long.
	OnFinished func(kind Kind, id string, err error)

	// scrubCopy runs on each finished database copy before it becomes a
	// backup, to end the sessions and remove the live secrets in it. It is
	// injected by the composition root; nil leaves the copy as it was copied
	// (tests of the copy itself).
	scrubCopy      func(ctx context.Context, copy *dbwork.Candidate) error
	beforeSnapshot func(context.Context) error

	// stageMu makes restore staging one at a time: every stage writes the same
	// restore-staged folder, so overlapping requests (a retry, a second tab)
	// wait and then supersede in order instead of deleting each other's copy.
	stageMu sync.Mutex

	// copyMu runs one backup at a time, whichever entry point starts it;
	// epoch names this service's artwork holds (holds.go).
	copyMu sync.Mutex
	epoch  string
	// limits gives each kind's copy limits; tests shorten them.
	limits func(Kind) copyLimits

	mu         sync.Mutex
	jobs       map[string]*Job
	operations map[string]string
	running    *Job
	lastBackup *BackupResult
	// Lifecycle admission and the join channel share mu with Start, so shutdown
	// cannot miss a queued worker between its admission and goroutine start.
	lifetime context.Context
	stop     context.CancelFunc
	stopping bool
	active   int
	idle     chan struct{}
}

// New opens the service on state. db is the live database handle the online
// backup copies, in short steps on its writer connection (copy.go).
func New(state string, db *sql.DB, serverID, serverVersion string, schemaVersion int) *Service {
	lifetime, stop := context.WithCancel(context.Background())
	idle := make(chan struct{})
	close(idle)
	return &Service{
		lifetime: lifetime, stop: stop, idle: idle,
		state:         state,
		db:            db,
		sourceGuard:   newSourceFileGuard(db),
		serverID:      serverID,
		serverVersion: serverVersion,
		schemaVersion: schemaVersion,
		now:           time.Now,
		epoch:         newToken(),
		limits:        copyLimitsFor,
		jobs:          map[string]*Job{},
		operations:    map[string]string{},
	}
}

func (s *Service) backupRoot() string {
	if s.directory != "" {
		return s.directory
	}
	return storagepaths.Path(s.state, BackupsDir)
}

// SetDirectory is startup-only; an empty path keeps the single-folder default.
func (s *Service) SetDirectory(directory string) error {
	if directory == "" {
		return nil
	}
	if err := ValidateDirectory(s.state, directory); err != nil {
		return err
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	s.directory = directory
	return cleanupPartialsDirectory(directory)
}

// SetRestartFunc injects the restart that follows a staged restore.
func (s *Service) SetRestartFunc(fn func()) { s.onStaged = fn }

func (s *Service) SetRestoreAvailable(available bool) { s.restoreUnavailable = !available }

// SetCopyScrubber injects what is done to each finished database copy before
// it becomes a backup. A backup whose copy could not be scrubbed is not kept.
func (s *Service) SetCopyScrubber(fn func(ctx context.Context, copy *dbwork.Candidate) error) {
	s.scrubCopy = fn
}

// SetBeforeSnapshot injects a bounded pending-observation flush at startup.
// Failure aborts this attempt and preserves the last complete backup.
func (s *Service) SetBeforeSnapshot(fn func(context.Context) error) { s.beforeSnapshot = fn }

// Start begins an asynchronous backup. It is idempotent per operationID: the
// same id twice returns the same job. Only one backup runs at a time; a new
// operation of the same kind while one is in progress joins the running job.
// Different kinds must wait: a requested manual copy must never be scheduled
// retention's property. The job's context
// is detached from the request, so the lane budget cannot cancel it.
func (s *Service) Start(ctx context.Context, kind Kind, operationID string) (string, error) {
	if kind != KindScheduled && kind != KindManual {
		return "", ErrInvalid
	}
	if !validOperationID(operationID) {
		return "", ErrInvalid
	}
	s.mu.Lock()
	if s.stopping {
		s.mu.Unlock()
		return "", context.Canceled
	}
	s.pruneJobsLocked(s.now())
	if id, ok := s.operations[operationID]; ok {
		if job, ok := s.jobs[id]; ok {
			select {
			case <-job.done:
				// A finished job replays its id, unless it failed: a failed
				// backup is not a backup, so the operation retries.
				job.mu.Lock()
				failed := job.err != nil
				job.mu.Unlock()
				if !failed {
					s.mu.Unlock()
					return job.id, nil
				}
				s.forgetJobLocked(id)
			default:
				s.mu.Unlock()
				return job.id, nil
			}
		}
	}
	if s.running != nil {
		if kind != s.running.kind {
			s.mu.Unlock()
			return "", ErrBackupRunning
		}
		id := s.running.id
		s.operations[operationID] = id
		s.mu.Unlock()
		return id, nil
	}
	job := &Job{id: newToken(), kind: kind, operationID: operationID, done: make(chan struct{}), phase: "queued"}
	s.jobs[job.id] = job
	s.operations[operationID] = job.id
	s.running = job
	s.admitLocked()
	s.mu.Unlock()
	supervise.Go("backup.job", func() {
		s.runBackup(dbwork.WithClass(context.WithoutCancel(ctx), dbwork.ClassMaintenance), job)
	})
	return job.id, nil
}

func (s *Service) runBackup(ctx context.Context, job *Job) {
	defer s.leave()
	var backupID string
	var err error
	returned := false
	defer func() {
		panicked := recover()
		if !returned {
			err = errors.New("backup: worker stopped before completion")
		}
		s.mu.Lock()
		result := &BackupResult{JobID: job.id, BackupID: backupID, Outcome: "completed"}
		if err != nil {
			result.Outcome = "failed"
			result.ErrorCode = "internal"
			if errors.Is(err, ErrInsufficientDisk) {
				result.ErrorCode = "insufficient_disk"
			} else if errors.Is(err, ErrBackupPressure) {
				// The server was too busy for the backup to finish without
				// holding it up; the last good backup stays.
				result.ErrorCode = "backup_busy"
			}
		}
		s.lastBackup = result
		if s.running == job {
			s.running = nil
		}
		job.finish(err, s.now())
		s.pruneJobsLocked(s.now())
		s.mu.Unlock()
		if panicked != nil {
			panic(panicked) // retain the supervisor's panic reporting
		}
	}()
	backupID, err = s.create(ctx, job, job.kind)
	returned = true
}

// Running reports the in-progress backup, if any.
func (s *Service) Running() (RunningJob, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running == nil {
		return RunningJob{}, false
	}
	return s.running.Progress(), true
}

// Job finds a past or running job by id.
func (s *Service) Job(id string) (*Job, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneJobsLocked(s.now())
	job, ok := s.jobs[id]
	return job, ok
}

// Caller holds s.mu. Never evict a live worker or leave any of its operation
// aliases behind when a terminal receipt leaves the replay window.
func (s *Service) pruneJobsLocked(now time.Time) {
	for id, job := range s.jobs {
		select {
		case <-job.done:
			job.mu.Lock()
			finished := job.finishedAt
			job.mu.Unlock()
			if !finished.IsZero() && !now.Before(finished.Add(backupReplayWindow)) {
				s.forgetJobLocked(id)
			}
		default:
		}
	}
}

func (s *Service) forgetJobLocked(id string) {
	delete(s.jobs, id)
	for operation, jobID := range s.operations {
		if jobID == id {
			delete(s.operations, operation)
		}
	}
}

// Create writes one backup synchronously and returns its id.
func (s *Service) Create(ctx context.Context, kind Kind) (string, error) {
	return s.create(ctx, nil, kind)
}

// create makes one backup and tells OnFinished how it went.
func (s *Service) create(ctx context.Context, job *Job, kind Kind) (string, error) {
	ctx, done, err := s.enter(ctx)
	if err != nil {
		return "", err
	}
	defer done()
	id, err := s.createBackup(ctx, job, kind)
	if s.OnFinished != nil {
		s.OnFinished(kind, id, err)
	}
	return id, err
}

func (s *Service) createBackup(ctx context.Context, job *Job, kind Kind) (string, error) {
	if s.db == nil || s.state == "" {
		return "", ErrInvalid
	}
	s.copyMu.Lock()
	defer s.copyMu.Unlock()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if s.beforeSnapshot != nil {
		if err := s.beforeSnapshot(ctx); err != nil {
			return "", err
		}
	}
	policy, err := s.snapshot(ctx)
	if err != nil {
		return "", err
	}
	defer policy.clear()
	dir := policy.BackupFolder
	if err = ValidateDirectory(s.state, dir); err != nil {
		return "", err
	}
	estimate, err := s.estimate(ctx, policy.IncludeArtwork)
	if err != nil {
		return "", err
	}
	if job != nil {
		job.setTotal(estimate)
		job.report("snapshot", 0, estimate)
	}
	if err = checkFreeSpace(dir, estimate); err != nil {
		return "", err
	}
	// The local snapshot and destination publication coexist until commit.
	if estimate > int64(^uint64(0)>>1)/2 {
		return "", ErrInsufficientDisk
	}
	if err = checkFreeSpace(s.state, estimate*2); err != nil {
		return "", err
	}
	authorityGeneration, quiet := authorityfiles.Process.Baseline()
	if !quiet {
		return "", ErrIntegrity
	}
	authorityBefore, err := authorityFingerprint(ctx, s.state)
	if err != nil {
		return "", err
	}
	root, err := openCanonicalRoot(dir)
	if err != nil {
		return "", err
	}
	defer root.Close()
	stamp := s.now().UTC().Format(timestampLayout)
	id := stamp
	for n := 2; ; n++ {
		_, a := root.Lstat(".partial-" + id)
		_, b := root.Lstat(id)
		if os.IsNotExist(a) && os.IsNotExist(b) {
			roots, e := s.destinations(ctx)
			if e != nil {
				return "", e
			}
			clash := false
			for _, previous := range roots {
				if previous == dir {
					continue
				}
				if _, e = os.Lstat(filepath.Join(previous, id)); e == nil {
					clash = true
					break
				} else if !os.IsNotExist(e) {
					// A disconnected former destination cannot prevent a copy
					// to the current, already-open destination.
					log.Printf("warn: Former backup destination is offline (%s): %v", previous, e)
				}
			}
			if !clash {
				break
			}
		}
		if a != nil && !os.IsNotExist(a) {
			return "", a
		}
		if b != nil && !os.IsNotExist(b) {
			return "", b
		}
		id = stamp + "-" + itoa(n)
	}
	publication := ".partial-" + id
	if err = root.Mkdir(publication, 0700); err != nil {
		return "", err
	}
	defer root.RemoveAll(publication)
	target, err := root.OpenRoot(publication)
	if err != nil {
		return "", err
	}
	defer target.Close()
	partial, err := s.privateScratch("backup-snapshot-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(partial)
	created := s.now().UTC()
	report := func(done, total int64) {
		if job != nil {
			job.report("snapshot", done, max64(total, estimate))
		}
	}
	// No collector may delete a file this backup's database names until the
	// backup is published or abandoned (holds.go).
	limits := s.limits(kind)
	if err = s.holdBlobs(ctx, limits, id, filepath.Base(partial)); err != nil {
		return "", err
	}
	defer s.releaseHold(id)
	if err = copyDatabase(ctx, s.db, filepath.Join(partial, DatabaseName), report, limits); err != nil {
		_ = os.RemoveAll(partial)
		return "", err
	}
	database, err := s.checkAndScrubCopy(ctx, partial)
	if err != nil {
		_ = os.RemoveAll(partial)
		return "", err
	}
	if err = s.advanceHold(ctx, limits, id, "files"); err != nil {
		_ = os.RemoveAll(partial)
		return "", err
	}
	if job != nil {
		job.report("files", estimate, estimate)
	}
	_, err = copyStateFilesForBackup(ctx, s.state, partial, policy.IncludeArtwork)
	if err != nil {
		_ = os.RemoveAll(partial)
		return "", err
	}
	authorityAfter, fingerprintErr := authorityFingerprint(ctx, s.state)
	if err = sameCarriedAuthority(ctx, s.state, partial); err != nil || fingerprintErr != nil || authorityBefore != authorityAfter || !authorityfiles.Process.Unchanged(authorityGeneration) {
		_ = os.RemoveAll(partial)
		return "", ErrIntegrity
	}
	_ = database // finalized and hashed before state is copied; rehashed in the stream
	manifest := Manifest{
		Format: Format, Version: Version,
		ServerID: s.serverID, ServerVersion: s.serverVersion,
		SchemaVersion: s.schemaVersion, CreatedAt: created.Format(time.RFC3339),
		Kind: kind, BackupID: id, BaselineDigest: persistence.BaselineDigest(),
	}
	manifest, err = writeStreamTree(ctx, partial, manifest)
	if err != nil {
		_ = os.RemoveAll(partial)
		return "", err
	}
	if err = syncManifestDirectories(ctx, partial, manifest); err != nil {
		_ = os.RemoveAll(partial)
		return "", err
	}
	if err = s.advanceHold(ctx, limits, id, "publishing"); err != nil {
		_ = os.RemoveAll(partial)
		return "", err
	}
	if policy.PassphraseProtected {
		if policy.material == nil {
			return "", ErrInvalid
		}
		if err = encryptFolder(ctx, partial, target, policy.material, manifest); err != nil {
			return "", err
		}
	} else if err = publishPlain(ctx, partial, target, manifest); err != nil {
		return "", err
	}
	if err = syncRoot(target); err != nil {
		return "", err
	}
	if err = root.Rename(publication, id); err != nil {
		return "", err
	}
	if err = syncRoot(root); err != nil {
		return "", err
	}
	return id, nil
}

// CreateKind writes one backup of the given kind synchronously. The schedule
// uses KindScheduled; the API uses KindManual through Start.
// checkAndScrubCopy checks the finished copy, ends the sessions in it and
// folds its log, all on a private candidate handle: none of it is a live
// write, so none of it takes the write gate. It returns the database's entry.
func (s *Service) checkAndScrubCopy(ctx context.Context, partial string) (FileEntry, error) {
	candidate, err := candidateDatabase(partial)
	if err != nil {
		return FileEntry{}, err
	}
	if err = quickCheck(ctx, candidate); err == nil && s.scrubCopy != nil {
		err = s.scrubCopy(ctx, candidate)
	}
	if err != nil {
		candidate.Close()
		return FileEntry{}, err
	}
	// The scrubber may have written WAL frames. Fold them into the database
	// and close SQLite before flushing/hashing the final standalone payload.
	return finalizeDatabase(ctx, partial, candidate)
}

func (s *Service) CreateKind(ctx context.Context, kind Kind) (string, error) {
	return s.create(ctx, nil, kind)
}

// List returns every complete backup, newest first. Dot-names (partial or
// transient folders) are ignored.
func (s *Service) List() ([]Info, error) {
	out := []Info{}
	seen := map[string]bool{}
	roots, err := s.destinations(context.Background())
	if err != nil {
		return nil, err
	}
	for index, dir := range roots {
		names, err := os.ReadDir(dir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			if index == 0 {
				return nil, err
			}
			log.Printf("warn: Former backup destination is offline (%s): %v", dir, err)
			continue
		}
		for _, name := range names {
			id := name.Name()
			if !name.IsDir() || checkID(id) != nil || seen[id] {
				continue
			}
			folder := filepath.Join(dir, id)
			manifest, err := readManifest(folder)
			encrypted := false
			if err != nil {
				manifest, err = encryptedManifest(folder)
				encrypted = err == nil
			}
			if err != nil {
				continue
			}

			created := manifest.CreatedAt
			if _, err = time.Parse(time.RFC3339, created); err != nil {
				created = ""
			}
			out = append(out, Info{ID: id, CreatedAt: created, Kind: manifest.Kind, SchemaVersion: manifest.SchemaVersion, ServerVersion: manifest.ServerVersion, Bytes: backupBytes(filepath.Join(dir, id), manifest), Path: id, Encrypted: encrypted})
			seen[id] = true
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out, nil
}

// The manifest already describes the held state files. Polling the backup
// list should not visit the same subtitle/logo tree again for each copy.
func backupBytes(folder string, manifest Manifest) int64 {
	var total int64
	listed := map[string]bool{}
	_ = visitManifest(context.Background(), folder, manifest, func(entry FileEntry) error {
		if entry.Bytes >= 0 {
			total += entry.Bytes
		}
		// Only the five fixed metadata names matter below, never retain every path.
		if entry.Name == DatabaseName || entry.Name == DatabaseName+"-wal" {
			listed[entry.Name] = true
		}
		return nil
	})
	for _, name := range []string{DatabaseName, DatabaseName + "-wal", ManifestName, encryptedSummary, encryptedPayload} {
		if listed[name] {
			continue
		}
		if info, err := os.Lstat(filepath.Join(folder, name)); err == nil && info.Mode().IsRegular() {
			total += info.Size()
		}
	}
	return total
}

// Recovery copies stay beside live state for atomic restore, even when the
// owner keeps regular backups on another disk. Their ids have a separate suffix.
func (s *Service) backupPath(id string) (string, error) {
	if err := checkID(id); err != nil {
		return "", err
	}
	if strings.HasSuffix(id, "-pre-restore") {
		local := storagepaths.Path(s.state, BackupsDir, id)
		info, err := os.Lstat(local)
		if err == nil {
			if !info.IsDir() {
				return "", ErrNotFound
			}
			return local, nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
	}
	roots, err := s.destinations(context.Background())
	if err != nil {
		return "", err
	}
	for _, dir := range roots {
		path := filepath.Join(dir, id)
		if info, e := os.Lstat(path); e == nil && info.IsDir() {
			return path, nil
		}
	}
	return filepath.Join(roots[0], id), nil
}

func (s *Service) pruneRecoveryCopies() error {
	listed, err := s.List()
	if err != nil {
		return err
	}
	kept := 0
	for _, info := range listed {
		if info.Kind != KindPreRestore {
			continue
		}
		kept++
		if kept > 3 {
			if err = s.Delete(info.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

// Delete removes one backup.
func (s *Service) Delete(id string) error {
	if err := checkID(id); err != nil {
		return err
	}
	path, err := s.backupPath(id)
	if err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ErrNotFound
		}
		return err
	}
	if !info.IsDir() {
		return ErrNotFound
	}
	root, err := openCanonicalRoot(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer root.Close()
	if err = root.RemoveAll(id); err != nil {
		return err
	}
	return syncRoot(root)
}

// Prune keeps the newest keep scheduled backups, and the newest
// three pre-restore folders.
func (s *Service) Prune(keep int) error {
	if keep < 1 {
		keep = 1
	}
	listed, err := s.List()
	if err != nil {
		return err
	}
	kept, keptPre := 0, 0
	for _, info := range listed {
		if info.Kind == KindPreRestore {
			keptPre++
			if keptPre > 3 {
				if err = s.Delete(info.ID); err != nil {
					return err
				}
			}
			continue
		}
		if info.Kind != KindScheduled {
			continue
		}
		kept++
		if kept > keep {
			if err = s.Delete(info.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

// LastRestore reads the recorded outcome of the last restore, if any.
func (s *Service) LastRestore() (*RestoreResult, error) { return ReadRestoreResult(s.state) }

// ReadRestoreResult is LastRestore for a caller that has only the state
// folder: start-up reads it before the backup service exists.
func ReadRestoreResult(state string) (*RestoreResult, error) {
	raw, err := os.ReadFile(storagepaths.Path(state, RestoreResultFile))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out RestoreResult
	if err = json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	if out.Outcome == "" {
		return nil, ErrInvalid
	}
	return &out, nil
}

// CleanupPartials deletes stale .partial-* folders. It runs at startup,
// before any staged restore is applied.
func CleanupPartials(state string) error {
	return cleanupPartialsDirectory(storagepaths.Path(state, BackupsDir))
}

func cleanupPartialsDirectory(dir string) error {
	root, err := openCanonicalRoot(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer root.Close()
	f, err := root.Open(".")
	if err != nil {
		return err
	}
	defer f.Close()
	for {
		entries, err := f.ReadDir(128)
		for _, entry := range entries {
			id := entry.Name()
			if !strings.HasPrefix(id, ".partial-") || checkID(strings.TrimPrefix(id, ".partial-")) != nil {
				continue
			}
			if e := root.RemoveAll(id); e != nil {
				return e
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [8]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// Directory is the destination currently used for backups.
func (s *Service) Directory() string {
	p, e := s.snapshot(context.Background())
	if e != nil {
		return ""
	}
	defer p.clear()
	return p.BackupFolder
}

func (s *Service) LastBackup() *BackupResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lastBackup == nil {
		return nil
	}
	out := *s.lastBackup
	return &out
}

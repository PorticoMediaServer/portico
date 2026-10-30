package mounts

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/authorityfiles"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/rcloneprogram"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
	"github.com/PorticoMediaServer/portico/server/internal/worker"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Mount struct {
	BackendRevision   int64    `json:"backendRevision"`
	ExecutableVersion string   `json:"executableVersion"`
	CacheBytes        int64    `json:"cacheBytes"`
	CacheFloor        int64    `json:"cacheFloor"`
	ControlRevision   int64    `json:"controlRevision"`
	RemovalPending    bool     `json:"removalPending"`
	Actions           []string `json:"actions"`
	Executable        string   `json:"executable"`
	ID                string   `json:"id"`
	Name              string   `json:"name"`
	MountPath         string   `json:"mountPath"`
	DesiredState      string   `json:"desiredState"`
	ObservedState     string   `json:"observedState"`
	Error             string   `json:"error,omitempty"`
	// ExecutableDigest is legacy native/mount custody only. Programs approved
	// under ruling B-42 record none (version is the only check); old rows may
	// still carry one, and the serve path never requires it.
	ExecutableDigest string `json:"executableDigest,omitempty"`
	// ProgramIncluded is the committed program selection: true when the owner
	// chose the rclone included with Portico Server. It is never derived from a
	// path comparison; historical rows default false (unknown/custom).
	ProgramIncluded bool `json:"programIncluded"`
}
type CreateInput struct {
	Name       string `json:"name"`
	Executable string `json:"executable"`
	Remote     string `json:"remote"`
	// UseIncluded is internal: the server resolves the actual bundled program
	// and ignores any caller-supplied path. It is not wire data.
	UseIncluded bool   `json:"-"`
	Config      string `json:"config"`
}
type child struct {
	runtimeID         string
	mountIdentity     string
	lifetime          context.Context
	invalidate        context.CancelFunc
	restartGeneration int64
	control           io.WriteCloser
	done              chan error
	started           time.Time
	stopping          bool
	failure           string
	stopAt            time.Time
}
type Service struct {
	// NativeBusy is installed by the remote source registry for playback removal safety.
	NativeBusy             func(string) bool
	GuardSourceRemoval     func(context.Context, string) (func(), error)
	NativeInvalidated      func(string)
	PrepareSourceCandidate func(context.Context, SourceCandidateInput, func(*sql.Tx) error) (*SourceCandidatePublication, error)
	PrepareSourceEgress    func(context.Context, ServeSource) (*SourceEgress, error)
	commands               sync.Mutex
	db                     *sql.DB
	root, private, helper  string
	work, cache            string
	// fpKey signs command fingerprints. Hashing that is not encryption stays:
	// the key lives in the configuration table, and replays survive restarts.
	// Folder permissions are the protection, as in Plex.
	fpKey                           [32]byte
	storage                         *storage.Client
	mu                              sync.Mutex
	active                          map[string]*child
	available                       map[string]bool
	paths                           map[string]string
	retry                           map[string]time.Time
	snapshot                        atomic.Value
	validation                      chan struct{}
	validationSupervisor            *storage.Supervisor
	nativeInventory, nativePlayback *storage.Supervisor
	observe                         func(context.Context, string) (bool, error)
	mountSupported                  func() bool
	observeIdentity                 func(context.Context, string) (string, error)
}

// canonicalDirectory reports whether path is reached without any link.
func canonicalDirectory(path string) error {
	canonical, e := filepath.EvalSymlinks(path)
	if e != nil || canonical != path {
		return errors.New("managed storage directories must have canonical paths")
	}
	return nil
}

// InspectPrivateDirectory reports whether New would accept the mount
// configuration folder already in a state folder, without creating anything.
// A folder that is not there yet is fine: New makes it.
func InspectPrivateDirectory(state string) error {
	path := storagepaths.Path(state, "managed-storage")
	info, e := os.Lstat(path)
	if errors.Is(e, os.ErrNotExist) {
		return nil
	}
	if e != nil || !info.IsDir() {
		return errors.New("managed storage directories must be directories")
	}
	return canonicalDirectory(path)
}

func New(db *sql.DB, state, root, helper string, store *storage.Client) (*Service, error) {
	if root == "" {
		root = storagepaths.Path(state, "mounts")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if root == filepath.VolumeName(root)+string(filepath.Separator) {
		return nil, errors.New("managed mount root must be a dedicated directory")
	}
	for _, path := range []string{root, storagepaths.Path(state, "managed-storage"), filepath.Join(state, "Temp", "managed-storage"), filepath.Join(state, "Cache", "managed-storage")} {
		if err = os.MkdirAll(path, 0700); err != nil {
			return nil, err
		}
		if e := canonicalDirectory(path); e != nil {
			return nil, e
		}
		if path == root {
			marker := filepath.Join(root, ".portico-managed-root")
			if _, e := os.Lstat(marker); errors.Is(e, os.ErrNotExist) {
				entries, e := os.ReadDir(root)
				if e != nil || len(entries) != 0 {
					return nil, errors.New("managed mount root must be empty or already owned by Portico")
				}
				if e = os.WriteFile(marker, []byte("Portico managed mount allocation\n"), 0600); e != nil {
					return nil, e
				}
			} else if e != nil {
				return nil, e
			}
		}
	}
	private := storagepaths.Path(state, "managed-storage")
	// Mount configs are rclone's plain config format. A state from the
	// sealed era converts its sealed configs while the old key exists.
	migrateSealedConfigs(private)
	// Migration 0028 owns every mount table and trigger. Startup only resets
	// process-local observations left by a prior run.
	_, err = dbwork.ExecWrite(context.Background(), db, dbwork.ClassFrom(context.Background(), dbwork.ClassInteractive), `UPDATE managed_mounts SET observed='stopped',error='' WHERE observed<>'stopped' OR error<>''`)
	if err != nil {
		return nil, err
	}
	var fpKey [32]byte
	if err = loadOrCreateFingerprintKey(db, &fpKey); err != nil {
		return nil, err
	}
	s := &Service{db: db, root: root, private: private, work: filepath.Join(state, "Temp", "managed-storage"), cache: filepath.Join(state, "Cache", "managed-storage"), helper: helper, fpKey: fpKey, storage: store, active: map[string]*child{}, available: map[string]bool{}, paths: map[string]string{}, retry: map[string]time.Time{}, validation: make(chan struct{}, 2), validationSupervisor: &storage.Supervisor{Limit: 2}, nativeInventory: &storage.Supervisor{Limit: 4}, nativePlayback: &storage.Supervisor{Limit: 8}}
	s.observe = store.Mounted
	s.mountSupported = MountSupported
	s.observeIdentity = store.MountedIdentity
	rows, err := db.Query(`SELECT id,mount_path FROM managed_mounts`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, path string
		if err = rows.Scan(&id, &path); err != nil {
			return nil, err
		}
		s.paths[id] = path
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	recovery, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	s.recoverAllocations(recovery)
	cancel()
	s.recoverNativeFiles()
	s.publish()
	return s, nil
}

// fingerprintKeyName keeps the command-fingerprint HMAC key in the
// configuration table so replays survive restarts.
const fingerprintKeyName = "mounts_fingerprint_key"

// loadOrCreateFingerprintKey reads the stable fingerprint key, generating and
// storing it on first use.
func loadOrCreateFingerprintKey(db *sql.DB, out *[32]byte) error {
	var raw string
	if err := db.QueryRow(`SELECT value FROM configuration WHERE key=?`, fingerprintKeyName).Scan(&raw); err == nil {
		decoded, derr := hex.DecodeString(raw)
		if derr == nil && len(decoded) == 32 {
			copy(out[:], decoded)
			return nil
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if _, err := rand.Read(out[:]); err != nil {
		return err
	}
	// First use stores the key as an ordinary interactive configuration write.
	_, err := dbwork.ExecWrite(context.Background(), db, dbwork.ClassInteractive, `INSERT INTO configuration(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, fingerprintKeyName, hex.EncodeToString(out[:]))
	return err
}
func (s *Service) List() ([]Mount, error) {
	rows, err := s.db.Query(`SELECT m.id,m.name,m.mount_path,m.desired,m.observed,m.error,m.digest,m.executable,m.program_included,c.revision,c.removal_pending,b.generation,b.version,b.cache_bytes,b.cache_floor FROM managed_mounts m JOIN mount_controls c ON c.mount_id=m.id JOIN mount_backend_configs b ON b.mount_id=m.id ORDER BY m.name,m.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Mount{}
	for rows.Next() {
		var m Mount
		if err = rows.Scan(&m.ID, &m.Name, &m.MountPath, &m.DesiredState, &m.ObservedState, &m.Error, &m.ExecutableDigest, &m.Executable, &m.ProgramIncluded, &m.ControlRevision, &m.RemovalPending, &m.BackendRevision, &m.ExecutableVersion, &m.CacheBytes, &m.CacheFloor); err != nil {
			return nil, err
		}
		projectMountActions(&m)
		result = append(result, m)
	}
	return result, rows.Err()
}
func (s *Service) get(id string) (Mount, error) {
	rows, err := s.List()
	if err != nil {
		return Mount{}, err
	}
	for _, m := range rows {
		if m.ID == id {
			return m, nil
		}
	}
	return Mount{}, sql.ErrNoRows
}
func (s *Service) Create(ctx context.Context, input CreateInput) (Mount, error) {
	return s.create(ctx, input, nil, nil)
}
func (s *Service) create(ctx context.Context, input CreateInput, record func(*sql.Tx, Mount) error, authorize func(*sql.Tx) error) (Mount, error) {
	defer authorityfiles.Process.Admit()()
	select {
	case s.validation <- struct{}{}:
		defer func() { <-s.validation }()
	default:
		return Mount{}, errors.New("mount configuration validation capacity reached")
	}
	// Native object access does not require FUSE. Mount enablement checks host support separately.
	if strings.TrimSpace(input.Name) == "" || len(input.Name) > 100 {
		return Mount{}, ErrInvalid
	}
	if err := validateConfig(input.Config, input.Remote); err != nil {
		return Mount{}, ErrConfigInvalid
	}
	if input.UseIncluded {
		program, e := rcloneprogram.Resolve()
		if e != nil || input.Executable != "" && input.Executable != program.Path {
			return Mount{}, ErrExecutableInvalid
		}
		input.Executable = program.Path
	}
	executable, digest, version, err := s.approveExecutable(ctx, input.Executable)
	if err != nil {
		return Mount{}, err
	}
	input.Executable = executable
	publication, err := s.sourceCandidate(ctx, SourceCandidateInput{Configuration: []byte(input.Config), Remote: input.Remote}, authorize)
	if err != nil {
		return Mount{}, err
	}
	candidate, err := s.candidateFile([]byte(input.Config))
	if err != nil {
		return Mount{}, err
	}
	defer os.Remove(candidate)
	if err = publication.Test(ctx, rcloneprogram.Program{Path: executable}, digest); err != nil {
		return Mount{}, err
	}
	if publication != nil {
		if err = publication.CheckAfterTest(ctx); err != nil {
			return Mount{}, err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	defer s.publish()
	if len(s.paths) >= 8 {
		return Mount{}, errors.New("managed mount configuration limit reached")
	}
	unlock, err := s.allocationLock()
	if err != nil {
		return Mount{}, ErrAllocationCapacity
	}
	defer unlock()
	id := identity.Token()
	path := filepath.Join(s.root, id)
	raw := []byte(input.Config)
	if err = s.reserveAllocation(id, raw); err != nil {
		return Mount{}, err
	}
	if err = os.Mkdir(path, 0700); err != nil {
		return Mount{}, err
	}
	directoryID, err := storage.DirectoryIdentity(path)
	if err != nil {
		return Mount{}, err
	}
	if err = syncDirectory(s.root); err != nil {
		return Mount{}, err
	}
	if _, err = dbwork.ExecWrite(ctx, s.db, dbwork.ClassFrom(ctx, dbwork.ClassInteractive), `UPDATE mount_allocations SET directory_identity=? WHERE id=?`, directoryID, id); err != nil {
		return Mount{}, err
	}
	configPath := filepath.Join(s.private, id+".conf")
	file, err := os.OpenFile(configPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return Mount{}, err
	}
	_, err = file.Write(raw)
	if err == nil {
		err = file.Sync()
	}
	file.Close()
	if err != nil {
		return Mount{}, err
	}
	if err = syncDirectory(s.private); err != nil {
		return Mount{}, err
	}
	m := Mount{BackendRevision: 1, ExecutableVersion: version, CacheFloor: 2 << 30, ID: id, Name: input.Name, MountPath: path, DesiredState: "stopped", ObservedState: "stopped", ExecutableDigest: digest, Executable: input.Executable, ProgramIncluded: input.UseIncluded, ControlRevision: 1}
	projectMountActions(&m)
	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassFrom(ctx, dbwork.ClassInteractive))
	tx := gated.Tx()
	if err == nil {
		_, err = tx.Exec(`INSERT INTO managed_mounts(id,name,executable,digest,program_included,remote,mount_path) VALUES(?,?,?,?,?,?,?)`, id, input.Name, input.Executable, digest, input.UseIncluded, input.Remote, path)
		if err == nil {
			_, err = tx.Exec(`UPDATE mount_backend_configs SET version=? WHERE mount_id=?`, version, id)
		}
		if err == nil {
			_, err = tx.Exec(`DELETE FROM mount_allocations WHERE id=?`, id)
		}
		if err == nil && record != nil {
			err = record(tx, m)
		}
		if err == nil && publication != nil {
			err = publication.CommitTx(ctx, tx, m)
		}
		if err == nil {
			err = gated.Commit()
		} else {
			gated.Rollback()
		}
	}
	if err != nil {
		return Mount{}, err
	}
	s.paths[id] = path
	return m, nil
}
func (s *Service) Action(id, action string) (Mount, error) {
	if action != "start" && action != "stop" && action != "restart" {
		return Mount{}, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	defer s.publish()
	desired := "running"
	if action == "stop" {
		desired = "stopped"
	}
	result, err := dbwork.ExecWrite(context.Background(), s.db, dbwork.ClassFrom(context.Background(), dbwork.ClassInteractive), `UPDATE managed_mounts SET desired=?,error='' WHERE id=?`, desired, id)
	if err != nil {
		return Mount{}, err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return Mount{}, sql.ErrNoRows
	}
	if action != "start" {
		s.available[id] = false
		if c := s.active[id]; c != nil && !c.stopping {
			c.invalidateRuntime()
			c.stopping = true
			c.stopAt = time.Now()
			_ = c.control.Close()
		}
	}
	delete(s.retry, id)
	return s.get(id)
}
func (s *Service) Delete(ctx context.Context, id string) error {
	defer authorityfiles.Process.Admit()()
	s.mu.Lock()
	defer s.mu.Unlock()
	defer s.publish()
	m, err := s.get(id)
	if err != nil {
		return err
	}
	if m.DesiredState != "stopped" || s.active[id] != nil {
		return errors.New("stop the managed mount before removing it")
	}
	mounted, err := s.observe(ctx, m.MountPath)
	if err != nil || mounted {
		return errors.New("mount root must be confirmed unmounted before removal")
	}
	// Remove only the empty allocated root, never recursive mounted data.
	if err = os.Remove(m.MountPath); err != nil {
		return err
	}
	if _, err = dbwork.ExecWrite(ctx, s.db, dbwork.ClassFrom(ctx, dbwork.ClassInteractive), `DELETE FROM managed_mounts WHERE id=?`, id); err != nil {
		return err
	}
	_ = os.Remove(filepath.Join(s.private, id+".conf"))
	delete(s.paths, id)
	delete(s.available, id)
	return nil
}

type availability struct {
	path  string
	ready bool
}

func (s *Service) publish() {
	rows := []availability{}
	for id, path := range s.paths {
		rows = append(rows, availability{path, s.available[id]})
	}
	s.snapshot.Store(rows)
}
func (s *Service) Guard(path string) error {
	rows, _ := s.snapshot.Load().([]availability)
	for _, row := range rows {
		rel, err := filepath.Rel(row.path, path)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !row.ready {
			return ErrUnavailable
		}
	}
	return nil
}
func (s *Service) state(id, status, message string) {
	_, _ = dbwork.ExecWrite(context.Background(), s.db, dbwork.ClassFrom(context.Background(), dbwork.ClassInteractive), `UPDATE managed_mounts SET observed=?,error=? WHERE id=?`, status, message, id)
}
func (s *Service) Run(ctx context.Context) {
	wake := worker.NewSignal()
	unregister := dbwork.WakeOnTables(wake, "managed_mounts", "mount_*", "remote_*")
	defer unregister()
	defer s.shutdown()
	worker.Run(ctx, "mounts.reconcile", wake, func(ctx context.Context) time.Duration {
		if s.reconcile(ctx) {
			return time.Second
		}
		return 0
	})
}

func (s *Service) reconcile(ctx context.Context) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer s.publish()
	s.recoverAllocations(ctx)
	mounts, err := s.List()
	if err != nil {
		return true
	}
	pending := false
	for _, m := range mounts {
		pending = pending || m.DesiredState == "running" || m.RemovalPending
		if m.RemovalPending {
			s.removePending(ctx, m)
			continue
		}
		c := s.active[m.ID]
		if c != nil {
			var wanted int64
			if err := s.db.QueryRow(`SELECT restart_generation FROM mount_controls WHERE mount_id=?`, m.ID).Scan(&wanted); err == nil && wanted != c.restartGeneration && !c.stopping {
				c.invalidateRuntime()
				c.stopping = true
				c.stopAt = time.Now()
				_ = c.control.Close()
				s.available[m.ID] = false
			}
			select {
			case <-c.done:
				c.invalidateRuntime()
				delete(s.active, m.ID)
				s.available[m.ID] = false
				s.state(m.ID, "stopped", "")
				if c.failure != "" {
					s.state(m.ID, "failed", c.failure)
				}
				if !c.stopping {
					s.state(m.ID, "failed", "Mount process exited. Verify rclone, FUSE and remote credentials.")
					s.retry[m.ID] = time.Now().Add(30 * time.Second)
				}
				continue
			default:
			}
			if c.stopping {
				s.available[m.ID] = false
				s.state(m.ID, "stopping", c.failure)
				if time.Since(c.stopAt) > 10*time.Second {
					s.state(m.ID, "quarantined", "Owned process has not exited; its slot remains reserved.")
				}
				continue
			}
			if m.CacheBytes > 0 && !cacheSpaceAvailable(s.cache, m.CacheFloor) {
				c.invalidateRuntime()
				c.stopping = true
				c.stopAt = time.Now()
				_ = c.control.Close()
				s.available[m.ID] = false
				s.state(m.ID, "unavailable", "Private cache free-space floor reached.")
				continue
			}
			if m.DesiredState == "stopped" {
				c.invalidateRuntime()
				c.stopping = true
				c.stopAt = time.Now()
				_ = c.control.Close()
				s.available[m.ID] = false
				continue
			}
			check, cancel := context.WithTimeout(ctx, 2*time.Second)
			mounted, e := s.observe(check, m.MountPath)
			physical := ""
			if e == nil && mounted {
				if s.observeIdentity == nil {
					e = ErrUnavailable
				} else {
					physical, e = s.observeIdentity(check, m.MountPath)
				}
			}
			cancel()
			if e == nil && mounted && c.bindMountIdentity(physical) {
				s.available[m.ID] = true
				s.state(m.ID, "mounted", "")
			} else {
				if s.available[m.ID] {
					c.invalidateRuntime()
					c.stopping = true
					c.stopAt = time.Now()
					_ = c.control.Close()
				}
				s.available[m.ID] = false
				if time.Since(c.started) > 30*time.Second {
					c.invalidateRuntime()
					c.stopping = true
					c.stopAt = time.Now()
					_ = c.control.Close()
					c.failure = "Mount did not become available within the startup deadline. Verify FUSE support and remote credentials."
					s.state(m.ID, "failed", c.failure)
					s.retry[m.ID] = time.Now().Add(30 * time.Second)
				}
			}
			continue
		}
		if m.DesiredState != "running" || len(s.active) >= 4 || time.Now().Before(s.retry[m.ID]) {
			continue
		}
		check, cancel := context.WithTimeout(ctx, 2*time.Second)
		mounted, e := s.observe(check, m.MountPath)
		cancel()
		if e != nil || mounted {
			s.state(m.ID, "unavailable", "Mount root is unavailable or already mounted. External mounts are never adopted or unmounted.")
			s.retry[m.ID] = time.Now().Add(30 * time.Second)
			continue
		}
		var executable, remote string
		var backendGeneration int64
		if e = s.db.QueryRow(`SELECT m.executable,m.remote,b.generation FROM managed_mounts m JOIN mount_backend_configs b ON b.mount_id=m.id WHERE m.id=?`, m.ID).Scan(&executable, &remote, &backendGeneration); e != nil {
			continue
		}
		// Ruling B-42: no fingerprint custody here. A custom program is checked
		// by version at its actual start (guardian); old rows are included too.
		info, e := os.Lstat(m.MountPath)
		if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			s.state(m.ID, "failed", "Managed mount directory changed.")
			continue
		}
		if m.CacheBytes > 0 && !cacheSpaceAvailable(s.cache, m.CacheFloor) {
			s.state(m.ID, "unavailable", "Private cache free-space floor reached.")
			s.retry[m.ID] = time.Now().Add(30 * time.Second)
			continue
		}
		var generation int64
		if e = s.db.QueryRow(`SELECT restart_generation FROM mount_controls WHERE mount_id=?`, m.ID).Scan(&generation); e != nil {
			continue
		}
		c = newRuntimeChild(generation)
		config, egress, e := s.prepareBackendEgress(c.lifetime, m.ID, strconv.FormatInt(backendGeneration, 10))
		if e != nil {
			c.invalidateRuntime()
			s.state(m.ID, "unavailable", "Source network policy could not be admitted.")
			s.retry[m.ID] = time.Now().Add(30 * time.Second)
			continue
		}
		request := launch{Executable: executable, Remote: remote, MountPath: m.MountPath, ConfigPath: config, PrivateHome: s.work, Password: "", Included: m.ProgramIncluded, CachePath: filepath.Join(s.cache, m.ID+".cache"), CacheBytes: m.CacheBytes, CacheFloor: m.CacheFloor, HTTPProxy: egress.HTTPProxy, SFTPProxy: egress.SFTPProxy}
		cmd := exec.Command(s.helper, "--portico-rclone-guardian")
		cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
		cmd.Stdout = io.Discard
		cmd.Stderr = io.Discard
		control, e := cmd.StdinPipe()
		if e != nil {
			c.invalidateRuntime()
			egress.Close()
			continue
		}
		if e = cmd.Start(); e != nil {
			control.Close()
			c.invalidateRuntime()
			egress.Close()
			s.state(m.ID, "failed", "Could not start mount supervisor.")
			continue
		}
		c.control, c.done, c.started = control, make(chan error, 1), time.Now()
		s.active[m.ID] = c
		owned := c
		supervise.Go("mounts.child.wait", func() {
			// The handoff is deferred so a contained panic still retires the child
			// rather than leaving whoever waits on it blocked forever.
			var err error
			defer func() { owned.invalidateRuntime(); egress.Close(); owned.done <- err; control.Close() }()
			err = cmd.Wait()
		})
		if e = json.NewEncoder(control).Encode(request); e != nil {
			_ = control.Close()
			c.invalidateRuntime()
			c.stopping = true
			c.stopAt = time.Now()
		}
		s.state(m.ID, "starting", "")
	}
	return pending
}
func (s *Service) shutdown() {
	s.mu.Lock()
	defer s.mu.Unlock()
	defer s.publish()
	for id, c := range s.active {
		c.invalidateRuntime()
		_ = c.control.Close()
		s.available[id] = false
		s.state(id, "stopping", "")
	}
	// Guardians retain child ownership after server exit. Desired state is kept
	// for reboot recovery; fresh startup never kills or adopts recorded PIDs.
}

func (s *Service) RootFor(path string) string {
	rows, _ := s.snapshot.Load().([]availability)
	for _, row := range rows {
		rel, err := filepath.Rel(row.path, path)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return row.path
		}
	}
	return ""
}

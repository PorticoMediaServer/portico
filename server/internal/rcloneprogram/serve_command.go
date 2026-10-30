package rcloneprogram

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/childprocess"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/remotemedia"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
)

var ErrServeConfiguration = errors.New("remote serve configuration is invalid")

// ErrServeProgram reports a custom program that is no longer a usable rclone at
// start. Ruling B-42 has no approval fingerprint: a changed file is re-checked
// by running it with version, and only a failure reaches this state.
var ErrServeProgram = errors.New("approved remote source program changed or is unavailable")

// ServeCommandSpec is private, already validated source configuration. Enforce
// must install the source's enforcing outbound boundary, not just approve a URL
// or set generic proxy environment variables. SFTP needs its native policy
// tunnel; SMB leaf hosts must already be pinned in the transient Configuration.
// There is deliberately no default enforcement or inherited environment.
type ServeCommandSpec struct {
	ApprovedProgramDigest string
	SourceID              string
	SourceGeneration      string
	PrivateDirectory      string
	Configuration         []byte
	Remote                string
	Port                  int
	MetadataPort          int
	Username              string
	Password              string
	Connections           int
	RequestsPerSecond     int
	BytesPerSecond        int64
	// SharedOperations keeps a provisional connection test within the current
	// source's one operation budget. Its capacity must match Connections and
	// its lifetime also owns this process. This conveys no operation authority;
	// the independent process lease and Enforce checks remain mandatory.
	SharedOperations *remotemedia.OperationBudget
	// SequentialReads disables VFS chunk reopens for known start-only sources.
	// This is transport behavior, never operation permission or fake ReadAt.
	SequentialReads bool
	CacheDirectory  string
	CacheBytes      int64
	CacheFloor      int64
	// Transport belongs to this source's own rclone program. Zero keeps the
	// bundled program's defaults, which are also what the owner sees.
	DirCacheSeconds       int
	ReadBufferBytes       int64
	ConnectTimeoutSeconds int
	IdleTimeoutSeconds    int
	Retries               int
	LowLevelRetries       int
	Enforce               func(*exec.Cmd) error
}

// PreparedServe is not a running process. Its owning supervisor retains process
// admission until Wait, uses childprocess.Kill on shutdown, and calls Loaded only
// after an authenticated readiness request proves configuration has been read.
// Loaded must succeed before publishing the endpoint. Caches are never placed
// in the transient configuration directory.
type PreparedServe struct {
	Command *exec.Cmd
	mu      sync.Mutex
	private string
	reports *serveReports
	// programVersion is the custom program's observed version at this start; it
	// is empty for the bundled program, which is never checked.
	programVersion string
}

var serveRemote = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*:[^\x00\r\n\\]*$`)

func PrepareServe(ctx context.Context, program Program, spec ServeCommandSpec) (*PreparedServe, error) {
	if spec.SourceID == "" || len(spec.SourceID) > 128 || strings.ContainsAny(spec.SourceID, ":/\\\x00\r\n") {
		return nil, ErrServeConfiguration
	}
	if generation, err := strconv.ParseInt(spec.SourceGeneration, 10, 64); err != nil || generation < 1 || strconv.FormatInt(generation, 10) != spec.SourceGeneration {
		return nil, ErrServeConfiguration
	}
	if spec.DirCacheSeconds < 0 || spec.DirCacheSeconds > 86400 || spec.ReadBufferBytes < 0 || spec.ReadBufferBytes > 1<<30 ||
		spec.ConnectTimeoutSeconds < 0 || spec.ConnectTimeoutSeconds > 600 || spec.IdleTimeoutSeconds < 0 || spec.IdleTimeoutSeconds > 3600 ||
		spec.Retries < 0 || spec.Retries > 10 || spec.LowLevelRetries < 0 || spec.LowLevelRetries > 10 {
		return nil, ErrServeConfiguration
	}
	if spec.Enforce == nil || !filepath.IsAbs(spec.PrivateDirectory) || len(spec.Configuration) == 0 || len(spec.Configuration) > 64<<10 || !serveRemote.MatchString(spec.Remote) || spec.Port < 1 || spec.Port > 65535 || spec.Username == "" || spec.Password == "" || strings.ContainsAny(spec.Username, ":\r\n\x00") || strings.ContainsAny(spec.Password, "\r\n\x00") || spec.Connections < 1 || spec.Connections > 256 || spec.RequestsPerSecond < 1 || spec.RequestsPerSecond > 1000000 || spec.BytesPerSecond < 0 {
		return nil, ErrServeConfiguration
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if shared := spec.SharedOperations; shared != nil && (shared.Counts().Capacity != spec.Connections || shared.Context() == nil || shared.Context().Err() != nil) {
		return nil, ErrServeConfiguration
	}
	if spec.MetadataPort < 0 || spec.MetadataPort > 65535 || spec.MetadataPort != 0 && spec.MetadataPort == spec.Port {
		return nil, ErrServeConfiguration
	}
	if spec.CacheBytes < 0 || spec.CacheFloor < 0 || spec.CacheBytes > 0 && (!filepath.IsAbs(spec.CacheDirectory) || spec.CacheFloor == 0) {
		return nil, ErrServeConfiguration
	}
	canonical, err := filepath.EvalSymlinks(spec.PrivateDirectory)
	if err != nil || canonical != spec.PrivateDirectory {
		return nil, ErrServeConfiguration
	}
	private, err := os.MkdirTemp(spec.PrivateDirectory, "serve-config-")
	if err != nil {
		return nil, err
	}
	p := &PreparedServe{private: private, reports: &serveReports{}}
	good := false
	defer func() {
		if !good {
			_ = p.Loaded()
		}
	}()
	file := filepath.Join(private, "rclone.conf")
	if err = os.WriteFile(file, spec.Configuration, 0600); err != nil {
		return nil, err
	}
	// The source's own transport values replace the program defaults; zero in a
	// field means "use the bundled default" and is reported the same way.
	connectTimeout, idleTimeout := spec.ConnectTimeoutSeconds, spec.IdleTimeoutSeconds
	if connectTimeout == 0 {
		connectTimeout = 10
	}
	if idleTimeout == 0 {
		idleTimeout = 30
	}
	retries, lowLevelRetries := spec.Retries, spec.LowLevelRetries
	if retries == 0 {
		retries = 1
	}
	if lowLevelRetries == 0 {
		lowLevelRetries = 1
	}
	args := []string{"serve", "webdav", spec.Remote, "--read-only", "--addr", net.JoinHostPort("127.0.0.1", strconv.Itoa(spec.Port)), "--config", file, "--ask-password=false",
		"--dir-cache-time=" + strconv.Itoa(spec.DirCacheSeconds) + "s", "--poll-interval=0", "--vfs-read-ahead=0",
		"--buffer-size=" + strconv.FormatInt(spec.ReadBufferBytes, 10), "--vfs-read-chunk-size=1M", "--vfs-read-chunk-size-limit=1M", "--vfs-read-chunk-streams=0",
		"--retries=" + strconv.Itoa(retries), "--low-level-retries=" + strconv.Itoa(lowLevelRetries),
		"--contimeout=" + strconv.Itoa(connectTimeout) + "s", "--timeout=" + strconv.Itoa(idleTimeout) + "s",
		"--use-json-log", "--log-level=DEBUG", "--max-connections", strconv.Itoa(spec.Connections), "--tpslimit", strconv.Itoa(spec.RequestsPerSecond), "--tpslimit-burst=1", "--sftp-connections", strconv.Itoa(spec.Connections)}
	if spec.SequentialReads {
		for i, arg := range args {
			if arg == "--vfs-read-chunk-size=1M" {
				args[i] = "--vfs-read-chunk-size=0"
			}
		}
	}
	if spec.CacheBytes > 0 && !spec.SequentialReads {
		// The source owner supplies its incarnation-scoped cache directory and
		// invalidates it on changed observations; a cache is never byte identity.
		args = append(args, "--vfs-cache-mode=full", "--cache-dir", spec.CacheDirectory, "--vfs-cache-max-size", strconv.FormatInt(spec.CacheBytes, 10)+"B", "--vfs-cache-min-free-space", strconv.FormatInt(spec.CacheFloor, 10)+"B")
	} else {
		args = append(args, "--vfs-cache-mode=off")
	}
	if spec.BytesPerSecond > 0 {
		args = append(args, "--bwlimit", strconv.FormatInt(spec.BytesPerSecond, 10)+"B")
	}
	if spec.MetadataPort != 0 {
		args = append(args, "--rc", "--rc-addr", net.JoinHostPort("127.0.0.1", strconv.Itoa(spec.MetadataPort)), "--rc-server-read-timeout=10s", "--rc-server-write-timeout=30s", "--rc-max-header-bytes=32768")
	}
	p.Command = exec.CommandContext(ctx, program.Path, args...)
	p.Command.Env = []string{"HOME=" + private, "TMPDIR=" + private, "RCLONE_USER=" + spec.Username, "RCLONE_PASS=" + spec.Password, "RCLONE_ASK_PASSWORD=false"}
	if spec.MetadataPort != 0 {
		p.Command.Env = append(p.Command.Env, "RCLONE_RC_USER="+spec.Username, "RCLONE_RC_PASS="+spec.Password)
	}
	p.Command.Stdout, p.Command.Stderr = io.Discard, p.reports
	childprocess.Configure(p.Command)
	if err = spec.Enforce(p.Command); err != nil {
		return nil, err
	}
	if !program.Included {
		if p.Command.Path != program.Path {
			return nil, ErrServeProgram
		}
		version, checkErr := program.Check(ctx)
		if checkErr != nil {
			return nil, ErrServeProgram
		}
		p.programVersion = "rclone v" + version.Version
	}
	good = true
	return p, nil
}

// Version is the custom program version observed by this prepared start, or
// empty for the included program.
func (p *PreparedServe) Version() string {
	if p == nil {
		return ""
	}
	return p.programVersion
}

// Loaded removes only this command's fresh transient configuration, both on
// failed startup and after load. The retained source/auth configuration is owned
// elsewhere and is never removed or rewritten by this helper. Keep ownership of
// the exact transient pathname until physical Wait: a child can recreate files
// there after readiness, and final retirement must remove those copies too.
func (p *PreparedServe) Loaded() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.private == "" {
		return nil
	}
	if err := os.RemoveAll(p.private); err != nil {
		return err
	}
	return nil
}

// ServeProcessLease owns an already acquired physical source-process slot.
// Its context ends on source/profile/egress revocation. It conveys no actor or
// operation permission. StartServe consumes it on success and every failure;
// Release runs only after physical Wait, or proof that no child was started.
type ServeProcessLease interface {
	Context() context.Context
	Check(context.Context) error
	Release()
}

var ErrServeStopped = errors.New("remote serve process stopped")

// ServeProcess owns exactly one rclone serve process and private loopback
// endpoint. A new start always has a new generation; it never revives existing
// range sessions. Provider egress enforcement remains mandatory in the spec.
type ServeProcess struct {
	generation       string
	sourceID         string
	sourceGeneration string
	endpoint         *remotemedia.ServeEndpoint
	observer         *remotemedia.ServeEndpoint
	metadata         *remotemedia.RcloneMetadata
	reports          *serveReports
	programVersion   string
	budget           *remotemedia.OperationBudget
	lease            ServeProcessLease
	life             context.Context
	cancel           context.CancelFunc
	done             chan struct{}
	mu               sync.Mutex
	err              error
}

// OperationCounts is internal acceptance evidence for the one shared source
// budget. It is neither a public setting nor actor/source permission.
func (s *ServeProcess) OperationCounts() remotemedia.OperationCounts {
	if s == nil {
		return remotemedia.OperationCounts{}
	}
	return s.budget.Counts()
}

// SharedOperations returns only current resource custody for a separately
// authorized source connection test. It never admits an actor or media read.
func (s *ServeProcess) SharedOperations(ctx context.Context) (*remotemedia.OperationBudget, error) {
	if err := s.Check(ctx); err != nil {
		return nil, err
	}
	return s.budget, nil
}

func StartServe(ctx context.Context, program Program, spec ServeCommandSpec, lease ServeProcessLease) (*ServeProcess, error) {
	if lease == nil {
		return nil, ErrServeConfiguration
	}
	transferred := false
	defer func() {
		if !transferred {
			lease.Release()
		}
	}()
	if lease.Context() == nil {
		return nil, ErrServeConfiguration
	}
	if err := lease.Check(ctx); err != nil {
		return nil, err
	}
	life, cancel := context.WithCancel(ctx)
	stopLease := context.AfterFunc(lease.Context(), cancel)
	stopBudget := func() bool { return false }
	stop := func() { stopLease(); stopBudget() }
	prepared, err := PrepareServe(life, program, spec)
	if err != nil {
		stop()
		cancel()
		return nil, err
	}
	budget := spec.SharedOperations
	if budget == nil {
		budget = remotemedia.NewOperationBudget(life, spec.Connections)
	} else {
		stopBudget = context.AfterFunc(budget.Context(), cancel)
	}
	endpoint, err := remotemedia.NewServeEndpoint(spec.Port, spec.Username, spec.Password, budget)
	if err != nil {
		_ = prepared.Loaded()
		stop()
		cancel()
		return nil, err
	}
	observer, err := remotemedia.NewServeEndpoint(spec.Port, spec.Username, spec.Password, budget)
	if err != nil {
		endpoint.Close()
		_ = prepared.Loaded()
		stop()
		cancel()
		return nil, err
	}
	s := &ServeProcess{generation: identity.Token(), sourceID: spec.SourceID, sourceGeneration: spec.SourceGeneration, endpoint: endpoint, observer: observer, reports: prepared.reports, programVersion: prepared.Version(), budget: budget, lease: lease, life: life, cancel: cancel, done: make(chan struct{})}
	if spec.MetadataPort != 0 {
		s.metadata, err = remotemedia.NewRcloneMetadata(spec.MetadataPort, spec.Username, spec.Password, budget, spec.Remote)
		if err != nil {
			observer.Close()
			endpoint.Close()
			_ = prepared.Loaded()
			stop()
			cancel()
			return nil, err
		}
	}
	// The custom-program check happened once in PrepareServe, immediately
	// before this command was built; no second version run happens here.
	if err = lease.Check(life); err == nil {
		err = budget.Context().Err()
	}
	if err == nil {
		// Bounds pipe inheritance even when a custom program starts descendants.
		prepared.Command.WaitDelay = 100 * time.Millisecond
		err = prepared.Command.Start()
	}
	if err != nil {
		observer.Close()
		if s.metadata != nil {
			s.metadata.Close()
		}
		endpoint.Close()
		_ = prepared.Loaded()
		stop()
		cancel()
		return nil, err
	}
	transferred = true
	supervise.Go("rclone.serve.wait", func() {
		defer close(s.done)
		defer lease.Release()
		defer stop()
		defer cancel()
		defer endpoint.Close()
		defer observer.Close()
		if s.metadata != nil {
			defer s.metadata.Close()
		}
		err := prepared.Command.Wait()
		prepared.reports.discardPartial()
		// A wrapper exiting must not leave its process group listening.
		childprocess.Kill(prepared.Command)
		cleanup := prepared.Loaded()
		s.mu.Lock()
		if err != nil {
			s.err = ErrServeStopped
		} else if cleanup != nil {
			s.err = cleanup
		} else {
			s.err = ErrServeStopped
		}
		s.mu.Unlock()
	})
	// Readiness is an authenticated Depth0 request to this exact numeric port.
	// Only connection unavailability is retried, before any endpoint is exposed.
	ready, readyCancel := context.WithTimeout(life, 20*time.Second)
	defer readyCancel()
	for {
		attempt, attemptCancel := context.WithTimeout(ready, time.Second)
		var entry remotemedia.DAVEntry
		entry, err = endpoint.Stat(attempt, "", s.Check)
		attemptExpired := errors.Is(attempt.Err(), context.DeadlineExceeded)
		attemptCancel()
		if err == nil {
			if !entry.Directory {
				err = ErrServeConfiguration
			}
			if err == nil && s.metadata != nil {
				_, err = s.metadata.Capabilities(ready, s.Check)
			}
			if err == nil {
				err = prepared.Loaded()
				if err == nil {
					err = s.Check(ready)
				}
				break
			}
		}
		// A scheduled-out one-second probe is not proof the admitted child is
		// dead. Retry within the unchanged overall20s readiness budget, only
		// while its actual source/process lease is still current. Authentication
		// and malformed representation failures remain terminal.
		retryExpiredProbe := attemptExpired && errors.Is(err, context.DeadlineExceeded) && s.CheckLocal(ready) == nil
		if !errors.Is(err, remotemedia.ErrUnavailable) && !retryExpiredProbe || ready.Err() != nil {
			break
		}
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-timer.C:
		case <-s.done:
			err = ErrServeStopped
		case <-ready.Done():
			err = ready.Err()
		}
		timer.Stop()
		if err == ErrServeStopped || ready.Err() != nil {
			break
		}
	}
	if err != nil {
		cancel()
		childprocess.Kill(prepared.Command)
		<-s.done
		return nil, err
	}
	return s, nil
}

// Check is the non-I/O process fence used before/after metadata/range I/O and
// every dial. Actor, target and fresh provider observations are separate checks.
// CheckLocal is safe inside the caller's publication transaction. It checks
// physical custody/lifetime only, without DB snapshots, network or capacity waits.
func (s *ServeProcess) CheckLocal(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.budget == nil || s.budget.Context() == nil {
		return ErrServeConfiguration
	}
	if err := s.budget.Context().Err(); err != nil {
		return err
	}
	if err := s.life.Err(); err != nil {
		return err
	}
	select {
	case <-s.done:
		return ErrServeStopped
	default:
	}
	return s.lease.Context().Err()
}
func (s *ServeProcess) Check(ctx context.Context) error {
	if err := s.CheckLocal(ctx); err != nil {
		return err
	}
	if err := s.lease.Check(ctx); err != nil {
		if ctx.Err() == nil {
			s.cancel()
		}
		return err
	}
	return s.CheckLocal(ctx)
}

func (s *ServeProcess) Generation() string { return s.generation }

// ProgramVersion is the custom program version observed at this process start;
// empty means the included program, which is never checked.
func (s *ServeProcess) ProgramVersion() string {
	if s == nil {
		return ""
	}
	return s.programVersion
}

func (s *ServeProcess) Source() (id, generation string) { return s.sourceID, s.sourceGeneration }

func (s *ServeProcess) Endpoint(ctx context.Context) (*remotemedia.ServeEndpoint, error) {
	if err := s.Check(ctx); err != nil {
		return nil, err
	}
	return s.endpoint, nil
}

func (s *ServeProcess) Metadata(ctx context.Context) (*remotemedia.RcloneMetadata, error) {
	if err := s.Check(ctx); err != nil {
		return nil, err
	}
	if s.metadata == nil {
		return nil, ErrServeConfiguration
	}
	return s.metadata, nil
}

// ObservationEndpoint uses a separate private transport to the SAME server.
// A range transport can call its reconnect fence while holding its only dial
// slot, so fresh Stat must not recursively wait for that range transport slot.
// These loopback connection pools are not provider connection reservations.
func (s *ServeProcess) ObservationEndpoint(ctx context.Context) (*remotemedia.ServeEndpoint, error) {
	if err := s.Check(ctx); err != nil {
		return nil, err
	}
	return s.observer, nil
}

// Stop starts shutdown immediately. Wait remains the physical retirement fence;
// a caller's canceled wait never releases a slot held by a surviving process.
func (s *ServeProcess) Stop() { s.cancel() }

func (s *ServeProcess) Wait(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.done:
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.err
	}
}

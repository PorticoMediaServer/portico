package mounts

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/rcloneprogram"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// NativeBackend is a private, generation-fenced handle. It intentionally exposes
// neither the remote locator nor the configuration file contents.
type NativeBackend struct {
	included                                           bool
	service                                            *Service
	id, executable, digest, remote, config, generation string
}

type NativeEntry struct {
	Path    string `json:"Path"`
	Name    string `json:"Name"`
	Size    int64  `json:"Size"`
	ModTime string `json:"ModTime"`
	IsDir   bool   `json:"IsDir"`
	ID      string `json:"ID"`
}

func (s *Service) Native(ctx context.Context, id string) (*NativeBackend, error) {
	b := &NativeBackend{service: s, id: id}
	var generation int64
	var file string
	err := s.db.QueryRowContext(ctx, `SELECT m.executable,m.digest,m.remote,m.program_included,c.config_file,c.generation FROM managed_mounts m JOIN mount_backend_configs c ON c.mount_id=m.id JOIN mount_controls k ON k.mount_id=m.id WHERE m.id=? AND k.removal_pending=0`, id).Scan(&b.executable, &b.digest, &b.remote, &b.included, &file, &generation)
	if err != nil {
		return nil, storage.ErrRemoteOffline
	}
	if filepath.Base(file) != file {
		return nil, storage.ErrRemoteConfig
	}
	b.config = filepath.Join(s.private, file)
	b.generation = strconv.FormatInt(generation, 10)
	if err := b.Validate(ctx); err != nil {
		return nil, err
	}
	return b, nil
}
func (b *NativeBackend) Generation() string { return b.generation }
func (b *NativeBackend) Validate(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var generation int64
	var removing bool
	err := b.service.db.QueryRowContext(ctx, `SELECT c.generation,k.removal_pending FROM mount_backend_configs c JOIN mount_controls k ON k.mount_id=c.mount_id WHERE c.mount_id=?`, b.id).Scan(&generation, &removing)
	if err != nil || removing {
		return storage.ErrRemoteOffline
	}
	if strconv.FormatInt(generation, 10) != b.generation {
		return storage.ErrRemoteChanged
	}
	return nil
}

func nativeRelative(value string) bool {
	return utf8.ValidString(value) && len(value) <= 8192 && !strings.ContainsAny(value, "\\\x00\r\n") && strings.IndexFunc(value, unicode.IsControl) < 0 && (value == "" || value != "." && !strings.HasPrefix(value, "/") && path.Clean(value) == value && value != ".." && !strings.HasPrefix(value, "../"))
}
func (b *NativeBackend) target(relative string) (string, error) {
	if !nativeRelative(relative) {
		return "", storage.ErrRemoteConfig
	}
	if relative == "" {
		return b.remote, nil
	}
	return strings.TrimSuffix(b.remote, "/") + "/" + relative, nil
}

type nativeRequest struct {
	Executable, Digest, Remote, ConfigPath, PrivateHome, Password, Operation string
	Offset, Count                                                            int64
	HTTPProxy, SFTPProxy                                                     string
}

// NativeHelper is an allowlisted child boundary. The supervisor kills its entire
// process group on cancellation, retaining the permit until Wait completes.
// Secret configuration enters only through private stdin, never argv or logs.
func NativeHelper(input io.Reader, output io.Writer) error {
	var r nativeRequest
	dec := json.NewDecoder(io.LimitReader(input, 128<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		return storage.ErrRemoteConfig
	}
	if !filepath.IsAbs(r.Executable) {
		return storage.ErrRemoteConfig
	}
	if !validPolicyProxy(r.HTTPProxy) || r.SFTPProxy != "" && !validPolicyProxy(r.SFTPProxy) {
		return storage.ErrRemoteConfig
	}
	// The parent checks a custom program by version at this start; there is no
	// fingerprint custody here any more (ruling B-42).
	if !filepath.IsAbs(r.ConfigPath) || !filepath.IsAbs(r.PrivateHome) || !remotePattern.MatchString(r.Remote) {
		return storage.ErrRemoteConfig
	}
	args := []string{}
	switch r.Operation {
	case "list":
		args = []string{"lsjson", r.Remote, "--max-depth=1", "--disable=ListR", "--no-mimetype"}
	case "stat":
		args = []string{"lsjson", r.Remote, "--stat", "--no-mimetype"}
	case "read":
		if r.Offset < 0 || r.Count < 0 {
			return storage.ErrRemoteConfig
		}
		args = []string{"cat", r.Remote, "--offset", strconv.FormatInt(r.Offset, 10)}
		if r.Count > 0 {
			args = append(args, "--count", strconv.FormatInt(r.Count, 10))
		}
	default:
		return storage.ErrRemoteConfig
	}
	args = append(args, "--config", r.ConfigPath, "--ask-password=false", "--contimeout=10s", "--timeout=30s", "--low-level-retries=2", "--retries=1", "--log-level=ERROR")
	cmd := exec.Command(r.Executable, args...)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + r.PrivateHome, "TMPDIR=" + r.PrivateHome, "RCLONE_CONFIG_PASS=" + r.Password, "RCLONE_ASK_PASSWORD=false", "RCLONE_HTTP_PROXY=" + r.HTTPProxy, "RCLONE_SFTP_HTTP_PROXY=" + r.SFTPProxy}
	cmd.Stdout = output
	cmd.Stderr = io.Discard
	// Stay in the helper's process group: the owning storage supervisor must also
	// cancel descendants, not just the wrapper.
	if err := cmd.Run(); err != nil {
		return storage.ErrRemoteOffline
	}
	return nil
}

func (b *NativeBackend) run(ctx context.Context, relative, operation, lane string, offset, count int64, consume func(io.Reader) error) error {
	return b.runOwned(ctx, relative, operation, lane, offset, count, consume, nil)
}
func (b *NativeBackend) runOwned(ctx context.Context, relative, operation, lane string, offset, count int64, consume func(io.Reader) error, afterRetired func()) error {
	var once sync.Once
	var egress *SourceEgress
	retire := func() {
		once.Do(func() {
			if egress != nil {
				egress.Close()
			}
			if afterRetired != nil {
				afterRetired()
			}
		})
	}
	transferred := false
	defer func() {
		if !transferred {
			retire()
		}
	}()
	if err := b.Validate(ctx); err != nil {
		return err
	}
	target, err := b.target(relative)
	if err != nil {
		return err
	}
	// A custom program is checked by version at this native start under the
	// source's original operation context and validation custody. A bundled
	// selection is never checked. No digest is compared for old or new rows.
	if !b.included {
		if _, err := checkProgramVersion(ctx, b.service.validationSupervisor, b.executable); err != nil {
			return storage.ErrRemoteBinary
		}
	}
	config, egress, err := b.service.prepareBackendEgress(ctx, b.id, b.generation)
	if err != nil {
		return err
	}
	data, _ := json.Marshal(nativeRequest{Executable: b.executable, Digest: b.digest, Remote: target, ConfigPath: config, PrivateHome: b.service.work, Operation: operation, Offset: offset, Count: count, HTTPProxy: egress.HTTPProxy, SFTPProxy: egress.SFTPProxy})
	cmd := exec.Command(b.service.helper, "--portico-rclone-native")
	cmd.Stdin = bytes.NewReader(data)
	supervisor := b.service.nativeInventory
	if lane == "playback" {
		supervisor = b.service.nativePlayback
	}
	transferred = true
	err = supervisor.RunOwnedCompletion(ctx, lane+":"+b.id+":"+identity.Token(), cmd, consume, nil, retire)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, storage.ErrBusy) || errors.Is(err, storage.ErrRemoteLimit) || errors.Is(err, storage.ErrRemoteConfig) || errors.Is(err, storage.ErrRemoteChanged) || errors.Is(err, storage.ErrRemoteBinary) {
			return err
		}
		return storage.ErrRemoteOffline
	}
	return b.Validate(ctx)
}
func validNativeEntry(e NativeEntry) bool {
	return nativeRelative(e.Path) && (e.Path != "" || e.IsDir) && len(e.ID) <= 4096 && (e.IsDir || e.Size >= 0)
}
func (b *NativeBackend) List(ctx context.Context, relative string, visit func(NativeEntry) error) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	return b.ListOwned(ctx, relative, visit, nil)
}

// ListOwned is a retained inventory producer. The completion callback belongs
// to physical process retirement, never to a caller's cancelled wait.
func (b *NativeBackend) ListOwned(ctx context.Context, relative string, visit func(NativeEntry) error, afterRetired func()) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	return b.runOwned(ctx, relative, "list", "inventory", 0, 0, func(r io.Reader) error {
		return eachNativeJSON(r, func(raw []byte) error {
			var e NativeEntry
			if json.Unmarshal(raw, &e) != nil || !validNativeEntry(e) || strings.Contains(e.Path, "/") {
				return storage.ErrRemoteConfig
			}
			return visit(e)
		})
	}, afterRetired)
}
func (b *NativeBackend) Stat(ctx context.Context, relative string) (NativeEntry, error) {
	return b.StatForLane(ctx, relative, "inventory")
}
func (b *NativeBackend) StatForLane(ctx context.Context, relative, lane string) (NativeEntry, error) {
	var out NativeEntry
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	err := b.run(ctx, relative, "stat", lane, 0, 0, func(r io.Reader) error {
		data, e := io.ReadAll(io.LimitReader(r, 65537))
		if e != nil || len(data) > 65536 {
			return storage.ErrRemoteLimit
		}
		if json.Unmarshal(data, &out) != nil || !validNativeEntry(out) {
			return storage.ErrRemoteConfig
		}
		return nil
	})
	return out, err
}
func (b *NativeBackend) Read(ctx context.Context, relative, lane string, offset, count int64, consume func(io.Reader) error) error {
	return b.run(ctx, relative, "read", lane, offset, count, consume)
}
func (e NativeEntry) Snapshot(root, relative string) storage.Snapshot {
	modified := int64(0)
	if t, err := time.Parse(time.RFC3339Nano, e.ModTime); err == nil {
		modified = t.UnixNano()
	}
	raw, _ := json.Marshal([]any{e.ID, e.Size, e.ModTime, e.IsDir})
	sum := sha256.Sum256(raw)
	return storage.Snapshot{Path: filepath.Join(root, filepath.FromSlash(relative)), Name: path.Base(relative), Size: max(e.Size, 0), ModifiedNS: modified, Directory: e.IsDir, ObjectID: identity.Digest(e.ID + "\x00" + relative), Revision: hex.EncodeToString(sum[:])}
}

func (s *Service) approveExecutable(ctx context.Context, executable string) (string, string, string, error) {
	// Ruling B-42: the selected path must be absolute, and the only acceptance
	// check is running it with version under the existing validation custody.
	// Scripts, version-manager shims and Windows programs are valid. No file is
	// hashed here: a runnable program may not be readable, and Save must neither
	// reject it nor block on a read. The digest is honestly absent.
	executable = strings.TrimSpace(executable)
	if !filepath.IsAbs(executable) || filepath.Clean(executable) != executable || len(executable) > 8192 || strings.IndexFunc(executable, unicode.IsControl) >= 0 {
		return "", "", "", ErrExecutableInvalid
	}
	version, err := checkProgramVersion(ctx, s.validationSupervisor, executable)
	if err != nil {
		return "", "", "", err
	}
	return executable, "", "rclone v" + version.Version, nil
}

// checkProgramVersion is the one B-42 program check: a bounded version run under
// existing supervision and admission, with a bounded first line drained fully.
func checkProgramVersion(ctx context.Context, supervisor *storage.Supervisor, executable string) (rcloneprogram.Version, error) {
	check, cancel := context.WithTimeout(ctx, rcloneprogram.CheckTimeout)
	defer cancel()
	cmd := exec.Command(executable, "version")
	var line []byte
	if err := supervisor.Run(check, "rclone-version", cmd, func(r io.Reader) error {
		buf := make([]byte, 256)
		captured := false
		for {
			n, readErr := r.Read(buf)
			if n > 0 && !captured {
				for _, c := range buf[:n] {
					if c == '\n' {
						captured = true
						break
					}
					if len(line) < 129 {
						line = append(line, c)
					}
				}
			}
			if readErr == io.EOF {
				return nil
			}
			if readErr != nil {
				return ErrExecutableInvalid
			}
		}
	}); err != nil {
		return rcloneprogram.Version{}, ErrExecutableInvalid
	}
	version, err := rcloneprogram.CheckVersionLine(string(line))
	if err != nil {
		// B-42's two chooser errors are distinct domain facts; map the typed
		// check reason to them so HTTP never falls to internal.
		var checkErr *rcloneprogram.CheckError
		if errors.As(err, &checkErr) && checkErr.Code == "rclone_too_old" {
			return rcloneprogram.Version{}, &ExecutableTooOldError{Version: checkErr.Version}
		}
		return rcloneprogram.Version{}, ErrExecutableInvalid
	}
	return version, nil
}

func (s *Service) candidateFile(raw []byte) (string, error) {
	// Plain rclone config format: folder permissions are the protection.
	f, err := os.OpenFile(filepath.Join(s.work, identity.Token()+".candidate"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", err
	}
	name := f.Name()
	_, err = f.Write(raw)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(name)
		return "", err
	}
	if err = syncDirectory(s.work); err != nil {
		os.Remove(name)
		return "", err
	}
	return name, nil
}

func (s *Service) BackendInfo(ctx context.Context, id string) (generation int64, version string, cacheBytes, cacheFloor int64, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT generation,version,cache_bytes,cache_floor FROM mount_backend_configs WHERE mount_id=?`, id).Scan(&generation, &version, &cacheBytes, &cacheFloor)
	return
}

// Seal/Open store rclone's plain config format. There is no key.
func (s *Service) Seal(raw []byte) ([]byte, error) { return append([]byte(nil), raw...), nil }
func (s *Service) Open(plain []byte) ([]byte, error) {
	if len(plain) == 0 || len(plain) > 64<<10 {
		return nil, ErrConfigInvalid
	}
	return append([]byte(nil), plain...), nil
}

func (s *Service) NativeRoot(id string) (string, error) { m, e := s.get(id); return m.MountPath, e }

var _ = fmt.Sprintf

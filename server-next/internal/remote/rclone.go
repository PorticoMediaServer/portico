package remote

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/childprocess"
)

// RcloneBinary is the rclone that comes with Portico, found beside the server, or set with
// PORTICO_RCLONE. Tests replace it.
var RcloneBinary = func() string {
	if path := os.Getenv("PORTICO_RCLONE"); path != "" {
		return path
	}
	if exe, err := os.Executable(); err == nil {
		root := filepath.Dir(exe)
		for _, candidate := range []string{filepath.Join(root, "third_party", "rclone", "rclone"), filepath.Join(root, "..", "third_party", "rclone", "rclone")} {
			if path, err := exec.LookPath(candidate); err == nil {
				return path
			}
		}
	}
	return "rclone"
}

// ParseRcloneConfig reads one remote from rclone's configuration: a "[name]" line, then
// "key = value" lines, as rclone config show prints it. It returns the options, which must
// name the remote's type. Comments and blank lines are skipped; a second remote is refused.
// So that a pasted remote reaches only remote storage, an option that runs a program ("ssh",
// anything ending "_command") and a type that reads this machine's own disk or wraps another
// remote (which can be written inline with such options) are refused, named, never quoted.
func ParseRcloneConfig(text string) (map[string]string, error) {
	options, sections := map[string]string{}, 0
	for number, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";"):
		case strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]"):
			if sections++; sections > 1 {
				return nil, errors.New("paste one remote from the rclone configuration, not several")
			}
		default:
			key, value, ok := strings.Cut(line, "=")
			key = strings.ToLower(strings.TrimSpace(key))
			if !ok || !rcloneKey.MatchString(key) {
				// The line is not repeated: it may hold a token.
				return nil, fmt.Errorf("line %d is not a line of an rclone configuration", number+1)
			}
			if key == "ssh" || strings.HasSuffix(key, "_command") {
				return nil, fmt.Errorf("the option %q runs a program on this computer, which Portico does not allow", key)
			}
			options[key] = strings.TrimSpace(value)
		}
	}
	if options["type"] == "" || !rcloneKey.MatchString(options["type"]) {
		return nil, errors.New("the rclone configuration does not say the remote's type")
	}
	if rcloneRefusedTypes[options["type"]] {
		return nil, fmt.Errorf("a remote of type %q reads this computer's own disk or wraps another remote, which Portico does not allow", options["type"])
	}
	return options, nil
}

// rcloneRefusedTypes read this machine's disk (local, memory) or wrap another remote, which
// may be a local path or an inline remote carrying options this parser would have refused.
var rcloneRefusedTypes = map[string]bool{"local": true, "alias": true, "union": true, "combine": true, "crypt": true, "chunker": true,
	"compress": true, "hasher": true, "cache": true, "memory": true}

// RcloneHome is rclone's home folder: the server's own, inside its data folder, never a shared
// one, because rclone and its libraries read credentials from files there. NewRegistry sets
// it; with none, a private temporary folder is made.
var RcloneHome string

func rcloneHome() (string, error) {
	if RcloneHome != "" {
		return RcloneHome, os.MkdirAll(RcloneHome, 0o700)
	}
	return privateHome()
}

var privateHome = sync.OnceValues(func() (string, error) { return os.MkdirTemp("", "portico-rclone-") })

var rcloneKey = regexp.MustCompile(`^[a-z0-9_]{1,64}$`)

// rclone is a source reached through rclone: an rclone process serving the remote over
// WebDAV on a loopback port with a password made for it, read with the WebDAV source. The
// remote's options go to rclone in its environment, never on its command line, and are
// written to no file. rclone keeps to each provider's own request rules besides Portico's.
type rclone struct {
	options map[string]string
	root    string // the folder within the remote

	mu   sync.Mutex
	cmd  *exec.Cmd
	dav  *WebDAV
	done chan struct{}
}

// NewRclone serves one remote, given its options, from a folder within it.
func NewRclone(options map[string]string, root string) *rclone {
	return &rclone{options: options, root: strings.Trim(root, "/")}
}

// current returns the WebDAV client of the running rclone, starting rclone if it is not
// running, as after it stopped by itself.
func (r *rclone) current(ctx context.Context) (*WebDAV, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.done != nil {
		select {
		case <-r.done:
			r.cmd, r.dav, r.done = nil, nil, nil
		default:
			return r.dav, nil
		}
	}
	var secret [16]byte
	rand.Read(secret[:])
	password := hex.EncodeToString(secret[:])
	home, err := rcloneHome()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(RcloneBinary(), "serve", "webdav", "portico:"+r.root, "--read-only", "--addr", "127.0.0.1:0")
	// Its own process group, killed with the server if the server dies.
	childprocess.Configure(cmd)
	cmd.Env = []string{"RCLONE_CONFIG=" + os.DevNull, "RCLONE_USER=portico", "RCLONE_PASS=" + password, "HOME=" + home, "PATH=" + os.Getenv("PATH")}
	for key, value := range r.options {
		cmd.Env = append(cmd.Env, "RCLONE_CONFIG_PORTICO_"+strings.ToUpper(key)+"="+value)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("rclone could not be started: %w", err)
	}
	done := make(chan struct{})
	address := make(chan string, 1)
	go func() {
		// rclone says where it serves; after that its log is read and dropped.
		lines := bufio.NewScanner(stderr)
		for lines.Scan() {
			if found := servingAt.FindString(lines.Text()); found != "" {
				select {
				case address <- found:
				default:
				}
			}
		}
		io.Copy(io.Discard, stderr)
		cmd.Wait()
		close(done)
	}()
	stop := func() { childprocess.Kill(cmd); <-done }
	select {
	case at := <-address:
		dav, err := NewWebDAV(at, "portico", password)
		if err != nil {
			stop()
			return nil, err
		}
		r.cmd, r.dav, r.done = cmd, dav, done
		return dav, nil
	case <-done:
		return nil, fmt.Errorf("%w: rclone refused the remote's configuration", ErrNeedsAttention)
	case <-time.After(30 * time.Second):
		stop()
		return nil, errors.New("rclone did not start serving the remote")
	case <-ctx.Done():
		stop()
		return nil, ctx.Err()
	}
}

var servingAt = regexp.MustCompile(`http://127\.0\.0\.1:[0-9]{1,5}/`)

func (r *rclone) List(ctx context.Context, dir string) ([]Entry, error) {
	dav, err := r.current(ctx)
	if err != nil {
		return nil, err
	}
	return dav.List(ctx, dir)
}

func (r *rclone) Open(ctx context.Context, path string) (File, error) {
	dav, err := r.current(ctx)
	if err != nil {
		return nil, err
	}
	return dav.Open(ctx, path)
}

// Close stops rclone.
func (r *rclone) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.done != nil {
		childprocess.Kill(r.cmd)
		<-r.done
		r.cmd, r.dav, r.done = nil, nil, nil
	}
	return nil
}

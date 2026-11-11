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
			value = strings.TrimSpace(value)
			if rcloneLocalOption(key, value) {
				return nil, fmt.Errorf("the option %q names another remote, or a file or sign-in of this computer's own, which Portico does not allow", key)
			}
			options[key] = value
		}
	}
	if options["type"] == "" || !rcloneKey.MatchString(options["type"]) {
		return nil, errors.New("the rclone configuration does not say the remote's type")
	}
	if err := rcloneAddressRefusal(options); err != nil {
		return nil, err
	}
	if rcloneRefusedTypes[options["type"]] {
		return nil, fmt.Errorf("a remote of type %q reads this computer's own disk or wraps another remote, which Portico does not allow", options["type"])
	}
	return options, nil
}

// rcloneRefusedTypes read this machine's disk (local, memory) or wrap another remote, which
// may be a local path or an inline remote carrying options this parser would have refused.
var rcloneRefusedTypes = map[string]bool{"local": true, "alias": true, "union": true, "combine": true, "crypt": true, "chunker": true,
	"compress": true, "hasher": true, "cache": true, "memory": true, "archive": true}

// rcloneLocalOption says an option points rclone at something other than the remote's own
// storage: another remote (every wrapping type takes one as "remote" or "upstreams", so a
// wrapping type newer than the list above is refused too), a file or socket on this computer
// (an SSH key, a credentials file), or this computer's own cloud or Kerberos sign-in.
func rcloneLocalOption(key, value string) bool {
	switch key {
	case "remote", "upstreams", "unix_socket", "kerberos_ccache":
		return true
	case "env_auth", "use_msi", "use_az", "use_kerberos", "key_use_agent":
		return value != "" && value != "false"
	}
	return strings.HasSuffix(key, "_file") || strings.HasSuffix(key, "_path")
}

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
// remote's options go to rclone in its environment, never on its command line. rclone keeps
// to each provider's own request rules besides Portico's.
//
// A provider's sign-in token expires and rclone gets a new one; some providers (Box, and
// OneDrive within 90 days) also replace the refresh token. rclone writes the new token to its
// configuration file, which here is a private file of its own in rclone's home folder, empty
// at start and deleted when rclone stops. A token rclone wrote there is handed to saved, which
// keeps it sealed with the source, so the next start signs in with it.
type rclone struct {
	options map[string]string
	root    string // the folder within the remote
	// saved, when set, keeps a token rclone got. Set before the first use.
	saved func(token string)
	// tokenMu guards latest, the token rclone was last given or wrote. It is its own lock
	// because the token is read after rclone exits, while Close holds mu waiting for that.
	tokenMu sync.Mutex
	latest  string

	mu   sync.Mutex
	cmd  *exec.Cmd
	dav  *WebDAV
	done chan struct{}
}

// NewRclone serves one remote, given its options, from a folder within it.
func NewRclone(options map[string]string, root string) *rclone {
	return &rclone{options: options, root: strings.Trim(root, "/"), latest: options["token"]}
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
	if err := checkRcloneHost(ctx, r.options); err != nil {
		return nil, err
	}
	var secret [16]byte
	rand.Read(secret[:])
	password := hex.EncodeToString(secret[:])
	home, err := rcloneHome()
	if err != nil {
		return nil, err
	}
	var name [16]byte
	rand.Read(name[:])
	config := filepath.Join(home, hex.EncodeToString(name[:])+".conf")
	if err := os.WriteFile(config, nil, 0o600); err != nil {
		return nil, err
	}
	cmd := exec.Command(RcloneBinary(), "serve", "webdav", "portico:"+r.root, "--read-only", "--addr", "127.0.0.1:0")
	// Its own process group, killed with the server if the server dies.
	childprocess.Configure(cmd)
	cmd.Env = []string{"RCLONE_CONFIG=" + config, "RCLONE_USER=portico", "RCLONE_PASS=" + password, "HOME=" + home, "PATH=" + os.Getenv("PATH")}
	for key, value := range r.options {
		if key == "token" {
			value = r.token()
		}
		cmd.Env = append(cmd.Env, "RCLONE_CONFIG_PORTICO_"+strings.ToUpper(key)+"="+value)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		os.Remove(config)
		return nil, fmt.Errorf("rclone could not be started: %w", err)
	}
	done := make(chan struct{})
	exited := make(chan struct{})
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
		close(exited)
	}()
	go func() {
		// Every so often while rclone runs, and once after it stops, look for a new token.
		tick := time.NewTicker(RcloneTokenCheck)
		defer tick.Stop()
		for running := true; running; {
			select {
			case <-tick.C:
			case <-exited:
				running = false
			}
			r.keepToken(config)
		}
		os.Remove(config)
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

// RcloneTokenCheck is how often a running rclone's configuration file is read for a new token.
var RcloneTokenCheck = time.Minute

// keepToken hands on a token rclone wrote to its configuration file, if it differs from the
// one the source started with.
func (r *rclone) keepToken(config string) {
	if r.saved == nil {
		return
	}
	raw, err := os.ReadFile(config)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(raw), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if value = strings.TrimSpace(value); ok && strings.TrimSpace(key) == "token" && value != "" && value != r.token() {
			r.tokenMu.Lock()
			r.latest = value
			r.tokenMu.Unlock()
			r.saved(value)
		}
	}
}

func (r *rclone) token() string {
	r.tokenMu.Lock()
	defer r.tokenMu.Unlock()
	return r.latest
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

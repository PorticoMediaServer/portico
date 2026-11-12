package remote

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/childprocess"
)

// ErrFolderMissing says the folder a source is served from is not on it, or is a file. rclone
// would serve such a folder as an empty one, so a mistyped folder would pass its test and show
// an empty browser.
var ErrFolderMissing = fmt.Errorf("%w: the source's folder is not on it", ErrNeedsAttention)

// remoteEnv is rclone's environment for this remote: its configuration file, its home, and the
// remote's options, never on its command line.
func (r *rclone) remoteEnv(config, home string) []string {
	env := []string{"RCLONE_CONFIG=" + config, "HOME=" + home, "PATH=" + os.Getenv("PATH")}
	for key, value := range r.options {
		if key == "token" {
			value = r.token()
		}
		env = append(env, "RCLONE_CONFIG_PORTICO_"+strings.ToUpper(key)+"="+value)
	}
	return env
}

// checkRemote asks rclone, before serving the remote, about the folder it serves (the top when
// none is set): one request of the provider, each time rclone starts and on Test. It is the
// only way to learn two things serving hides: rclone serves a folder that is not there as an
// empty one, and a remote that refuses its sign-in as an empty top with every file not found.
// A token rclone got doing so is used from then on. Other failures are left to serving.
func (r *rclone) checkRemote(ctx context.Context, home string) error {
	var name [16]byte
	rand.Read(name[:])
	config := filepath.Join(home, hex.EncodeToString(name[:])+".conf")
	if err := os.WriteFile(config, nil, 0o600); err != nil {
		return err
	}
	defer os.Remove(config)
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	// One try: a provider that refuses is answered sooner and asked less.
	cmd := exec.CommandContext(ctx, RcloneBinary(), "lsjson", "--stat", "--retries", "1", "--low-level-retries", "1", "portico:"+r.root)
	childprocess.Configure(cmd)
	cmd.Env = r.remoteEnv(config, home)
	var out, stderr bytes.Buffer
	cmd.Stdout = &limited{&out, 64 << 10}
	cmd.Stderr = &limited{&stderr, 64 << 10}
	err := cmd.Run()
	r.keepToken(config)
	var exited *exec.ExitError
	switch {
	case err == nil:
		var stat struct{ IsDir bool }
		if json.Unmarshal(out.Bytes(), &stat) != nil || !stat.IsDir {
			return ErrFolderMissing
		}
	case ctx.Err() != nil:
		return errors.New("rclone did not answer in time")
	case !errors.As(err, &exited):
		return fmt.Errorf("%w: %v", ErrRcloneMissing, err)
	case exited.ExitCode() == 3 || exited.ExitCode() == 4:
		// rclone's own codes: directory not found, file not found.
		return ErrFolderMissing
	case signInRefused.MatchString(stderr.String()):
		return ErrSignIn
	case notSignIn.MatchString(stderr.String()):
		// A rate limit or quota: the source asks for a rest, as it would of any request.
		return &SlowDown{}
	case exited.ExitCode() == 5:
		// rclone's own code for a temporary error: the source could not be reached (rclone
		// would otherwise serve it as an empty folder).
		return &url.Error{Op: "Get", URL: "the source", Err: errors.New("rclone could not reach the source")}
	}
	return nil
}

// check is checkRemote for a source already open: its saved Test.
func (r *rclone) check(ctx context.Context) error {
	home, err := rcloneHome()
	if err != nil {
		return err
	}
	return r.checkRemote(ctx, home)
}

// signInRefused reads what rclone printed for a provider refusing the remote's sign-in. rclone
// has no exit code of its own for it, so its words are read, line by line: an HTTP 401 or 403
// stated as a status ("401 Unauthorized", "403 Forbidden", "Error 403"), an OAuth refresh
// refused, an S3 key or signature refused, an SSH, FTP or SMB log-in refused. A number in a
// folder's name or a port is not a status, and a line about a rate limit or quota (Google
// Drive answers those with 403) is never a sign-in.
var signInRefused signInWords

type signInWords struct{}

var (
	signInStatus = regexp.MustCompile(`(?i)\b40[13] (?:unauthori[sz]ed|forbidden)\b|\b(?:error|http|status|code)\W{0,3}40[13]\b`)
	signInWord   = regexp.MustCompile(`(?i)invalid_grant|invalid_client|InvalidAccessKeyId|SignatureDoesNotMatch|AccessDenied|unable to authenticate|login incorrect|not logged in|logon.failure`)
	notSignIn    = regexp.MustCompile(`(?i)rate.?limit|quota|too many requests`)
)

func (signInWords) MatchString(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		if !notSignIn.MatchString(line) && (signInStatus.MatchString(line) || signInWord.MatchString(line)) {
			return true
		}
	}
	return false
}

// limited keeps at most n bytes of what is written to it, and takes the rest without error.
type limited struct {
	w io.Writer
	n int64
}

func (l *limited) Write(p []byte) (int, error) {
	if keep := min(int64(len(p)), l.n); keep > 0 {
		l.w.Write(p[:keep])
		l.n -= keep
	}
	return len(p), nil
}

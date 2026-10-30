package decoder

import (
	"context"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// PreparedFileSandboxAvailable checks the installed utility's real capability,
// not a test/qualification flag. Older bwrap versions without fd-bound mounts
// cannot safely replace this with a pathname bind or an unconfined invocation.
func PreparedFileSandboxAvailable(s *storage.Supervisor, executable string) bool {
	if s == nil || executable == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.Command(executable, "--help")
	var raw []byte
	retired := make(chan struct{})
	e := s.RunOwnedCompletion(ctx, "optimization:sandbox-capability", cmd, func(r io.Reader) error {
		var err error
		raw, err = io.ReadAll(io.LimitReader(r, 128<<10))
		return err
	}, nil, func() { close(retired) })
	<-retired
	return e == nil && strings.Contains(string(raw), "--ro-bind-fd") && strings.Contains(string(raw), "--sync-fd") && strings.Contains(string(raw), "--disable-userns")
}

type preparedFileCommand struct {
	cmd      *exec.Cmd
	close    func()
	validate func() error
}

// File-backed Linux preparation never exposes a library path or network. The
// input is an exact, closed, managed staging inode mounted read-only by bwrap.
// Its fd is consumed by mount setup and is not leaked to the codec process.
func runPreparedFile(ctx context.Context, s *storage.Supervisor, key, sandbox, executable string, input *os.File, custody *PreparedFileCustody, args []string, consume func(io.Reader) error, libraries ...string) error {
	if s == nil || key == "" || custody == nil || input == nil {
		return ErrInvalidConfiguration
	}
	// Prepare the independent lock description before any child can start.
	next, e := custody.waiter()
	if e != nil {
		return e
	}
	defer func() {
		if next != nil {
			next.Close()
		}
	}()
	owned, e := confinedPreparedFileCommand(sandbox, executable, input, custody.file, args, libraries...)
	if e != nil {
		return e
	}
	defer owned.close()
	retired := make(chan struct{})
	e = s.RunOwnedCompletion(ctx, "optimization:"+key, owned.cmd, consume, nil, func() { close(retired) })
	<-retired
	// Wait is for bwrap's monitor, which can exit before namespace init. The
	// inherited lock is held by init until the sandbox's last child has retired.
	custody.retired(next)
	next = nil
	if e != nil {
		return e
	}
	return owned.validate()
}
func RunPreparedFileProbe(ctx context.Context, s *storage.Supervisor, key, sandbox, executable string, input *os.File, custody *PreparedFileCustody, libraries ...string) ([]byte, error) {
	args := []string{"-v", "error", "-protocol_whitelist", "file", "-format_whitelist", preparedInputFormats, "-probesize", "8388608", "-analyzeduration", "10000000", "-show_format", "-show_streams", "-of", "json", "/input.media"}
	var raw []byte
	e := runPreparedFile(ctx, s, key, sandbox, executable, input, custody, args, func(r io.Reader) error {
		var err error
		raw, err = io.ReadAll(io.LimitReader(r, MaxProbeOutputBytes+1))
		if err == nil && len(raw) > MaxProbeOutputBytes {
			err = ErrProbeOutput
		}
		return err
	}, libraries...)
	return raw, e
}
func RunPreparedFileEncode(ctx context.Context, s *storage.Supervisor, key, sandbox, executable string, input *os.File, custody *PreparedFileCustody, recipe PreparedRecipe, out io.Writer, libraries ...string) error {
	args, e := preparedArgs("/input.media", recipe, false)
	if e != nil {
		return e
	}
	for i := range args {
		if args[i] == "-protocol_whitelist" {
			args[i+1] = "file"
		}
	}
	return runPreparedFile(ctx, s, key, sandbox, executable, input, custody, args, func(r io.Reader) error { _, e := io.Copy(out, r); return e }, libraries...)
}
func RunPreparedFileValidate(ctx context.Context, s *storage.Supervisor, key, sandbox, executable string, input *os.File, custody *PreparedFileCustody, libraries ...string) error {
	args, e := preparedArgs("/input.media", PreparedRecipe{}, true)
	if e != nil {
		return e
	}
	for i := range args {
		if args[i] == "-protocol_whitelist" {
			args[i+1] = "file"
		}
	}
	return runPreparedFile(ctx, s, key, sandbox, executable, input, custody, args, func(r io.Reader) error { _, e := io.Copy(io.Discard, r); return e }, libraries...)
}

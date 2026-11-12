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
	"os"
	"os/exec"
	"path/filepath"
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

// checkFolder asks rclone, before serving a folder within the remote, whether the folder is
// there: one request of the provider, each time rclone starts. A token rclone got doing so is
// kept as serving keeps one. Any other failure is left for serving to report.
func (r *rclone) checkFolder(ctx context.Context, home string) error {
	if r.root == "" {
		return nil
	}
	var name [16]byte
	rand.Read(name[:])
	config := filepath.Join(home, hex.EncodeToString(name[:])+".conf")
	if err := os.WriteFile(config, nil, 0o600); err != nil {
		return err
	}
	defer os.Remove(config)
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, RcloneBinary(), "lsjson", "--stat", "portico:"+r.root)
	childprocess.Configure(cmd)
	cmd.Env = r.remoteEnv(config, home)
	var out bytes.Buffer
	cmd.Stdout = &limited{&out, 64 << 10}
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
	}
	return nil
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

package mediaexec

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/PorticoMediaServer/portico/server/internal/supervise"
)

// ShimFlag is the server binary's entry point for the limits shim. The shim
// runs outside any sandbox, applies the job's resource limits and priority,
// records the process group in the orphan ledger and then replaces itself with
// the sandbox (or the tool), so it costs one exec and no extra process.
const ShimFlag = "--portico-media-exec"

type shimSpec struct {
	Instance string `json:"i,omitempty"`
	Ledger   string `json:"l,omitempty"`
	// FileBytes is RLIMIT_FSIZE; negative forbids creating or growing files.
	FileBytes  int64 `json:"f"`
	CPUSeconds int   `json:"c,omitempty"`
	Background bool  `json:"b,omitempty"`
}

// Options configure the server's media processes. Configure is called once at
// startup; until then (and in tests) jobs are sandboxed but run without the
// limits shim and the ledger.
type Options struct {
	// StateDir holds the orphan ledger (<state>/run/media-processes).
	StateDir string
	// Helper is the server binary that implements ShimFlag; os.Executable() by
	// default.
	Helper string
	// Libraries are extra exact dependency files for the sandbox.
	Libraries []string
	// SkipOrphanKill leaves the previous runs' media process groups alone. The
	// server sets it when the single-instance lock is unavailable: without the
	// lock another server may still be running against this state directory,
	// and its live jobs are not orphans.
	SkipOrphanKill bool
}

var configured struct {
	sync.RWMutex
	helper    string
	ledger    string
	instance  string
	libraries []string
}

// Configure enables the limits shim and the orphan ledger, and reaps the media
// process groups a previous run of this server left behind (BE-MEDIA-06). It
// returns how many groups it stopped.
func Configure(o Options) (int, error) {
	helper := o.Helper
	if helper == "" {
		exe, err := os.Executable()
		if err != nil {
			return 0, err
		}
		helper = exe
	}
	helper, err := filepath.Abs(helper)
	if err != nil {
		return 0, err
	}
	for _, p := range o.Libraries {
		if !filepath.IsAbs(p) || filepath.Clean(p) != p {
			return 0, ErrInvalidJob
		}
	}
	instance := make([]byte, 12)
	if _, err = rand.Read(instance); err != nil {
		return 0, err
	}
	ledger := ""
	if o.StateDir != "" && runtime.GOOS != "windows" {
		ledger = storagepaths.Path(o.StateDir, "run", "media-processes")
		if err = os.MkdirAll(ledger, 0700); err != nil {
			return 0, err
		}
	}
	configured.Lock()
	configured.helper, configured.ledger, configured.instance = helper, ledger, hex.EncodeToString(instance)
	configured.libraries = append([]string{}, o.Libraries...)
	configured.Unlock()
	if ledger == "" {
		return 0, nil
	}
	if o.SkipOrphanKill {
		// No lock, so possibly another live server: forget dead entries only.
		return reapLedger(ledger, configured.instance, false)
	}
	return reapLedger(ledger, configured.instance, true)
}

func configuredLibraries() []string {
	configured.RLock()
	defer configured.RUnlock()
	return configured.libraries
}

func shimArgv(job Job, inner []string) []string {
	configured.RLock()
	helper, ledger, instance := configured.helper, configured.ledger, configured.instance
	configured.RUnlock()
	if helper == "" || runtime.GOOS == "windows" {
		return inner
	}
	spec := shimSpec{Instance: instance, Ledger: ledger, FileBytes: job.MaxFileBytes, CPUSeconds: job.MaxCPUSeconds, Background: job.Background}
	raw, _ := json.Marshal(spec)
	return append([]string{helper, ShimFlag, base64.RawURLEncoding.EncodeToString(raw), "--"}, inner...)
}

// RunHelper is the shim's entry point: the server's main (and a test binary's
// TestMain) calls it first. It returns handled=false for any other argv, and
// otherwise only returns if the exec failed.
func RunHelper(args []string) (bool, error) {
	if len(args) == 0 || args[0] != ShimFlag {
		return false, nil
	}
	spec, inner, err := parseShim(args)
	if err != nil {
		return true, err
	}
	return true, execShim(spec, inner)
}

// ExecArgv replaces the calling helper process with an argv that Argv built in
// the server. A shim argv is run in-process, so the helper's own process group
// is what the ledger records. It only returns on failure.
func ExecArgv(argv []string) error {
	if len(argv) == 0 || !filepath.IsAbs(argv[0]) {
		return ErrInvalidJob
	}
	if len(argv) > 1 && argv[1] == ShimFlag {
		spec, inner, err := parseShim(argv[1:])
		if err != nil {
			return err
		}
		return execShim(spec, inner)
	}
	return execInPlace(argv)
}

func parseShim(args []string) (shimSpec, []string, error) {
	var spec shimSpec
	if len(args) < 4 || args[0] != ShimFlag || args[2] != "--" || len(args[1]) > 4096 {
		return spec, nil, ErrInvalidJob
	}
	raw, err := base64.RawURLEncoding.DecodeString(args[1])
	if err != nil || json.Unmarshal(raw, &spec) != nil {
		return spec, nil, ErrInvalidJob
	}
	inner := args[3:]
	if !filepath.IsAbs(inner[0]) || spec.Ledger != "" && (!filepath.IsAbs(spec.Ledger) || filepath.Clean(spec.Ledger) != spec.Ledger) {
		return spec, nil, ErrInvalidJob
	}
	return spec, inner, nil
}

// The ledger is swept in the background every sweepEvery jobs, so it holds
// roughly the running processes plus that many finished ones.
const sweepEvery = 256

var (
	commands atomic.Uint64
	sweeping atomic.Bool
)

func noteCommand() {
	if commands.Add(1)%sweepEvery != 0 {
		return
	}
	configured.RLock()
	ledger, instance := configured.ledger, configured.instance
	configured.RUnlock()
	if ledger == "" || !sweeping.CompareAndSwap(false, true) {
		return
	}
	supervise.Go("mediaexec.ledger.sweep", func() {
		defer sweeping.Store(false)
		_, _ = reapLedger(ledger, instance, false)
	})
}

// SweepLedger removes the entries of processes that have ended. The server
// calls it when a sweep is due; tests call it directly.
func SweepLedger() error {
	configured.RLock()
	ledger, instance := configured.ledger, configured.instance
	configured.RUnlock()
	if ledger == "" {
		return errors.New("media process ledger not configured")
	}
	_, err := reapLedger(ledger, instance, false)
	return err
}

package telemetry

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"github.com/PorticoMediaServer/portico/server/internal/childprocess"
	"strings"
	"time"
)

// Sampling shells out only to read-only platform reporters. Every call is
// bounded three ways: a short deadline, a capped read, and a fixed argument
// list that never contains owner-supplied text.
const (
	commandTimeout = 700 * time.Millisecond
	commandOutput  = 256 << 10
	fileReadLimit  = 1 << 20
)

var errCommandMissing = errors.New("reporter is not installed")

// lookPath is a seam so tests can decide which reporters exist.
var lookPath = exec.LookPath

func commandAvailable(name string) bool {
	_, e := lookPath(name)
	return e == nil
}

// runCommand executes one reporter and returns its bounded standard output.
// A reporter that writes more than the cap is truncated, not killed mid-parse.
func runCommand(ctx context.Context, name string, args ...string) (string, error) {
	if _, e := lookPath(name); e != nil {
		return "", errCommandMissing
	}
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	childprocess.Configure(cmd)
	cmd.WaitDelay = time.Second
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "LC_ALL=C"}
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	if e := cmd.Run(); e != nil && out.Len() == 0 {
		return "", e
	}
	if out.Len() > commandOutput {
		return out.String()[:commandOutput], nil
	}
	return out.String(), nil
}

func readFileBounded(path string) (string, error) {
	f, e := os.Open(path)
	if e != nil {
		return "", e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, fileReadLimit))
	if e != nil {
		return "", e
	}
	return string(b), nil
}

func fields(line string) []string { return strings.Fields(line) }

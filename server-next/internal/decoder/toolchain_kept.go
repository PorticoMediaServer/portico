package decoder

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/PorticoMediaServer/portico/server-next/internal/mediaexec"
)

// Asking ffmpeg what it can do takes several seconds: seven runs of it in its
// sandbox, and reading every byte of the program and its libraries to name
// exactly what was asked. The answer only changes when the program or one of
// its libraries does, so it is kept in a file between starts.
//
// The kept answer is trusted when the program and every library it loads are the
// same files as when it was made: the same paths, sizes and modified times. That
// is read from the file system without opening them, in a few milliseconds.

// keptToolchain is the file's content.
type keptToolchain struct {
	// Files names the program and its libraries as they were: see toolFiles.
	Files string         `json:"files"`
	Facts ToolchainFacts `json:"facts"`
}

// toolFiles names a program and the libraries it loads by path, size and
// modified time, or is "" when that cannot be told (then nothing is kept).
func toolFiles(path string) string {
	libraries, err := mediaexec.ResolveLibraries(path, path, nil)
	if err != nil {
		return ""
	}
	sum := sha256.New()
	for _, file := range append([]string{path}, libraries...) {
		info, err := os.Stat(file)
		if err != nil || !info.Mode().IsRegular() {
			return ""
		}
		fmt.Fprintf(sum, "%s\x00%d\x00%d\n", file, info.Size(), info.ModTime().UnixNano())
	}
	return hex.EncodeToString(sum.Sum(nil))
}

// ProbeToolchainKept is ProbeToolchain that keeps its answer in file between
// starts of the server. A file that is missing, damaged or about other files is
// ignored and written again. An empty file name keeps nothing.
func ProbeToolchainKept(ctx context.Context, ffmpeg, file string) (ToolchainFacts, error) {
	files := ""
	path, err := exec.LookPath(ffmpeg)
	if err == nil && file != "" {
		if path, err = filepath.Abs(path); err == nil {
			files = toolFiles(path)
		}
	}
	if files != "" {
		var kept keptToolchain
		if raw, err := os.ReadFile(file); err == nil && json.Unmarshal(raw, &kept) == nil && kept.Files == files &&
			kept.Facts.Identity != "" && kept.Facts.Version != "" && len(kept.Facts.Encoders) > 0 && len(kept.Facts.Muxers) > 0 {
			kept.Facts.inventoryCacheable = true
			return kept.Facts, nil
		}
	}
	facts, err := ProbeToolchain(ctx, ffmpeg)
	// Kept only if nothing changed while it was being asked.
	if err == nil && files != "" && facts.inventoryCacheable && toolFiles(path) == files {
		if raw, err := json.Marshal(keptToolchain{Files: files, Facts: facts}); err == nil {
			temporary := file + ".new"
			if os.WriteFile(temporary, raw, 0o600) == nil {
				_ = os.Rename(temporary, file)
			}
		}
	}
	return facts, err
}

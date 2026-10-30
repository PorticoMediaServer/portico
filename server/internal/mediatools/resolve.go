// Package mediatools resolves explicitly configured, packaged, and verified
// development tools. It deliberately has no dependency on decoder or testing.
package mediatools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type manifest struct {
	SchemaVersion int               `json:"schemaVersion"`
	LicenseMode   string            `json:"licenseMode"`
	Target        string            `json:"target"`
	Files         map[string]string `json:"files"`
}

// VerifyBundle implements the release verifier's inventory, provenance and
// SHA-256 rules before any discovered executable is allowed to run. Symlinks
// (including directory symlinks) are rejected, and metadata is bounded.
func VerifyBundle(root string) error {
	return VerifyBundleContext(context.Background(), root)
}

// VerifyBundleContext retains the same complete bundle policy while allowing
// a lifecycle qualification to stop during a large source archive hash.
func VerifyBundleContext(ctx context.Context, root string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	info, err := os.Lstat(root)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("bundle is not a directory")
	}
	path := filepath.Join(root, "toolchain-manifest.json")
	info, err = os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return fmt.Errorf("invalid manifest file")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var m manifest
	if err = json.Unmarshal(raw, &m); err != nil {
		return err
	}
	if m.SchemaVersion != 1 || m.LicenseMode != "gpl3" || len(m.Files) == 0 || len(m.Files) > 4096 {
		return fmt.Errorf("invalid manifest")
	}
	targetOS := map[string]string{"darwin": "macos", "linux": "linux", "windows": "windows"}[runtime.GOOS]
	targetArch := map[string]string{"amd64": "x64", "arm64": "arm64"}[runtime.GOARCH]
	if targetOS == "" || targetArch == "" || m.Target != targetOS+"-"+targetArch {
		return fmt.Errorf("bundle target %q does not match this host", m.Target)
	}
	required := []string{"corresponding-source.tar.xz", "NOTICE.md", "sources.lock.json", "requirements.v1.json", "portico-disc-build.json", "LICENSES/portico-disc.txt", "bin/" + Name("ffmpeg"), "bin/" + Name("ffprobe"), "bin/" + Name("portico-disc")}
	for _, name := range required {
		if _, ok := m.Files[name]; !ok {
			return fmt.Errorf("missing provenance or tool: %s", name)
		}
	}
	licensed := false
	count := 0
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink in bundle: %s", path)
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(rel)
		if name == "toolchain-manifest.json" {
			return nil
		}
		digest, ok := m.Files[name]
		if !ok {
			return fmt.Errorf("unexpected bundle file: %s", name)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("non-regular bundle file: %s", name)
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		h := sha256.New()
		_, err = io.Copy(h, bundleContextReader{ctx, f})
		closeErr := f.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		if hex.EncodeToString(h.Sum(nil)) != digest {
			return fmt.Errorf("bundle hash mismatch: %s", name)
		}
		licensed = licensed || strings.HasPrefix(name, "LICENSES/")
		count++
		return nil
	})
	if err != nil {
		return err
	}
	if count != len(m.Files) || !licensed {
		return fmt.Errorf("bundle inventory or licenses incomplete")
	}
	return verifyDiscRecord(root, m)
}
func Name(tool string) string {
	if runtime.GOOS == "windows" {
		return tool + ".exe"
	}
	return tool
}

// RepositoryTool searches ancestors, never executes a candidate to verify it.
// An invalid bundle is reported to callers rather than silently treated as absent.
func RepositoryTool(tool string, starts ...string) (string, error) {
	seen := map[string]bool{}
	for _, start := range starts {
		root, err := filepath.Abs(start)
		if err != nil {
			continue
		}
		for {
			if !seen[root] {
				seen[root] = true
				bundle := filepath.Join(root, "third_party", "ffmpeg", "bundle")
				if _, err := os.Lstat(bundle); err == nil {
					if err = VerifyBundle(bundle); err != nil {
						return "", fmt.Errorf("%s: %w", bundle, err)
					}
					return exec.LookPath(filepath.Join(bundle, "bin", Name(tool)))
				} else if !os.IsNotExist(err) {
					return "", err
				}
			}
			parent := filepath.Dir(root)
			if parent == root {
				break
			}
			root = parent
		}
	}
	return "", os.ErrNotExist
}
func DevelopmentTool(tool string) (string, error) {
	starts := []string{}
	if exe, err := os.Executable(); err == nil {
		starts = append(starts, filepath.Dir(exe))
	}
	if cwd, err := os.Getwd(); err == nil {
		starts = append(starts, cwd)
	}
	return RepositoryTool(tool, starts...)
}
func Resolve(tool string) string {
	if configured := activePair.Load(); configured != nil {
		switch tool {
		case "ffmpeg":
			return configured.ffmpeg
		case "ffprobe":
			return configured.ffprobe
		case "portico-disc":
			// Disc reads use the helper shipped with the selected toolchain.
			// A missing companion must not select an unrelated PATH helper.
			path, err := exec.LookPath(filepath.Join(filepath.Dir(configured.ffmpeg), Name(tool)))
			if err != nil {
				return "\x00selected media toolchain has no portico-disc: " + err.Error()
			}
			return path
		}
	}
	if value := os.Getenv("PORTICO_" + strings.ToUpper(tool)); value != "" {
		return value
	}
	name := Name(tool)
	if exe, err := os.Executable(); err == nil {
		root := filepath.Dir(exe)
		for _, candidate := range []string{filepath.Join(root, name), filepath.Join(root, "..", "third_party", "ffmpeg", "bin", name), filepath.Join(root, "third_party", "ffmpeg", "bin", name)} {
			if path, err := exec.LookPath(candidate); err == nil {
				return path
			}
		}
	}
	if path, err := DevelopmentTool(tool); err == nil {
		return path
	} else if !os.IsNotExist(err) {
		// A NUL cannot be a filesystem path on any supported host. Preserve the
		// verification error for startup diagnostics and fail before execution,
		// even if an attacker has populated a predictable temporary directory.
		return "\x00unverified media toolchain: " + err.Error()
	}
	if path, err := exec.LookPath(name); err == nil {
		return path
	}
	return name
}

// ResolvePair verifies a discovered bundle once and selects its companion probe.
// Explicit overrides still win independently. Packaged tools and PATH tools use
// the same sibling convention; a missing companion goes through full resolution.
func ResolvePair() (string, string) {
	if configured := activePair.Load(); configured != nil {
		return configured.ffmpeg, configured.ffprobe
	}
	ffmpeg := Resolve("ffmpeg")
	if probe := os.Getenv("PORTICO_FFPROBE"); probe != "" {
		return ffmpeg, probe
	}
	if filepath.IsAbs(ffmpeg) {
		if probe, err := exec.LookPath(filepath.Join(filepath.Dir(ffmpeg), Name("ffprobe"))); err == nil {
			return ffmpeg, probe
		}
	}
	return ffmpeg, Resolve("ffprobe")
}

type bundleContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r bundleContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

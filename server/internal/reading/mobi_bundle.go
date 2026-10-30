package reading

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

type mobiBundleManifest struct {
	Schema        int    `json:"schema"`
	Target        string `json:"target"`
	Converter     string `json:"converter"`
	Options       string `json:"options"`
	WorkerSHA256  string `json:"workerSHA256"`
	SandboxSHA256 string `json:"sandboxSHA256"`
}

// LoadMobiBundle resolves only a specific Release/admin configured bundle and
// exact manifest digest. It never searches PATH, downloads or installs a tool.
// Scratch is a distinct private server directory, not a guest-selected path.
func LoadMobiBundle(directory, manifestSHA256, scratch string) (MobiBundle, error) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" || !filepath.IsAbs(directory) {
		return MobiBundle{}, ErrUnavailable
	}
	canonical, err := filepath.EvalSymlinks(directory)
	if err != nil || canonical != filepath.Clean(directory) {
		return MobiBundle{}, ErrUnavailable
	}
	root, err := os.Lstat(canonical)
	if err != nil || !root.IsDir() || root.Mode().Perm()&0022 != 0 {
		return MobiBundle{}, ErrUnavailable
	}
	name := filepath.Join(canonical, "manifest.json")
	before, err := os.Lstat(name)
	if err != nil || !before.Mode().IsRegular() || before.Size() <= 0 || before.Size() > 8192 || before.Mode().Perm()&0022 != 0 {
		return MobiBundle{}, ErrUnavailable
	}
	file, err := os.Open(name)
	if err != nil {
		return MobiBundle{}, err
	}
	defer file.Close()
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) {
		return MobiBundle{}, ErrChanged
	}
	raw, err := io.ReadAll(io.LimitReader(file, 8193))
	if err != nil || len(raw) > 8192 {
		return MobiBundle{}, ErrUnavailable
	}
	digest := sha256.Sum256(raw)
	if len(manifestSHA256) != 64 || hex.EncodeToString(digest[:]) != manifestSHA256 {
		return MobiBundle{}, ErrUnavailable
	}
	var manifest mobiBundleManifest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&manifest) != nil || decoder.Decode(new(any)) != io.EOF {
		return MobiBundle{}, ErrUnavailable
	}
	if manifest.Schema != 1 || manifest.Target != "x86_64-linux-musl" || manifest.Converter != mobiDependency || manifest.Options != mobiOptions {
		return MobiBundle{}, ErrUnavailable
	}
	for _, hash := range []string{manifest.WorkerSHA256, manifest.SandboxSHA256} {
		decoded, err := hex.DecodeString(hash)
		if err != nil || len(decoded) != 32 || hex.EncodeToString(decoded) != hash {
			return MobiBundle{}, ErrUnavailable
		}
	}
	return MobiBundle{Sandbox: filepath.Join(canonical, "bwrap"), SandboxSHA256: manifest.SandboxSHA256, Worker: filepath.Join(canonical, "worker"), WorkerSHA256: manifest.WorkerSHA256, Scratch: scratch}, nil
}

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

// PDF raster bundle custody mirrors the MOBI bundle: one private absolute
// directory, an out-of-band manifest digest, verified worker/sandbox bytes and
// a private scratch root. It never searches PATH, downloads or installs a tool.
const (
	pdfRasterTarget  = "x86_64-linux-gnu"
	pdfRasterPin     = "pdfium:7b3f6f93625e6df97a7d9e677137a558df8648c4"
	pdfRasterOptions = "portico-pdf-page-v1;bgrx;no-js;no-xfa;no-actions;no-network"
)

type pdfRasterManifest struct {
	Schema        int      `json:"schema"`
	Target        string   `json:"target"`
	Renderer      string   `json:"renderer"`
	Options       string   `json:"options"`
	WorkerSHA256  string   `json:"workerSHA256"`
	SandboxSHA256 string   `json:"sandboxSHA256"`
	Licenses      []string `json:"licenses"`
}

// PDFRasterBundle is the exact admitted renderer artifact. Paths are served
// from the private bundle, never from a lookup or a caller.
type PDFRasterBundle struct {
	Sandbox, SandboxSHA256 string
	Worker, WorkerSHA256   string
	Scratch                string
}

// LoadPDFRasterBundle resolves only an explicit Release-provided bundle and
// exact manifest digest. A missing, changed, wrong-pin or wrong-target bundle
// is unavailable, leaving the capability off.
func LoadPDFRasterBundle(directory, manifestSHA256, scratch string) (PDFRasterBundle, error) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" || !filepath.IsAbs(directory) || !filepath.IsAbs(scratch) {
		return PDFRasterBundle{}, ErrUnavailable
	}
	canonical, err := filepath.EvalSymlinks(directory)
	if err != nil || canonical != filepath.Clean(directory) {
		return PDFRasterBundle{}, ErrUnavailable
	}
	root, err := os.Lstat(canonical)
	if err != nil || !root.IsDir() || root.Mode().Perm()&0022 != 0 {
		return PDFRasterBundle{}, ErrUnavailable
	}
	name := filepath.Join(canonical, "manifest.json")
	before, err := os.Lstat(name)
	if err != nil || !before.Mode().IsRegular() || before.Size() <= 0 || before.Size() > 8192 || before.Mode().Perm()&0022 != 0 {
		return PDFRasterBundle{}, ErrUnavailable
	}
	file, err := os.Open(name)
	if err != nil {
		return PDFRasterBundle{}, err
	}
	defer file.Close()
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) {
		return PDFRasterBundle{}, ErrChanged
	}
	raw, err := io.ReadAll(io.LimitReader(file, 8193))
	if err != nil || len(raw) > 8192 {
		return PDFRasterBundle{}, ErrUnavailable
	}
	digest := sha256.Sum256(raw)
	if len(manifestSHA256) != 64 || hex.EncodeToString(digest[:]) != manifestSHA256 {
		return PDFRasterBundle{}, ErrUnavailable
	}
	var manifest pdfRasterManifest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&manifest) != nil || decoder.Decode(new(any)) != io.EOF {
		return PDFRasterBundle{}, ErrUnavailable
	}
	if manifest.Schema != 1 || manifest.Target != pdfRasterTarget || manifest.Renderer != pdfRasterPin || manifest.Options != pdfRasterOptions {
		return PDFRasterBundle{}, ErrUnavailable
	}
	for _, hash := range []string{manifest.WorkerSHA256, manifest.SandboxSHA256} {
		decoded, err := hex.DecodeString(hash)
		if err != nil || len(decoded) != 32 || hex.EncodeToString(decoded) != hash {
			return PDFRasterBundle{}, ErrUnavailable
		}
	}
	if len(manifest.Licenses) == 0 || len(manifest.Licenses) > 16 {
		return PDFRasterBundle{}, ErrUnavailable
	}
	for _, license := range manifest.Licenses {
		if !filepath.IsLocal(license) || filepath.Clean(license) != license || license == "." {
			return PDFRasterBundle{}, ErrUnavailable
		}
		info, err := os.Lstat(filepath.Join(canonical, license))
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > 1<<20 || info.Mode().Perm()&0022 != 0 {
			return PDFRasterBundle{}, ErrUnavailable
		}
	}
	return PDFRasterBundle{
		Sandbox:       filepath.Join(canonical, "bwrap"),
		SandboxSHA256: manifest.SandboxSHA256,
		Worker:        filepath.Join(canonical, "worker"),
		WorkerSHA256:  manifest.WorkerSHA256,
		Scratch:       scratch,
	}, nil
}

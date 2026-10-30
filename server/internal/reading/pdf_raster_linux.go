//go:build linux

package reading

import (
	"context"
	"crypto/sha256"
	"debug/elf"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"image"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

const (
	maxPDFRasterSource = 128 << 20
	maxPDFRasterOutput = 32 + 64<<20
	maxPDFRasterWidth  = 2048
	maxPDFRasterHeight = 8192
	maxPDFRasterPixels = 8388608
	maxPDFRasterPNG    = 32 << 20
	pdfRasterBudget    = 90 * time.Second
)

// PDFRaster runs one page per bounded helper process through the admitted
// PDFium bundle. It never renders in the server process, never searches PATH
// and never substitutes an image when rendering fails.
type PDFRaster struct {
	bundle     PDFRasterBundle
	supervisor *storage.Supervisor
	slot       chan struct{}
}

func NewPDFRaster(bundle PDFRasterBundle, supervisor *storage.Supervisor) (*PDFRaster, error) {
	if runtime.GOARCH != "amd64" || supervisor == nil || !filepath.IsAbs(bundle.Scratch) {
		return nil, ErrUnavailable
	}
	info, err := os.Lstat(bundle.Scratch)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
		return nil, ErrUnavailable
	}
	for _, entry := range []struct{ path, digest string }{{bundle.Sandbox, bundle.SandboxSHA256}, {bundle.Worker, bundle.WorkerSHA256}} {
		if !filepath.IsAbs(entry.path) || len(entry.digest) != 64 {
			return nil, ErrUnavailable
		}
		static := entry.path == bundle.Worker
		file, err := verifiedPDFRasterExecutable(entry.path, entry.digest, static)
		if err != nil {
			return nil, err
		}
		_ = file.Close()
	}
	return &PDFRaster{bundle: bundle, supervisor: supervisor, slot: make(chan struct{}, 1)}, nil
}

// verifiedPDFRasterExecutable is the bundle verifier for the worker and its
// sandbox. It is stricter about bytes than a page and looser than the MOBI
// worker's 32 MiB static binary cap, which a PDFium link can exceed.
func verifiedPDFRasterExecutable(path, digest string, static bool) (*os.File, error) {
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Size() <= 0 || before.Size() > 128<<20 ||
		before.Mode().Perm()&0022 != 0 || before.Mode().Perm()&0111 == 0 {
		return nil, ErrUnavailable
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	good := false
	defer func() {
		if !good {
			file.Close()
		}
	}()
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) {
		return nil, ErrChanged
	}
	hash := sha256.New()
	if _, err = io.Copy(hash, io.LimitReader(file, 128<<20+1)); err != nil {
		return nil, err
	}
	if hex.EncodeToString(hash.Sum(nil)) != digest {
		return nil, ErrUnavailable
	}
	executable, err := elf.NewFile(file)
	if err != nil || executable.Class != elf.ELFCLASS64 || executable.Machine != elf.EM_X86_64 {
		return nil, ErrUnavailable
	}
	if static {
		// bwrap has no loader or dependency closure for the worker. A dynamic
		// artifact is not usable capability.
		if executable.Type != elf.ET_EXEC {
			return nil, ErrUnavailable
		}
		needed, err := executable.DynString(elf.DT_NEEDED)
		if err != nil || len(needed) != 0 {
			return nil, ErrUnavailable
		}
		for _, program := range executable.Progs {
			if program.Type == elf.PT_INTERP {
				return nil, ErrUnavailable
			}
		}
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	good = true
	return file, nil
}

// RenderCoverPNG renders the first page at the notes' cover width. A failure
// returns no image; the caller keeps the client's generated cover.
func (r *PDFRaster) RenderCoverPNG(ctx context.Context, source archiveStagingSource, size int64) ([]byte, error) {
	runCtx, cancel := context.WithTimeout(ctx, pdfRasterBudget)
	defer cancel()
	page, err := r.renderPage(runCtx, source, size, 0, 600)
	if err != nil {
		return nil, err
	}
	out := writeBuffer{limit: maxPDFRasterPNG}
	if err = png.Encode(&out, page); err != nil {
		return nil, err
	}
	// Encoding is additional CPU work; re-prove the source with the same child
	// context instead of only checking the deadline.
	if err = source.Recheck(runCtx); err != nil {
		return nil, err
	}
	if err = runCtx.Err(); err != nil {
		return nil, err
	}
	return out.data, nil
}

type writeBuffer struct {
	data  []byte
	limit int
}

func (w *writeBuffer) Write(p []byte) (int, error) {
	if w.limit > 0 && len(w.data)+len(p) > w.limit {
		return 0, ErrLimit
	}
	w.data = append(w.data, p...)
	return len(p), nil
}

// RenderPage renders one page from an already admitted local source. The source
// is staged only through its own local read/recheck seam, and it is rechecked
// after the child retires.
func (r *PDFRaster) RenderPage(ctx context.Context, source archiveStagingSource, size int64, page, width int) (*image.RGBA, error) {
	runCtx, cancel := context.WithTimeout(ctx, pdfRasterBudget)
	defer cancel()
	return r.renderPage(runCtx, source, size, page, width)
}

func (r *PDFRaster) renderPage(ctx context.Context, source archiveStagingSource, size int64, page, width int) (*image.RGBA, error) {
	if r == nil || source == nil || !source.LocalArchiveStaging() || size <= 0 || size > maxPDFRasterSource ||
		page < 0 || page > 1000000 || width < 1 || width > maxPDFRasterWidth {
		return nil, ErrInvalid
	}
	select {
	case r.slot <- struct{}{}:
		defer func() { <-r.slot }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	staged, err := stagePDFSource(ctx, source, size, r.bundle.Scratch)
	if err != nil {
		return nil, err
	}
	defer os.Remove(staged)
	input, err := os.Open(staged)
	if err != nil {
		return nil, ErrLimit
	}
	defer input.Close()
	sandbox, err := verifiedPDFRasterExecutable(r.bundle.Sandbox, r.bundle.SandboxSHA256, false)
	if err != nil {
		return nil, err
	}
	defer sandbox.Close()
	worker, err := verifiedPDFRasterExecutable(r.bundle.Worker, r.bundle.WorkerSHA256, true)
	if err != nil {
		return nil, err
	}
	defer worker.Close()
	args := []string{
		"--unshare-all", "--die-with-parent", "--new-session", "--disable-userns",
		"--assert-userns-disabled", "--clearenv", "--cap-drop", "ALL",
		// Empty guest root: no host /, /usr, loader or dependency tree is bound.
		// The static worker is the only executable visible inside the sandbox.
		"--tmpfs", "/", "--dev", "/dev",
		"--ro-bind", "/proc/self/fd/4", "/worker",
		"--chdir", "/", "/worker", strconv.Itoa(page), strconv.Itoa(width),
	}
	command := exec.CommandContext(ctx, "/proc/self/fd/3", args...)
	command.ExtraFiles = []*os.File{sandbox, worker}
	command.Stdin = input
	command.Env = []string{}
	var raw []byte
	retired := make(chan struct{})
	err = r.supervisor.RunOwnedCompletion(ctx, "reading-pdf-raster", command, func(reader io.Reader) error {
		var readErr error
		raw, readErr = io.ReadAll(io.LimitReader(reader, maxPDFRasterOutput+1))
		if len(raw) > maxPDFRasterOutput {
			return ErrLimit
		}
		return readErr
	}, nil, func() { close(retired) })
	<-retired
	if err != nil {
		return nil, err
	}
	if err = source.Recheck(ctx); err != nil {
		return nil, err
	}
	frame, err := decodePDFRasterFrame(raw)
	if err != nil {
		return nil, err
	}
	// Decoding is CPU work after the child retires; the same bounded child
	// context must prove the source is still current before the image leaves.
	if err = source.Recheck(ctx); err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	return frame, nil
}

// stagePDFSource copies the leased local bytes through the source's own
// context-aware extent seam into private scratch; a short or cancelled copy is
// a real error, never a partial document.
func stagePDFSource(ctx context.Context, reader archiveStagingSource, size int64, scratch string) (string, error) {
	file, err := os.CreateTemp(scratch, "document-*.pdf")
	if err != nil {
		return "", ErrLimit
	}
	path := file.Name()
	const chunk = 1 << 20
	buffer := make([]byte, chunk)
	written := int64(0)
	for written < size {
		if err := ctx.Err(); err != nil {
			_ = file.Close()
			_ = os.Remove(path)
			return "", err
		}
		want := int64(chunk)
		if size-written < want {
			want = size - written
		}
		read, readErr := reader.ReadAtContext(ctx, buffer[:want], written)
		if read > 0 {
			if _, writeErr := file.Write(buffer[:read]); writeErr != nil {
				_ = file.Close()
				_ = os.Remove(path)
				return "", ErrLimit
			}
			written += int64(read)
		}
		if readErr != nil {
			_ = file.Close()
			_ = os.Remove(path)
			if readErr == io.EOF {
				return "", ErrInvalid
			}
			return "", readErr
		}
		if read == 0 {
			_ = file.Close()
			_ = os.Remove(path)
			return "", ErrInvalid
		}
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return "", ErrLimit
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", ErrLimit
	}
	if err := ctx.Err(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

// decodePDFRasterFrame checks every dimension, stride, payload and allocation
// bound before touching image memory. The worker's header is not trusted.
func decodePDFRasterFrame(raw []byte) (*image.RGBA, error) {
	if len(raw) < 32 || string(raw[:4]) != "PDR1" {
		return nil, ErrInvalid
	}
	width := uint64(binary.LittleEndian.Uint32(raw[4:8]))
	height := uint64(binary.LittleEndian.Uint32(raw[8:12]))
	stride := uint64(binary.LittleEndian.Uint32(raw[12:16]))
	format := binary.LittleEndian.Uint32(raw[16:20])
	payload := binary.LittleEndian.Uint64(raw[20:28])
	if width < 1 || width > maxPDFRasterWidth || height < 1 || height > maxPDFRasterHeight ||
		format != 3 || stride != width*4 || payload != stride*height ||
		width*height > maxPDFRasterPixels || payload > maxPDFRasterOutput-32 ||
		uint64(len(raw)) != 32+payload {
		return nil, ErrInvalid
	}
	pixels := raw[32:]
	out := image.NewRGBA(image.Rect(0, 0, int(width), int(height)))
	for y := uint64(0); y < height; y++ {
		row := pixels[y*stride:]
		dst := out.Pix[int(y)*out.Stride:]
		for x := uint64(0); x < width; x++ {
			src := row[x*4:]
			at := x * 4
			dst[at+0] = src[2]
			dst[at+1] = src[1]
			dst[at+2] = src[0]
			dst[at+3] = 0xff
		}
	}
	return out, nil
}

func (r *PDFRaster) String() string {
	if r == nil {
		return "pdf-raster: unavailable"
	}
	return fmt.Sprintf("pdf-raster: %s", pdfRasterPin)
}

package reading

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/childprocess"
)

// PDF text coverage runs in a bounded helper process, never in the server's own
// address space: a malformed or malicious document can consume only the child's
// hard memory/CPU budget, and an OOM kills the child instead of the server. The
// parent copies the admitted source into a private temporary file, runs the
// pinned parser in that child, waits for it to retire, and removes the file
// before releasing the source custody. The child receives exactly one fixed
// argument vector; there is no shell, no network, no PDF action and no external
// resource path.
const (
	maxCountPDFSource = 128 << 20
	maxCountPDFPages  = 3000
	maxCountPDFText   = 64 << 20
	maxCountPDFRun    = 4 << 20
	maxPDFResultBytes = 64 << 10
	PDFTextHelperArg  = "pdf-text-extract"
)

const (
	pdfHelperOK      = 0
	pdfHelperUnknown = 3
	pdfHelperRetry   = 4
)

// pdfTextHelperExecutable is the server binary; a package test may replace it
// with a real helper invocation instead of the test binary.
var pdfTextHelperExecutable = os.Executable

type pdfTextResult struct {
	Count   *int64 `json:"count,omitempty"`
	Opening string `json:"opening,omitempty"`
	Pages   *int64 `json:"pages,omitempty"`
	Author  string `json:"author,omitempty"`
}

// countPDFText returns the complete text count (nil when unknown), the exact
// structural page count and the exact Info/Author string from the same bounded
// child. A nil count is unknown: an empty/image-only document, encryption,
// unsupported filters/fonts, malformed or over-budget structure never yields
// zero or a partial count. A structurally complete page tree is a real fact even
// when the text is image-only, and a blank page counts as a page. The page count
// itself is never counted as text, and the author is only the document's own
// accepted string: no title, outline, XMP or filename substitute.
func countPDFText(ctx context.Context, r io.ReaderAt, size int64) (*int64, string, string, *int64, error) {
	if size <= 0 || size > maxCountPDFSource {
		return nil, "", "", nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, "", "", nil, err
	}
	tempDir := filepath.Clean(os.TempDir())
	file, err := os.CreateTemp(tempDir, "portico-pdftext-*")
	if err != nil {
		return nil, "", "", nil, err
	}
	name := file.Name()
	defer os.Remove(name)
	if err = copyPDFSource(ctx, r, size, file); err != nil {
		file.Close()
		if errors.Is(err, ErrInvalid) {
			return nil, "", "", nil, nil
		}
		return nil, "", "", nil, err
	}
	if err = file.Close(); err != nil {
		return nil, "", "", nil, err
	}
	executable, err := pdfTextHelperExecutable()
	if err != nil {
		return nil, "", "", nil, err
	}
	// The helper's argument vector is ours alone: the executable, the fixed
	// subcommand and the private temporary path. No source-controlled text is
	// ever an argument.
	command := exec.CommandContext(ctx, executable, PDFTextHelperArg, name)
	command.Env = []string{"TMPDIR=" + tempDir}
	output := &boundedBuffer{max: maxPDFResultBytes}
	command.Stdout = output
	command.Stderr = io.Discard
	childprocess.Configure(command)
	runErr := command.Run()
	if ctx.Err() != nil {
		return nil, "", "", nil, ctx.Err()
	}
	if runErr != nil {
		var exit *exec.ExitError
		if errors.As(runErr, &exit) && exit.ExitCode() == pdfHelperUnknown {
			return nil, "", "", nil, nil
		}
		// A signal, OOM kill, CPU limit or helper crash is a parser-process
		// failure, not a determination: retry it rather than caching a fact.
		return nil, "", "", nil, fmt.Errorf("pdf text extractor: %w", runErr)
	}
	var result pdfTextResult
	if err = json.Unmarshal(output.Bytes(), &result); err != nil {
		return nil, "", "", nil, fmt.Errorf("pdf text extractor result: %w", err)
	}
	if result.Pages == nil || *result.Pages < 1 || *result.Pages > 4096 || len(result.Opening) > 4096 || len(result.Author) > 4096 ||
		(result.Count != nil && *result.Count <= 0) || (result.Count == nil && result.Opening != "") {
		return nil, "", "", nil, fmt.Errorf("pdf text extractor result")
	}
	// Keep only the exact storable creator; an invalid value publishes nothing.
	author := acceptedEmbeddedAuthor(result.Author)
	return result.Count, result.Opening, author, result.Pages, nil
}

// copyPDFSource snapshots the admitted source through its own ReaderAt, so every
// extent keeps the source's context and currentness checks. A short read is an
// incomplete acquisition, never a shorter document.
func copyPDFSource(ctx context.Context, r io.ReaderAt, size int64, w io.Writer) error {
	buffer := make([]byte, 256<<10)
	for offset := int64(0); offset < size; {
		if err := ctx.Err(); err != nil {
			return err
		}
		want := int64(len(buffer))
		if offset+want > size {
			want = size - offset
		}
		n, err := r.ReadAt(buffer[:want], offset)
		if n > 0 {
			if _, writeErr := w.Write(buffer[:n]); writeErr != nil {
				return writeErr
			}
			offset += int64(n)
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		if n == 0 {
			return io.ErrUnexpectedEOF
		}
	}
	return nil
}

// boundedBuffer refuses to retain more than max output bytes; a runaway helper
// cannot grow the server's memory through its stdout.
type boundedBuffer struct {
	max int
	buf []byte
}

func (b *boundedBuffer) Write(value []byte) (int, error) {
	if len(b.buf)+len(value) > b.max {
		return 0, fmt.Errorf("pdf text extractor output exceeds bound")
	}
	b.buf = append(b.buf, value...)
	return len(value), nil
}

func (b *boundedBuffer) Bytes() []byte { return b.buf }

// RunPDFTextHelper is the child entry point. It never starts the server. The
// path must be the parent's private temporary snapshot.
func RunPDFTextHelper(path string) int {
	// Only the process the parent actually started with the fixed helper argv
	// may apply hard limits and parse; an accidental in-process call must never
	// restrict the server.
	if len(os.Args) != 3 || os.Args[1] != PDFTextHelperArg {
		return pdfHelperUnknown
	}
	if !filepath.IsAbs(path) || filepath.Dir(path) != filepath.Clean(os.TempDir()) || !strings.HasPrefix(filepath.Base(path), "portico-pdftext-") {
		return pdfHelperUnknown
	}
	if err := limitPDFProcess(); err != nil {
		return pdfHelperUnknown
	}
	file, err := os.Open(path)
	if err != nil {
		return pdfHelperRetry
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxCountPDFSource {
		return pdfHelperUnknown
	}
	count, opening, author, pages, err := parsePDFText(context.Background(), file, info.Size())
	if err != nil {
		return pdfHelperRetry
	}
	if pages == nil {
		return pdfHelperUnknown
	}
	if err = json.NewEncoder(os.Stdout).Encode(pdfTextResult{Count: count, Opening: opening, Pages: pages, Author: author}); err != nil {
		return pdfHelperRetry
	}
	return pdfHelperOK
}

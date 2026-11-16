package reading

import (
	"bytes"
	"context"
	"image/jpeg"
	"io"
	"runtime/debug"
	"sync"
	"time"

	"github.com/klippa-app/go-pdfium"
	"github.com/klippa-app/go-pdfium/requests"
	"github.com/klippa-app/go-pdfium/webassembly"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/experimental"
)

// A PDF's cover is its first page, drawn (area 4 rules 39 and 40), as Kavita and Calibre do it:
// a scan, a magazine cover, a vector title page and a page of text all get one. The page is
// drawn by PDFium compiled to WebAssembly and run inside this server by wazero: no outside
// tool and no cgo. An untrusted file is drawn in WebAssembly's sandbox, with its memory capped
// and a deadline that stops it, so a hostile or broken PDF only fails, and keeps the cover the
// app draws (rule 45).
const (
	pdfCoverWidth, pdfCoverHeight = 1000, 1500
	pdfDeadline                   = 30 * time.Second
	pdfMemoryPages                = 4096 // 64 KiB each: 256 MiB for PDFium's memory
	pdfIdle                       = 5 * time.Minute
)

// The PDFium module is compiled the first time a cover is wanted (a few seconds, and a few
// hundred megabytes for that moment), kept compiled in the cache folder for later starts, and
// let go of when no cover has been drawn for a while.
var pdfRenderer struct {
	sync.Mutex
	pool  pdfium.Pool
	idle  *time.Timer
	cache string
}

// UsePDFCache sets the folder the compiled PDFium module is kept in, so that a later start
// does not compile it again. Without one it is compiled each time the server first needs it.
func UsePDFCache(dir string) {
	pdfRenderer.Lock()
	pdfRenderer.cache = dir
	pdfRenderer.Unlock()
}

func pdfPool() (pdfium.Pool, error) {
	pdfRenderer.Lock()
	defer pdfRenderer.Unlock()
	if pdfRenderer.pool == nil {
		config := wazero.NewRuntimeConfig().WithCoreFeatures(api.CoreFeaturesV2 | experimental.CoreFeaturesExceptionHandling).
			WithMemoryLimitPages(pdfMemoryPages).WithCloseOnContextDone(true)
		if pdfRenderer.cache != "" {
			if cache, err := wazero.NewCompilationCacheWithDir(pdfRenderer.cache); err == nil {
				config = config.WithCompilationCache(cache)
			}
		}
		pool, err := webassembly.Init(webassembly.Config{MinIdle: 1, MaxIdle: 1, MaxTotal: 1, RuntimeConfig: config})
		if err != nil {
			return nil, err
		}
		pdfRenderer.pool = pool
		// Compiling took far more memory than drawing needs: give it back now.
		debug.FreeOSMemory()
	}
	if pdfRenderer.idle != nil {
		pdfRenderer.idle.Stop()
	}
	pdfRenderer.idle = time.AfterFunc(pdfIdle, func() {
		pdfRenderer.Lock()
		defer pdfRenderer.Unlock()
		if pdfRenderer.pool != nil {
			pdfRenderer.pool.Close()
			pdfRenderer.pool = nil
		}
	})
	return pdfRenderer.pool, nil
}

// pdfCover draws a PDF's first page as a JPEG of at most limit bytes. A file PDFium cannot
// open or draw, or one that takes longer than the deadline, has no cover.
func pdfCover(r io.ReaderAt, size, limit int64) ([]byte, error) {
	pool, err := pdfPool()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), pdfDeadline)
	defer cancel()
	instance, err := pool.GetInstanceWithContext(ctx)
	if err != nil {
		return nil, err
	}
	type drawn struct {
		cover []byte
		err   error
	}
	done := make(chan drawn, 1)
	go func() {
		cover, err := firstPage(instance, r, size, limit)
		done <- drawn{cover, err}
	}()
	select {
	case d := <-done:
		instance.Close()
		return d.cover, d.err
	case <-ctx.Done():
		// Stops the drawing where it is, and throws that PDFium away.
		instance.Kill()
		return nil, errDamaged
	}
}

func firstPage(instance pdfium.Pdfium, r io.ReaderAt, size, limit int64) ([]byte, error) {
	doc, err := instance.OpenDocument(&requests.OpenDocument{FileReader: io.NewSectionReader(r, 0, size), FileReaderSize: size})
	if err != nil {
		return nil, errDamaged
	}
	defer instance.FPDF_CloseDocument(&requests.FPDF_CloseDocument{Document: doc.Document})
	page, err := instance.RenderPageInPixels(&requests.RenderPageInPixels{Width: pdfCoverWidth, Height: pdfCoverHeight,
		Page: requests.Page{ByIndex: &requests.PageByIndex{Document: doc.Document, Index: 0}}})
	if err != nil {
		return nil, errDamaged
	}
	defer page.Cleanup()
	var out bytes.Buffer
	if err := jpeg.Encode(&out, page.Result.Image, &jpeg.Options{Quality: 85}); err != nil || int64(out.Len()) > limit {
		return nil, nil
	}
	return out.Bytes(), nil
}

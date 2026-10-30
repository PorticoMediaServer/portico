package main

import (
	"os"
	"path/filepath"

	"github.com/PorticoMediaServer/portico/server/internal/reading"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// Explicit Release/operator delivery only, mirroring the MOBI bundle. No PATH
// tool, download, install or runtime probe discovers a PDF renderer.
func configuredReadingPDFRaster(store *storage.Client, cache string) (*reading.PDFRaster, error) {
	directory := os.Getenv("PORTICO_READING_PDF_RASTER_BUNDLE")
	digest := os.Getenv("PORTICO_READING_PDF_RASTER_MANIFEST_SHA256")
	if directory == "" && digest == "" {
		return nil, nil
	}
	if directory == "" || digest == "" || store == nil || store.Supervisor == nil {
		return nil, reading.ErrUnavailable
	}
	scratch := filepath.Join(cache, "reading-pdf-raster")
	if err := os.MkdirAll(scratch, 0700); err != nil {
		return nil, err
	}
	bundle, err := reading.LoadPDFRasterBundle(directory, digest, scratch)
	if err != nil {
		return nil, err
	}
	return reading.NewPDFRaster(bundle, store.Supervisor)
}

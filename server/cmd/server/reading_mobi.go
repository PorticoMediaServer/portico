package main

import (
	"os"
	"path/filepath"

	"github.com/PorticoMediaServer/portico/server/internal/reading"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// Explicit operator/Release delivery only. No PATH tool, download, install,
// worker probe or runtime command is used to discover a converter at startup.
func configuredReadingMobi(store *storage.Client, cache string) (*reading.MobiConverter, error) {
	directory := os.Getenv("PORTICO_READING_MOBI_BUNDLE")
	digest := os.Getenv("PORTICO_READING_MOBI_MANIFEST_SHA256")
	if directory == "" && digest == "" {
		return nil, nil
	}
	if directory == "" || digest == "" || store == nil || store.Supervisor == nil {
		return nil, reading.ErrUnavailable
	}
	scratch := filepath.Join(cache, "reading-mobi")
	if err := os.MkdirAll(scratch, 0700); err != nil {
		return nil, err
	}
	bundle, err := reading.LoadMobiBundle(directory, digest, scratch)
	if err != nil {
		return nil, err
	}
	return reading.NewMobiConverter(bundle, store.Supervisor)
}

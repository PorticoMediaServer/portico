package main

import (
	"context"
	"log"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/ingestion"
	"github.com/PorticoMediaServer/portico/server/internal/remotesources"
)

// runSourceAdvancedScans applies the scans a remote source's own page asks for,
// through the ordinary ingestion queue: one pass when the server starts, a pass
// every five minutes while it watches for changes, and a full re-check once its
// interval has passed. Queueing is the same deduplicated path an owner's Scan
// now uses, so nothing here invents a second scanner.
func runSourceAdvancedScans(ctx context.Context, scanner *ingestion.Service, sources *remotesources.Service) {
	if tasks, err := sources.StartupSourceScans(ctx); err != nil {
		log.Printf("remote source start scans: %v", err)
	} else {
		enqueueSourceScans(ctx, scanner, sources, tasks)
	}
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			tasks, err := sources.DueSourceScans(ctx)
			if err != nil {
				log.Printf("remote source scans: %v", err)
				continue
			}
			enqueueSourceScans(ctx, scanner, sources, tasks)
		}
	}
}

func enqueueSourceScans(ctx context.Context, scanner *ingestion.Service, sources *remotesources.Service, tasks []remotesources.SourceScanTask) {
	if len(tasks) == 0 {
		return
	}
	applied := make([]remotesources.SourceScanTask, 0, len(tasks))
	for _, task := range tasks {
		if ctx.Err() != nil {
			return
		}
		if _, err := scanner.QueueMode(ctx, task.LibraryID, task.FolderID, task.Mode, nil); err != nil {
			continue
		}
		applied = append(applied, task)
	}
	if err := sources.MarkSourceScans(ctx, applied); err != nil {
		log.Printf("remote source scans: %v", err)
	}
}

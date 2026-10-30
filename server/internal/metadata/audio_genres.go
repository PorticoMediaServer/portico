package metadata

// The local audio genre catch-up projection. New and changed files publish
// their effective local genres inside the scanner's own commit (catalog's
// commitAudio path), but a catalog that predates that projection has its
// selected evidence already stored and its files unchanged — an unchanged
// rescan never re-enters the analysis commit. This step is the bounded
// existing-library path: it reprojects items of audio libraries whose metadata
// policy revision moved (or which have never been projected), in durable
// keyset batches, through the exact same shared projection the scanner uses.
//
// An idle server does nothing here at all: the due query is one indexed read
// and the state row records the policy revision it was completed under, so a
// rescan of unchanged files neither writes nor wakes. A policy change (off,
// supplement, prefer, language) resets the state, and the pass re-projects
// under the new policy — including the withdrawal and republish around
// local_mode flips. No raw evidence is read: the input is the selected tag
// projection the scanner maintains.

import (
	"context"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

// AudioGenreStep projects at most one batch per call and leaves the rest to
// the durable cursor, yielding between batches like every other background
// publication lane.
func (s *Service) AudioGenreStep(ctx context.Context) error {
	due, err := compactcatalog.AudioLocalGenreProjectionDue(ctx, s.db)
	if err != nil {
		return err
	}
	if len(due) == 0 {
		return nil
	}
	_, err = compactcatalog.ProjectAudioLocalGenresBatch(ctx, s.db, due[0])
	return err
}

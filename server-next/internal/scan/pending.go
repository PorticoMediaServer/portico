package scan

import (
	"context"
	"errors"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// ScanAddedFolders starts the required scan after a saved folder addition. If a narrower
// scan is active, or a running scan already took its folder list, the addition stays pending.
func (m *Manager) ScanAddedFolders(ctx context.Context, library string, ownerID int64) error {
	job, err := m.Scan(ctx, library, ownerID, false)
	if errors.Is(err, ErrScanBusy) || err == nil && job.State == db.JobRunning {
		m.pend(library, false)
		return nil
	}
	return err
}

func (m *Manager) pend(library string, scheduled bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if previous, exists := m.pending[library]; exists {
		scheduled = scheduled && previous // a folder addition must run even when its schedule is Never
	}
	m.pending[library] = scheduled
}

func (m *Manager) startPending(ctx context.Context) {
	m.mu.Lock()
	pending := m.pending
	m.pending = map[string]bool{}
	m.mu.Unlock()
	for library, scheduled := range pending {
		if scheduled {
			if !m.scheduled(ctx, library) {
				m.pend(library, true)
			}
		} else if err := m.ScanAddedFolders(ctx, library, 0); err != nil && !errors.Is(err, db.ErrNotFound) {
			m.scanner.log().Warn("an added folder could not be scanned yet", "library", library, "error", err)
			m.pend(library, false)
		}
	}
}

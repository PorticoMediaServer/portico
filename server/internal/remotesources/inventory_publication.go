package remotesources

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"

	"github.com/PorticoMediaServer/portico/server/internal/rcloneprogram"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

type serveListingReceipt struct {
	Epoch   string
	Profile SourceReadProfile
}

// currentServeProcessTx closes source-wide revocation against the publication
// snapshot without network, nested transactions, capacity waits or defaults.
func (s *Service) currentServeProcessTx(ctx context.Context, tx *sql.Tx, source string, profile SourceReadProfile, epoch string) (*rcloneprogram.ServeProcess, error) {
	if tx == nil || epoch == "" {
		return nil, storage.ErrRemoteChanged
	}
	s.serveMu.Lock()
	slot := s.serveLeases[source]
	var process *rcloneprogram.ServeProcess
	if slot != nil {
		process = slot.process
	}
	s.serveMu.Unlock()
	if slot == nil || process == nil || process.Generation() != epoch || slot.profile != profile || slot.CheckLocal() != nil || process.CheckLocal(ctx) != nil {
		return nil, storage.ErrRemoteChanged
	}
	network, err := s.sourceNetworkTx(ctx, tx, source, slot.root)
	if err != nil {
		return nil, err
	}
	sequential, err := sourceSequentialTx(ctx, tx, source, slot.root)
	if err != nil {
		return nil, err
	}
	if network.Fingerprint != slot.networkFingerprint || sequential != slot.sequential {
		return nil, storage.ErrRemoteChanged
	}
	return process, nil
}

// commitListingObservationsTx compares the exact private page, rather than
// accepting caller-supplied file facts, then publishes normalized observations
// in the catalogue's transaction. It never contacts rclone inside that writer.
func (s *Service) commitListingObservationsTx(ctx context.Context, tx *sql.Tx, job string, page storage.RemotePage, replay bool) error {
	if page.Limit < 1 || page.Limit > 128 {
		return storage.ErrRemoteCursor
	}
	var receipt serveListingReceipt
	var rawReceipt []byte
	if err := tx.QueryRowContext(ctx, `SELECT token FROM remote_listing_progress WHERE listing_id=? AND mode='serve' AND steps=1`, page.ID).Scan(&rawReceipt); err != nil {
		return storage.ErrRemoteCursor
	}
	if len(rawReceipt) > 4096 || json.Unmarshal(rawReceipt, &receipt) != nil || receipt.Profile.SourceID != page.SourceID {
		return storage.ErrRemoteConfig
	}
	profile, err := SourceReadProfileTx(ctx, tx, page.SourceID)
	if err != nil {
		return err
	}
	if profile != receipt.Profile {
		return storage.ErrRemoteChanged
	}
	if _, err = s.currentServeProcessTx(ctx, tx, page.SourceID, profile, receipt.Epoch); err != nil {
		return err
	}
	after := ""
	if page.Cursor != "" {
		c, err := parseCursor(page.Cursor)
		if err != nil || c.ID != page.ID {
			return storage.ErrRemoteCursor
		}
		after = c.After
	}
	rows, err := tx.QueryContext(ctx, `SELECT snapshot FROM remote_listing_entries WHERE listing_id=? AND name>? ORDER BY name LIMIT ?`, page.ID, after, page.Limit+1)
	if err != nil {
		return err
	}
	values := make([]serveListingEntry, 0, page.Limit+1)
	for rows.Next() {
		var raw string
		var value serveListingEntry
		if err = rows.Scan(&raw); err == nil {
			err = json.Unmarshal([]byte(raw), &value)
		}
		if err != nil {
			break
		}
		values = append(values, value)
	}
	rowErr := rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if rowErr != nil {
		return rowErr
	}
	complete := len(values) <= page.Limit
	if !complete {
		values = values[:page.Limit]
	}
	if page.Complete != complete || len(values) != len(page.Entries) {
		return storage.ErrRemoteCursor
	}
	last := after
	if len(values) > 0 {
		last = values[len(values)-1].Path
	}
	if page.Next != cursor(continuation{page.ID, last}) {
		return storage.ErrRemoteCursor
	}
	for i, value := range values {
		if value.Snapshot != page.Entries[i] || filepath.Dir(value.Path) != page.Directory || value.Profile.SourceID != page.SourceID {
			return storage.ErrRemoteChanged
		}
		if receipt.Profile != value.Profile || receipt.Epoch != value.Epoch {
			return storage.ErrRemoteChanged
		}
		if value.Directory {
			if value.Observation != nil {
				return storage.ErrRemoteConfig
			}
			continue
		}
		if value.Observation == nil || value.Observation.ScanRevision != value.Revision {
			return storage.ErrRemoteConfig
		}
		if !replay {
			if _, err = s.RecordScanObservationTx(ctx, tx, job, value.Path, *value.Observation, value.Expected); err != nil {
				return err
			}
		}
	}
	return nil
}

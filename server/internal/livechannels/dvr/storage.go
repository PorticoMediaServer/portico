package dvr

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
	"time"
)

// forecastWindow is the one scope the storage status forecasts over. The
// reported hours are derived from it, so the fact cannot drift from the query.
const forecastWindow = 24 * time.Hour

var ErrStorageFloor = errors.New("recording storage free-space floor reached")
var ErrStorageCap = errors.New("recording storage cap reached")
var ErrStorageUnavailable = errors.New("recording storage is not writable")

// StorageDriver measures the private recording root and serializes reservations
// and writes. Values are local facts, never a replacement for runtime write checks.
type StorageDriver interface {
	Measure(context.Context) (StorageMeasurement, error)
	CheckFloor(context.Context) error
	SetPolicySource(func() StoragePolicy)
}
type StorageMeasurement struct {
	FreeBytes     int64  `json:"freeBytes"`
	UsedBytes     int64  `json:"usedBytes"`
	ReservedBytes int64  `json:"reservedBytes"`
	WriteHealthy  bool   `json:"writeHealthy"`
	MeasuredAt    string `json:"measuredAt"`
}
type StorageStatus struct {
	Policy      StoragePolicy      `json:"policy"`
	Measurement StorageMeasurement `json:"measurement"`
	// PendingDeleteBytes is the recorded size of rows queued for deletion. It is
	// a logical catalogue sum, not a measured physical reclaim count.
	PendingDeleteBytes  int64  `json:"pendingDeleteBytes"`
	ForecastBytes       int64  `json:"forecastBytes"`
	ForecastHours       int    `json:"forecastHours"`
	ForecastDescription string `json:"forecastDescription"`
	CaptureAvailable    bool   `json:"captureAvailable"`
	Warning             string `json:"warning"`
}
type Usage struct {
	Bytes              int64 `json:"bytes"`
	PendingDeleteBytes int64 `json:"pendingDeleteBytes"`
	Recordings         int   `json:"recordings"`
}

func (s *Store) StorageLimits() StoragePolicy {
	if p, ok := s.policy.Load().(StoragePolicy); ok {
		return p
	}
	return StoragePolicy{}
}

// cacheStoragePolicy publishes only newer committed revisions. A receipt replay
// returns its original response, but must never roll back the live write fence.
// A slow maintenance read must not overwrite a concurrently committed policy.
func (s *Store) cacheStoragePolicy(p StoragePolicy) {
	for {
		old := s.policy.Load()
		if current, ok := old.(StoragePolicy); ok && current.Revision >= p.Revision {
			return
		}
		if s.policy.CompareAndSwap(old, p) {
			return
		}
	}
}
func (s *Store) refreshStoragePolicy(ctx context.Context) error {
	p, e := ReadStoragePolicy(ctx, s.db)
	if e == nil {
		s.cacheStoragePolicy(p)
	}
	return e
}

// ReadStoragePolicy reads the recording storage policy. Capture cannot be
// configured without it, so a database that has lost the row is one the server
// does not start on.
func ReadStoragePolicy(ctx context.Context, db *sql.DB) (StoragePolicy, error) {
	var p StoragePolicy
	e := db.QueryRowContext(ctx, `SELECT revision,retention_days,episode_limit,floor_bytes,cap_bytes FROM dvr_storage_policy WHERE singleton=1`).Scan(&p.Revision, &p.RetentionDays, &p.EpisodeLimit, &p.FloorBytes, &p.CapBytes)
	return p, e
}

// Estimate uses the source's recent measured byte rate with 25% headroom. With
// no evidence it uses 20 Mb/s, and labels it an estimate, not a storage guarantee.
func estimateBytesTx(ctx context.Context, tx *sql.Tx, source string, seconds int64) (int64, error) {
	var rate float64
	e := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(bytes*1000.0/(captured_end_ms-captured_start_ms)),0) FROM (SELECT bytes,captured_start_ms,captured_end_ms FROM dvr_recordings WHERE source_id=? AND state IN('completed','incomplete-playable') AND captured_end_ms>captured_start_ms AND bytes>0 ORDER BY finished_ms DESC LIMIT 16)`, source).Scan(&rate)
	if e != nil {
		return 0, e
	}
	rate = min(rate*1.25, float64(1<<30))
	if rate <= 0 {
		// 20 Mb/s is the labelled fallback only when the source has no measured
		// completion. It is not a minimum for a source that already has evidence.
		rate = 2500000
	}
	return int64(rate * float64(max(int64(0), min(seconds, int64((1<<53-1)/(1<<30)))))), nil
}
func (s *Store) StorageStatus(ctx context.Context, a livechannels.Authority) (StorageStatus, error) {
	hours := int(forecastWindow / time.Hour)
	out := StorageStatus{ForecastHours: hours, ForecastDescription: fmt.Sprintf("Next %d hours. Recent measured source rate plus 25%% headroom, or 20 Mb/s without evidence. Forecasts are estimates; free-space floor and cap are checked on every write.", hours), CaptureAvailable: s.captureAvailable}
	p, e := s.GetStoragePolicy(ctx, a)
	if e != nil {
		return out, e
	}
	out.Policy = p
	measurement := StorageMeasurement{}
	if s.storage == nil {
		// Pending-delete and forecast bytes are recording-state facts, not
		// filesystem measurements. Missing storage does not make them zero.
		out.Warning = "storage-unavailable"
	} else if measurement, e = s.storage.Measure(ctx); e != nil {
		measurement = StorageMeasurement{}
		out.Warning = "storage-unavailable"
	} else {
		out.Measurement = measurement
	}
	gated, e := dbwork.BeginSnapshot(ctx, s.db)
	if e != nil {
		return out, e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	if a == nil {
		return out, ErrDenied
	}
	if f, _, e := a(ctx, tx, true); e != nil || f == "" {
		return out, ErrDenied
	}
	if e = tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(bytes),0) FROM dvr_recordings WHERE state='pending-delete'`).Scan(&out.PendingDeleteBytes); e != nil {
		return out, e
	}
	now := s.now()
	horizon := now.Add(forecastWindow)
	// Conservative row estimate: every queued row is counted, and an established
	// shared capture may appear on both its leader and follower. The state list
	// also omits a cancelled/pending-delete leader the worker still keeps capturing
	// for live followers, so this can overstate but is not a physical byte claim.
	rows, e := tx.QueryContext(ctx, `SELECT source_id,SUM(max(0,(min(end_ms,?)-max(start_ms,?))/1000)) FROM dvr_recordings WHERE state IN('scheduled','conflicted','waiting-source','waiting-guide','preparing','recording') AND start_ms<? AND end_ms>? GROUP BY source_id`, horizon.UnixMilli(), now.UnixMilli(), horizon.UnixMilli(), now.UnixMilli())
	if e != nil {
		return out, e
	}
	type forecast struct {
		source  string
		seconds int64
	}
	sources := []forecast{}
	for rows.Next() {
		var f forecast
		if e = rows.Scan(&f.source, &f.seconds); e != nil {
			rows.Close()
			return out, e
		}
		sources = append(sources, f)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	for _, f := range sources {
		b, e := estimateBytesTx(ctx, tx, f.source, f.seconds)
		if e != nil {
			return out, e
		}
		out.ForecastBytes = min(int64(1<<53-1), out.ForecastBytes+b)
	}
	if out.Warning == "" && (!measurement.WriteHealthy || measurement.FreeBytes < p.FloorBytes+out.ForecastBytes || (p.CapBytes > 0 && measurement.UsedBytes+out.ForecastBytes > p.CapBytes)) {
		out.Warning = "storage-pressure"
	}
	return out, nil
}
func usageTx(ctx context.Context, tx *sql.Tx, o livechannels.Owner) (Usage, error) {
	var u Usage
	// Per-owner usage is a logical catalogue sum: one row's recorded size, even
	// when the capture is hard-linked to another profile.
	e := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(bytes),0),COALESCE(SUM(CASE WHEN state='pending-delete' THEN bytes ELSE 0 END),0),COUNT(*) FROM dvr_recordings WHERE owner_key=? AND state!='deleted'`, o.Key()).Scan(&u.Bytes, &u.PendingDeleteBytes, &u.Recordings)
	return u, e
}

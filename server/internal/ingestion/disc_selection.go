package ingestion

import (
	"context"
	"database/sql"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// ChooseDiscMainTitle changes owner intent and schedules the actual timed-media
// read atomically. An in-flight probe of the old choice cannot publish afterwards.
func (s *Service) ChooseDiscMainTitle(ctx context.Context, item string, in catalog.DiscMainTitleRequest, authorize func(*sql.Tx) error) (catalog.DiscTitles, error) {
	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassInteractive)
	if err != nil {
		return catalog.DiscTitles{}, err
	}
	defer gated.Rollback()
	tx := gated.Tx()
	if authorize == nil {
		return catalog.DiscTitles{}, sql.ErrNoRows
	}
	if err = authorize(tx); err != nil {
		return catalog.DiscTitles{}, err
	}
	out, d, err := catalog.ChooseDiscTitleTx(ctx, tx, item, in)
	if err != nil {
		return out, err
	}
	// Do not let an older running analysis consume this new choice's queue row.
	var busy bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM inventory_source_active a JOIN jobs j ON j.id=a.job_id WHERE a.source_id=? AND j.status IN('queued','running','paused'))`, d.Source).Scan(&busy); err != nil {
		return out, err
	}
	if busy {
		return out, catalog.ErrSourceBusy
	}
	var library string
	if err = tx.QueryRowContext(ctx, `SELECT library_id FROM library_sources WHERE id=?`, d.Source).Scan(&library); err != nil {
		return out, err
	}
	policy, err := catalog.ScanPolicyTx(ctx, tx, library)
	if err != nil {
		return out, err
	}
	if !policy.Allows("probe") {
		return out, context.Canceled
	}
	job, err := queueSourceTx(ctx, tx, d.Source, true)
	if err != nil {
		return out, err
	}
	// This is a single-object analysis job, not a new inventory census. Keep
	// ordinary job/policy/root fences without walking the rest of the library.
	if _, err = tx.ExecContext(ctx, `UPDATE inventory_runs SET phase='basic_pending',discovered=1 WHERE job_id=?`, job.ID); err != nil {
		return out, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO inventory_analysis_queue(job_id,object_id,revision,policy_revision) VALUES(?,?,?,?) ON CONFLICT(job_id,object_id) DO UPDATE SET revision=excluded.revision,policy_revision=excluded.policy_revision,state='pending'`, job.ID, d.Object, d.Revision, policy.Revision)
	if err != nil {
		return out, err
	}
	if err = gated.Commit(); err != nil {
		return out, err
	}
	return out, nil
}

// PrepareDiscTitle probes one requested title without changing the main title,
// its duration, personal state, or any physical-version association.
func (s *Service) PrepareDiscTitle(ctx context.Context, item string, in catalog.DiscMainTitleRequest, authorize func(*sql.Tx) error) (catalog.DiscTitles, error) {
	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassInteractive)
	if err != nil {
		return catalog.DiscTitles{}, err
	}
	defer gated.Rollback()
	tx := gated.Tx()
	if authorize == nil {
		return catalog.DiscTitles{}, sql.ErrNoRows
	}
	if err = authorize(tx); err != nil {
		return catalog.DiscTitles{}, err
	}
	out, d, err := catalog.DiscTitlesTx(ctx, tx, item, in.VersionID)
	if err != nil {
		return out, err
	}
	if out.Revision != in.Revision {
		return out, catalog.ErrDiscSelectionChanged
	}
	if err = d.Ready(ctx, tx); err != nil {
		return out, err
	}
	found := false
	for _, t := range out.Titles {
		if t.ID == in.TitleID {
			found = true
			if t.Ready {
				return out, nil
			}
		}
	}
	if !found {
		return out, sql.ErrNoRows
	}
	var busy bool
	var library string
	if err = tx.QueryRowContext(ctx, `SELECT library_id,EXISTS(SELECT 1 FROM inventory_source_active a JOIN jobs j ON j.id=a.job_id WHERE a.source_id=s.id AND j.status IN('queued','running','paused')) FROM library_sources s WHERE id=?`, d.Source).Scan(&library, &busy); err != nil {
		return out, err
	}
	if busy {
		return out, catalog.ErrSourceBusy
	}
	policy, err := catalog.ScanPolicyTx(ctx, tx, library)
	if err != nil {
		return out, err
	}
	if !policy.Allows("probe") {
		return out, context.Canceled
	}
	job, err := queueSourceTx(ctx, tx, d.Source, true)
	if err != nil {
		return out, err
	}
	if err = catalog.RequestDiscTitleTx(ctx, tx, item, job.ID, in); err != nil {
		return out, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE inventory_runs SET phase='basic_pending',discovered=1 WHERE job_id=?`, job.ID); err != nil {
		return out, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO inventory_analysis_queue(job_id,object_id,revision,policy_revision) VALUES(?,?,?,?)`, job.ID, d.Object, d.Revision, policy.Revision); err != nil {
		return out, err
	}
	if err = gated.Commit(); err != nil {
		return out, err
	}
	return out, nil
}

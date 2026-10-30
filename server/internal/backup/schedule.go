package backup

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/workpolicy"
)

// Scheduler runs the backup schedule: when a maintenance window holding the
// backup task is open and no scheduled backup has been created since that
// occurrence started, it creates one and prunes to the keep count.
type Scheduler struct {
	service       *Service
	db            *sql.DB
	keep          func(context.Context) (int, error)
	now           func() time.Time
	ReportFailure func(context.Context, bool)
}

// Schedule builds the scheduler on the live database. keep answers the
// owner's BackupKeepCount; a failing keep falls back to the shipped default.
func (s *Service) Schedule(db *sql.DB, keep func(context.Context) (int, error)) *Scheduler {
	return &Scheduler{service: s, db: db, keep: keep, now: time.Now}
}

// Run checks the schedule on start and every five minutes until ctx ends.
func (s *Scheduler) Run(ctx context.Context) {
	s.check(ctx)
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.check(ctx)
		}
	}
}

func (s *Scheduler) check(ctx context.Context) {
	ctx = dbwork.WithClass(ctx, dbwork.ClassMaintenance)
	policy, err := s.readPolicy(ctx)
	if err != nil {
		s.failed(ctx, err)
		return
	}
	now := s.now()
	start, ok := workpolicy.OccurrenceStart(policy, "backup", now)
	if !ok {
		return
	}
	latest, err := s.latestScheduled()
	if err != nil {
		s.failed(ctx, err)
		return
	}
	if !latest.IsZero() && !latest.Before(start) {
		return
	}
	id, err := s.service.Start(ctx, KindScheduled, "scheduled-"+start.UTC().Format("20060102T150405Z"))
	if err == nil {
		job, ok := s.service.Job(id)
		if !ok {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-job.Done():
			err = job.Result()
		}
	}
	if errors.Is(err, ErrBackupRunning) {
		return
	}
	if s.ReportFailure != nil {
		s.ReportFailure(ctx, err != nil)
	}
	if err != nil {
		log.Printf("warn: Scheduled backup: %v", err)
		return
	}
	log.Printf("Scheduled backup %s", id)
	keep := DefaultKeepCount
	if s.keep != nil {
		if n, err := s.keep(ctx); err == nil && n > 0 {
			keep = n
		}
	}
	if err = s.service.Prune(keep); err != nil {
		s.failed(ctx, err)
	}
}

func (s *Scheduler) failed(ctx context.Context, err error) {
	if ctx.Err() != nil {
		return
	}
	if s.ReportFailure != nil {
		s.ReportFailure(ctx, true)
	}
	log.Printf("warn: Scheduled backup: %v", err)
}

func (s *Scheduler) readPolicy(ctx context.Context) (workpolicy.Policy, error) {
	var out workpolicy.Policy
	snapshot, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return out, err
	}
	defer snapshot.Rollback()
	return workpolicy.ReadTx(ctx, snapshot.Tx())
}

func (s *Scheduler) latestScheduled() (time.Time, error) {
	listed, err := s.service.List()
	if err != nil {
		return time.Time{}, err
	}
	var latest time.Time
	for _, info := range listed {
		if info.Kind != KindScheduled {
			continue
		}
		at, err := time.Parse(time.RFC3339, info.CreatedAt)
		if err != nil {
			continue
		}
		if at.After(latest) {
			latest = at
		}
	}
	return latest, nil
}

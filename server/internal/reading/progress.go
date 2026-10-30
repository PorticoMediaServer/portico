package reading

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strings"
	"time"
	"unicode"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
)

var ErrConflict = errors.New("reading position changed")

// Location is a normalized, format-specific position. It never shares the
// timed-media millisecond/history path. JSON DTOs belong to the contract owner.
type Location struct {
	Format, CFI, Section string
	PageIndex            *int64
	Progression          float64
}
type Progress struct {
	SourceID, SourceRevision string
	Location                 Location
	Revision, ObservedAt     int64
	Stale                    bool
}

func (l Location) Validate() error {
	if math.IsNaN(l.Progression) || math.IsInf(l.Progression, 0) || l.Progression < 0 || l.Progression > 1 {
		return ErrInvalid
	}
	switch l.Format {
	case "epub":
		if l.PageIndex != nil || len(l.CFI) > 4096 || !strings.HasPrefix(l.CFI, "epubcfi(/") || !strings.HasSuffix(l.CFI, ")") || len(l.Section) > 1024 {
			return ErrInvalid
		}
		if strings.IndexFunc(l.CFI, unicode.IsControl) >= 0 || strings.ContainsAny(l.CFI, "<>") {
			return ErrInvalid
		}
		if l.Section != "" && !safePath(l.Section) {
			return ErrInvalid
		}
	case "pdf", "cbz", "cb7", "cbr":
		if l.CFI != "" || l.Section != "" || l.PageIndex == nil || *l.PageIndex < 0 || *l.PageIndex > 1000000 {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}

func progressTx(ctx context.Context, tx *sql.Tx, owner string, v Selection) (*Progress, error) {
	var out Progress
	var cfi, section sql.NullString
	var page sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT source_revision,format,cfi,section,page_index,progression,revision,observed_at FROM reading_progress WHERE owner_key=? AND item_id=? AND asset_id=?`, owner, v.Entity, v.Asset).Scan(&out.SourceRevision, &out.Location.Format, &cfi, &section, &page, &out.Location.Progression, &out.Revision, &out.ObservedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out.SourceID = v.SourceID
	out.Location.CFI = cfi.String
	out.Location.Section = section.String
	if page.Valid {
		out.Location.PageIndex = &page.Int64
	}
	out.Stale = out.SourceRevision != v.Revision
	return &out, nil
}

func (s *Service) Progress(ctx context.Context, p identity.Principal, item, sourceID string) (*Progress, error) {
	f, err := s.Open(ctx, p, item, sourceID, "")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return f.Progress()
}

// Progress uses this acquisition's captured principal, source and lifetime.
// It never opens another reader while this one owns source custody.
func (f *Source) Progress() (*Progress, error) {
	s, ctx, p := f.service, f.ctx, f.p
	var err error
	if err = f.Validate(); err != nil {
		return nil, err
	}
	g, err := dbwork.BeginSnapshot(ctx, s.DB)
	if err != nil {
		return nil, err
	}
	defer g.Rollback()
	v, err := s.selectTx(ctx, g.Tx(), p, f.ItemID, f.SourceID)
	if err != nil {
		return nil, err
	}
	if v.Revision != f.Revision {
		return nil, ErrChanged
	}
	if f.remoteSession != nil {
		if err = f.remoteSession.remote.CheckTx(ctx, g.Tx()); err != nil {
			return nil, err
		}
	}
	v = f.PublicationSelection()
	progress, err := progressTx(ctx, g.Tx(), identity.PersonalKey(p.Viewer), v)
	g.Rollback()
	if err == nil {
		err = f.Validate()
	}
	return progress, err
}

// SaveProgress validates actual source bytes outside the writer, then checks
// the live session, association, source revision, Pause history and CAS under
// one writer. The selected version cannot be replaced by a body profile key.
func (s *Service) SaveProgress(ctx context.Context, p identity.Principal, item, sourceID, sourceRevision string, expected int64, location Location) (*Progress, bool, error) {
	if expected < 0 || sourceID == "" || sourceRevision == "" {
		return nil, false, ErrInvalid
	}
	if err := location.Validate(); err != nil {
		return nil, false, err
	}
	f, err := s.Open(ctx, p, item, sourceID, sourceRevision)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	return f.SaveProgress(expected, location)
}

// SaveProgress retains the same observed source through format validation and
// the location writer. This also avoids nested source leases on cap-1 remotes.
type OfflineValidator func(context.Context, *sql.Tx, identity.Principal) error
type OfflineObservation struct {
	Validate              OfflineValidator
	ObservedAt, StartedAt int64
	Outcome               string
}

func (f *Source) SaveOfflineProgress(expected int64, location Location, observed, started int64, validate OfflineValidator) (*Progress, bool, string, error) {
	if validate == nil || observed <= 0 || started <= 0 || observed < started || observed > time.Now().Add(time.Minute).UnixMilli() {
		return nil, false, "", ErrInvalid
	}
	observation := &OfflineObservation{ObservedAt: observed, StartedAt: started, Validate: validate}
	progress, saved, err := f.saveProgress(expected, location, observation)
	return progress, saved, observation.Outcome, err
}
func (f *Source) SaveProgress(expected int64, location Location) (*Progress, bool, error) {
	return f.saveProgress(expected, location, nil)
}
func (f *Source) saveProgress(expected int64, location Location, offline *OfflineObservation) (*Progress, bool, error) {
	s, ctx, p := f.service, f.ctx, f.p
	if expected < 0 {
		return nil, false, ErrInvalid
	}
	if err := location.Validate(); err != nil {
		return nil, false, err
	}
	var err error
	if location.Format != f.PublicationSelection().Format {
		return nil, false, ErrInvalid
	}
	switch f.PublicationSelection().Format {
	case "epub":
		e, err := f.epub()
		if err != nil {
			return nil, false, f.StopAndRecheck(err)
		}
		if location.Section != "" {
			found := false
			for _, section := range e.Spine {
				if section == location.Section {
					found = true
					break
				}
			}
			if !found {
				return nil, false, ErrInvalid
			}
		}
	case "cbz", "cb7", "cbr":
		c, err := f.comic()
		if err != nil {
			return nil, false, f.StopAndRecheck(err)
		}
		if *location.PageIndex >= int64(len(c.Names)) {
			return nil, false, ErrInvalid
		}
	case "pdf":
		if err = f.CheckPDF(); err != nil {
			return nil, false, err
		}
	}
	if err = f.Validate(); err != nil {
		return nil, false, err
	}
	g, err := dbwork.Begin(ctx, s.DB, dbwork.ClassInteractive)
	if err != nil {
		return nil, false, err
	}
	defer g.Rollback()
	tx := g.Tx()
	v, err := s.selectTx(ctx, tx, p, f.ItemID, f.SourceID)
	if err != nil {
		return nil, false, err
	}
	if v.Revision != f.Revision {
		return nil, false, ErrChanged
	}
	if f.remoteSession != nil {
		if err = f.remoteSession.remote.CheckTx(ctx, tx); err != nil {
			return nil, false, err
		}
	}
	if offline != nil {
		if err = offline.Validate(ctx, tx, p); err != nil {
			return nil, false, err
		}
	}
	v = f.PublicationSelection()
	owner := identity.PersonalKey(p.Viewer)
	current, err := progressTx(ctx, tx, owner, v)
	if err != nil {
		return nil, false, err
	}
	preferences, _, _, err := operations.EffectivePreferences(tx, p.Viewer, "")
	if err != nil {
		return nil, false, err
	}
	if preferences.Bool("privacy.pauseWatchHistory") {
		if offline != nil {
			offline.Outcome = "history_paused"
		}
		return current, false, nil
	}
	var revision int64
	if current != nil {
		revision = current.Revision
	}
	if offline != nil {
		if current != nil && current.ObservedAt >= offline.ObservedAt {
			offline.Outcome = "stale_observation"
			return current, false, nil
		}
		if revision != expected {
			offline.Outcome = "superseded_online"
			return current, false, nil
		}
	} else if revision != expected {
		return nil, false, ErrConflict
	}
	var cfi, section, page any
	if location.Format == "epub" {
		cfi = location.CFI
		if location.Section != "" {
			section = location.Section
		}
	} else {
		page = *location.PageIndex
	}
	observed := time.Now().UnixMilli()
	if offline != nil {
		observed = offline.ObservedAt
		offline.Outcome = "applied"
	}
	revision++
	_, err = tx.ExecContext(ctx, `INSERT INTO reading_progress(owner_key,item_id,asset_id,source_revision,format,cfi,section,page_index,progression,revision,observed_at) VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(owner_key,item_id,asset_id) DO UPDATE SET source_revision=excluded.source_revision,format=excluded.format,cfi=excluded.cfi,section=excluded.section,page_index=excluded.page_index,progression=excluded.progression,revision=excluded.revision,observed_at=excluded.observed_at`, owner, v.Entity, v.Asset, v.Revision, location.Format, cfi, section, page, location.Progression, revision, observed)
	if err != nil {
		return nil, false, err
	}
	if err = ctx.Err(); err != nil {
		return nil, false, err
	}
	if f.remoteSession != nil {
		if err = f.remoteSession.remote.CheckTx(ctx, tx); err != nil {
			return nil, false, err
		}
	}
	if err = g.Commit(); err != nil {
		return nil, false, err
	}
	return &Progress{SourceID: v.SourceID, SourceRevision: v.Revision, Location: location, Revision: revision, ObservedAt: observed}, true, nil
}

// OfflineProgress reads this viewer's saved position for one downloaded copy's
// publication, for an inbound refresh. It opens no session and writes nothing:
// no history, observation or intent time changes, and a changed server source
// never revokes the copy. A nil result is confirmed absence for that exact
// publication. Stale compares the saved position with the copy's own revision.
func (s *Service) OfflineProgress(ctx context.Context, p identity.Principal, item, source, revision string) (*Progress, error) {
	if s == nil || s.DB == nil {
		return nil, identity.ErrForbidden
	}
	g, err := dbwork.BeginSnapshot(ctx, s.DB)
	if err != nil {
		return nil, err
	}
	defer g.Rollback()
	v, err := s.selectTx(ctx, g.Tx(), p, item, source)
	if err != nil {
		return nil, err
	}
	progress, err := progressTx(ctx, g.Tx(), identity.PersonalKey(p.Viewer), v)
	if progress != nil {
		progress.Stale = progress.SourceRevision != revision
	}
	return progress, err
}

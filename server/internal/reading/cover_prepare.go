package reading

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/readingcap"
)

// Scan-time cover preparation covers unopened local EPUB and CBZ publications.
// It uses the same admitted publication, explicit cover selection and accepting
// artwork writer as an owner's open; only the opener's authority differs: a
// registered background worker instead of a viewer session. There is no PDF
// branch: the server has no PDF renderer fact and does not guess one.
//
// The pending set is derived from current state, so a pass is resumable without
// a queue table. A determined outcome is recorded for the exact observation and
// rule, so a coverless or invalid cover is not reopened on every pass; a source
// change or a rule bump makes it pending again. Acquisition failures record
// nothing and are retried after the pass rest.
const (
	defaultCoverWindow = 32
	defaultCoverRest   = 10 * time.Minute
	coverBudget        = 2 * time.Minute

	readingCoverRuleVersion = 1
)

// ErrCoverInvalid marks bounded publication bytes the artwork pipeline refused
// as an image. For the inspected observation that is a determined outcome, not
// a transient acquisition failure.
var ErrCoverInvalid = errors.New("reading cover is not an adoptable image")

// The worker's own domain: book EPUB or comic CBZ from a local source. Remote
// publications are excluded rather than staged whole; PDF is excluded because
// its cover would be a renderer guess.
var coverWorkerDomain = `((e.kind=20 AND lib.kind='book' AND p.format='epub') OR (e.kind=21 AND lib.kind='comic' AND p.format IN(` + readingcap.ComicArchiveFormatsSQL() + `))) AND s.kind='local'`

// The leased source's registered background owner label. It is never a user or
// owner principal.
const coverSourceAuthority = "reading-covers"

// Bounded, indexed order by item id. The candidate window is bounded first;
// whether an item already carries selected art or a determined outcome for this
// exact object/revision/rule is returned as a column and skipped after the
// window, so a library of determined items never widens the query. The cursor
// advances over every examined candidate, including skipped ones.
func coverPendingSQL(primary string) string {
	return `SELECT e.id,pid(e.public_id),cl.library_id,a.token,
 EXISTS(SELECT 1 FROM artwork_selections chosen WHERE chosen.kind='item' AND chosen.entity_id=e.id AND chosen.role='cover' AND chosen.subject=''),
 EXISTS(SELECT 1 FROM reading_cover_inspections i WHERE i.item_id=p.item_id AND i.asset_id=p.asset_id AND i.inventory_object_id=o.id AND i.source_revision=o.revision AND i.rule_version=?)
 FROM catalog_entities e
 CROSS JOIN catalog_libraries cl ON cl.id=e.library_id
 CROSS JOIN libraries lib ON lib.id=cl.library_id
 CROSS JOIN reading_publications p ON p.item_id=e.id AND p.asset_id=` + primary + `
 CROSS JOIN catalog_asset_links link ON link.entity_id=e.id AND link.asset_id=p.asset_id
 CROSS JOIN catalog_assets a ON a.id=link.asset_id
 CROSS JOIN inventory_objects o ON o.asset_id=a.token
 CROSS JOIN library_sources s ON s.id=o.source_id AND s.library_id=cl.library_id
 CROSS JOIN inventory_item_availability available ON available.item_id=e.id AND available.asset_id=a.token
 WHERE e.retired=0 AND cl.retired=0 AND ` + coverWorkerDomain + ` AND ` + publicationEligible + `
 AND ` + backgroundCanonicalObject + `
 AND e.id>? AND e.id<=? ORDER BY e.id LIMIT ?`
}

// CoverPreparation is one worker.Run step over pending canonical EPUB/CBZ
// publications. It is woken by catalog/source writes and rests after a complete
// pass, so an unreadable publication cannot spin the worker.
type CoverPreparation struct {
	DB      *sql.DB
	Prepare func(context.Context, string, string, string) error
	// PrimaryPublicationSQL is the canonical availability-first primary
	// version rule supplied by the live Service.
	PrimaryPublicationSQL func() string
	// Admission is the existing background policy. Cover preparation is
	// descriptive metadata work, so it uses the metadata-refresh task.
	Admission func(context.Context, *sql.Tx, string) (bool, error)
	Window    int
	Rest      time.Duration

	cursor   int64
	passing  bool
	finished time.Time
	logged   int
	retry    bool
}

type coverCandidate struct {
	id                  int64
	item, libraryToken  string
	asset               string
	selected, inspected bool
}

// Step processes at most one indexed window and reports the delay before the
// next step. Every item is attempted once per pass. Transient failures are not
// cached and are retried after the pass rest.
func (p *CoverPreparation) Step(ctx context.Context) time.Duration {
	if p == nil || p.DB == nil || p.Prepare == nil {
		return 0
	}
	ctx = dbwork.WithClass(ctx, dbwork.ClassBackgroundMedia)
	if p.Admission != nil {
		allowed, err := p.Admission(ctx, nil, "metadata-refresh")
		if err != nil {
			log.Printf("Reading covers: admission: %v", err)
			return time.Minute
		}
		if !allowed {
			return time.Minute
		}
	}
	window, rest := p.Window, p.Rest
	if window <= 0 {
		window = defaultCoverWindow
	}
	if rest <= 0 {
		rest = defaultCoverRest
	}
	if !p.passing {
		if !p.finished.IsZero() && time.Since(p.finished) < rest {
			return rest - time.Since(p.finished)
		}
		p.passing, p.cursor, p.logged, p.retry = true, 0, 0, false
	}
	primary := ""
	if p.PrimaryPublicationSQL != nil {
		primary = p.PrimaryPublicationSQL()
	}
	if primary == "" {
		return time.Minute
	}
	after, examined, err := preparationWindow(ctx, p.DB, p.cursor, window)
	if err != nil {
		log.Printf("Reading covers: %v", err)
		return time.Minute
	}
	rows, err := dbwork.ReadHandle(ctx, p.DB).QueryContext(ctx, coverPendingSQL(primary), readingCoverRuleVersion, p.cursor, after, window)
	if err != nil {
		log.Printf("Reading covers: %v", err)
		return time.Minute
	}
	todo := []coverCandidate{}
	for rows.Next() {
		var candidate coverCandidate
		if err = rows.Scan(&candidate.id, &candidate.item, &candidate.libraryToken, &candidate.asset, &candidate.selected, &candidate.inspected); err != nil {
			break
		}
		todo = append(todo, candidate)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		log.Printf("Reading covers: %v", err)
		return time.Minute
	}
	for _, candidate := range todo {
		if ctx.Err() != nil {
			return 0
		}
		if !dbwork.PaceBackground(ctx, time.Time{}) {
			return 0
		}
		// The cursor advances over every examined candidate, including the ones
		// this pass had no work for, so the next window never repeats them.
		p.cursor = candidate.id
		if candidate.selected || candidate.inspected {
			continue
		}
		bounded, cancel := context.WithTimeout(ctx, coverBudget)
		err = p.Prepare(bounded, candidate.libraryToken, candidate.item, candidate.asset)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return 0
			}
			p.retry = true
			if p.logged < 5 {
				p.logged++
				log.Printf("Reading covers: %s/%s not prepared: %v", candidate.libraryToken, candidate.item, err)
			}
		}
	}
	p.cursor = after
	if examined < window {
		p.passing = false
		if p.retry {
			// A transient failure is retried after the pass rest.
			p.finished = time.Now()
			return rest
		}
		// Nothing is left to retry: the next pass starts on the next wake.
		p.finished = time.Time{}
		return 0
	}
	return time.Millisecond
}

// PrepareCover inspects one canonical unopened local EPUB or CBZ and adopts its
// explicit cover through the same CoverAdopter an open uses. The writer proves
// the current primary source/observation inside its own transaction; this
// worker never invents a principal, borrows a session or replaces owner art.
func (s *Service) PrepareCover(ctx context.Context, library, item, asset string) error {
	if s == nil || s.DB == nil || s.AdoptCover == nil {
		return nil
	}
	v, err := s.resolveBackgroundDomain(ctx, coverWorkerDomain, library, item, asset)
	if err != nil {
		return err
	}
	cached, err := s.coverInspectionCached(ctx, v)
	if err != nil || cached {
		return err
	}
	f, err := s.openBackground(ctx, coverWorkerDomain, coverSourceAuthority, v)
	if err != nil {
		return err
	}
	defer f.Close()
	accept := f.ctx
	document, err := Inspect(f)
	if err != nil {
		return err
	}
	if document.cover == nil {
		if err = f.Validate(); err != nil {
			return err
		}
		return s.recordCoverInspection(accept, v, "absent")
	}
	raw, err := document.cover()
	if err != nil {
		if errors.Is(err, ErrLimit) || errors.Is(err, ErrInvalid) {
			if err = f.Validate(); err != nil {
				return err
			}
			return s.recordCoverInspection(accept, v, "invalid")
		}
		return err
	}
	if err = f.Validate(); err != nil {
		return err
	}
	authorize := func(tx *sql.Tx) error {
		if err := accept.Err(); err != nil {
			return err
		}
		return s.validateBackgroundTx(accept, tx, coverWorkerDomain, v)
	}
	err = s.AdoptCover(accept, v.ItemID, v.Revision, raw, authorize)
	if errors.Is(err, ErrCoverInvalid) {
		if err = f.Validate(); err != nil {
			return err
		}
		return s.recordCoverInspection(accept, v, "invalid")
	}
	if err != nil {
		return err
	}
	if err = f.Validate(); err != nil {
		return err
	}
	return s.recordCoverInspection(accept, v, "adopted")
}

// One determined outcome per item/asset for the exact observation and rule.
func (s *Service) coverInspectionCached(ctx context.Context, selected Selection) (bool, error) {
	g, err := dbwork.BeginSnapshot(ctx, s.DB)
	if err != nil {
		return false, err
	}
	defer g.Rollback()
	var cached int
	err = g.Tx().QueryRowContext(ctx, `SELECT 1 FROM reading_cover_inspections WHERE item_id=? AND asset_id=? AND inventory_object_id=? AND source_revision=? AND rule_version=?`, selected.Entity, selected.Asset, selected.inventoryRow, selected.inventory, readingCoverRuleVersion).Scan(&cached)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return false, err
}

// recordCoverInspection publishes a determined outcome only for the observation
// this worker actually inspected. The same current canonical selection is
// rechecked in the writer, so a changed source never inherits an outcome.
func (s *Service) recordCoverInspection(ctx context.Context, selected Selection, outcome string) error {
	gate, err := dbwork.Begin(ctx, s.DB, dbwork.ClassBackgroundMedia)
	if err != nil {
		return err
	}
	defer gate.Rollback()
	current, err := s.resolveBackgroundTx(ctx, gate.Tx(), coverWorkerDomain, selected.library, selected.ItemID, selected.SourceID)
	if err != nil {
		return err
	}
	if current.Revision != selected.Revision || current.inventoryRow != selected.inventoryRow || current.object != selected.object || current.inventory != selected.inventory {
		return ErrChanged
	}
	_, err = gate.Tx().ExecContext(ctx, `INSERT INTO reading_cover_inspections(item_id,asset_id,inventory_object_id,source_revision,rule_version,outcome,attempted_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(item_id,asset_id) DO UPDATE SET inventory_object_id=excluded.inventory_object_id,source_revision=excluded.source_revision,rule_version=excluded.rule_version,outcome=excluded.outcome,attempted_at=excluded.attempted_at WHERE reading_cover_inspections.inventory_object_id<>excluded.inventory_object_id OR reading_cover_inspections.source_revision<>excluded.source_revision OR reading_cover_inspections.rule_version<>excluded.rule_version OR reading_cover_inspections.outcome<>excluded.outcome`, current.Entity, current.Asset, current.inventoryRow, current.inventory, readingCoverRuleVersion, outcome, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	return gate.Commit()
}

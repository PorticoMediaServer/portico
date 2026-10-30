package reading

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"strconv"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/sourceaccess"
)

// Text count preparation covers unopened EPUB publications. It is the same
// complete-text counter and the same reading_text_counts row/revision/rule
// custody as a viewer's own open; it is not a second estimator, a sample or a
// learned pace. Only the opener's authority differs: a registered background
// worker instead of a viewer session.
//
// The pending set is the canonical primary EPUB of an authorized, enabled local
// source whose exact inventory object/revision has no count row yet. That makes
// the pass resumable from derived state: adopted rows disappear, so a restart
// or a later pass continues where the last one stopped without a queue table.
// A source change makes the row's inventory object or revision differ, so the
// publication becomes pending again and the new bytes are counted afresh.
const (
	defaultTextCountWindow = 64
	defaultTextCountRest   = 10 * time.Minute
	textCountBudget        = 2 * time.Minute
)

// The canonical object mirrors selectPublication's ORDER BY a.id,o.id LIMIT 1:
// one asset has one selected inventory object, so the pending join and the
// opener can never disagree about which row a fact belongs to.
const backgroundCanonicalObject = `o.id=(SELECT o2.id FROM inventory_objects o2 INDEXED BY inventory_objects_asset WHERE o2.asset_id=a.token AND o2.retired=0 AND o2.state='available' AND o2.unsupported_reason='' AND o2.root_incarnation=s.incarnation AND o2.size=a.size AND o2.modified_ns=a.modified_ns ORDER BY o2.id LIMIT 1)`

// The worker's own domain, used by both the pending selection and the fresh
// resolver/accepting transaction. It is narrower than a viewer selection: book
// EPUB or PDF only, and only a local source. Each format keeps its own extractor
// branch; this widening does not remove the approved EPUB branch. Enabled/
// healthy/current inventory is still enforced by publicationEligible below.
const textCountWorkerDomain = `e.kind=20 AND lib.kind='book' AND p.format IN('epub','pdf') AND s.kind='local'`

// The leased source's registered background owner label. It is never a user or
// owner principal; the opaque token keeps separate worker steps distinct.
const textCountSourceAuthority = "reading-prepare"

// Bounded, indexed order by item id. Remote sources are excluded rather than
// approximating a full-file read through a viewer session: an unadmitted remote
// publication simply stays unknown. The reader and Discover queries already
// exclude unknown or stale facts. The cache join carries the whole current
// rule, including the opening-text rule, so a row with an older opening rule is
// pending rather than excluded forever.
func textCountPendingSQL(primary string) string {
	return `SELECT e.id,pid(e.public_id),cl.library_id,a.token
 FROM catalog_entities e
 CROSS JOIN catalog_libraries cl ON cl.id=e.library_id
 CROSS JOIN libraries lib ON lib.id=cl.library_id
 CROSS JOIN reading_publications p ON p.item_id=e.id AND p.asset_id=` + primary + `
 CROSS JOIN catalog_asset_links link ON link.entity_id=e.id AND link.asset_id=p.asset_id
 CROSS JOIN catalog_assets a ON a.id=link.asset_id
 CROSS JOIN inventory_objects o ON o.asset_id=a.token
 CROSS JOIN library_sources s ON s.id=o.source_id AND s.library_id=cl.library_id
 CROSS JOIN inventory_item_availability available ON available.item_id=e.id AND available.asset_id=a.token
 LEFT JOIN reading_text_counts c ON c.item_id=p.item_id AND c.asset_id=p.asset_id AND c.inventory_object_id=o.id AND c.source_revision=o.revision AND c.rule_version=? AND c.opening_rule_version=1 AND (p.format<>'pdf' OR (c.page_rule_version=1 AND c.page_count BETWEEN 1 AND 4096))
 WHERE e.retired=0 AND cl.retired=0 AND ` + textCountWorkerDomain + ` AND ` + publicationEligible + `
 AND ` + backgroundCanonicalObject + `
 AND c.item_id IS NULL AND e.id>? AND e.id<=? ORDER BY e.id LIMIT ?`
}

// The text-index worker's domain is EPUB-first: only a local book EPUB has this
// producer. PDF and comic facts keep their existing preparation.
const textIndexWorkerDomain = `e.kind=20 AND lib.kind='book' AND p.format='epub' AND s.kind='local'`

// The text-index candidate window is bounded before determination is known.
// It selects at most one window of canonical primary publications in indexed
// item order and returns whether each already has a determined index state for
// this exact observation and rule as a column. A library of determined
// indexed/evicted/excluded/unsupported rows is therefore examined in bounded
// windows, never walked in one statement to find zero pending rows. The cursor
// advances over every examined candidate; extraction runs only for the ones the
// flag leaves undetermined.
func textIndexPendingSQL(primary string) string {
	return `SELECT e.id,pid(e.public_id),cl.library_id,a.token,
 EXISTS(SELECT 1 FROM reading_text_index_state x WHERE x.item_id=p.item_id AND x.asset_id=p.asset_id AND x.inventory_object_id=o.id AND x.source_revision=o.revision AND x.index_rule_version=?)
 FROM catalog_entities e
 CROSS JOIN catalog_libraries cl ON cl.id=e.library_id
 CROSS JOIN libraries lib ON lib.id=cl.library_id
 CROSS JOIN reading_publications p ON p.item_id=e.id AND p.asset_id=` + primary + `
 CROSS JOIN catalog_asset_links link ON link.entity_id=e.id AND link.asset_id=p.asset_id
 CROSS JOIN catalog_assets a ON a.id=link.asset_id
 CROSS JOIN inventory_objects o ON o.asset_id=a.token
 CROSS JOIN library_sources s ON s.id=o.source_id AND s.library_id=cl.library_id
 CROSS JOIN inventory_item_availability available ON available.item_id=e.id AND available.asset_id=a.token
 WHERE e.retired=0 AND cl.retired=0 AND ` + textIndexWorkerDomain + ` AND ` + publicationEligible + `
 AND ` + backgroundCanonicalObject + `
 AND e.id>? AND e.id<=? ORDER BY e.id LIMIT ?`
}

// TextCountPreparation is one worker.Run step over the pending canonical EPUBs.
// It is woken by catalog/source writes and rests after a complete pass, so an
// unreadable book cannot spin the worker or block another library's queue.
type TextCountPreparation struct {
	DB      *sql.DB
	Prepare func(context.Context, string, string, string) error
	// Index prepares one pending EPUB text-index document. It runs in this same
	// worker step under the same admission, rest and cursors: there is no second
	// scheduler or queue table.
	Index func(context.Context, string, string, string) error
	// PrimaryPublicationSQL is the canonical availability-first primary
	// version rule supplied by the live Service (it depends on MOBI support).
	PrimaryPublicationSQL func() string
	// Admission is the existing background policy (pause and maintenance
	// windows). Preparation is descriptive metadata work, so it uses the
	// metadata-refresh task rather than adding a policy name.
	Admission func(context.Context, *sql.Tx, string) (bool, error)
	Window    int
	Rest      time.Duration

	cursor   int64
	passing  bool
	finished time.Time
	logged   int

	indexCursor   int64
	indexPassing  bool
	indexFinished time.Time
	indexLogged   int
	indexRetry    bool
}

type textCountCandidate struct {
	id                 int64
	item, libraryToken string
	asset              string
}

type textIndexCandidate struct {
	id                 int64
	item, libraryToken string
	asset              string
	determined         bool
}

// Step processes at most one indexed window for counts and one for the text
// index, and reports the delay before the next step. Both share this worker, its
// admission and its rest, so no second scheduler or queue table exists. Every
// item is attempted once per pass. Acquisition/refusal/cancellation/timeout
// failures are not cached and are retried after the pass rest. A complete but
// unsupported/partial publication keeps its determined fact, never a partial
// value.
func (p *TextCountPreparation) Step(ctx context.Context) time.Duration {
	if p == nil || p.DB == nil || (p.Prepare == nil && p.Index == nil) {
		return 0
	}
	ctx = dbwork.WithClass(ctx, dbwork.ClassBackgroundMedia)
	if p.Admission != nil {
		allowed, err := p.Admission(ctx, nil, "metadata-refresh")
		if err != nil {
			log.Printf("Reading preparation: admission: %v", err)
			return time.Minute
		}
		if !allowed {
			return time.Minute
		}
	}
	window, rest := p.Window, p.Rest
	if window <= 0 {
		window = defaultTextCountWindow
	}
	if rest <= 0 {
		rest = defaultTextCountRest
	}
	var wait time.Duration
	if p.Prepare != nil {
		wait = p.stepCounts(ctx, window, rest)
	}
	if p.Index != nil {
		if indexWait := p.stepIndex(ctx, window, rest); indexWait > 0 && (wait == 0 || indexWait < wait) {
			wait = indexWait
		}
	}
	return wait
}

func (p *TextCountPreparation) stepCounts(ctx context.Context, window int, rest time.Duration) time.Duration {
	if !p.passing {
		if !p.finished.IsZero() && time.Since(p.finished) < rest {
			return rest - time.Since(p.finished)
		}
		p.passing, p.cursor, p.logged = true, 0, 0
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
		log.Printf("Reading text counts: %v", err)
		return time.Minute
	}
	rows, err := dbwork.ReadHandle(ctx, p.DB).QueryContext(ctx, textCountPendingSQL(primary), readingTextRuleVersion, p.cursor, after, window)
	if err != nil {
		log.Printf("Reading text counts: %v", err)
		return time.Minute
	}
	todo := []textCountCandidate{}
	for rows.Next() {
		var candidate textCountCandidate
		if err = rows.Scan(&candidate.id, &candidate.item, &candidate.libraryToken, &candidate.asset); err != nil {
			break
		}
		todo = append(todo, candidate)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		log.Printf("Reading text counts: %v", err)
		return time.Minute
	}
	for _, candidate := range todo {
		if ctx.Err() != nil {
			return 0
		}
		if !dbwork.PaceBackground(ctx, time.Time{}) {
			return 0
		}
		p.cursor = candidate.id
		bounded, cancel := context.WithTimeout(ctx, textCountBudget)
		err = p.Prepare(bounded, candidate.libraryToken, candidate.item, candidate.asset)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return 0
			}
			if p.logged < 5 {
				p.logged++
				log.Printf("Reading text counts: %s/%s not prepared: %v", candidate.libraryToken, candidate.item, err)
			}
		}
	}
	p.cursor = after
	if examined < window {
		p.passing, p.finished = false, time.Now()
		return 0
	}
	return time.Millisecond
}

func (p *TextCountPreparation) stepIndex(ctx context.Context, window int, rest time.Duration) time.Duration {
	primary := ""
	if p.PrimaryPublicationSQL != nil {
		primary = p.PrimaryPublicationSQL()
	}
	if primary == "" {
		return time.Minute
	}
	if !p.indexPassing {
		if !p.indexFinished.IsZero() && time.Since(p.indexFinished) < rest {
			return rest - time.Since(p.indexFinished)
		}
		p.indexPassing, p.indexCursor, p.indexLogged, p.indexRetry = true, 0, 0, false
	}
	after, examined, err := preparationWindow(ctx, p.DB, p.indexCursor, window)
	if err != nil {
		log.Printf("Reading text index: %v", err)
		return time.Minute
	}
	rows, err := dbwork.ReadHandle(ctx, p.DB).QueryContext(ctx, textIndexPendingSQL(primary), readingIndexRuleVersion, p.indexCursor, after, window)
	if err != nil {
		log.Printf("Reading text index: %v", err)
		return time.Minute
	}
	todo := []textIndexCandidate{}
	for rows.Next() {
		var candidate textIndexCandidate
		if err = rows.Scan(&candidate.id, &candidate.item, &candidate.libraryToken, &candidate.asset, &candidate.determined); err != nil {
			break
		}
		todo = append(todo, candidate)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		log.Printf("Reading text index: %v", err)
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
		p.indexCursor = candidate.id
		if candidate.determined {
			continue
		}
		bounded, cancel := context.WithTimeout(ctx, textCountBudget)
		err = p.Index(bounded, candidate.libraryToken, candidate.item, candidate.asset)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return 0
			}
			p.indexRetry = true
			if p.indexLogged < 5 {
				p.indexLogged++
				log.Printf("Reading text index: %s/%s not prepared: %v", candidate.libraryToken, candidate.item, err)
			}
		}
	}
	p.indexCursor = after
	if examined < window {
		p.indexPassing = false
		if p.indexRetry {
			// A transient failure is retried after the pass rest.
			p.indexFinished = time.Now()
			return rest
		}
		// A fully determined pass waits for the next catalog/source wake.
		p.indexFinished = time.Time{}
		return 0
	}
	return time.Millisecond
}

// ResolveBackground selects the same canonical primary publication a viewer's
// open would, without inventing a principal or borrowing an expired session.
// It keeps the text-count worker's domain; another worker uses its own private
// domain resolver over the same catalog fences.
func (s *Service) ResolveBackground(ctx context.Context, library, item, asset string) (Selection, error) {
	return s.resolveBackgroundDomain(ctx, textCountWorkerDomain, library, item, asset)
}

func (s *Service) resolveBackgroundDomain(ctx context.Context, domain, library, item, asset string) (Selection, error) {
	if s == nil || s.DB == nil || s.Storage == nil {
		return Selection{}, ErrUnavailable
	}
	g, err := dbwork.BeginSnapshot(ctx, s.DB)
	if err != nil {
		return Selection{}, err
	}
	defer g.Rollback()
	return s.resolveBackgroundTx(ctx, g.Tx(), domain, library, item, asset)
}

func (s *Service) resolveBackgroundTx(ctx context.Context, tx *sql.Tx, domain, library, item, asset string) (Selection, error) {
	var v Selection
	v.ItemID = item
	err := tx.QueryRowContext(ctx, `SELECT `+publicationFields+` FROM catalog_entities e`+publicationJoins+`
 WHERE e.public_id=pid_blob(?) AND a.token=? AND cl.library_id=? AND p.asset_id=`+s.primaryPublicationSQL()+` AND `+domain+` AND `+publicationEligible+` ORDER BY a.id,o.id LIMIT 1`, item, asset, library).Scan(selectionColumns(&v)...)
	if errors.Is(err, sql.ErrNoRows) {
		return Selection{}, ErrUnavailable
	}
	if err != nil {
		return Selection{}, err
	}
	if err = s.selectionValid(v); err != nil {
		return Selection{}, err
	}
	v.SourceID = v.token
	v.Revision = v.fence()
	return v, nil
}

func (s *Service) validateBackgroundDomain(ctx context.Context, domain string, selected Selection) error {
	g, err := dbwork.BeginSnapshot(ctx, s.DB)
	if err != nil {
		return err
	}
	defer g.Rollback()
	return s.validateBackgroundTx(ctx, g.Tx(), domain, selected)
}

// validateBackgroundTx is the same recheck inside a caller transaction, usable
// as the accepting writer's own source guard.
func (s *Service) validateBackgroundTx(ctx context.Context, tx *sql.Tx, domain string, selected Selection) error {
	current, err := s.resolveBackgroundTx(ctx, tx, domain, selected.library, selected.ItemID, selected.SourceID)
	if err != nil {
		return err
	}
	if current.Revision != selected.Revision {
		return ErrChanged
	}
	return nil
}

// OpenBackground owns a fresh source lease for the preparation worker. Its
// lifetime is the worker step, never a viewer session; validation is the
// catalog/fence tuple above. It keeps the text-count worker's domain and
// registered background owner label.
func (s *Service) OpenBackground(ctx context.Context, v Selection) (*Source, error) {
	return s.openBackground(ctx, textCountWorkerDomain, textCountSourceAuthority, v)
}

func (s *Service) openBackground(ctx context.Context, domain, authority string, v Selection) (*Source, error) {
	if s == nil || s.Storage == nil {
		return nil, ErrUnavailable
	}
	// Full-file counts through a viewer-bound remote session are not admitted
	// here. The publication stays unknown unless an authorized remote read path
	// is added for the worker.
	if s.Storage.IsRemote(v.path) {
		return nil, ErrUnavailable
	}
	var roots *sourceaccess.Registry
	if s.Mounts == nil {
		roots = sourceaccess.New(s.Storage, nil)
	} else {
		roots = sourceaccess.New(s.Storage, s.Mounts)
	}
	validate := func(ctx context.Context) error { return s.validateBackgroundDomain(ctx, domain, v) }
	lease, err := roots.Borrow(ctx, sourceaccess.Authority{OwnerID: authority + ":" + identity.Token(), RootID: v.rootID, RootPath: v.root, RelativePath: v.relative, Revision: v.incarnation + ":" + strconv.FormatInt(v.generation, 10), Lifetime: ctx, Validate: validate})
	if err != nil {
		roots.Close()
		return nil, err
	}
	r, err := s.Storage.OpenObservedPlayback(ctx, lease)
	if err != nil {
		roots.Close()
		return nil, err
	}
	f := &Source{Selection: v, ctx: ctx, service: s, reader: r, roots: roots, validateCurrent: validate}
	o, err := r.Observe(ctx)
	if err == nil {
		err = f.matchInventory(o)
	}
	if err == nil {
		err = validate(ctx)
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	f.observed = o
	return f, nil
}

// PrepareTextCount counts one canonical unopened EPUB or supported PDF with the
// existing complete counters and adopts it under the same row/revision/rule
// custody as an open. Each format has its own explicit extractor branch. Bytes
// that cannot be acquired completely write no row; complete but unsupported/
// partial text records the existing determined NULL (unknown), not zero. The
// fresh resolver repeats the worker's exact local book EPUB/PDF domain and the
// observed source fence before acceptance.
func (s *Service) PrepareTextCount(ctx context.Context, library, item, asset string) error {
	v, err := s.ResolveBackground(ctx, library, item, asset)
	if err != nil {
		return err
	}
	if v.Format != "epub" && v.Format != "pdf" {
		return nil
	}
	cached, err := s.textCountCached(ctx, v)
	if err != nil || cached {
		return err
	}
	f, err := s.OpenBackground(ctx, v)
	if err != nil {
		return err
	}
	defer f.Close()
	var count *int64
	var opening string
	var author string
	var pageCount *int64
	switch v.Format {
	case "epub":
		e, epubErr := f.epub()
		if epubErr != nil {
			return epubErr
		}
		count, opening, err = inspectEPUBText(ctx, e)
	case "pdf":
		count, opening, author, pageCount, err = countPDFText(ctx, f, v.Size)
	}
	if err != nil {
		return err
	}
	if v.Format == "pdf" && count == nil && pageCount == nil {
		// Neither the text nor the structural page tree is a determination.
		// Write no row so a later pass can retry instead of caching absence.
		return nil
	}
	if err = f.Validate(); err != nil {
		return err
	}
	// The accepting writer is bound to the opened source's lifetime. If its
	// original lease retires while waiting for the writer, the commit is refused.
	accept := f.ctx
	gate, err := dbwork.Begin(accept, s.DB, dbwork.ClassBackgroundMedia)
	if err != nil {
		return err
	}
	defer gate.Rollback()
	current, err := s.resolveBackgroundTx(accept, gate.Tx(), textCountWorkerDomain, library, item, asset)
	if err != nil {
		return err
	}
	if current.Revision != v.Revision || current.inventoryRow != v.inventoryRow || current.object != v.object || current.inventory != v.inventory {
		return ErrChanged
	}
	pageRule := 0
	if v.Format == "pdf" {
		pageRule = readingPageRuleVersion
	}
	if err = adoptTextCountRow(accept, gate.Tx(), current, count, opening, pageCount, pageRule); err != nil {
		return err
	}
	if v.Format == "pdf" {
		// The helper's exact Info/Author is file-metadata evidence for this same
		// observation. The writer rechecks the current object and keeps owner
		// overrides; an absent or unstorable value publishes nothing instead of
		// a guess, and no title, outline or filename substitutes for it.
		if err = adoptFileMetadataTx(accept, gate.Tx(), current, author, "", nil); err != nil {
			return err
		}
	}
	if err = accept.Err(); err != nil {
		return err
	}
	return gate.Commit()
}

package catalog

import (
	"context"
	"database/sql"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"strings"
	"sync"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// Content restrictions are enforced in exactly one place: the SQL fragment this
// file compiles. Every catalog surface that can show a title to a viewer appends
// it — the browse engine, facet counts, home rows, search and the single-item
// read. Nothing else in the package decides visibility, so a new surface that
// forgets the predicate is a missing call to one named function rather than a
// subtly different rule.
//
// The predicate answers one question about one entity id:
//
//	may this profile see this entity?
//
// and it answers it with three clauses, all of which must hold:
//
//  1. Rating ceiling. The entity carries no contentRating whose issuing body
//     admits only people older than the profile's ceiling.
//  2. Unrated. When AllowUnrated is false, the entity carries at least one
//     contentRating this server recognises. Content whose rating is missing,
//     empty or spelled in a way the rating table does not know is unrated: it is
//     never silently treated as suitable.
//  3. Labels. The entity carries none of the profile's blocked labels, and none
//     of the labels or tags its account is denied.
//
// The ceiling and the unrated rule arrive already combined: the profile's own
// and its account's limit are folded into one admission age and one unrated
// answer before they reach this file (identity.ContentRestrictions.Narrow), so
// there is a single rating rule and a single scale.
//
// A container entity (a show, season, artist, album, book, author or collection)
// has no rating of its own, so it is visible when it holds at least one item the
// profile may see, or when it holds no items at all. That keeps an empty shelf
// visible while making a series of adult episodes disappear as a unit.

// content_rating_ages is the projection that lets the predicate stay pure SQL. It
// maps every contentRating spelling the catalog actually holds to the admission
// age the rating table in internal/identity resolves it to, or -1 for "unrated".
// Background ingest classifies newly published spellings. Pending spellings have
// no age row and are treated as unrated by the visibility predicate.
//
// restrictionProjection is per-Service, never package-level: a package-level cache
// would be shared between Services over different databases, and the first one to
// warm it would convince every other that a projection it has never built is
// already current — which fails open, admitting content nothing classified.
type restrictionProjection struct {
	mu       sync.Mutex
	revision int64
	loaded   bool
	after    string
	started  bool
}

// refreshRatingAges classifies the content-rating spellings the catalogue has
// published and this server has not seen before.
//
// It used to ask whether it had work to do by comparing `sum(revision) FROM
// library_revisions` against a cached value. That number changes about four
// times per catalogue row written, so while a scan was running the cache never
// held, and every restricted read re-ran a `SELECT DISTINCT` over the whole
// attribute table — 154 ms at three hundred thousand items, under a global mutex
// held across the scan *and* across up to five thousand separate write-gate
// acquisitions, on the request path.
//
// The question is now asked of a queue a trigger fills: a content rating arrives
// that `content_rating_ages` does not classify, and the trigger records the
// spelling. In steady state this is one probe of an empty table. When there is
// work, it is one gated transaction rather than one per row, and the mutex is
// held around the queue drain rather than around the database.
//
// This runs only in background ingest. An unrecognised spelling is classified
// as unrated (-1) rather than silently treated as suitable.
// ClassifyPendingRatings drains one bounded batch outside request paths
// (compactcatalog.ClassifyPendingRatings); the caller repeats while more work
// remains. The mutex keeps this process's drains from overlapping.
func (s *Service) ClassifyPendingRatings(ctx context.Context) (bool, error) {
	s.state.ratings.mu.Lock()
	defer s.state.ratings.mu.Unlock()
	// The first drain of a process also checks which rating table the stored
	// classifications were made with. A build that knows more spellings, or
	// reads one differently, corrects them here, once.
	if !s.state.ratings.loaded {
		if persistence.Get(s.db, ratingTableKey) != identity.RatingTableVersion {
			after, more, err := reclassifyRatingPage(ctx, s.db, s.state.ratings.after, s.state.ratings.started)
			if err != nil {
				return false, err
			}
			s.state.ratings.after, s.state.ratings.started = after, true
			if more {
				return true, nil
			}
			if _, err := dbwork.ExecWrite(ctx, s.db, dbwork.ClassBackgroundMedia, `INSERT INTO configuration VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, ratingTableKey, identity.RatingTableVersion); err != nil {
				return false, err
			}
		}
		s.state.ratings.loaded = true
	}
	return compactcatalog.ClassifyPendingRatings(ctx, s.db, identity.RatingAge)
}

const ratingTableKey = "rating_table_version"

// reclassifyRatingPage resumes the spelling-key walk after a rating table
// change. The cursor advances only after the page's writes commit. A restarted
// service may repeat pages safely; it records the table version only at EOF.
func reclassifyRatingPage(ctx context.Context, db *sql.DB, after string, started bool) (string, bool, error) {
	ctx = dbwork.WithClass(ctx, dbwork.ClassBackgroundMedia)
	query := `SELECT value_key,minimum_age FROM content_rating_ages`
	args := []any{}
	if started {
		query += ` WHERE value_key>?`
		args = append(args, after)
	}
	query += ` ORDER BY value_key LIMIT ?`
	args = append(args, compactcatalog.RatingClassificationBatch+1)
	rows, err := dbwork.ReadHandle(ctx, db).QueryContext(ctx, query, args...)
	if err != nil {
		return after, false, err
	}
	changed := map[string]int{}
	n, last := 0, after
	more := false
	for rows.Next() {
		var key string
		var stored int
		if err = rows.Scan(&key, &stored); err != nil {
			rows.Close()
			return after, false, err
		}
		if n == compactcatalog.RatingClassificationBatch {
			more = true
			break
		}
		n++
		last = key
		minimum := -1
		if resolved, ok := identity.RatingAge(key); ok {
			minimum = resolved
		}
		if minimum != stored {
			changed[key] = minimum
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return after, false, err
	}
	if len(changed) == 0 {
		return last, more, nil
	}
	err = dbwork.WithWriteTx(ctx, db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
		for key, minimum := range changed {
			if _, err := tx.ExecContext(ctx, `UPDATE content_rating_ages SET minimum_age=? WHERE value_key=?`, minimum, key); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return after, false, err
	}
	return last, more, nil
}

// itemRestrictionClause compiles the three clauses for one item-id expression.
// The expression must yield the INTEGER catalog_entities.id; every probe is an
// index seek. A missing entity fails closed through the EXISTS wrapper.
func itemRestrictionClause(item string, r identity.ContentRestrictions) (string, []any) {
	clauses := []string{}
	args := []any{}
	if r.MaximumAge != nil {
		clauses = append(clauses, `NOT EXISTS(SELECT 1 FROM catalog_item_attribute_edges ae JOIN catalog_attribute_terms term ON term.id=ae.term_id JOIN content_rating_ages ra ON ra.value_key=term.value_key WHERE ae.item_id=`+item+` AND term.field_id=1 AND ra.minimum_age>?)`)
		args = append(args, *r.MaximumAge)
	}
	if r.BlockUnrated {
		clauses = append(clauses, `EXISTS(SELECT 1 FROM catalog_item_attribute_edges ae JOIN catalog_attribute_terms term ON term.id=ae.term_id JOIN content_rating_ages ra ON ra.value_key=term.value_key WHERE ae.item_id=`+item+` AND term.field_id=1 AND ra.minimum_age>=0)`)
	}
	if len(r.BlockedLabels) > 0 {
		marks := make([]string, len(r.BlockedLabels))
		for i, label := range r.BlockedLabels {
			marks[i] = "?"
			args = append(args, strings.ToLower(label))
		}
		clauses = append(clauses, `NOT EXISTS(SELECT 1 FROM catalog_item_attribute_edges ae JOIN catalog_attribute_terms term ON term.id=ae.term_id WHERE ae.item_id=`+item+` AND term.field_id=2 AND term.value_key IN(`+strings.Join(marks, ",")+`))`)
	}
	for _, label := range r.MemberDeniedLabels {
		clauses = append(clauses, `NOT EXISTS(SELECT 1 FROM catalog_item_attribute_edges ae JOIN catalog_attribute_terms term ON term.id=ae.term_id WHERE ae.item_id=`+item+` AND term.field_id IN(2,3) AND lower(ae.source_value)=?)`)
		args = append(args, strings.ToLower(label))
	}
	// Attribute field ids are the baseline seeds: 1 contentRating, 2 label,
	// 3 tag. Facts are written synchronously, so there is no projection gate;
	// callers hold the derived domains they read via compactProjectionReady.
	// Items in a retired library stay hidden, as before.
	where := `EXISTS(SELECT 1 FROM catalog_entities ci JOIN catalog_libraries lib ON lib.id=ci.library_id WHERE ci.id=` + item + ` AND lib.retired=0`
	if len(clauses) > 0 {
		where += ` AND ` + strings.Join(clauses, ` AND `)
	}
	return where + `)`, args
}

// ItemRestrictionSQL is the predicate for a surface whose rows are items. `item`
// must be a SQL expression yielding the INTEGER catalog_entities.id.
func ItemRestrictionSQL(item string, r identity.ContentRestrictions) (string, []any) {
	if !r.Active() {
		return "1", nil
	}
	return itemRestrictionClause(item, r)
}

// EntityRestrictionSQL is the predicate for a surface whose rows may be containers
// as well as items. `entity` must be a SQL expression yielding the INTEGER
// catalog_browse_rows.entity_id. Facts are synchronous; the membership join
// reads derived domain 19, which callers hold via compactProjectionReady.
func EntityRestrictionSQL(entity string, r identity.ContentRestrictions) (string, []any) {
	if !r.Active() {
		return "1", nil
	}
	inner, args := itemRestrictionClause("membership.item_id", r)
	clause := `EXISTS(SELECT 1 FROM catalog_browse_rows row WHERE row.entity_id=` + entity + `
	 AND (NOT EXISTS(SELECT 1 FROM catalog_browse_memberships membership WHERE membership.entity_id=row.entity_id)
	 OR EXISTS(SELECT 1 FROM catalog_browse_memberships membership WHERE membership.entity_id=row.entity_id AND ` + inner + `)))`
	return clause, args
}

// RestrictionFence folds the restriction identity into a viewer fence so an open
// cursor, a cached count or a rendered page cannot outlive a restriction change.
func RestrictionFence(r identity.ContentRestrictions) string {
	if !r.Active() {
		if r.BlockRecordings {
			return "-:no-recordings"
		}
		return "-"
	}
	var b strings.Builder
	if r.MaximumAge != nil {
		b.WriteString(itoa(int64(*r.MaximumAge)))
	}
	b.WriteByte(':')
	if r.BlockUnrated {
		b.WriteByte('u')
	}
	b.WriteByte(':')
	b.WriteString(itoa(r.Revision))
	b.WriteByte(':')
	b.WriteString(strings.ToLower(strings.Join(r.BlockedLabels, ",")))
	b.WriteByte(':')
	b.WriteString(strings.ToLower(strings.Join(r.MemberDeniedLabels, ",")))
	b.WriteByte(':')
	b.WriteString(itoa(r.MemberRevision))
	if r.BlockRecordings {
		b.WriteString(":no-recordings")
	}
	return b.String()
}

func itoa(v int64) string {
	negative := v < 0
	if negative {
		v = -v
	}
	if v == 0 {
		return "0"
	}
	var digits [21]byte
	i := len(digits)
	for v > 0 {
		i--
		digits[i] = byte('0' + v%10)
		v /= 10
	}
	if negative {
		i--
		digits[i] = '-'
	}
	return string(digits[i:])
}

// recordingsClause withholds published recordings from a single-item check
// when the profile's Recordings switch is off (P8). Lists do not need it: the
// recordings library is withheld from the viewer's libraries at the gate.
func recordingsClause(item string, r identity.ContentRestrictions) string {
	if !r.BlockRecordings {
		return ""
	}
	return ` AND NOT EXISTS(SELECT 1 FROM dvr_catalog_provenance recording WHERE recording.item_id=` + item + `)`
}

// RestrictedItem answers the predicate for one item without running a page query.
// The detail read and the playback authority use it, so a title that is filtered
// out of every list cannot be reached by knowing its id. `item` is the public
// id; unknown ids fail closed.
func (s *Service) RestrictedItem(ctx context.Context, r identity.ContentRestrictions, item string) (bool, error) {
	if item == "" {
		return false, nil
	}
	if r.BlockRecordings {
		var recording bool
		if err := s.read().QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM dvr_catalog_provenance WHERE item_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)))`, item).Scan(&recording); err != nil {
			return false, err
		}
		if recording {
			return true, nil
		}
	}
	if !r.Active() {
		return false, nil
	}
	// The entity form covers both an item id and a container id, because
	// catalog_browse_memberships maps an item to itself.
	// The clause names the row's integer id: an expression with its own
	// placeholder would need an argument for every place the clause repeats it.
	clause, args := EntityRestrictionSQL("restricted.id", r)
	var blocked bool
	err := s.read().QueryRowContext(ctx, `SELECT NOT(`+clause+`) FROM catalog_entities restricted WHERE restricted.public_id=pid_blob(?)`, append(args, item)...).Scan(&blocked)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return blocked, err
}

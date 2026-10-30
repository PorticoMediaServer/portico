package compactcatalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"github.com/PorticoMediaServer/portico/server/internal/personalstate"
	"time"
)

// Recommendation taste, per profile (rec_profile_* tables). Triggers queue a
// (profile, work) job on every personal fact that says something about taste
// (feeds.sql); StepRecProfiles turns the work's facts into one signal and
// moves the profile's taste by the difference: a watch costs the work's facets,
// never the profile's history. A reader overlays jobs not yet processed with
// the same computation (RecSignalFor), so taste is exact as soon as the
// personal write commits.
//
// Taste is a sum of signal weights over each work's recommendation facets at
// two horizons. Each contribution is stored scaled to the profile's epoch
// (value × 2^((at − epoch)/half-life)), so decay needs no rewriting: a reader
// multiplies by 2^(−(now − epoch)/half-life) (RecDecay).

// Half-lives of the two taste horizons, in days.
const (
	RecLongHalfLife  = 182.5
	RecShortHalfLife = 14.0
	// recRebaseDays is how far the epoch may fall behind before the profile's
	// rows are rescaled: 2^(1500/14) stays far inside float range.
	recRebaseDays = 1500.0
)

// Querier is what reads need: a transaction or a database.
type Querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// RecSignal is what one work says about a profile's taste.
type RecSignal struct {
	Weight   float64 // positive: more like this; negative: less
	Engaged  bool    // the profile has started, finished, saved or rated it
	Hidden   bool    // disliked or not interested: never recommended
	Finished bool    // the profile watched, read or listened to it to the end
	At       float64 // Unix days of the latest evidence
}

// RecStoredSignal is a signal as stored, with its epoch-scaled contribution.
type RecStoredSignal struct {
	RecSignal
	Long, Short float64
}

// UnixDays converts a time to Unix days.
func UnixDays(t time.Time) float64 { return float64(t.UnixMilli()) / 86400000 }

// RecDecay is the factor that turns an epoch-scaled value into today's value.
func RecDecay(epoch, now, halfLife float64) float64 { return math.Exp2(-(now - epoch) / halfLife) }

func recScale(weight, at, epoch float64) (long, short float64) {
	return weight * math.Exp2((at-epoch)/RecLongHalfLife), weight * math.Exp2((at-epoch)/RecShortHalfLife)
}

// recWorkMembers lists a work and its playable members (an episode's show,
// an album's songs, a book's files), each one indexed seek.
const recWorkMembers = `SELECT ?1 UNION ALL SELECT entity_id FROM catalog_episodes WHERE show_id=?1
 UNION ALL SELECT entity_id FROM catalog_songs WHERE album_id=?1 UNION ALL SELECT entity_id FROM catalog_book_files WHERE book_id=?1`

// RecSignalFor reads what a work's personal facts say about the profile now.
// now is used when nothing dated says when (a rating, a favorite).
func RecSignalFor(ctx context.Context, q Querier, profile string, work int64, now time.Time) (RecSignal, error) {
	var kind sql.NullInt64
	if err := q.QueryRowContext(ctx, `SELECT kind FROM catalog_entities WHERE id=?`, work).Scan(&kind); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return RecSignal{}, err
	}
	var finished, completions, favorite, watchlisted, notInterested, progress int
	var rating sql.NullFloat64
	var at sql.NullString
	err := q.QueryRowContext(ctx, `WITH m(id) AS (`+recWorkMembers+`),
	 done AS (SELECT m.id FROM m WHERE EXISTS(SELECT 1 FROM personal_history h INDEXED BY personal_history_item_id_fk WHERE h.item_id=m.id AND h.profile_id=?2 AND h.completed=1)
	  OR `+personalstate.SQL("?2", "m.id")+`=1)
	 SELECT (SELECT count(*) FROM done),
	  (SELECT count(*) FROM m CROSS JOIN personal_history h INDEXED BY personal_history_item_id_fk ON h.item_id=m.id WHERE h.profile_id=?2 AND h.completed=1),
	  COALESCE((SELECT max(p.favorite) FROM m CROSS JOIN personal_items p ON p.profile_id=?2 AND p.item_id=m.id),0),
	  COALESCE((SELECT max(p.watchlisted) FROM m CROSS JOIN personal_items p ON p.profile_id=?2 AND p.item_id=m.id),0),
	  COALESCE((SELECT max(p.not_interested) FROM m CROSS JOIN personal_items p ON p.profile_id=?2 AND p.item_id=m.id),0),
	  (SELECT avg(p.rating) FROM m CROSS JOIN personal_items p ON p.profile_id=?2 AND p.item_id=m.id WHERE p.rating IS NOT NULL),
	  EXISTS(SELECT 1 FROM m CROSS JOIN progress g ON g.profile_id=?2 AND g.item_id=m.id WHERE g.position>0),
	  (SELECT max(v) FROM (
	   SELECT max(NULLIF(p.last_played_at,'')) v FROM m CROSS JOIN personal_items p ON p.profile_id=?2 AND p.item_id=m.id
	   UNION ALL SELECT max(h.updated_at) FROM m CROSS JOIN personal_history h INDEXED BY personal_history_item_id_fk ON h.item_id=m.id WHERE h.profile_id=?2 AND h.completed=1
	   UNION ALL SELECT max(w.authored_at) FROM m CROSS JOIN personal_watched_intents w ON w.profile_id=?2 AND w.item_id=m.id WHERE w.watched=1))`,
		work, profile).Scan(&finished, &completions, &favorite, &watchlisted, &notInterested, &rating, &progress, &at)
	if err != nil {
		return RecSignal{}, err
	}
	s := RecSignal{At: UnixDays(now)}
	if at.Valid {
		if t, e := time.Parse(time.RFC3339Nano, at.String); e == nil {
			s.At = UnixDays(t)
		}
	}
	if finished > 0 {
		switch kind.Int64 {
		case int64(Show):
			// A show counts more the further in the profile got.
			s.Weight += 1 + math.Min(1, 0.1*float64(finished-1))
		case int64(Album):
			s.Weight += 0.6 + math.Min(0.6, 0.05*float64(completions-1))
		default:
			s.Weight++
			if completions > 1 {
				s.Weight += 0.5 // watched again
			}
		}
	}
	if favorite == 1 {
		s.Weight += 2
	}
	if rating.Valid {
		// Ratings are kept from 0 to 10, two points to a star: three stars
		// says nothing, five stars adds 1.6, one star takes 1.6 away.
		s.Weight += (rating.Float64/2 - 3) * 0.8
	}
	if watchlisted == 1 {
		s.Weight += 0.5
	}
	if notInterested == 1 {
		s.Weight--
	}
	s.Engaged = finished > 0 || progress == 1 || favorite == 1 || rating.Valid || watchlisted == 1
	s.Hidden = notInterested == 1
	s.Finished = finished > 0
	return s, nil
}

// RecStored reads a work's stored signal (ok false when none).
func RecStored(ctx context.Context, q Querier, profile string, work int64) (RecStoredSignal, bool, error) {
	var s RecStoredSignal
	var engaged, hidden, finished int
	var at string
	err := q.QueryRowContext(ctx, `SELECT weight,engaged,hidden,finished,at,long,short FROM rec_profile_signals WHERE profile_id=? AND work_id=?`, profile, work).
		Scan(&s.Weight, &engaged, &hidden, &finished, &at, &s.Long, &s.Short)
	if errors.Is(err, sql.ErrNoRows) {
		return s, false, nil
	}
	if err != nil {
		return s, false, err
	}
	s.Engaged, s.Hidden, s.Finished = engaged == 1, hidden == 1, finished == 1
	if t, e := time.Parse(time.RFC3339Nano, at); e == nil {
		s.At = UnixDays(t)
	}
	return s, true, nil
}

// RecEpoch is the profile's epoch (ok false for a profile with no taste yet).
func RecEpoch(ctx context.Context, q Querier, profile string) (epoch float64, revision int64, ok bool, err error) {
	err = q.QueryRowContext(ctx, `SELECT epoch,revision FROM rec_profile_revisions WHERE profile_id=?`, profile).Scan(&epoch, &revision)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, false, nil
	}
	return epoch, revision, err == nil, err
}

// RecWorkFacets are the recommendation facets of a work.
func RecWorkFacets(ctx context.Context, q Querier, work int64) ([]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT facet FROM catalog_rec_facets WHERE entity_id=?`, work)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var f string
		if err = rows.Scan(&f); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// StepRecProfiles processes up to limit taste jobs.
func StepRecProfiles(ctx context.Context, tx *sql.Tx, limit int) (int, error) {
	rows, err := tx.QueryContext(ctx, `SELECT profile_id,work_id FROM rec_profile_jobs ORDER BY profile_id,work_id LIMIT ?`, limit)
	if err != nil {
		return 0, err
	}
	type job struct {
		profile string
		work    int64
	}
	var jobs []job
	for rows.Next() {
		var j job
		if err = rows.Scan(&j.profile, &j.work); err != nil {
			rows.Close()
			return 0, err
		}
		jobs = append(jobs, j)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return 0, err
	}
	now := time.Now()
	for _, j := range jobs {
		if err = applyRecJob(ctx, tx, j.profile, j.work, now); err != nil {
			return 0, err
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM rec_profile_jobs WHERE profile_id=? AND work_id=?`, j.profile, j.work); err != nil {
			return 0, err
		}
	}
	return len(jobs), nil
}

func applyRecJob(ctx context.Context, tx *sql.Tx, profile string, work int64, now time.Time) error {
	// Removing media never rewrites taste: a job for a work that no longer
	// exists (its personal rows went with it) leaves the signal as it was.
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM catalog_entities WHERE id=?)`, work).Scan(&exists); err != nil || !exists {
		return err
	}
	next, err := RecSignalFor(ctx, tx, profile, work, now)
	if err != nil {
		return err
	}
	// The epoch first: moving it rescales the stored signal, and the old
	// contribution taken back must be the rescaled one.
	epoch, err := recEpochFor(ctx, tx, profile, next.At)
	if err != nil {
		return err
	}
	old, had, err := RecStored(ctx, tx, profile, work)
	if err != nil {
		return err
	}
	if had && old.RecSignal == next {
		return nil
	}
	long, short := recScale(next.Weight, next.At, epoch)
	if dl, ds := long-old.Long, short-old.Short; dl != 0 || ds != 0 {
		facets, err := RecWorkFacets(ctx, tx, work)
		if err != nil {
			return err
		}
		for _, f := range facets {
			if err = addTaste(ctx, tx, profile, f, dl, ds); err != nil {
				return err
			}
		}
	}
	if next.Weight == 0 && !next.Engaged && !next.Hidden && !next.Finished {
		_, err = tx.ExecContext(ctx, `DELETE FROM rec_profile_signals WHERE profile_id=? AND work_id=?`, profile, work)
	} else {
		_, err = tx.ExecContext(ctx, `INSERT INTO rec_profile_signals(profile_id,work_id,weight,engaged,hidden,finished,at,long,short) VALUES(?,?,?,?,?,?,?,?,?)
		 ON CONFLICT(profile_id,work_id) DO UPDATE SET weight=excluded.weight,engaged=excluded.engaged,hidden=excluded.hidden,finished=excluded.finished,at=excluded.at,long=excluded.long,short=excluded.short`,
			profile, work, next.Weight, b2i(next.Engaged), b2i(next.Hidden), b2i(next.Finished), recDaysText(next.At), long, short)
	}
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE rec_profile_revisions SET revision=revision+1 WHERE profile_id=?`, profile)
	return err
}

func addTaste(ctx context.Context, tx *sql.Tx, profile, facet string, long, short float64) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO rec_profile_taste(profile_id,facet,long,short) VALUES(?,?,?,?)
	 ON CONFLICT(profile_id,facet) DO UPDATE SET long=long+excluded.long,short=short+excluded.short`, profile, facet, long, short)
	return err
}

// recEpochFor returns the profile's epoch, creating it, or moving it forward
// (rescaling every stored contribution) when at has run far ahead of it.
func recEpochFor(ctx context.Context, tx *sql.Tx, profile string, at float64) (float64, error) {
	epoch, _, ok, err := RecEpoch(ctx, tx, profile)
	if err != nil {
		return 0, err
	}
	if !ok {
		epoch = math.Floor(at)
		_, err = tx.ExecContext(ctx, `INSERT INTO rec_profile_revisions(profile_id,revision,epoch) VALUES(?,1,?)`, profile, epoch)
		return epoch, err
	}
	if at-epoch <= recRebaseDays {
		return epoch, nil
	}
	next := math.Floor(at)
	fl, fs := math.Exp2(-(next-epoch)/RecLongHalfLife), math.Exp2(-(next-epoch)/RecShortHalfLife)
	for _, table := range []string{"rec_profile_taste", "rec_profile_signals"} {
		if _, err = tx.ExecContext(ctx, `UPDATE `+table+` SET long=long*?,short=short*? WHERE profile_id=?`, fl, fs, profile); err != nil {
			return 0, err
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE rec_profile_revisions SET epoch=? WHERE profile_id=?`, next, profile)
	return next, err
}

// recRefacet moves the taste of every profile holding a signal on a work
// whose facets changed: removed facets take the signal back, added ones give
// it. A work's facets change rarely; the profiles holding signals on it are
// read by index.
func recRefacet(ctx context.Context, tx *sql.Tx, work int64, added, removed []string) error {
	if len(added) == 0 && len(removed) == 0 {
		return nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT profile_id,long,short FROM rec_profile_signals INDEXED BY rec_profile_signals_work WHERE work_id=? AND (long<>0 OR short<>0)`, work)
	if err != nil {
		return err
	}
	type held struct {
		profile     string
		long, short float64
	}
	var signals []held
	for rows.Next() {
		var h held
		if err = rows.Scan(&h.profile, &h.long, &h.short); err != nil {
			rows.Close()
			return err
		}
		signals = append(signals, h)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	for _, h := range signals {
		for _, f := range added {
			if err = addTaste(ctx, tx, h.profile, f, h.long, h.short); err != nil {
				return err
			}
		}
		for _, f := range removed {
			if err = addTaste(ctx, tx, h.profile, f, -h.long, -h.short); err != nil {
				return err
			}
		}
		if _, err = tx.ExecContext(ctx, `UPDATE rec_profile_revisions SET revision=revision+1 WHERE profile_id=?`, h.profile); err != nil {
			return err
		}
	}
	return nil
}

func recDaysText(days float64) string {
	return time.UnixMilli(int64(math.Round(days * 86400000))).UTC().Format("2006-01-02T15:04:05.000Z")
}

func b2i(v bool) int {
	if v {
		return 1
	}
	return 0
}

// CheckRecPostings verifies the best-first lists and rarity counts against
// the facets they're kept from (for tests and diagnostics; it reads the
// tables whole).
func CheckRecPostings(ctx context.Context, db *sql.DB) error {
	works := `SELECT f.entity_id,f.facet FROM catalog_rec_facets f JOIN catalog_entities e ON e.id=f.entity_id AND e.retired=0 AND e.kind IN(1,2,6,8)
	 UNION SELECT e.id,'*' FROM catalog_entities e WHERE e.retired=0 AND e.kind IN(1,2,6,8)`
	checks := []struct{ what, query string }{
		{"postings without a facet", `SELECT count(*) FROM (SELECT entity_id,facet FROM catalog_rec_postings EXCEPT SELECT * FROM (` + works + `))`},
		{"facets without a posting", `SELECT count(*) FROM (SELECT * FROM (` + works + `) EXCEPT SELECT entity_id,facet FROM catalog_rec_postings)`},
		{"rarity counts that disagree", `SELECT count(*) FROM (SELECT facet,works FROM catalog_rec_df EXCEPT SELECT facet,count(DISTINCT entity_id) FROM catalog_rec_postings GROUP BY facet)`},
		{"facets without a rarity count", `SELECT count(*) FROM (SELECT facet,count(DISTINCT entity_id) FROM catalog_rec_postings GROUP BY facet EXCEPT SELECT facet,works FROM catalog_rec_df)`},
	}
	for _, c := range checks {
		var n int
		if err := db.QueryRowContext(ctx, c.query).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			return fmt.Errorf("recommendation postings: %d %s", n, c.what)
		}
	}
	return nil
}

// CheckRecTaste verifies every profile's stored taste against the sum of its
// stored signals over each work's current facets.
func CheckRecTaste(ctx context.Context, db *sql.DB) error {
	type key struct{ profile, facet string }
	want := map[key][2]float64{}
	rows, err := db.QueryContext(ctx, `SELECT s.profile_id,f.facet,sum(s.long),sum(s.short) FROM rec_profile_signals s JOIN catalog_rec_facets f ON f.entity_id=s.work_id GROUP BY 1,2`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var k key
		var v [2]float64
		if err = rows.Scan(&k.profile, &k.facet, &v[0], &v[1]); err != nil {
			rows.Close()
			return err
		}
		want[k] = v
	}
	rows.Close()
	rows, err = db.QueryContext(ctx, `SELECT profile_id,facet,long,short FROM rec_profile_taste`)
	if err != nil {
		return err
	}
	defer rows.Close()
	near := func(a, b float64) bool { return math.Abs(a-b) <= 1e-9*math.Max(1, math.Max(math.Abs(a), math.Abs(b))) }
	for rows.Next() {
		var k key
		var v [2]float64
		if err = rows.Scan(&k.profile, &k.facet, &v[0], &v[1]); err != nil {
			return err
		}
		w := want[k]
		if !near(v[0], w[0]) || !near(v[1], w[1]) {
			return fmt.Errorf("recommendation taste: %s %s is %v, its signals give %v", k.profile, k.facet, v, w)
		}
		delete(want, k)
	}
	for k, w := range want {
		if !near(w[0], 0) || !near(w[1], 0) {
			return fmt.Errorf("recommendation taste: %s %s is missing, its signals give %v", k.profile, k.facet, w)
		}
	}
	return rows.Err()
}

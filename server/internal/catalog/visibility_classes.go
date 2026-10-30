package catalog

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

var ErrVisibilityBuilding = compactcatalog.ErrBuilding

type visibilityRequest struct {
	library      string
	restrictions identity.ContentRestrictions
}

func (s *Service) queueVisibilityClass(library string, r identity.ContentRestrictions) {
	if s == nil || s.state == nil {
		return
	}
	key, _ := visibilityClassKey(library, r)
	clone := r
	clone.BlockedLabels = append([]string{}, r.BlockedLabels...)
	clone.MemberDeniedLabels = append([]string{}, r.MemberDeniedLabels...)
	if r.MaximumAge != nil {
		age := *r.MaximumAge
		clone.MaximumAge = &age
	}
	s.state.visibilityMu.Lock()
	s.state.visibilityRequests[key] = visibilityRequest{library, clone}
	s.state.visibilityMu.Unlock()
	s.state.visibilityWake.Wake()
}

// RunVisibilityRebuilder publishes visibility snapshots in the background and
// keeps them current: a class is built once, then updated entity by entity from
// its journal (compact_visibility_dirty, filled by triggers on the facts a
// class reads).
func (s *Service) RunVisibilityRebuilder(ctx context.Context) {
	if s == nil || s.state == nil {
		return
	}
	ctx = dbwork.WithClass(ctx, dbwork.ClassBackgroundMedia)
	unregister := dbwork.WakeOnTables(s.state.visibilityWake,
		"compact_visibility_dirty", "compact_visibility_classes")
	defer unregister()
	for {
		if ctx.Err() != nil {
			return
		}
		s.state.visibilityMu.Lock()
		requests := make([]visibilityRequest, 0, len(s.state.visibilityRequests))
		for _, request := range s.state.visibilityRequests {
			requests = append(requests, request)
		}
		s.state.visibilityMu.Unlock()
		stored, err := s.storedVisibilityRequests(ctx)
		if err != nil {
			log.Printf("visibility policy registry: %v", err)
		}
		seenClasses := map[string]bool{}
		for _, request := range requests {
			key, _ := visibilityClassKey(request.library, request.restrictions)
			seenClasses[key] = true
		}
		for _, request := range stored {
			key, _ := visibilityClassKey(request.library, request.restrictions)
			if !seenClasses[key] {
				requests = append(requests, request)
			}
		}
		for _, request := range requests {
			// The owner's pause holds the builder between classes too.
			if !dbwork.WaitBackground(ctx) {
				return
			}
			// A library without a revision row is gone or not yet created.
			_, err := s.visibilityRevision(ctx, request.library)
			if err == sql.ErrNoRows {
				continue
			}
			if err != nil {
				log.Printf("visibility revision: %v", err)
				continue
			}
			key, _ := visibilityClassKey(request.library, request.restrictions)
			var classID, libraryID, generation int64
			var dirty bool
			err = dbwork.ReadHandle(ctx, s.db).QueryRowContext(ctx,
				`SELECT c.id,c.library_id,c.active_generation,EXISTS(SELECT 1 FROM compact_visibility_dirty d WHERE d.class_id=c.id)
				 FROM compact_visibility_classes c JOIN catalog_libraries l ON l.id=c.library_id
				 WHERE c.class_key=? AND l.library_id=? AND l.retired=0 AND c.retired=0`, key, request.library).Scan(&classID, &libraryID, &generation, &dirty)
			if err == nil && generation > 0 && !dirty {
				continue
			}
			if err != nil && err != sql.ErrNoRows {
				log.Printf("visibility class: %v", err)
				continue
			}
			if err == nil && generation > 0 {
				err = s.refreshVisibilityClass(ctx, request.library, request.restrictions, classID, libraryID, generation)
			} else {
				err = s.RebuildVisibilityClass(ctx, request.library, request.restrictions)
			}
			if err != nil &&
				!errors.Is(err, ErrStaleContinuation) && ctx.Err() == nil {
				log.Printf("visibility class rebuild: %v", err)
			}
		}
		if !s.state.visibilityWake.Wait(ctx) {
			return
		}
	}
}

func (s *Service) storedVisibilityRequests(ctx context.Context) ([]visibilityRequest, error) {
	rows, err := dbwork.ReadHandle(ctx, s.db).QueryContext(ctx,
		`SELECT l.library_id,c.policy_json FROM compact_visibility_classes c
		 JOIN catalog_libraries l ON l.id=c.library_id WHERE l.retired=0 AND c.retired=0`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []visibilityRequest{}
	for rows.Next() {
		var library, raw string
		if err = rows.Scan(&library, &raw); err != nil {
			return nil, err
		}
		var policy visibilityPolicy
		if err = json.Unmarshal([]byte(raw), &policy); err != nil {
			return nil, err
		}
		out = append(out, visibilityRequest{library: library, restrictions: identity.ContentRestrictions{
			MaximumAge: policy.MaximumAge, BlockUnrated: policy.BlockUnrated,
			BlockedLabels: policy.BlockedLabels, MemberDeniedLabels: policy.MemberDeniedLabels,
		}})
	}
	return out, rows.Err()
}

// A visibility class contains the policy facts that change title membership.
// Profile/account IDs and policy revisions are deliberately absent: two viewers
// with the same restrictions share one read model. There are only as many
// classes as distinct policies actually configured by the owner.
type visibilityPolicy struct {
	MaximumAge         *int     `json:"maximumAge,omitempty"`
	BlockUnrated       bool     `json:"blockUnrated"`
	BlockedLabels      []string `json:"blockedLabels"`
	MemberDeniedLabels []string `json:"memberDeniedLabels"`
}

func canonicalLabels(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" && !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	sort.Strings(out)
	return out
}

func visibilityClassKey(library string, r identity.ContentRestrictions) (string, string) {
	p := visibilityPolicy{
		MaximumAge: r.MaximumAge, BlockUnrated: r.BlockUnrated,
		BlockedLabels:      canonicalLabels(r.BlockedLabels),
		MemberDeniedLabels: canonicalLabels(r.MemberDeniedLabels),
	}
	raw, _ := json.Marshal(p)
	digest := sha256.Sum256(append(append([]byte(library), 0), raw...))
	return hex.EncodeToString(digest[:]), string(raw)
}

func (s *Service) visibilityRevision(ctx context.Context, library string) (int64, error) {
	var revision int64
	err := dbwork.ReadHandle(ctx, s.db).QueryRowContext(ctx,
		`SELECT revision FROM library_revisions WHERE library_id=?`, library).Scan(&revision)
	return revision, err
}

func (s *Service) publishedVisibilityClass(library string, r identity.ContentRestrictions) (string, int64, bool, error) {
	key, _ := visibilityClassKey(library, r)
	if err := s.compactProjectionReady(18, 19, 20); err != nil {
		if errors.Is(err, ErrVisibilityBuilding) {
			return key, 0, false, nil
		}
		return key, 0, false, err
	}
	var current, built, generation int64
	if err := s.read().QueryRow(`SELECT revision FROM library_revisions WHERE library_id=?`, library).Scan(&current); err != nil {
		return key, 0, false, err
	}
	err := s.read().QueryRow(`SELECT c.active_generation,c.catalog_revision FROM compact_visibility_classes c
	 JOIN catalog_libraries l ON l.id=c.library_id
	 WHERE c.class_key=? AND l.library_id=? AND l.retired=0 AND c.retired=0`, key, library).Scan(&generation, &built)
	if err != nil && err != sql.ErrNoRows {
		return key, 0, false, err
	}
	if err == nil && generation > 0 {
		if built != current {
			s.queueVisibilityClass(library, r)
		}
		return key, generation, true, nil
	}
	s.queueVisibilityClass(library, r)
	return key, 0, false, nil
}

// publishedClassesSQL is a first test of an entity (an integer id
// expression) against the published visibility classes of the viewer's
// libraries: false when its library's class does not hold it. A container
// the viewer may see none of is then refused by one key lookup, before the
// live restriction walks its members. A library whose class is not yet
// published passes, and the live restriction decides alone.
func (s *Service) publishedClassesSQL(entity string, libraries []string, r identity.ContentRestrictions) (string, error) {
	if !r.Active() {
		return "1", nil
	}
	arms := []string{}
	for _, library := range libraries {
		key, generation, ready, err := s.publishedVisibilityClass(library, r)
		if err != nil {
			return "", err
		}
		if !ready {
			continue
		}
		var classID, libraryID int64
		if err = s.read().QueryRow(`SELECT c.id,c.library_id FROM compact_visibility_classes c WHERE c.class_key=?`, key).Scan(&classID, &libraryID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return "", err
		}
		arms = append(arms, fmt.Sprintf(` WHEN %d THEN EXISTS(SELECT 1 FROM compact_visibility_rows v WHERE v.class_id=%d AND v.generation=%d AND v.entity_id=%s)`, libraryID, classID, generation, entity))
	}
	if len(arms) == 0 {
		return "1", nil
	}
	return `(CASE (SELECT library_id FROM catalog_entities WHERE id=` + entity + `)` + strings.Join(arms, "") + ` ELSE 1 END)`, nil
}

func (s *Service) visibilitySummary(library string, kinds []string, r identity.ContentRestrictions) (browseSummary, bool, error) {
	out := browseSummary{headStart: map[string]int{}}
	key, generation, ready, err := s.publishedVisibilityClass(library, r)
	if err != nil || !ready {
		return out, ready, err
	}
	if err = s.read().QueryRow(`SELECT id FROM compact_visibility_classes WHERE class_key=?`, key).Scan(&out.classID); err != nil {
		return out, false, err
	}
	if len(kinds) == 0 {
		out.exact, out.classKey, out.generation = true, key, generation
		return out, true, nil
	}
	args := []any{key, generation, library}
	for _, name := range kinds {
		kind, parseErr := compactcatalog.ParseKind(name)
		if parseErr != nil {
			return browseSummary{}, false, parseErr
		}
		args = append(args, int(kind))
	}
	rows, err := s.read().Query(`SELECT vc.head,sum(vc.total) FROM compact_visibility_counts vc
	 JOIN compact_visibility_classes c ON c.id=vc.class_id
	 JOIN catalog_libraries l ON l.id=vc.library_id
	 WHERE c.class_key=? AND vc.generation=? AND l.library_id=? AND c.retired=0 AND vc.kind IN (`+
		placeholders(len(kinds))+`) GROUP BY vc.head ORDER BY vc.head COLLATE NOCASE`, args...)
	if err != nil {
		return out, false, err
	}
	seen := map[string]bool{}
	for rows.Next() {
		var head string
		var count int
		if err = rows.Scan(&head, &count); err != nil {
			break
		}
		out.headStart[head] = out.total
		letter := browseHeadLetter(head)
		if !seen[letter] {
			out.letter = append(out.letter, BrowsePositionAnchor{Key: letter, Index: out.total})
			seen[letter] = true
		}
		out.total += count
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return browseSummary{}, false, err
	}
	out.exact, out.classKey, out.generation = true, key, generation
	return out, true, nil
}

func (s *Service) visibilitySmallLibrary(library string) (bool, error) {
	var large bool
	err := s.read().QueryRow(`SELECT EXISTS(SELECT 1 FROM catalog_browse_rows
	 WHERE library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) LIMIT 1 OFFSET 1000)`, library).Scan(&large)
	return !large, err
}

// RebuildVisibilityClass publishes one library/policy snapshot. It is an
// explicit background operation: request paths never call it. Rows are built
// in keyset batches and a generation stays invisible until counts and anchors
// are complete and the library revision still matches under the gate.
// visibilityChunkSQL bounds the next 100 rows of a library a class build
// reads. It walks the rows in id order from the cursor, so a whole build reads
// each row once: through a library index the planner would sort the library's
// every row for each chunk, a class build quadratic in the library.
const visibilityChunkSQL = `SELECT max(entity_id) FROM (SELECT entity_id FROM catalog_browse_rows NOT INDEXED
 WHERE library_id=? AND entity_id>? ORDER BY entity_id LIMIT 100)`

func (s *Service) RebuildVisibilityClass(ctx context.Context, library string, r identity.ContentRestrictions) error {
	if s == nil || s.db == nil || library == "" {
		return errors.New("missing visibility class scope")
	}
	ctx = dbwork.WithClass(ctx, dbwork.ClassBackgroundMedia)
	if err := s.compactProjectionReady(18, 19, 20); err != nil {
		return err
	}
	key, policy := visibilityClassKey(library, r)
	revision, err := s.visibilityRevision(ctx, library)
	if err != nil {
		return err
	}
	var libraryID int64
	if err = dbwork.ReadHandle(ctx, s.db).QueryRowContext(ctx,
		`SELECT id FROM catalog_libraries WHERE library_id=? AND retired=0`, library).Scan(&libraryID); err != nil {
		return err
	}
	var classID, active, target int64
	err = dbwork.WithWriteTx(ctx, s.db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx,
			`INSERT INTO compact_visibility_classes(class_key,library_id,policy_json,active_generation,catalog_revision,requested_ms,built_ms,retired)
			 VALUES(?,?,?,0,?,?,0,0)
			 ON CONFLICT(class_key) DO UPDATE SET policy_json=excluded.policy_json,requested_ms=excluded.requested_ms,retired=0
			 RETURNING id,active_generation`, key, libraryID, policy, revision, time.Now().UnixMilli()).Scan(&classID, &active); err != nil {
			return err
		}
		var err error
		target, revision, err = captureVisibilityTargetTx(ctx, tx, classID, library)
		return err
	})
	if err != nil {
		return err
	}
	generation := active + 1
	if err = s.settleVisibilitySourceTarget(ctx, target); err != nil {
		return err
	}
	if err = s.clearVisibilityDirtyThrough(ctx, classID, target); err != nil {
		return err
	}
	var endEntity int64
	if err = dbwork.ReadHandle(ctx, s.db).QueryRowContext(ctx, `SELECT COALESCE(max(entity_id),0) FROM catalog_browse_rows`).Scan(&endEntity); err != nil {
		return err
	}
	if err = s.clearVisibilityGeneration(ctx, classID, generation); err != nil {
		return err
	}
	clause, clauseArgs := visibilityMembershipSQL(`e.entity_id`, r)
	var cursor int64
	started := time.Now()
	for {
		if err = visibilityBoundary(ctx, started); err != nil {
			return err
		}
		started = time.Now()
		var upper sql.NullInt64
		err = dbwork.ReadHandle(ctx, s.db).QueryRowContext(ctx,
			`SELECT max(entity_id) FROM (SELECT entity_id FROM catalog_browse_rows WHERE entity_id>? AND entity_id<=? ORDER BY entity_id LIMIT 100)`, cursor, endEntity).Scan(&upper)
		if err != nil {
			return err
		}
		if !upper.Valid {
			break
		}
		args := append([]any{libraryID, cursor, upper.Int64}, clauseArgs...)
		rows, queryErr := dbwork.ReadHandle(ctx, s.db).QueryContext(ctx,
			`SELECT e.entity_id,e.kind,e.sort_key,e.head,COALESCE((e.year/10)*10,0),e.available,`+visibleRowKeys+`
			 FROM catalog_browse_rows e NOT INDEXED JOIN catalog_entities ce ON ce.id=e.entity_id
			 WHERE e.library_id=? AND e.entity_id>? AND e.entity_id<=? AND ce.retired=0 AND `+clause+
				` ORDER BY e.entity_id`, args...)
		if queryErr != nil {
			return queryErr
		}
		type entity struct {
			id                int64
			kind              int
			sort, head        string
			decade            int
			available         bool
			added             string
			year              int
			duration, rating  float64
			recent            sql.NullString
			random            int64
			availabilityState string
		}
		batch := make([]entity, 0, 100)
		for rows.Next() {
			var e entity
			if queryErr = rows.Scan(&e.id, &e.kind, &e.sort, &e.head, &e.decade, &e.available, &e.added, &e.year, &e.duration, &e.rating, &e.recent, &e.random, &e.availabilityState); queryErr != nil {
				break
			}
			batch = append(batch, e)
		}
		if queryErr == nil {
			queryErr = rows.Err()
		}
		rows.Close()
		if queryErr != nil {
			return queryErr
		}
		for off := 0; off < len(batch); off += compactcatalog.BatchLimit {
			if off > 0 {
				if err = visibilityBoundary(ctx, started); err != nil {
					return err
				}
				started = time.Now()
			}
			chunk := batch[off:min(off+compactcatalog.BatchLimit, len(batch))]
			err = dbwork.WithWriteTx(ctx, s.db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
				for _, e := range chunk {
					if _, writeErr := tx.ExecContext(ctx,
						`INSERT INTO compact_visibility_rows(class_id,generation,entity_id,library_id,kind,sort_key,head,decade,added,year,duration,rating,recent,random_key,availability_state) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
						classID, generation, e.id, libraryID, e.kind, e.sort, e.head, e.decade, e.added, e.year, e.duration, e.rating, e.recent, e.random, e.availabilityState); writeErr != nil {
						return writeErr
					}
					if _, writeErr := tx.ExecContext(ctx,
						`INSERT INTO compact_visibility_counts(class_id,generation,library_id,kind,head,decade,total) VALUES(?,?,?,?,?,?,1)
						 ON CONFLICT(class_id,generation,library_id,kind,head,decade) DO UPDATE SET total=total+1`,
						classID, generation, libraryID, e.kind, e.head, e.decade); writeErr != nil {
						return writeErr
					}
					if !e.available {
						if writeErr := markVisibilityUnavailable(ctx, tx, classID, generation, libraryID, e.kind, e.id, true); writeErr != nil {
							return writeErr
						}
					}
				}
				return nil
			})
			if err != nil {
				return err
			}
		}
		cursor = upper.Int64
	}
	// Apply all captured-target journal rows to this candidate. Later events
	// retain their own markers and do not extend this finite publication pass.
	for {
		n := 0
		err = dbwork.WithWriteTx(ctx, s.db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
			rows, e := tx.QueryContext(ctx, `SELECT entity_id FROM compact_visibility_dirty INDEXED BY compact_visibility_dirty_frontier WHERE class_id=? AND first_sequence<=? ORDER BY first_sequence,entity_id LIMIT ?`, classID, target, compactcatalog.BatchLimit)
			if e != nil {
				return e
			}
			var ids []int64
			for rows.Next() {
				var id int64
				if e = rows.Scan(&id); e != nil {
					rows.Close()
					return e
				}
				ids = append(ids, id)
			}
			e = rows.Err()
			rows.Close()
			if e != nil {
				return e
			}
			for _, id := range ids {
				if e = updateVisibleEntity(ctx, tx, classID, generation, libraryID, id, clause, clauseArgs); e != nil {
					return e
				}
				if _, e = tx.ExecContext(ctx, `DELETE FROM compact_visibility_dirty WHERE class_id=? AND entity_id=?`, classID, id); e != nil {
					return e
				}
			}
			n = len(ids)
			return nil
		})
		if err != nil {
			return err
		}
		if n == 0 {
			break
		}
	}
	published := false
	if err = dbwork.WithWriteTx(ctx, s.db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
		ready, err := visibilityTargetReadyTx(ctx, tx, classID, target)
		if err != nil || !ready {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE compact_visibility_classes SET active_generation=?,catalog_revision=?,captured_sequence=?,built_ms=? WHERE id=?`, generation, revision, target, time.Now().UnixMilli(), classID)
		published = err == nil
		return err
	}); err != nil {
		return err
	}
	if !published {
		return ErrVisibilityBuilding
	}

	if active > 0 {
		if err = s.clearVisibilityGeneration(ctx, classID, active); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) clearVisibilityGeneration(ctx context.Context, classID, generation int64) error {
	// The blocks go first: each row's leave trigger then finds no block to
	// maintain and costs two index probes.
	if _, err := dbwork.ExecWrite(ctx, s.db, dbwork.ClassBackgroundMedia,
		`DELETE FROM compact_visibility_blocks WHERE class_id=? AND generation=?`, classID, generation); err != nil {
		return err
	}
	started := time.Now()
	for {
		if err := visibilityBoundary(ctx, started); err != nil {
			return err
		}
		started = time.Now()
		result, err := dbwork.ExecWrite(ctx, s.db, dbwork.ClassBackgroundMedia,
			`DELETE FROM compact_visibility_rows WHERE class_id=? AND generation=? AND entity_id IN
 (SELECT entity_id FROM compact_visibility_rows WHERE class_id=? AND generation=? LIMIT 8)`,
			classID, generation, classID, generation)
		if err != nil {
			return err
		}
		n, _ := result.RowsAffected()
		if n == 0 {
			break
		}
	}
	for _, table := range []string{"compact_visibility_counts", "compact_visibility_unavailable", "compact_visibility_unavailable_counts"} {
		if _, err := dbwork.ExecWrite(ctx, s.db, dbwork.ClassBackgroundMedia,
			`DELETE FROM `+table+` WHERE class_id=? AND generation=?`, classID, generation); err != nil {
			return err
		}
	}
	return nil
}

// visibilityMembershipSQL matches hierarchy membership rules using integer
// catalogue ids: a collection can be empty, other containers need members, and
// shows/seasons need a linked episode that passes the viewer's restrictions.
func visibilityMembershipSQL(entity string, r identity.ContentRestrictions) (string, []any) {
	clause, args := EntityRestrictionSQL(entity, r)
	memberRestriction, memberArgs := ItemRestrictionSQL("sx.item_id", r)
	return `(e.kind=10 OR EXISTS(SELECT 1 FROM catalog_browse_memberships present WHERE present.entity_id=` + entity + `))
	 AND ((NOT EXISTS(SELECT 1 FROM catalog_shows show_row WHERE show_row.entity_id=` + entity + `)
	 AND NOT EXISTS(SELECT 1 FROM catalog_seasons season_row WHERE season_row.entity_id=` + entity + `)) OR EXISTS(SELECT 1 FROM catalog_browse_memberships sx
	 JOIN catalog_asset_links sa ON sa.entity_id=sx.item_id WHERE sx.entity_id=` + entity + ` AND ` + memberRestriction + `))
	 AND ` + clause, append(memberArgs, args...)
}

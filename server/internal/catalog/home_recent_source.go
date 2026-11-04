package catalog

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// homeRecentMaximum bounds a Recently Added shelf, as the owner maximum bounds
// Continue Watching: the shelf, its total and its pages cover the newest works
// only. The whole library, newest first, is the library's own browse view.
const homeRecentMaximum = 100

// A Home preview must also stay bounded before a visibility class has been
// published, or while that class is catching up with changed facts. The full
// library browse remains complete; Home samples the newest work candidates.
const homeRecentCandidateMaximum = 4096

// recentClass names a restricted viewer's published visibility class, whose
// rows carry each visible work's newest addition (compact_visibility_recent).
type recentClass struct{ id, generation int64 }

// homeRecentWorkKeySQL folds editions and members into works: a show, an
// album, a book, or one film across its editions. It is the compact
// equivalent of the recommendation engine's work key, inlined here so this
// walk does not depend on another lane's rewrite.
func homeRecentWorkKeySQL(i string) string {
	return `CASE ` + i + `.kind
	 WHEN 4 THEN 'show:'||(SELECT pid(parent.public_id) FROM catalog_episodes e JOIN catalog_entities parent ON parent.id=e.show_id WHERE e.entity_id=` + i + `.id)
	 WHEN 7 THEN 'album:'||(SELECT pid(parent.public_id) FROM catalog_songs sg JOIN catalog_entities parent ON parent.id=sg.album_id WHERE sg.entity_id=` + i + `.id)
	 WHEN 9 THEN 'book:'||(SELECT pid(parent.public_id) FROM catalog_book_files f JOIN catalog_entities parent ON parent.id=f.book_id WHERE f.entity_id=` + i + `.id)
	 WHEN 1 THEN 'movie:'||COALESCE('tmdb:'||NULLIF((SELECT d.provider_id FROM metadata_details d WHERE d.item_id=` + i + `.id AND d.provider='tmdb' AND d.provider_id<>''),''),
	  'tvdb:'||NULLIF((SELECT d.provider_id FROM metadata_details d WHERE d.item_id=` + i + `.id AND d.provider='tvdb' AND d.provider_id<>''),''),
	  'anilist:'||NULLIF((SELECT d.provider_id FROM metadata_details d WHERE d.item_id=` + i + `.id AND d.provider='anilist' AND d.provider_id<>''),''),'i:'||pid(` + i + `.public_id))
	 ELSE 'item:'||pid(` + i + `.public_id) END`
}

// homeRecentPhases are the two walks of homeRecentSource. The first walks
// works newest first on catalog_browse_recent_works: a show, album or book by
// its newest available member (maintained by the catalogue worker), a
// standalone item by its own added time, so each row is a different work and
// the walk costs the works shown, never the episodes or songs under them.
// The second walks items with no added date, which sort last as they always
// did. Their arguments are (library, last added, last entity id, restriction
// arguments…); each phase has its own restriction arguments.
//
// A restricted viewer with a published class walks the class's own rows by
// the same key, so it reads only works it may see (each rechecked against the
// current restrictions, as a class may lag a change by one refresh) and the
// shelf is complete however little of the library the viewer can see.
func homeRecentPhases(r HomeRequest, class *recentClass) ([]string, [][]any) {
	// Permission and availability must hold for the same member: a work's
	// member metrics carry an added time only while that member is available.
	memberRestriction, memberArgs := ItemRestrictionSQL("member.item_id", r.Restrictions)
	ownRestriction, ownArgs := ItemRestrictionSQL("br.entity_id", r.Restrictions)
	// A work needs one member that is both currently permitted and available.
	// Probe its indexed membership range until EXISTS finds that member;
	// truncating before policy evaluation can hide a valid late member. This
	// check also fences a published class's potentially stale global metrics.
	workRestriction := `(CASE WHEN i.kind IN(2,6,8) THEN EXISTS(
	 SELECT 1 FROM catalog_browse_member_metrics member INDEXED BY catalog_browse_metric_added
	 CROSS JOIN catalog_item_availability available ON available.entity_id=member.item_id
	 WHERE member.entity_id=br.entity_id AND member.added IS NOT NULL
	 AND available.available=1 AND available.retired=0 AND ` + memberRestriction + recordingsClause("member.item_id", r.Restrictions) + `
	 AND NOT EXISTS(SELECT 1 FROM dvr_catalog_retirements gone WHERE gone.item_id=member.item_id)) ELSE ` + ownRestriction + ` END)`
	workArgs := append(append([]any{}, memberArgs...), ownArgs...)
	itemRestriction, itemArgs := ItemRestrictionSQL("i.id", r.Restrictions)
	// Each phase reads a raw window of its index and says per row whether the
	// viewer may see it; the walk advances over every row, so what a viewer
	// cannot see is inspected once and counted against the budget.
	window := `SELECT entity_id,recent_text FROM catalog_browse_rows INDEXED BY catalog_browse_recent_works
	  WHERE library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) AND recent_text IS NOT NULL AND (recent_text,entity_id)<(?,?)
	  ORDER BY recent_text DESC,entity_id DESC LIMIT 256`
	if class != nil {
		window = `SELECT entity_id,recent AS recent_text FROM compact_visibility_rows INDEXED BY compact_visibility_recent
	  WHERE class_id=` + strconv.FormatInt(class.id, 10) + ` AND generation=` + strconv.FormatInt(class.generation, 10) + `
	  AND library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) AND recent IS NOT NULL AND (recent,entity_id)<(?,?)
	  ORDER BY recent DESC,entity_id DESC LIMIT 256`
	}
	works := `SELECT br.recent_text,` + homeRecentWorkKeySQL("i") + `,pid(i.public_id),br.entity_id,i.retired=0 AND ` + workRestriction + `,i.kind
	 FROM (` + window + `) br CROSS JOIN catalog_entities i ON i.id=br.entity_id
	 ORDER BY br.recent_text DESC,br.entity_id DESC`
	columns := `SELECT COALESCE(br.added_text,''),` + homeRecentWorkKeySQL("i") + `,
	 CASE i.kind WHEN 1 THEN pid(i.public_id)
	  WHEN 4 THEN (SELECT pid(parent.public_id) FROM catalog_episodes e JOIN catalog_entities parent ON parent.id=e.show_id WHERE e.entity_id=i.id)
	  WHEN 7 THEN (SELECT pid(parent.public_id) FROM catalog_songs sg JOIN catalog_entities parent ON parent.id=sg.album_id WHERE sg.entity_id=i.id)
	  WHEN 9 THEN (SELECT pid(parent.public_id) FROM catalog_book_files f JOIN catalog_entities parent ON parent.id=f.book_id WHERE f.entity_id=i.id)
	  ELSE pid(i.public_id) END,
	 br.entity_id,k.playable=1 AND i.kind<>11 AND i.retired=0
	  AND EXISTS(SELECT 1 FROM catalog_item_availability v WHERE v.entity_id=i.id AND v.available=1 AND v.retired=0)
	  AND ` + itemRestriction + `,i.kind`
	undated := columns + ` FROM (SELECT entity_id,added_text FROM catalog_browse_rows INDEXED BY catalog_browse_recent_undated
	  WHERE library_id=(SELECT id FROM catalog_libraries WHERE library_id=?) AND item_id IS NOT NULL AND added_text IS NULL AND ''<=? AND entity_id<?
	  ORDER BY entity_id DESC LIMIT 256) br CROSS JOIN catalog_entities i ON i.id=br.entity_id JOIN catalog_kinds k ON k.id=i.kind ORDER BY br.entity_id DESC`
	return []string{works, undated}, [][]any{workArgs, itemArgs}
}

// homeRecentSource walks the library's items newest first on items_home_recent,
// folds them into works (a show, an album, a book, or one film across its
// editions) and stops once homeRecentMaximum works are found. It never
// materialises the library (ARCH-SRV-04): the cost is the items above the
// last shown work.
func (s *Service) homeRecentSource(r HomeRequest, library string) (homeSource, error) {
	values, delivery, err := s.homeRecentWorks(r, library)
	if err != nil {
		return homeSource{}, err
	}
	src := homeRecentList(values)
	src.delivery = delivery
	return src, nil
}

// homeRecentList is a recent shelf as a row source: (id, order key) pairs the
// row pages by keyset.
func homeRecentList(values [][2]string) homeSource {
	raw, _ := json.Marshal(values)
	n := len(values)
	return homeSource{
		base:           `SELECT json_extract(value,'$[0]') AS id,json_extract(value,'$[1]') AS ord FROM json_each(?)`,
		args:           []any{string(raw)},
		total:          &n,
		selfRestricted: true,
		fingerprint:    fmt.Sprintf("%x", sha256.Sum256(raw)),
	}
}

// homeRecentWorks is one library's shelf as (entity, order key) pairs, newest
// first.
func (s *Service) homeRecentWorks(r HomeRequest, library string) ([][2]string, bool, error) {
	// Dated items newest first; then, only while the shelf is still short,
	// items with no added date, which sort last as they always did.
	var class *recentClass
	if r.Restrictions.Active() {
		key, generation, ready, err := s.publishedVisibilityClass(library, r.Restrictions)
		if err != nil {
			return nil, false, err
		}
		if ready {
			class = &recentClass{generation: generation}
			if err = s.read().QueryRow(`SELECT id FROM compact_visibility_classes WHERE class_key=?`, key).Scan(&class.id); err != nil {
				return nil, false, err
			}
		}
	}
	phases, phaseArgs := homeRecentPhases(r, class)
	type work struct {
		key, entity, added string
	}
	found := []work{}
	delivery := false
	examined := 0
	seen := map[string]int{}
	for phase, query := range phases {
		restrictionArgs := phaseArgs[phase]
		lastAdded, lastEntity := "\uffff", int64(1<<62)
		for len(found) < homeRecentMaximum && examined < homeRecentCandidateMaximum {
			args := append(append([]any{}, restrictionArgs...), library, lastAdded, lastEntity)
			bounded := strings.Replace(query, "LIMIT 256", fmt.Sprintf("LIMIT %d", min(256, homeRecentCandidateMaximum-examined)), 1)
			rows, err := s.read().Query(bounded, args...)
			if err != nil {
				return nil, false, err
			}
			n := 0
			for rows.Next() {
				var added, key string
				var entity *string
				var ent, kind int64
				var visible bool
				if err = rows.Scan(&added, &key, &entity, &ent, &visible, &kind); err != nil {
					rows.Close()
					return nil, false, err
				}
				n++
				examined++
				lastAdded, lastEntity = added, ent
				if !visible || key == "" || entity == nil || *entity == "" {
					continue
				}
				if at, ok := seen[key]; ok {
					// A later-walked member of a work already shown: the
					// work's entity is its smallest member entity, as the
					// recommendation engine chooses it.
					if *entity < found[at].entity {
						found[at].entity = *entity
					}
					continue
				}
				if len(found) < homeRecentMaximum {
					seen[key] = len(found)
					w := work{key: key, entity: *entity, added: added}
					found = append(found, w)
					delivery = delivery || phase == 0 && kind == 2
				}
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return nil, false, err
			}
			if n < 256 {
				break
			}
		}
	}
	// Keep canonical works in the source. Delivery cards are resolved only
	// after selecting a page, so unseen shows require no delivery reads. The
	// stable work ID also breaks equal-added ties independently of delivery.
	values := make([][2]string, 0, len(found))
	ids := map[string]bool{}
	for _, w := range found {
		if ids[w.entity] {
			continue
		}
		ids[w.entity] = true
		values = append(values, [2]string{w.entity, w.added + "\x1f" + w.entity})
	}
	return values, delivery, nil
}

// homeRecentAdditionGap separates deliveries: a scan adds a season's files
// seconds apart, so additions to a show more than an hour apart (the next
// week's episode, or one fetched later the same evening) are separate.
const homeRecentAdditionGap = time.Hour

// homeRecentAdditionWalk bounds the members read for one show: the walk stops
// at the first gap or the second season, so it usually reads a season or less.
const homeRecentAdditionWalk = 200

// homeRecentPageTargets resolves one selected page in one statement. Each
// requested show seeks at most the existing delivery-walk limit through the
// added index, then joins only those bounded member rows. The page never
// scans a show's full membership, nor resolves shows outside the page.
func (s *Service) homeRecentPageTargets(r HomeRequest, ids []string) ([]string, error) {
	out := append([]string{}, ids...)
	if len(ids) == 0 {
		return out, nil
	}
	// Each UNION arm has its own uncorrelated work key. This lets SQLite
	// push the key into both indexed runs of the retained serving view; a
	// correlated key against that compound view can instead scan all metrics.
	windows := make([]string, len(ids))
	args := make([]any, 0, len(ids))
	for i, id := range ids {
		windows[i] = `SELECT ` + strconv.Itoa(i) + ` AS position,entity_id,item_id,added FROM (
 SELECT entity_id,item_id,added FROM catalog_browse_member_metrics INDEXED BY catalog_browse_metric_added
 WHERE entity_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?) AND kind=2)
 AND added IS NOT NULL ORDER BY added DESC LIMIT ` + strconv.Itoa(homeRecentAdditionWalk) + `)`
		args = append(args, id)
	}
	restriction, restrictionArgs := r.Viewer.itemVisibilitySQL("m.item_id")
	rows, err := s.read().Query(`SELECT m.position,m.item_id,m.added,COALESCE(e.season_id,0),pid(ep.public_id),COALESCE(pid(se.public_id),'')
 FROM (`+strings.Join(windows, ` UNION ALL `)+`) m
 CROSS JOIN catalog_episodes e ON e.entity_id=m.item_id
 CROSS JOIN catalog_entities ep ON ep.id=m.item_id
 CROSS JOIN catalog_item_availability available ON available.entity_id=m.item_id
 LEFT JOIN catalog_entities se ON se.id=e.season_id
 WHERE ep.retired=0 AND available.available=1 AND available.retired=0 AND `+restriction+recordingsClause("m.item_id", r.Restrictions)+`
 AND EXISTS(SELECT 1 FROM catalog_browse_memberships current WHERE current.entity_id=m.entity_id AND current.item_id=m.item_id)
 ORDER BY m.position,m.added DESC,m.item_id DESC`, append(args, restrictionArgs...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type delivery struct {
		episode, season string
		seasonID        int64
		last            time.Time
		episodes        int
		counted         map[int64]bool
		done            bool
	}
	deliveries := make([]delivery, len(ids))
	for rows.Next() {
		var position int
		var item, sid int64
		var added, episode, season string
		if err = rows.Scan(&position, &item, &added, &sid, &episode, &season); err != nil {
			return nil, err
		}
		d := &deliveries[position]
		if d.done || d.counted[item] {
			continue
		}
		if d.counted == nil {
			d.counted = map[int64]bool{}
		}
		d.counted[item] = true
		at, e := time.Parse(time.RFC3339Nano, added)
		if e != nil || d.episodes > 0 && d.last.Sub(at) > homeRecentAdditionGap {
			d.done = true
			continue
		}
		if d.episodes > 0 && sid != d.seasonID {
			d.episodes = 0
			d.done = true
			continue
		}
		if d.episodes == 0 {
			d.episode, d.seasonID, d.season = episode, sid, season
		}
		d.episodes++
		d.last = at
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	for i, d := range deliveries {
		if d.episodes == 1 {
			out[i] = d.episode
		} else if d.episodes > 1 && d.seasonID != 0 && d.season != "" {
			out[i] = d.season
		}
	}
	return out, nil
}

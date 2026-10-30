package catalog

import (
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net/url"
	"github.com/PorticoMediaServer/portico/server/internal/personalstate"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
)

const recCandidateLimit = 960
const recMinimumShelf = 3
const recMinimumTrend = 3
const communityWindow = 30 * 24 * time.Hour
const communityViewers = 3

// Normalize descriptive text only. Provider and local entity identifiers are
// never normalized or compared across namespaces.
func normalizeFacetText(v string) string {
	v = strings.ToLower(v)
	v = strings.NewReplacer("-", " ", "_", " ", ".", " ", ",", " ").Replace(v)
	v = strings.Join(strings.Fields(v), " ")
	if v == "sci fi" || v == "scifi" {
		return "science fiction"
	}
	return v
}

// A bounded set of ranked works is hydrated. Each candidate's facets are read
// by key from catalog_rec_facets (compactcatalog.DomainRecFacets), which the
// catalogue keeps as the facts change. Large recommended rows score
// indexed personal and recent candidates; small libraries retain the complete
// permitted relation. Evidence joins start from permitted, available children:
// a hidden sibling cannot supply a score or artwork fallback.
type recCandidate struct {
	ID     string   `json:"id"`
	Work   string   `json:"work"`
	Kind   string   `json:"kind"`
	Added  string   `json:"added"`
	Facets []string `json:"facets"`
	Score  float64  `json:"score"`
}

// recBase defines the single eligibility and facet projection shared by Home,
// Discover, suggestions and related rows. Local owner locks remain authoritative
// even when automatic audio metadata is switched off.
//
// The walk always starts from an explicit, bounded candidate set and seeks
// each item by key; there is no whole-library form (ARCH-SRV-04, BE-SRV-12).
// A nil or empty set scores nothing. Callers bound the set themselves:
// recommendationPool, relatedPool, trendCandidates, communityCandidates and
// recommendationRowMembers all cap it independently of library size.
func recBase(r HomeRequest, candidates []string) (string, []any) {
	if candidates == nil {
		candidates = []string{}
	}
	restriction, args := ItemRestrictionSQL("i.id", r.Restrictions)
	source := `json_each(?) candidate CROSS JOIN catalog_entities i ON i.public_id=pid_blob(candidate.value) JOIN catalog_libraries cl ON cl.id=i.library_id JOIN catalog_kinds k ON k.id=i.kind LEFT JOIN catalog_item_details det ON det.entity_id=i.id`
	base := `WITH permitted AS MATERIALIZED (
 SELECT i.*,cl.library_id AS library,COALESCE(det.added_text,'') AS added_at,det.studio AS studio,det.network AS network,` + workKeySQL("i") + ` AS work,
 CASE i.kind WHEN 1 THEN i.id
  WHEN 4 THEN (SELECT parent.id FROM catalog_episodes ep JOIN catalog_entities parent ON parent.id=ep.show_id WHERE ep.entity_id=i.id)
  WHEN 7 THEN (SELECT parent.id FROM catalog_songs sg JOIN catalog_entities parent ON parent.id=sg.album_id WHERE sg.entity_id=i.id)
  WHEN 9 THEN (SELECT parent.id FROM catalog_book_files bf JOIN catalog_entities parent ON parent.id=bf.book_id WHERE bf.entity_id=i.id) END AS entity,
 CASE i.kind WHEN 1 THEN 'movie' WHEN 4 THEN 'show' WHEN 7 THEN 'album' WHEN 9 THEN 'book' END AS entity_kind
 FROM ` + source + `
 WHERE cl.library_id IN(SELECT value FROM json_each(?)) AND k.playable=1 AND i.kind<>11
 AND EXISTS(SELECT 1 FROM catalog_asset_links link JOIN catalog_assets asset ON asset.id=link.asset_id WHERE link.entity_id=i.id AND link.available=1)
 AND NOT EXISTS(SELECT 1 FROM dvr_catalog_retirements g WHERE g.item_id=i.id) AND ` + restriction + `
 ), members AS MATERIALIZED (SELECT * FROM permitted WHERE entity IS NOT NULL AND work IS NOT NULL),
 works AS MATERIALIZED (SELECT work,min(entity) AS id,entity_kind AS kind,max(COALESCE(added_at,'')) AS added FROM members GROUP BY work),
 facets AS MATERIALIZED (
 SELECT DISTINCT work,f FROM (
 SELECT m.work,rf.facet f FROM members m CROSS JOIN catalog_rec_facets rf ON rf.entity_id=m.id
 UNION ALL SELECT w.work,rf.facet FROM works w CROSS JOIN catalog_rec_facets rf ON rf.entity_id=w.id WHERE w.kind IN('show','album','book'))
 ), personal AS MATERIALIZED (
 SELECT m.work,
 max(CASE WHEN p.rating<=4 OR p.not_interested=1 THEN 1 ELSE 0 END) AS negative,
 max(CASE WHEN ` + personalstate.CompactSQL("wp.profile", "m.id") + `=1 OR p.watchlisted=1 OR p.favorite=1 OR p.rating IS NOT NULL OR pg.position>0 THEN 1 ELSE 0 END) AS touched,
 max(CASE WHEN p.favorite=1 THEN 40 ELSE 0 END + CASE WHEN p.rating>=7 THEN 35 WHEN p.rating>=5 THEN 15 ELSE 0 END + CASE WHEN ` + personalstate.CompactSQL("wp.profile", "m.id") + `=1 THEN 25 ELSE 0 END + CASE WHEN p.watchlisted=1 THEN 12 ELSE 0 END
 + CASE WHEN pg.position>=30000 AND pg.position >= COALESCE((SELECT max(a.duration)*50 FROM catalog_asset_links link JOIN catalog_assets a ON a.id=link.asset_id WHERE link.entity_id=m.id AND a.available=1),1e99) THEN 6 ELSE 0 END) AS weight,
 max(COALESCE(p.last_played_at,'')) AS at
 FROM members m CROSS JOIN (SELECT ? AS profile) wp LEFT JOIN personal_items p ON p.item_id=m.id AND p.profile_id=wp.profile LEFT JOIN progress pg ON pg.item_id=m.id AND pg.profile_id=? GROUP BY m.work
 ), seeds AS MATERIALIZED (SELECT work,weight * CASE WHEN at='' THEN 1.0 WHEN julianday(?) - julianday(at) <= 14 THEN 1.0 WHEN julianday(?) - julianday(at)<=60 THEN 0.8 ELSE 0.5 END AS weight FROM personal WHERE negative=0 AND weight>0 ORDER BY at DESC,weight DESC,work LIMIT 64),
 interest AS MATERIALIZED (SELECT f.f,min(sum(s.weight),120) AS weight FROM seeds s JOIN facets f ON f.work=s.work GROUP BY f.f),
 negative_facets AS MATERIALIZED (SELECT DISTINCT f.f FROM facets f JOIN personal pn ON pn.work=f.work WHERE pn.negative=1)
 `
	bind := []any{idsJSON(candidates), idsJSON(r.Libraries)}
	bind = append(bind, args...)
	bind = append(bind, r.Profile, r.Profile, r.now().UTC().Format(time.RFC3339), r.now().UTC().Format(time.RFC3339))
	return base, bind
}

func (s *Service) recommendationCandidates(r HomeRequest, row, seed string) ([]recCandidate, error) {
	r.Now = s.recommendationNow(r.Now)
	// Every row scores only its own bounded candidate set. A row without a
	// candidate source is not a recommendation row (Recently Added is
	// homeRecentSource's newest-first index walk).
	switch row {
	case "recommended", "related", "trending_now", "community_watching":
	default:
		return nil, ErrHomeRowUnknown
	}
	if row == "community_watching" && !r.CommunityActivity {
		return nil, nil
	}
	var candidates []string
	if row == "recommended" {
		var err error
		if candidates, _, err = s.recommendationPool(r); err != nil || len(candidates) == 0 {
			return nil, err
		}
	}
	if row == "related" {
		var err error
		if candidates, _, err = s.relatedPool(r, seed); err != nil || len(candidates) == 0 {
			return nil, err
		}
	}
	if row == "trending_now" {
		// Trends are opt-in provider data. With no consent there cannot be a
		// result, so do not materialize the entire eligible catalogue and all
		// its facets merely to discover that the feed is unavailable.
		var consent bool
		if err := s.read().QueryRow(`SELECT EXISTS(SELECT 1 FROM screen_metadata_consent WHERE singleton=1 AND confirmed=1)`).Scan(&consent); err != nil {
			return nil, err
		}
		if !consent {
			return nil, nil
		}
		// Score only the library items the current trend feed names.
		var err error
		if candidates, err = s.trendCandidates(r); err != nil || len(candidates) == 0 {
			return nil, err
		}
	}
	if row == "community_watching" {
		var err error
		if candidates, err = s.communityCandidates(r); err != nil || len(candidates) == 0 {
			return nil, err
		}
	}
	base, args := recBase(r, candidates)
	score := `COALESCE(ps.value,0)-COALESCE(ns.value,0)`
	where := `COALESCE(p.negative,0)=0`
	order := `score DESC,w.added DESC,w.id`
	extra := ""
	switch row {
	case "recommended":
		where += ` AND COALESCE(p.touched,0)=0 AND (
 NOT EXISTS(SELECT 1 FROM interest WHERE f NOT LIKE 'e:%') OR
 COALESCE(ps.non_year_match,0)=1 AND (` + score + `)>0)`
	case "related":
		extra = ` , target AS (SELECT work FROM members WHERE public_id=pid_blob(?))`
		args = append(args, seed)
		score = `COALESCE((SELECT count(*)*30 FROM facets f WHERE f.work=w.work AND f.f IN(SELECT f2.f FROM facets f2 JOIN target t ON t.work=f2.work)),0)`
		where += ` AND w.work NOT IN(SELECT work FROM target) AND (` + score + `)>0`
	case "trending_now":
		extra = ` , trends AS (SELECT m.work,min(t.rank) AS rank FROM members m JOIN catalog_libraries l ON l.id=m.library_id
  JOIN metadata_details d ON d.item_id=m.id JOIN metadata_discovery_items t ON t.provider=d.provider AND t.provider_id=d.provider_id AND t.media_kind='movie'
  JOIN screen_metadata_policies p ON p.library_id=l.library_id AND p.enabled=1
  WHERE m.kind=1 AND EXISTS(SELECT 1 FROM json_each(p.providers) WHERE value=t.provider) AND ` + validTrendSQL() + ` GROUP BY m.work
  UNION ALL SELECT w.work,min(t.rank) FROM works w JOIN catalog_entities sh ON sh.id=w.id AND sh.kind=2 JOIN catalog_libraries l ON l.id=sh.library_id
  JOIN screen_metadata_work sw ON sw.target_kind='show' AND sw.target_id=sh.id AND sw.library_id=l.library_id
  JOIN screen_metadata_publications pub ON pub.id=sw.accepted_publication JOIN metadata_discovery_items t ON t.provider=pub.provider AND t.provider_id=pub.provider_id AND (t.media_kind=CASE l.kind WHEN 2 THEN 'tv' WHEN 3 THEN 'anime' END OR l.kind=3 AND t.provider='tmdb' AND t.media_kind='tv')
  JOIN screen_metadata_policies p ON p.library_id=l.library_id AND p.enabled=1
  WHERE w.kind='show' AND EXISTS(SELECT 1 FROM json_each(p.providers) WHERE value=t.provider) AND ` + validTrendSQL() + ` GROUP BY w.work)`
		for i := 0; i < 2; i++ {
			args = append(args, r.now().UTC().Format(time.RFC3339), r.now().UTC().Format(time.RFC3339), r.now().UTC().Format(time.RFC3339))
		}
		where += ` AND EXISTS(SELECT 1 FROM screen_metadata_consent WHERE singleton=1 AND confirmed=1) AND EXISTS(SELECT 1 FROM trends t WHERE t.work=w.work)`
		score = `10000.0/(SELECT min(t.rank) FROM trends t WHERE t.work=w.work)`
	case "community_watching":
		if !r.CommunityActivity {
			return nil, nil
		}
		eligible, err := s.communityEligibleProfiles(r.now())
		if err != nil {
			return nil, err
		}
		if len(eligible) < communityViewers {
			return nil, nil
		}
		extra = ` , community AS (SELECT m.work,count(DISTINCT h.profile_id) AS viewers FROM members m JOIN personal_history h ON h.item_id=m.id
  JOIN json_each(?) actor ON json_extract(actor.value,'$.key')=h.profile_id
  WHERE (json_extract(actor.value,'$.authority')<>'local' OR EXISTS(SELECT 1 FROM direct_profiles dp JOIN direct_memberships dm ON dm.account_id=dp.account_id WHERE dp.id=json_extract(actor.value,'$.profile') AND dp.account_id=json_extract(actor.value,'$.account') AND dp.deleted=0 AND dm.disabled=0 AND (dm.role='owner' OR EXISTS(SELECT 1 FROM json_each(dm.allowed_libraries) WHERE value=m.library)) AND (dp.allowed_libraries IS NULL OR EXISTS(SELECT 1 FROM json_each(dp.allowed_libraries) WHERE value=m.library))))
  AND NOT EXISTS(SELECT 1 FROM profile_restrictions pr WHERE pr.profile_id=json_extract(actor.value,'$.profile') AND (
   EXISTS(SELECT 1 FROM catalog_item_attribute_edges eae JOIN catalog_attribute_terms at ON at.id=eae.term_id JOIN catalog_attribute_fields af ON af.id=at.field_id WHERE eae.item_id=m.id AND af.field='label' AND at.value_key IN(SELECT lower(value) FROM json_each(pr.blocked_labels)))
   OR pr.maximum_age>=0 AND EXISTS(SELECT 1 FROM catalog_item_attribute_edges eae JOIN catalog_attribute_terms at ON at.id=eae.term_id JOIN catalog_attribute_fields af ON af.id=at.field_id LEFT JOIN content_rating_ages ra ON ra.value_key=at.value_key WHERE eae.item_id=m.id AND af.field='contentRating' AND (ra.minimum_age>pr.maximum_age OR ra.minimum_age IS NULL))
   OR pr.allow_unrated=0 AND NOT EXISTS(SELECT 1 FROM catalog_item_attribute_edges eae JOIN catalog_attribute_terms at ON at.id=eae.term_id JOIN catalog_attribute_fields af ON af.id=at.field_id JOIN content_rating_ages ra ON ra.value_key=at.value_key WHERE eae.item_id=m.id AND af.field='contentRating' AND ra.minimum_age>=0)))
  AND julianday(h.updated_at)>=julianday(?) AND julianday(h.updated_at)<=julianday(?)
  AND (h.completed=1 OR h.position>=30000 AND h.position>=(SELECT max(a.duration)*50 FROM catalog_asset_links link JOIN catalog_assets a ON a.id=link.asset_id WHERE link.entity_id=m.id AND a.available=1 AND a.duration>0)) GROUP BY m.work HAVING count(DISTINCT h.profile_id)>=` + strconv.Itoa(communityViewers) + `)`
		actors := []map[string]string{}
		for _, key := range eligible {
			v, _ := viewerFromPersonalKey(key)
			actors = append(actors, map[string]string{"key": key, "profile": v.ProfileID, "account": v.AccountID, "authority": v.Authority})
		}
		actorJSON, _ := json.Marshal(actors)
		args = append(args, string(actorJSON), r.now().Add(-communityWindow).UTC().Format(time.RFC3339), r.now().UTC().Format(time.RFC3339))
		where += ` AND EXISTS(SELECT 1 FROM community c WHERE c.work=w.work)`
		score = `(SELECT c.viewers FROM community c WHERE c.work=w.work)`
	default:
		return nil, ErrHomeRowUnknown
	}
	// Ratings are a small cold-start hint, never a substitute for provider trends.
	if row == "recommended" {
		score += ` + COALESCE((SELECT max(rr.value/NULLIF(rr.scale,0))*3 FROM members m JOIN metadata_ratings rr ON rr.item_id=m.id WHERE m.work=w.work),0)`
	}
	query := base + extra + `, facet_json AS MATERIALIZED (SELECT work,json_group_array(f) AS value FROM facets GROUP BY work),
 positive_scores AS MATERIALIZED (SELECT f.work,sum(CASE WHEN f.f LIKE 'e:%' THEN x.weight*0.1 WHEN f.f LIKE 'g:%' THEN x.weight ELSE x.weight*1.4 END) AS value,max(f.f NOT LIKE 'e:%') AS non_year_match FROM facets f JOIN interest x ON x.f=f.f GROUP BY f.work),
 negative_scores AS MATERIALIZED (SELECT f.work,count(*)*25 AS value FROM facets f JOIN negative_facets n ON n.f=f.f GROUP BY f.work)
 SELECT pid(ce.public_id),w.work,w.kind,w.added,COALESCE(fj.value,'[]'),` + score + ` AS score FROM works w JOIN catalog_entities ce ON ce.id=w.id LEFT JOIN personal p ON p.work=w.work LEFT JOIN facet_json fj ON fj.work=w.work LEFT JOIN positive_scores ps ON ps.work=w.work LEFT JOIN negative_scores ns ON ns.work=w.work WHERE ` + where + ` ORDER BY ` + order + ` LIMIT ?`
	args = append(args, recCandidateLimit)
	rows, err := s.read().Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []recCandidate{}
	for rows.Next() {
		var c recCandidate
		var raw string
		if err = rows.Scan(&c.ID, &c.Work, &c.Kind, &c.Added, &raw, &c.Score); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(raw), &c.Facets); err != nil {
			return nil, err
		}
		sort.Strings(c.Facets)
		out = append(out, c)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if row == "recommended" || row == "related" {
		out = diversifyRecommendations(out, r.now())
	}
	return out, nil
}
func validTrendSQL() string {
	return `t.rank>0 AND julianday(t.expires_at)>julianday(?) AND julianday(t.fetched_at)<=julianday(?) AND julianday(t.fetched_at)>julianday(?)-2`
}

// Greedy reranking penalizes repetition, but cannot turn a weak match into a
// stronger one just because it is new. Rotation only breaks comparable ties.
func diversifyRecommendations(in []recCandidate, now time.Time) []recCandidate {
	out := make([]recCandidate, 0, len(in))
	used := map[string]int{}
	day := now.In(time.Local).Format("2006-01-02")
	tie := func(id string) uint64 { h := fnv.New64a(); h.Write([]byte(day + ":" + id)); return h.Sum64() }
	// Only the first 60 entries need the quadratic diversity pass. Remaining
	// candidates retain score order for stable bounded row paging.
	for len(in) > 0 && len(out) < HomeRowMaxLimit {
		best := 0
		bestScore := -1e100
		for i, c := range in {
			score := c.Score
			for _, f := range c.Facets {
				penalty := 3.0
				if strings.HasPrefix(f, "c:") || strings.HasPrefix(f, "a:") || strings.HasPrefix(f, "b:") || strings.HasPrefix(f, "k:") {
					penalty = 24
				}
				score -= float64(used[f]) * penalty
			}
			score -= float64(used["kind:"+c.Kind]) * 2
			if score > bestScore || score == bestScore && tie(c.ID) < tie(in[best].ID) {
				best = i
				bestScore = score
			}
		}
		c := in[best]
		out = append(out, c)
		for _, f := range c.Facets {
			used[f]++
		}
		used["kind:"+c.Kind]++
		in = append(in[:best], in[best+1:]...)
	}
	return append(out, in...)
}

func candidateSource(c []recCandidate) homeSource {
	if c == nil {
		c = []recCandidate{}
	}
	raw, _ := json.Marshal(c)
	n := len(c)
	return homeSource{fingerprint: fmt.Sprintf("%x", sha256.Sum256(raw)), base: `SELECT json_extract(value,'$.id') AS id,printf('%08d',CAST(key AS INTEGER)) AS ord FROM json_each(?)`, args: []any{string(raw)}, total: &n, selfRestricted: true, candidates: c}
}
func (s *Service) homeEngineSource(r HomeRequest, spec homeRowSpec) (homeSource, error) {
	cacheKey := spec.ID + ":" + idsJSON(r.Libraries)
	if src, ok := s.recSources[cacheKey]; ok {
		return src, nil
	}
	row := spec.ID
	if strings.HasPrefix(row, homeRecentPrefix) {
		// A bounded newest-first index walk, never the whole library.
		return s.homeRecentSource(r, spec.LibraryID)
	}
	var candidates []recCandidate
	var err error
	if row == "recommended" {
		candidates, err = s.modelledCandidates(r, row)
	} else {
		// Trends and community activity start from small maintained sets
		// (the provider feed, the recent-history window) and stay live.
		candidates, err = s.recommendationCandidates(r, row, "")
	}
	if err != nil {
		return homeSource{}, err
	}
	// Trends retain their genuine feed identity. Suppress only the visible first
	// rail of trends in personalized results; saved/resume rows are untouched.
	if row == "recommended" && !containsString(r.HiddenRowIDs, "trending_now") {
		trending, e := s.homeEngineSource(r, homeRowSpec{ID: "trending_now"})
		if e != nil {
			return homeSource{}, e
		}
		trends := trending.candidates
		if len(trends) >= recMinimumTrend {
			seen := map[string]bool{}
			for i, c := range trends {
				if i == HomeRowDefaultLimit {
					break
				}
				seen[c.Work] = true
			}
			filtered := candidates[:0]
			for _, c := range candidates {
				if !seen[c.Work] {
					filtered = append(filtered, c)
				}
			}
			candidates = filtered
		}
	}
	src := candidateSource(candidates)
	if row == "recommended" {
		// The ranking is defined per day (rotation): a cursor from yesterday's
		// order never continues into today's, even where the order agrees.
		src.fingerprint += ":" + s.recommendationNow(r.Now).In(time.Local).Format("2006-01-02")
	}
	// A modelled list may predate a restriction change: re-apply the viewer's
	// current restriction to its entities on every read. Candidates are public
	// ids, so they resolve to integer entities for the predicate.
	if restriction, args := EntityRestrictionSQL("(SELECT id FROM catalog_entities WHERE public_id=pid_blob(candidate.id))", r.Restrictions); restriction != "1" {
		published, err := s.publishedClassesSQL("(SELECT id FROM catalog_entities WHERE public_id=pid_blob(candidate.id))", r.Libraries, r.Restrictions)
		if err != nil {
			return homeSource{}, err
		}
		src.base = `SELECT id,ord FROM (` + src.base + `) candidate WHERE ` + published + ` AND ` + restriction
		src.args = append(src.args, args...)
		src.total = nil
	}
	if s.recSources != nil {
		s.recSources[cacheKey] = src
	}
	return src, nil
}

func viewerFromPersonalKey(key string) (identity.Viewer, bool) {
	raw, ok := strings.CutPrefix(key, "viewer:")
	if !ok {
		return identity.Viewer{}, false
	}
	decoded, e := base64.RawURLEncoding.DecodeString(raw)
	if e != nil {
		return identity.Viewer{}, false
	}
	var tuple [3]string
	if json.Unmarshal(decoded, &tuple) != nil {
		return identity.Viewer{}, false
	}
	return identity.Viewer{Authority: tuple[0], AccountID: tuple[1], ProfileID: tuple[2]}, true
}

func (s *Service) communityEligibleProfiles(now time.Time) ([]string, error) {
	// updated_at is written in one fixed millisecond layout, so the window is a
	// range on personal_history_recent (julianday() here read all history).
	rows, err := s.read().Query(`SELECT DISTINCT profile_id FROM personal_history INDEXED BY personal_history_recent WHERE updated_at>=?`, now.Add(-communityWindow).UTC().Format("2006-01-02T15:04:05.000Z"))
	if err != nil {
		return nil, err
	}
	keys := []string{}
	for rows.Next() {
		var key string
		if err = rows.Scan(&key); err != nil {
			rows.Close()
			return nil, err
		}
		keys = append(keys, key)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	eligible := []string{}
	for _, key := range keys {
		v, ok := viewerFromPersonalKey(key)
		if !ok {
			continue
		}
		var active bool
		if v.Authority == "local" {
			err = s.read().QueryRow(`SELECT EXISTS(SELECT 1 FROM direct_profiles p JOIN direct_memberships m ON m.account_id=p.account_id WHERE p.id=? AND p.account_id=? AND p.deleted=0 AND m.disabled=0)`, v.ProfileID, v.AccountID).Scan(&active)
		} else {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !active {
			continue
		}
		prefs, _, _, e := operations.EffectivePreferences(s.read(), v, "")
		if e != nil {
			return nil, e
		}
		if prefs.Bool("privacy.showActivityToMembers") && !prefs.Bool("privacy.pauseWatchHistory") {
			eligible = append(eligible, key)
		}
	}
	return eligible, nil
}

func workKeySQL(i string) string {
	return `CASE ` + i + `.kind
	 WHEN 4 THEN 'show:'||(SELECT parent.id FROM catalog_episodes e JOIN catalog_entities parent ON parent.id=e.show_id WHERE e.entity_id=` + i + `.id)
	 WHEN 7 THEN 'album:'||(SELECT parent.id FROM catalog_songs sg JOIN catalog_entities parent ON parent.id=sg.album_id WHERE sg.entity_id=` + i + `.id)
	 WHEN 9 THEN 'book:'||(SELECT parent.id FROM catalog_book_files f JOIN catalog_entities parent ON parent.id=f.book_id WHERE f.entity_id=` + i + `.id)
	 WHEN 1 THEN 'movie:'||COALESCE('tmdb:'||NULLIF((SELECT d.provider_id FROM metadata_details d WHERE d.item_id=` + i + `.id AND d.provider='tmdb' AND d.provider_id<>''),''),
	  'tvdb:'||NULLIF((SELECT d.provider_id FROM metadata_details d WHERE d.item_id=` + i + `.id AND d.provider='tvdb' AND d.provider_id<>''),''),
	  'anilist:'||NULLIF((SELECT d.provider_id FROM metadata_details d WHERE d.item_id=` + i + `.id AND d.provider='anilist' AND d.provider_id<>''),''),'i:'||` + i + `.id)
	 ELSE '' END`
}

// homePosterLenders is how many of a container's members a Home card without
// artwork tries, in order, for a poster to borrow.
const homePosterLenders = 64

func (s *Service) homeEngineEntries(profile string, ids []string) ([]ContentEntry, error) {
	out := []ContentEntry{}
	if len(ids) == 0 {
		return out, nil
	}
	if err := s.compactProjectionReady(18, 19); err != nil {
		return nil, err
	}
	// From the requested ids, each a unique public_id seek and a browse row by
	// key (an item's row is its own entity's). Joined on an expression instead,
	// this scanned every browse row of the server on every Home row.
	rows, e := s.read().Query(`SELECT pid(ent.public_id),CASE e.kind WHEN 1 THEN 'movie' WHEN 2 THEN 'show' WHEN 3 THEN 'season' WHEN 4 THEN 'episode' WHEN 5 THEN 'artist' WHEN 6 THEN 'album' WHEN 7 THEN 'song' WHEN 8 THEN 'book' WHEN 9 THEN 'audiobook_file' END,e.title,e.year,l.library_id
 FROM (SELECT DISTINCT value FROM json_each(?)) requested CROSS JOIN catalog_entities ent ON ent.public_id=pid_blob(requested.value)
 CROSS JOIN catalog_browse_rows e ON e.entity_id=ent.id JOIN catalog_libraries l ON l.id=e.library_id`, idsJSON(ids))
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	type recEntity struct {
		id, kind, title, library string
		year                     int
	}
	entities := []recEntity{}
	for rows.Next() {
		var entity recEntity
		if e = rows.Scan(&entity.id, &entity.kind, &entity.title, &entity.year, &entity.library); e != nil {
			return nil, e
		}
		entities = append(entities, entity)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	// Recently Added names a season when a whole season arrived: it reads as
	// the season, under its show (a season without a browse row is read here).
	answered := map[string]bool{}
	for _, entity := range entities {
		answered[entity.id] = true
	}
	seasons, e := s.homeSeasonEntities(ids)
	if e != nil {
		return nil, e
	}
	for _, id := range ids {
		if season, ok := seasons[id]; ok && !answered[id] {
			answered[id] = true
			entities = append(entities, recEntity{id: season.id, kind: "season", title: season.title, library: season.library})
		}
	}
	itemIDs := []string{}
	containerIDs := []string{}
	for _, entity := range entities {
		if isItemEntity(entity.kind) {
			itemIDs = append(itemIDs, entity.id)
		} else {
			containerIDs = append(containerIDs, entity.id)
		}
	}
	media := map[string]Item{}
	if len(itemIDs) > 0 {
		items, e := s.mediaPage(profile, itemIDs, false)
		if e != nil {
			return nil, e
		}
		for _, item := range items {
			media[item.ID] = item
		}
	}

	artwork := map[string]string{}
	targets := make([]artworkTarget, 0, len(entities))
	for _, entity := range entities {
		if !isItemEntity(entity.kind) {
			targets = append(targets, artworkTarget{entity.kind, entity.id})
		}
	}
	arts, err := s.resolveArtwork(targets)
	if err != nil {
		return nil, err
	}
	for target, art := range arts {
		artwork[target.ID] = art.PosterURL
	}

	// Only a container without its own artwork borrows a child's poster; the
	// walk below can read a whole season's or show's members to find one.
	bare := make([]string, 0, len(containerIDs))
	for _, id := range containerIDs {
		if artwork[id] == "" {
			bare = append(bare, id)
		}
	}
	containerIDs = bare
	if len(containerIDs) > 0 {
		clause, args := ItemRestrictionSQL("i.id", s.recRestrictions)
		// Each container's first qualifying child (lowest id, so scan order)
		// lends its poster: a walk of the container's memberships in id order
		// that stops at the first, not an aggregate over every child. The walk
		// reads at most homePosterLenders members, so a container whose first
		// members have no poster costs the same in any library.
		bind := append(append([]any{}, args...), idsJSON(containerIDs))
		rows, e := s.read().Query(`SELECT pid(container.public_id),(SELECT pid(i.public_id) FROM (SELECT item_id FROM catalog_browse_memberships WHERE entity_id=container.id ORDER BY item_id LIMIT `+strconv.Itoa(homePosterLenders)+`) bx
 CROSS JOIN catalog_entities i ON i.id=bx.item_id CROSS JOIN catalog_item_details d ON d.entity_id=i.id
 WHERE d.poster_url<>''
 AND EXISTS(SELECT 1 FROM catalog_asset_links link JOIN catalog_assets asset ON asset.id=link.asset_id WHERE link.entity_id=i.id AND link.available=1)
 AND NOT EXISTS(SELECT 1 FROM dvr_catalog_retirements gone WHERE gone.item_id=i.id)
 AND `+clause+` ORDER BY bx.item_id LIMIT 1)
 FROM (SELECT DISTINCT value FROM json_each(?)) requested CROSS JOIN catalog_entities container ON container.public_id=pid_blob(requested.value)`, bind...)
		if e != nil {
			return nil, e
		}
		for rows.Next() {
			var id string
			var child sql.NullString
			if e = rows.Scan(&id, &child); e != nil {
				rows.Close()
				return nil, e
			}
			if child.Valid && artwork[id] == "" {
				artwork[id] = "/v1/items/" + url.PathEscape(child.String) + "/art/poster"
			}
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return nil, e
		}
		if e = describeItemArtMap(s, artwork); e != nil {
			return nil, e
		}
	}
	for _, entity := range entities {
		if isItemEntity(entity.kind) {
			item, ok := media[entity.id]
			if !ok {
				return nil, ErrStaleContinuation
			}
			out = append(out, contentItem(item))
			continue
		}
		available := true
		entry := ContentEntry{ID: entity.id, Kind: entity.kind, LibraryID: entity.library, Available: &available, Title: entity.title, Navigation: &ContentNavigation{View: containerView(entity.kind), EntityID: entity.id}}
		if entity.year > 0 {
			entry.Subtitle = strconv.Itoa(entity.year)
		}
		if season, ok := seasons[entity.id]; ok {
			number := season.number
			entry.Subtitle, entry.SeasonNumber = season.show, &number
		}
		entry.PosterURL = artwork[entity.id]
		out = append(out, entry)
	}
	loaded := map[string]ContentEntry{}
	for _, entry := range out {
		loaded[entry.ID] = entry
	}
	ordered := make([]ContentEntry, 0, len(ids))
	for _, id := range ids {
		entry, ok := loaded[id]
		if !ok {
			return nil, ErrStaleContinuation
		}
		ordered = append(ordered, entry)
	}
	if e = s.nameEntryMakers(ordered); e != nil {
		return nil, e
	}
	return ordered, nil
}

// idsJSON marshals one id list for json_each bound parameters.
func idsJSON(ids []string) string {
	raw, _ := json.Marshal(ids)
	return string(raw)
}

func (s *Service) recommendationNow(t time.Time) time.Time {
	if !t.IsZero() {
		return t
	}
	if s.Clock != nil {
		return s.Clock()
	}
	return time.Now()
}

// trendCandidates maps the valid provider trend feed (a few hundred rows) to
// the viewer's library items by index: films by their provider details, shows
// by their accepted publication, one episode standing for each show.
func (s *Service) trendCandidates(r HomeRequest) ([]string, error) {
	now := r.now().UTC().Format(time.RFC3339)
	rows, err := s.read().Query(`SELECT pid(i.public_id) FROM metadata_discovery_items t
	  CROSS JOIN metadata_details d INDEXED BY metadata_details_provider ON d.provider=t.provider AND d.provider_id=t.provider_id
	  CROSS JOIN catalog_entities i ON i.id=d.item_id JOIN catalog_libraries l ON l.id=i.library_id
	 WHERE t.media_kind='movie' AND `+validTrendSQL()+` AND i.kind=1 AND l.library_id IN(SELECT value FROM json_each(?))
	 UNION
	 SELECT (SELECT pid(item.public_id) FROM catalog_episodes ep JOIN catalog_entities item ON item.id=ep.entity_id WHERE ep.show_id=sh.id ORDER BY item.id LIMIT 1) FROM metadata_discovery_items t
	  CROSS JOIN screen_metadata_publications pub INDEXED BY screen_publications_provider ON pub.provider=t.provider AND pub.provider_id=t.provider_id AND pub.decision='accepted'
	  CROSS JOIN catalog_entities sh ON sh.id=pub.target_id AND sh.kind=2 JOIN catalog_libraries l ON l.id=sh.library_id
	 WHERE pub.target_kind='show' AND t.media_kind IN('tv','anime') AND `+validTrendSQL()+` AND l.library_id IN(SELECT value FROM json_each(?))
	 LIMIT 2048`, now, now, now, idsJSON(r.Libraries), now, now, now, idsJSON(r.Libraries))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id *string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		if id != nil {
			out = append(out, *id)
		}
	}
	return out, rows.Err()
}

// communityCandidates is the items anyone on the server played inside the
// community window, from the recent-history index.
func (s *Service) communityCandidates(r HomeRequest) ([]string, error) {
	rows, err := s.read().Query(`SELECT DISTINCT pid(e.public_id) FROM personal_history h INDEXED BY personal_history_recent
	 JOIN catalog_entities e ON e.id=h.item_id
	 WHERE h.updated_at>=? AND h.updated_at<=? LIMIT 4096`, r.now().Add(-communityWindow).UTC().Format("2006-01-02T15:04:05.000Z"), r.now().UTC().Add(time.Minute).Format("2006-01-02T15:04:05.000Z"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

type homeSeason struct {
	id, title, show, library string
	number                   int
}

// homeSeasonEntities reads the requested ids that are seasons: the season's
// own title, its number and its show's title.
func (s *Service) homeSeasonEntities(ids []string) (map[string]homeSeason, error) {
	out := map[string]homeSeason{}
	rest := ids
	if len(rest) == 0 {
		return out, nil
	}
	rows, err := s.read().Query(`SELECT pid(se.public_id),se.title,sn.number,show.title,l.library_id
 FROM (SELECT DISTINCT value FROM json_each(?)) requested CROSS JOIN catalog_entities se ON se.public_id=pid_blob(requested.value)
 CROSS JOIN catalog_seasons sn ON sn.entity_id=se.id CROSS JOIN catalog_entities show ON show.id=sn.show_id
 JOIN catalog_libraries l ON l.id=se.library_id WHERE se.retired=0`, idsJSON(rest))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var season homeSeason
		if err = rows.Scan(&season.id, &season.title, &season.number, &season.show, &season.library); err != nil {
			return nil, err
		}
		if season.title == "" {
			season.title = seasonTitle(season.number)
		}
		out[season.id] = season
	}
	return out, rows.Err()
}

// homeRecentSeasonCounts says how many seasons each show on a Recently Added
// shelf has ("6 seasons"): a show there stands for several seasons added at
// once. Specials and seasons with no episodes are not counted.
func (s *Service) homeRecentSeasonCounts(entries []ContentEntry) error {
	for i := range entries {
		if entries[i].Kind != "show" {
			continue
		}
		var count int
		if err := s.read().QueryRow(`SELECT count(*) FROM catalog_seasons sn CROSS JOIN catalog_entities show ON show.id=sn.show_id
 WHERE show.public_id=pid_blob(?) AND sn.number>0 AND EXISTS(SELECT 1 FROM catalog_episodes e WHERE e.show_id=sn.show_id AND e.season_id=sn.entity_id)`, entries[i].ID).Scan(&count); err != nil {
			return err
		}
		if count > 0 {
			entries[i].SeasonCount = &count
		}
	}
	return nil
}

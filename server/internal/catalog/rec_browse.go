package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

// The "For you" browse sort: the viewer's recommendation ranking of the
// pivot and its filter, then every other row in title order (the profile
// sorts' shape, browse_personal.go). A filtered set of up to recForYouExact
// rows is ranked whole, so the order is the taste's over every title; a
// larger one (a whole library) ranks the engine's candidates that pass the
// filter, a bounded read whatever the library's size. Titles the viewer has
// hidden (disliked, not interested) sort with the rest: browse lists
// everything.
const recForYouExact = 2000

func (s *Service) recForYouValued(p pageShape) ([]personalRow, error) {
	r := HomeRequest{Viewer: Viewer{Profile: p.request.Profile, Fence: p.request.ViewerFence, Libraries: []string{p.request.Library}, Restrictions: p.request.Restrictions},
		Profile: p.request.Profile, ViewerFence: p.request.ViewerFence, Libraries: []string{p.request.Library}, Restrictions: p.request.Restrictions}
	r.Now = s.recommendationNow(s.browseInstant)
	x, err := s.recSession(r)
	if err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(append([]any{p.where}, p.args...))
	digest := sha256.Sum256(raw)
	ranked, err := x.memo("forYou:"+hex.EncodeToString(digest[:12])+":"+strconv.Itoa(p.total), func() ([]recCandidate, error) {
		if p.total <= recForYouExact {
			works, err := scanInt64s(s.read().Query(`SELECT e.entity_id FROM catalog_browse_rows e WHERE `+p.where, p.args...))
			if err != nil {
				return nil, err
			}
			return x.rank(recOptions{facets: []string{}, works: works, noFill: true, noSimilar: true, anyMatch: true, includeEngaged: true, all: true})
		}
		return x.rank(recOptions{includeEngaged: true})
	})
	if err != nil {
		return nil, err
	}
	if len(ranked) == 0 {
		return nil, nil
	}
	if err := s.recLoadSignals(r.Profile, &x.taste, recCandidateWorks(ranked)); err != nil {
		return nil, err
	}
	// What the viewer has already watched or started follows what it hasn't.
	fresh := make([]recCandidate, 0, len(ranked))
	var seen []recCandidate
	for _, c := range ranked {
		if id, _ := strconv.ParseInt(c.Work, 10, 64); x.taste.engaged[id] {
			seen = append(seen, c)
		} else {
			fresh = append(fresh, c)
		}
	}
	ranked = append(fresh, seen...)
	ids := make([]int64, 0, len(ranked))
	for _, c := range ranked {
		id, err := strconv.ParseInt(c.Work, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("for you: %w", err)
		}
		ids = append(ids, id)
	}
	// Keep the ranked works the pivot and filter hold, in rank order, with
	// their title keys (the rest's position counts them).
	args := append([]any{idsJSON64(ids)}, p.args...)
	rows, err := s.read().Query(`SELECT e.entity_id,lower(e.sort_key) FROM json_each(?) j CROSS JOIN catalog_browse_rows e ON e.entity_id=j.value WHERE `+p.where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	in := map[int64]string{}
	for rows.Next() {
		var id int64
		var key string
		if err = rows.Scan(&id, &key); err != nil {
			return nil, err
		}
		in[id] = key
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	out := make([]personalRow, 0, len(in))
	for _, id := range ids {
		if key, ok := in[id]; ok {
			out = append(out, personalRow{entity: id, title: titleKey{key: key, entity: id}})
		}
	}
	return out, nil
}

// recBrowseFence is what a For you order depends on beyond the catalogue and
// viewer revisions: the taste revision, unprocessed taste jobs, the
// recommendation data revision and the day.
func (s *Service) recBrowseFence(profile string) (string, error) {
	var taste, jobs, data int64
	if err := s.read().QueryRow(`SELECT COALESCE((SELECT revision FROM rec_profile_revisions WHERE profile_id=?1),0),
	 COALESCE((SELECT sum(revision) FROM rec_profile_jobs WHERE profile_id=?1),0),(SELECT revision FROM rec_data_revision WHERE id=1)`, profile).Scan(&taste, &jobs, &data); err != nil {
		return "", err
	}
	return fmt.Sprintf("%d:%d:%d:%s", taste, jobs, data, s.recommendationNow(time.Time{}).UTC().Format("2006-01-02")), nil
}

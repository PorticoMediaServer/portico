package catalog

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
)

// Continue Watching has a configured maximum, so it only needs enough recent
// started shows to fill that maximum. If restrictions or completed shows remove
// candidates, expand the indexed activity window until it fills or all started
// shows have been considered. The final list is frozen into this request's
// source so its total, cursor and page refer to the same selected episodes.
func (s *Service) homeContinueSource(r HomeRequest, libraries, cutoffs, premieres string, maximum int) (homeSource, error) {
	if maximum < 1 {
		maximum = 1
	}
	movies := homeContinueBase("continue_movies")
	restriction, bound := ItemRestrictionSQL("m.entity", r.Restrictions)
	movieArgs := append([]any{r.Profile, libraries}, bound...)
	movieRows, err := s.homeCandidates(`SELECT m.id,m.ord FROM (`+movies+`) m WHERE `+restriction+` ORDER BY m.ord DESC,m.id DESC LIMIT `+strconv.Itoa(maximum), movieArgs...)
	if err != nil {
		return homeSource{}, err
	}
	// Walk the started shows newest first in slices that continue from the
	// previous slice's last key, so each show is evaluated once, and stop as
	// soon as the maximum is filled: every later show's slot is older than every
	// slot already chosen (B83). The walk never counts the profile's history.
	// A started show the viewer may see none of is passed over by one key
	// lookup in its library's class, never by testing its episodes.
	visible, err := s.publishedClassesSQL("w.show_id", r.Libraries, r.Restrictions)
	if err != nil {
		return homeSource{}, err
	}
	if visible != "1" {
		r.visibleShows = visible
	}
	shows := []homeCandidate{}
	var after *showActivityKey
	considered := 0
	for slice := maximum; len(shows) < maximum && considered < continueShowWindow; slice = min(slice*2, 1024) {
		considered += slice
		query, args := homeShowContinuation(r, cutoffs, premieres, slice, "", after)
		found, err := s.homeCandidates(`SELECT id,ord FROM (`+query+`)`, args...)
		if err != nil {
			return homeSource{}, err
		}
		shows = append(shows, found...)
		if len(shows) >= maximum {
			break
		}
		next, err := s.showActivityAfter(r.Profile, libraries, after, slice)
		if errors.Is(err, sql.ErrNoRows) {
			break // every started show has been considered
		}
		if err != nil {
			return homeSource{}, err
		}
		after = &next
	}
	selected := mergeHome(movieRows, shows, maximum)
	values := make([][2]string, len(selected))
	for i, candidate := range selected {
		values[i] = [2]string{candidate.id, candidate.order}
	}
	raw, _ := json.Marshal(values)
	n := len(values)
	return homeSource{
		base:           `SELECT json_extract(value,'$[0]') AS id,json_extract(value,'$[1]') AS ord FROM json_each(?)`,
		args:           []any{string(raw)},
		total:          &n,
		selfRestricted: true,
		fingerprint:    fmt.Sprintf("%x", sha256.Sum256(raw)),
	}, nil
}

// continueShowWindow is how many of the profile's most recently watched
// shows Continue Watching considers. A restricted viewer may be refused most
// of a long history; the row then shows what this many recent shows offer,
// at the same cost in any library.
const continueShowWindow = 512

// showActivityAfter is the key of the last show in the slice of size n that
// starts after the given key (the n-th newest from there), or sql.ErrNoRows
// when the slice held fewer than n shows. It reads n index entries.
func (s *Service) showActivityAfter(profile, libraries string, after *showActivityKey, n int) (showActivityKey, error) {
	query := `SELECT last_activity,show_id FROM profile_show_activity WHERE profile_id=? AND library_id IN(SELECT value FROM json_each(?))`
	args := []any{profile, libraries}
	if after != nil {
		query += ` AND (last_activity<? OR last_activity=? AND show_id>?)`
		args = append(args, after.lastActivity, after.lastActivity, after.showID)
	}
	var key showActivityKey
	err := s.read().QueryRow(query+` ORDER BY last_activity DESC,show_id LIMIT 1 OFFSET `+strconv.Itoa(n-1), args...).Scan(&key.lastActivity, &key.showID)
	return key, err
}

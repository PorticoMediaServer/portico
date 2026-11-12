package db

import (
	"fmt"
	"time"
)

// popularOrder is Popular (browse rule 40): plays on the whole server in the last seven days,
// and then plays ever. Who played is never read, and the counts are never sent. Only the plays
// of profiles that share their activity count (PrefShareActivity, off unless the profile turns
// it on), as the setting stands now, so turning it off takes a profile's plays out at once; a
// play made while a profile had its history paused never counts. The seven days end on the
// hour, so a list keeps one order while it is paged through.
func popularOrder(now time.Time) sortSpec {
	since := now.Truncate(time.Hour).Add(-7 * 24 * time.Hour).UnixMilli()
	return sortSpec{columns: []string{
		fmt.Sprintf("(SELECT count(*) FROM play_history h WHERE h.item_id = i.id AND %s AND h.played_at >= %d)", sharedPlay, since),
		"(SELECT count(*) FROM play_history h WHERE h.item_id = i.id AND " + sharedPlay + ")"}}
}

// PrefShareActivity is the profile's Privacy setting "Show my activity to others on this server".
const PrefShareActivity = "privacy.showActivityToMembers"

// sharedPlay is the condition that the play h counts towards Popular: its profile shares its
// activity, and its history was not paused.
const sharedPlay = `h.hidden_from_profile = 0 AND h.profile_id IN (SELECT profile_id FROM preferences
	WHERE key = '` + PrefShareActivity + `' AND value = 'true' AND device_class = '' AND device_id IS NULL)`

// hasArtist reports whether the list is narrowed to one artist's titles.
func (q ItemQuery) hasArtist() bool {
	for _, c := range q.Conditions {
		if c.Field == "artist" && len(c.Values) == 1 && (c.Mode == "" || c.Mode == ModeAny) {
			return true
		}
	}
	return false
}

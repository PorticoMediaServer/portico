package librarychannels

import "github.com/PorticoMediaServer/portico/server-next/internal/db"

// Visible decides whether a viewer may see a Library Channel. It is the one place this is
// decided, and the guide, On now, the channel list, the logo and tuning in all ask it.
//
// It fails closed. A profile sees a channel only when it is allowed Live TV and may see every
// library the channel and its blocks draw from (libraries, the copy written with each save).
// A library in the list that no longer exists is in nobody's set, so it hides the channel from
// everyone but the owner, and an empty or unreadable list does the same. Each programme is
// then still checked on its own against the profile's rating limit and blocked Tags.
func Visible(viewer db.LiveViewer, libraries []string) bool {
	if viewer.Owner {
		return true
	}
	if !viewer.LiveTV || len(libraries) == 0 {
		return false
	}
	for _, id := range libraries {
		if !viewer.Libraries[id] {
			return false
		}
	}
	return true
}

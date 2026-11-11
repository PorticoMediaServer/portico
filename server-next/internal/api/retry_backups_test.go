package api

import "time"

// The owner console: backups.
func init() {
	safeToRepeat(map[string]string{
		"POST /v1/admin/backups/inspect": "changes nothing: it reads a backup",
	})
	retryAreas = append(retryAreas, func(th *theatre) []retryCase {
		return []retryCase{
			// Back up now takes no body; a repeat with the key answers the backup made first.
			{route: "POST /v1/admin/backups", count: func() int {
				for th.s.Backups().Running() != nil {
					time.Sleep(10 * time.Millisecond)
				}
				list, _ := th.s.Backups().List()
				return len(list)
			}},
		}
	})
}

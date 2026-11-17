package db

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// AddJobToList adds a page of a view in short background writes. Each source title has
// its own key, so a restart with different page boundaries cannot add a successful title
// twice. A holder expands once; another intentional job may add it again.
func (d *DB) AddJobToList(ctx context.Context, v Viewer, list string, items []string, job Job, now time.Time) (ListAdded, error) {
	var out ListAdded
	for next := 0; next < len(items); {
		err := d.listWrite(ctx, Background, v, list, 0, false, now, func(e *listEdit) error {
			e.jobID = job.ID
			written, added := 0, 0
			for next < len(items) && written < 300 {
				id := items[next]
				key := "list-view:" + job.PublicID + ":" + id
				before, err := createdBefore(e.tx, v.UserID, key, key)
				if err != nil {
					return err
				}
				var n, already int
				if before != "" {
					if _, err := fmt.Sscanf(before, "%d %d", &n, &already); err != nil {
						return err
					}
				} else {
					// With one source title, not-found and overfull-holder errors happen
					// before add writes anything. Other errors roll the whole write back.
					n, already, err = e.add([]string{id}, 0)
					code := ""
					switch {
					case errors.Is(err, ErrNotFound): // gone or no longer this profile's to see: skip
					case errors.Is(err, ErrQueueFull):
						code = "too_many_titles"
					case err != nil:
						return err
					default:
						// Ordinary creating requests prune old keys; a job need not run that
						// sweep once for every title it adds.
						_, err := e.tx.Exec(`INSERT INTO idempotency_keys (user_id, key, request_hash, created_ref, created_at)
							VALUES (?, ?, ?, ?, ?)`, v.UserID, key, key, fmt.Sprintf("%d %d", n, already), now.UnixMilli())
						if err != nil {
							return err
						}
						added += n
					}
					if err := jobItemOutcome(e.tx, job.ID, id, code); err != nil {
						return err
					}
				}
				out.Added, out.Already = out.Added+n, out.Already+already
				written += max(n, 1) // one holder can exceed the write's budget
				next++
			}
			e.unchanged = added == 0
			return nil
		}, &out.List)
		if err != nil {
			return ListAdded{}, err
		}
	}
	return out, nil
}

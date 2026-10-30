package scanevents

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/apievents"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"sort"
	"strings"
	"time"
)

type activityTaskEntry struct {
	signature string
	sentAt    time.Time
}

// The existing publisher follows live work queues, never completed library
// contents. Administrative hints contain no progress, names or file paths.
func (p *Publisher) tickTaskProgress(ctx context.Context, now time.Time) (*time.Time, error) {
	rows, e := p.db.QueryContext(ctx, `SELECT 'analysis:'||a.job_id||':'||a.object_id||':'||a.stage,a.updated_ms||':'||a.processed_bytes
 FROM inventory_source_active active CROSS JOIN inventory_runs r ON r.job_id=active.job_id CROSS JOIN jobs j ON j.id=r.job_id AND j.status='running'
 CROSS JOIN analysis_stage_runs a ON a.job_id=r.job_id AND a.state='running' AND a.policy_revision=r.policy_revision
 CROSS JOIN inventory_objects o ON o.id=a.object_id AND o.revision=a.source_revision
 UNION ALL SELECT c.id,c.revision||':'||COALESCE(c.processed,0)||':'||COALESCE(p.done,0)||':'||COALESCE(p.failed,0) FROM console_operations c INDEXED BY console_active_work LEFT JOIN personal_jobs p ON p.id=c.resource WHERE c.state IN ('queued','running','cancellation-requested','reconciling','paused') AND c.state IN('queued','running','reconciling') AND c.kind<>'library-scan'
 UNION ALL SELECT 'segments:'||library_id,pending_count||':'||max_updated_ms FROM segment_pending_activity
 UNION ALL SELECT 'scan-file:'||r.job_id,'' FROM inventory_source_active active CROSS JOIN inventory_runs r ON r.job_id=active.job_id CROSS JOIN jobs j ON j.id=r.job_id AND j.status='running'`)
	if e != nil {
		return nil, e
	}
	current := map[string]string{}
	for rows.Next() {
		var id, signature string
		if e = rows.Scan(&id, &signature); e != nil {
			break
		}
		if job, scan := strings.CutPrefix(id, "scan-file:"); scan {
			if p.files == nil {
				continue
			}
			// Keep private paths out of both durable events and tracked state.
			signature = fmt.Sprintf("%x", sha256.Sum256([]byte(p.files.CurrentActivityFile(job))))
		}
		current[id] = signature
	}
	if e == nil {
		e = rows.Err()
	}
	rows.Close()
	if e != nil {
		return nil, e
	}
	if p.tasks == nil {
		p.tasks = map[string]activityTaskEntry{}
	}
	ids := map[string]bool{}
	for id := range current {
		ids[id] = true
	}
	for id := range p.tasks {
		ids[id] = true
	}
	ordered := []string{}
	for id := range ids {
		ordered = append(ordered, id)
	}
	sort.Strings(ordered)
	changes := map[string]*activityTaskEntry{}
	var due *time.Time
	for _, id := range ordered {
		signature, running := current[id]
		prev, tracked := p.tasks[id]
		if tracked && running && signature == prev.signature {
			continue
		}
		if running && tracked && now.Before(prev.sentAt.Add(time.Second)) {
			next := prev.sentAt.Add(time.Second)
			if due == nil || next.Before(*due) {
				due = &next
			}
			continue
		}
		if running {
			next := activityTaskEntry{signature, now}
			changes[id] = &next
		} else {
			changes[id] = nil
		}
	}
	if len(changes) > 0 {
		e = dbwork.WithWriteTx(ctx, p.db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
			for _, id := range ordered {
				if _, ok := changes[id]; !ok {
					continue
				}
				if e := apievents.Append(tx, apievents.AdminAudience, "job.updated", "activity-task", id, fmt.Sprint(now.UnixMilli()), nil); e != nil {
					return e
				}
			}
			return nil
		})
		if e != nil {
			return due, e
		}
		for id, next := range changes {
			if next == nil {
				delete(p.tasks, id)
			} else {
				p.tasks[id] = *next
			}
		}
	}
	if len(current) > 0 {
		next := now.Add(time.Second)
		if due == nil || next.Before(*due) {
			due = &next
		}
	}
	return due, nil
}

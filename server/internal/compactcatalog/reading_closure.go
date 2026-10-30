package compactcatalog

import (
	"context"
	"database/sql"
	"strings"
)

// pathClosureSeal is an exact point proof, not an OPEN-barrier exception based
// on listing state. Physical top-level closure includes every descendant;
// logical keys retain the established Go generic-folder rules.
func pathClosureSeal(ctx context.Context, tx *sql.Tx, s pathScope) (int64, error) {
	if s.Key == "" {
		return 0, nil
	}
	top := strings.Split(s.Key, "/")[0]
	var id int64
	err := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT p.id FROM inventory_reading_seals p
 JOIN inventory_reading_jobs c ON c.job_id=p.job_id
 JOIN inventory_reading_directories d ON d.job_id=p.job_id AND d.relative_path=p.top_level
 JOIN inventory_runs r ON r.job_id=p.job_id JOIN jobs j ON j.id=r.job_id
 JOIN library_sources src ON src.id=p.source_id JOIN reading_path_source_barrier b ON b.source_id=src.id
 WHERE p.id=(SELECT id FROM inventory_reading_seals WHERE job_id=?3 AND top_level=?2 ORDER BY id DESC LIMIT 1)
 AND p.source_id=?1 AND p.top_level=?2 AND p.job_id=?3 AND p.incarnation=?4 AND p.generation=?5 AND p.root_identity=?6 AND p.root=src.root
 AND c.source_id=p.source_id AND c.incarnation=p.incarnation AND c.generation=p.generation AND c.root_identity=p.root_identity AND c.root=p.root AND c.coverage_revision=p.issue_revision
 AND src.incarnation=p.incarnation AND src.generation=p.generation AND src.root_identity=p.root_identity AND src.enabled=1 AND src.identity_confirmed=1 AND src.kind='local'
 AND r.source_id=p.source_id AND r.root_incarnation=p.incarnation AND r.source_generation=p.generation AND r.root_identity=p.root_identity AND r.scan_mode='first-import' AND j.status='running'
 AND b.job_id=p.job_id AND b.incarnation=p.incarnation AND b.source_generation=p.generation AND b.root_identity=p.root_identity AND b.root=p.root AND b.phase='open' AND b.reason='' AND b.coverage_revision=p.coverage_revision
 AND d.listing_eof=1 AND d.children=0 AND d.phase='done' AND d.issue_revision=p.issue_revision AND d.issue_highwater=p.issue_highwater
 ),0)`, s.Source, top, s.Job, s.Incarnation, s.Generation, s.RootIdentity).Scan(&id)
	return id, err
}

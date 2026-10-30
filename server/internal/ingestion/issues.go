package ingestion

import (
	"context"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
)

type ScanIssue struct {
	Category   string `json:"category"`
	Line       int    `json:"line"`
	Sequence   int64  `json:"sequence"`
	Path       string `json:"path"`
	Reason     string `json:"reason"`
	MessageKey string `json:"messageKey"`
	Warning    bool   `json:"warning"`
}
type ScanIssues struct {
	Count        int64       `json:"count"`
	JobID        string      `json:"jobId"`
	Items        []ScanIssue `json:"items"`
	NextSequence int64       `json:"nextSequence"`
	HasMore      bool        `json:"hasMore"`
}

func (s *Service) Issues(ctx context.Context, library, job string, after int64, limit int) (ScanIssues, error) {
	out := ScanIssues{JobID: job, Items: []ScanIssue{}, NextSequence: after}
	if after < 0 || limit < 1 || limit > 128 {
		return out, catalog.ErrAdminQuery
	}
	var exists bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM jobs j JOIN inventory_runs r ON r.job_id=j.id WHERE j.id=? AND j.library_id=?)`, job, library).Scan(&exists); err != nil {
		return out, err
	}
	if !exists {
		return out, catalog.ErrAdminQuery
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM inventory_scan_issues WHERE job_id=? AND `+catalog.SkippedIssuePredicate, job).Scan(&out.Count); err != nil {
		return out, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT sequence,relative_path,reason,line,warning FROM inventory_scan_issues WHERE job_id=? AND sequence>? AND `+catalog.SkippedIssuePredicate+` ORDER BY sequence LIMIT ?`, job, after, limit+1)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var issue ScanIssue
		if err = rows.Scan(&issue.Sequence, &issue.Path, &issue.Reason, &issue.Line, &issue.Warning); err != nil {
			return out, err
		}
		issue.Category = skippedCategory(issue.Reason)
		issue.MessageKey = scanIssueMessageKey(issue.Reason)
		issue.Warning = false
		out.Items = append(out.Items, issue)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	if len(out.Items) > limit {
		out.HasMore = true
		out.Items = out.Items[:limit]
	}
	if len(out.Items) > 0 {
		out.NextSequence = out.Items[len(out.Items)-1].Sequence
	}
	return out, nil
}

// Only failures that establish an unplayable file reach the owner. A combined
// probe_or_sidecar_failed code is intentionally absent: a failed sidecar does
// not establish damage. Hidden/system/changed/out-of-scope issues stay in logs.

func skippedCategory(reason string) string {
	switch reason {
	case "probe_failed", "file_truncated", "damaged_media":
		return "damaged"
	case "unsupported_disc_layout", "unsupported_disc_image", "unsupported_remote_disc", "unsupported_container", "unsupported_codec":
		return "unsupported"
	case "not_media", "unfinished_download", "partial_download", "sample_file":
		return "not-media"
	}
	return ""
}
func scanIssueMessageKey(reason string) string {
	switch reason {
	case "file_truncated":
		return "scan.issue.file_truncated"
	case "unsupported_disc_image":
		return "scan.issue.unsupported_disc_image"
	case "unsupported_disc_layout":
		return "scan.issue.unsupported_disc_layout"
	case "unsupported_remote_disc":
		return "scan.issue.unsupported_remote_disc"
	case "unfinished_download", "partial_download":
		return "scan.issue.unfinished_download"
	}
	switch skippedCategory(reason) {
	case "damaged":
		return "scan.issue.damaged"
	case "unsupported":
		return "scan.issue.unsupported"
	case "not-media":
		return "scan.issue.not_media"
	}
	return ""
}

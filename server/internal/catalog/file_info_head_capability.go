package catalog

import (
	"context"
	"database/sql"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// Renderer capability is a process composition fact, captured in the FileInfo
// audience. Apply it as a per-raw-row match decision so totals and anchors are
// exact; filtering only during hydration would silently shorten a ready lane.
type fileInfoHeadCapabilitySource struct {
	raw fileInfoOrderedSource
	db  *sql.DB
}

func (s *fileInfoHeadCapabilitySource) AccountedBytes() int64           { return s.raw.AccountedBytes() + 128 }
func (s *fileInfoHeadCapabilitySource) Check(ctx context.Context) error { return s.raw.Check(ctx) }
func (s *fileInfoHeadCapabilitySource) Compare(ctx context.Context, a, b fileInfoOccurrence) (int, error) {
	return s.raw.Compare(ctx, a, b)
}
func (s *fileInfoHeadCapabilitySource) Next(ctx context.Context) (fileInfoOrderedRow, bool, error) {
	row, exists, e := s.raw.Next(ctx)
	if e != nil || !exists || !row.Match {
		return row, exists, e
	}
	var disc bool
	e = dbwork.ReadHandle(ctx, s.db).QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM subtitle_disc_resources d WHERE d.resource_id=(SELECT id FROM subtitle_resources WHERE rowid=?))`, row.Occurrence.Item).Scan(&disc)
	if disc {
		row.Match = false
		row.Eligible = false
	}
	return row, exists, e
}

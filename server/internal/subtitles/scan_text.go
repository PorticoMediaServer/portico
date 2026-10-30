package subtitles

import (
	"context"
	"database/sql"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/subtitleoffer"
)

// ImportScanText is called only by the durable inventory analysis worker after
// its exact source revision has been published. It completes the caller's
// inventory fence without extracting bytes or borrowing viewer credentials.
func (s *Service) ImportScanText(ctx context.Context, item, source string, check func(context.Context, *sql.Tx) error) error {
	if check == nil {
		return ErrInput
	}
	// Discovery creates selectable resources. Extraction is a cache fill at
	// first delivery, so basic scans never demux the whole media file.
	snapshot, e := dbwork.BeginSnapshot(ctx, s.db)
	if e != nil {
		return e
	}
	defer snapshot.Rollback()
	return check(ctx, snapshot.Tx())
}

func textFormat(s string) bool { return subtitleoffer.TextFormat(s) }

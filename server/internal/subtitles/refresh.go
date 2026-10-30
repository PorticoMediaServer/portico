package subtitles

import (
	"context"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// Refresh acquires current media facts only after viewer/library authorization.
// Decoder I/O is outside the transaction; publication rechecks the source and
// authority. STRM descriptors remain access configuration, not media identity.
func (s *Service) Refresh(ctx context.Context, p identity.Principal, item, source string) (Catalog, error) {
	if !validID(source) || s.Extractor == nil {
		return Catalog{}, ErrInput
	}
	gated, p, e := s.tx(ctx, p, item)
	if e != nil {
		return Catalog{}, e
	}
	tx := gated.Tx()
	src, e := sourceQuery(ctx, tx, item, source)
	gated.Rollback()
	if e != nil {
		return Catalog{}, e
	}
	inventory, evidence, e := s.Extractor.Probe(ctx, item, source)
	if e != nil {
		return Catalog{}, e
	}
	combined, e := Discover(ctx, s.storage, item, src.path, inventory.Tracks, inventory.OriginUS, inventory.TimingKnown)
	if e != nil {
		return Catalog{}, e
	}
	combined.SourceEvidence = evidence
	var gated2 *dbwork.Write
	gated2, p, e = s.tx(ctx, p, item)
	if e != nil {
		return Catalog{}, e
	}
	tx = gated2.Tx()
	defer gated2.Rollback()
	now, e := sourceQuery(ctx, tx, item, source)
	if e != nil {
		return Catalog{}, e
	}
	if !now.Available || src.size != now.size || src.modified != now.modified || src.path != now.path {
		return Catalog{}, ErrConflict
	}
	if e = Persist(tx, source, src.size, src.modified, combined); e != nil {
		return Catalog{}, e
	}
	if e = gated2.Commit(); e != nil {
		return Catalog{}, e
	}
	return s.List(ctx, p, item)
}

package subtitles

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/mediaartifact"
)

// SelectedBurnInput is an internal producer capability. Only the exact selected
// revision of a live, source-pinned session is opened; callers must match the persisted plan revision and cannot supply a filesystem path. The immutable object lease survives GC.
func (s *Service) SelectedBurnInput(ctx context.Context, session, resource string, revision int64) (*RenderAsset, int64, error) {
	gate, e := dbwork.Begin(ctx, s.db, dbwork.ClassEstablishedPlayback)
	if e != nil {
		return nil, 0, e
	}
	defer gate.Rollback()
	if e = checkDiscResourceSessionTx(ctx, gate.Tx(), session, resource); e != nil {
		return nil, 0, e
	}
	var digest string
	var size, offset int64
	e = gate.Tx().QueryRowContext(ctx, `SELECT v.digest,v.size,st.offset_us FROM playback_subtitle_state st
 JOIN playback_sessions ps ON ps.id=st.session_id AND ps.generation=st.generation
 JOIN authorization_access auth ON auth.hash=`+identity.CurrentFamilyTokenSQL("ps.session_hash")+` AND auth.revoked=0 AND auth.expires_at>strftime('%Y-%m-%dT%H:%M:%SZ','now') AND (auth.authority!='local' OR EXISTS(SELECT 1 FROM accounts ac WHERE ac.id=auth.account_id AND ac.epoch=auth.epoch))
 JOIN playback_source_pins source ON source.session_id=ps.id
  JOIN catalog_assets a ON a.token=source.asset_id AND a.size=source.size AND a.modified_ns=source.modified_ns AND a.available=1
 JOIN playback_subtitle_pins pin ON pin.session_id=st.session_id AND pin.resource_id=st.resource_id AND pin.resource_revision=st.resource_revision
 JOIN subtitle_revisions v ON v.resource_id=pin.resource_id AND v.revision=pin.resource_revision AND v.source_size=source.size AND v.source_modified_ns=source.modified_ns
 WHERE st.session_id=? AND st.resource_id=? AND st.resource_revision=? AND st.renderer='burn_in' AND ps.state NOT IN('stopped','ended','failed') AND ps.expires_at>strftime('%Y-%m-%dT%H:%M:%SZ','now')`, session, resource, revision).Scan(&digest, &size, &offset)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, 0, nil
	}
	if e != nil {
		return nil, 0, e
	}
	reader, e := s.objects.Open(ctx, mediaartifact.Object{Digest: digest, Size: size})
	if e != nil {
		return nil, 0, e
	}
	defer reader.Close()
	if e = gate.Commit(); e != nil {
		return nil, 0, e
	}
	raw, e := io.ReadAll(io.LimitReader(reader, int64(AssetLimitBytes())+1))
	if e != nil {
		return nil, 0, e
	}
	asset, e := DecodeRenderAsset(raw)
	if e != nil {
		return nil, 0, e
	}
	return &asset, offset, nil
}

package subtitles

import (
	"context"
	"database/sql"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/disc"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// DiscScope is server-private normalized source/title/facts identity, never a
// public selection key. Readers recheck it in the caller's admitted snapshot.
type DiscScope struct{ Source, Object, TitleKey, FactsRevision, SourceRevision, Fingerprint string }

const discResourceCurrent = `EXISTS(SELECT 1 FROM subtitle_disc_resources dr
 JOIN inventory_disc_facts df ON df.object_id=dr.object_id AND df.asset_id=r.source_id
 JOIN inventory_objects o ON o.id=df.object_id AND o.asset_id=df.asset_id
 JOIN catalog_assets a ON a.token=df.asset_id AND a.available=1 AND a.size=o.size AND a.modified_ns=o.modified_ns
 JOIN library_sources ls ON ls.id=o.source_id
 JOIN library_scan_policies policy ON policy.library_id=ls.library_id AND policy.revision=dr.policy_revision
 JOIN inventory_disc_title_media tm ON tm.object_id=dr.object_id AND tm.title_key=dr.title_key AND tm.revision=dr.title_facts_revision
 JOIN inventory_disc_title_media_streams st ON st.object_id=tm.object_id AND st.title_key=tm.title_key AND st.stream_index=dr.stream_index AND st.type='subtitle'
 WHERE dr.resource_id=r.id AND dr.source_revision=df.source_revision AND dr.fingerprint=df.fingerprint
 AND tm.source_revision=df.source_revision AND tm.fingerprint=df.fingerprint AND tm.timing_known=1 AND dr.timing_known=1 AND dr.origin_us=tm.origin_us AND df.encryption='none'
 AND o.revision=df.source_revision AND o.root_incarnation=ls.incarnation AND df.root_incarnation=ls.incarnation
 AND policy.tier<>'file_list_only' AND EXISTS(SELECT 1 FROM json_each(policy.operations_json) op WHERE op.value='subtitles')
 AND df.source_generation=ls.generation AND ls.enabled=1 AND ls.kind='local' AND o.retired=0 AND o.state='available')`
const discDefaultResource = `NOT EXISTS(SELECT 1 FROM playback_disc_subtitle_resources private WHERE private.resource_id=r.id) AND ((NOT EXISTS(SELECT 1 FROM inventory_disc_facts d WHERE d.asset_id=r.source_id) AND NOT EXISTS(SELECT 1 FROM subtitle_disc_resources dr WHERE dr.resource_id=r.id))
 OR (` + discResourceCurrent + ` AND EXISTS(SELECT 1 FROM subtitle_disc_resources dr JOIN inventory_disc_facts d ON d.object_id=dr.object_id WHERE dr.resource_id=r.id AND dr.title_key=d.media_title_key AND (d.owner_title_key='' OR d.owner_title_key=dr.title_key) AND EXISTS(SELECT 1 FROM catalog_assets a JOIN asset_stream_facts st ON st.asset_id=a.token AND st.size=a.size AND st.modified_ns=a.modified_ns JOIN asset_chapter_facts ch ON ch.asset_id=a.token AND ch.size=a.size AND ch.modified_ns=a.modified_ns WHERE a.token=r.source_id AND st.revision=d.media_stream_revision AND ch.revision=d.media_chapter_revision AND ch.status='known'))))`

func CheckDiscScopeTx(ctx context.Context, tx *sql.Tx, scope DiscScope) error {
	var ok bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM inventory_disc_facts d JOIN inventory_objects o ON o.id=d.object_id AND o.asset_id=d.asset_id JOIN catalog_assets a ON a.token=d.asset_id AND a.available=1 AND a.size=o.size AND a.modified_ns=o.modified_ns JOIN library_sources s ON s.id=o.source_id JOIN inventory_disc_title_media m ON m.object_id=d.object_id WHERE d.object_id=? AND d.asset_id=? AND m.title_key=? AND m.revision=? AND d.source_revision=? AND d.fingerprint=? AND m.source_revision=d.source_revision AND m.fingerprint=d.fingerprint AND d.encryption='none' AND o.revision=d.source_revision AND o.retired=0 AND o.state='available' AND s.enabled=1 AND s.kind='local' AND d.source_generation=s.generation AND d.root_incarnation=s.incarnation AND o.root_incarnation=s.incarnation)`, scope.Object, scope.Source, scope.TitleKey, scope.FactsRevision, scope.SourceRevision, scope.Fingerprint).Scan(&ok)
	if err != nil {
		return err
	}
	if !ok {
		return ErrConflict
	}
	return nil
}
func discSessionScopeTx(ctx context.Context, tx *sql.Tx, session string) (*DiscScope, error) {
	var private bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM playback_disc_snapshots WHERE session_id=?)`, session).Scan(&private); err != nil {
		return nil, err
	}
	if private {
		// An absent local pin cannot make a private title an ordinary asset
		// scope. Its resource resolver must retain the exact accepted snapshot;
		// until that resolver is composed, no inventory resource is substituted.
		return nil, ErrUnsupported
	}
	if err := disc.CheckPlaybackSelectionTx(ctx, tx, session); err != nil {
		return nil, ErrConflict
	}
	var scope DiscScope
	err := tx.QueryRowContext(ctx, `SELECT ps.asset_id,p.object_id,p.title_key,m.revision,p.source_revision,p.fingerprint FROM playback_disc_pins p JOIN playback_sessions ps ON ps.id=p.session_id JOIN inventory_disc_title_media m ON m.object_id=p.object_id AND m.title_key=p.title_key AND m.source_revision=p.source_revision AND m.fingerprint=p.fingerprint WHERE p.session_id=? AND p.title_facts_revision=m.revision`, session).Scan(&scope.Source, &scope.Object, &scope.TitleKey, &scope.FactsRevision, &scope.SourceRevision, &scope.Fingerprint)
	if errors.Is(err, sql.ErrNoRows) {
		var known bool
		if e := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM playback_disc_pins WHERE session_id=?)`, session).Scan(&known); e != nil {
			return nil, e
		}
		if known {
			return nil, ErrConflict
		}
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &scope, CheckDiscScopeTx(ctx, tx, scope)
}
func checkDiscResourceSessionTx(ctx context.Context, tx *sql.Tx, session, resource string) error {
	var private bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM playback_disc_subtitle_resources WHERE resource_id=?)`, resource).Scan(&private); err != nil {
		return err
	}
	if private {
		return ErrConflict
	}
	scope, err := discSessionScopeTx(ctx, tx, session)
	if err != nil {
		return err
	}
	var related bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM subtitle_disc_resources WHERE resource_id=?)`, resource).Scan(&related); err != nil {
		return err
	}
	if scope == nil {
		if related {
			return ErrConflict
		}
		return nil
	}
	if !related {
		return ErrConflict
	}
	var ok bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM subtitle_resources r JOIN subtitle_disc_resources dr ON dr.resource_id=r.id WHERE r.id=? AND r.source_id=? AND dr.object_id=? AND dr.title_key=? AND dr.title_facts_revision=? AND `+discResourceCurrent+`)`, resource, scope.Source, scope.Object, scope.TitleKey, scope.FactsRevision).Scan(&ok)
	if err != nil {
		return err
	}
	if !ok {
		return ErrConflict
	}
	return nil
}

// ListDiscTitleTx is only a view over already accepted title facts. Callers
// must retain ordinary current item/viewer authorization in this transaction.
func (s *Service) ListDiscTitleTx(ctx context.Context, tx *sql.Tx, p identity.Principal, item string, scope DiscScope) (Catalog, error) {
	if err := CheckDiscScopeTx(ctx, tx, scope); err != nil {
		return Catalog{}, err
	}
	return s.listTx(ctx, tx, p, item, scope.Source, &scope)
}
func (s *Service) listSessionTx(ctx context.Context, tx *sql.Tx, p identity.Principal, item, source, session string) (Catalog, error) {
	private, known, err := s.privateDiscPresentationTx(ctx, tx, session)
	if err != nil {
		return Catalog{}, err
	}
	if known {
		if private.AssetID != source {
			return Catalog{}, ErrConflict
		}
		return s.listPrivateDiscTx(ctx, tx, p, item, private)
	}
	scope, err := discSessionScopeTx(ctx, tx, session)
	if err != nil {
		return Catalog{}, err
	}
	return s.listTx(ctx, tx, p, item, source, scope)
}

// CheckDiscResourceSessionTx adds title binding to a caller's existing session,
// viewer and resource admission. It never grants authority on its own.
func CheckDiscResourceSessionTx(ctx context.Context, tx *sql.Tx, session, resource string) error {
	return checkDiscResourceSessionTx(ctx, tx, session, resource)
}

// The placeholder is replaced only with a fixed positional SQL parameter.
// Filtering before LIMIT prevents another title from consuming this title's slots.
const discSessionResourcePredicate = `NOT EXISTS(SELECT 1 FROM playback_disc_snapshots private WHERE private.session_id=$SESSION) AND NOT EXISTS(SELECT 1 FROM playback_disc_subtitle_resources private WHERE private.resource_id=r.id) AND ((NOT EXISTS(SELECT 1 FROM playback_disc_pins dp WHERE dp.session_id=$SESSION) AND NOT EXISTS(SELECT 1 FROM subtitle_disc_resources dr WHERE dr.resource_id=r.id)) OR (` + discResourceCurrent + ` AND EXISTS(SELECT 1 FROM playback_disc_pins dp JOIN subtitle_disc_resources dr ON dr.object_id=dp.object_id AND dr.title_key=dp.title_key JOIN inventory_disc_title_media tm ON tm.object_id=dr.object_id AND tm.title_key=dr.title_key AND tm.revision=dr.title_facts_revision WHERE dp.session_id=$SESSION AND dr.resource_id=r.id AND dp.title_facts_revision=dr.title_facts_revision AND dp.source_revision=dr.source_revision AND dp.fingerprint=dr.fingerprint)))`

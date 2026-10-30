package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"

	"github.com/PorticoMediaServer/portico/server/internal/readprep"
)

// CaptureFileInfoAdmission supplies the data half of live admission. HTTP also
// rechecks the authenticated family, profile, library grants and owner role.
// ContentRevision is deliberately absent: the original Detail is an independent
// opening, while this audience includes policy and personal track visibility.
func (s *Service) CaptureFileInfoAdmission(ctx context.Context, v Viewer, server, item string, owner bool, subtitleActor string, discSubtitles bool) (FileInfoAdmission, error) {
	var ad FileInfoAdmission
	q := fileInfoQuery(ctx, s.db)
	var public []byte
	var nonce, library string
	var playable bool
	var kind string
	if e := q.QueryRowContext(ctx, `SELECT k.name FROM catalog_entities e JOIN catalog_kinds k ON k.id=e.kind WHERE e.public_id=pid_blob(?) AND e.retired=0`, item).Scan(&kind); e != nil {
		return ad, e
	}
	visible, args := v.itemVisibilitySQL("e.id")
	switch kind {
	case "album", "show", "season", "artist", "book", "collection":
		visible, args = v.entityVisibilitySQL("e.id", "cl.library_id")
	}
	// metadata_publication_heads exists only for playable kinds. A container
	// root instead owns the full artwork incarnation, created with the entity
	// and deleted with it, so that nonce witnesses the same lifetime. The join
	// must name the artwork head's own (kind,entity_id) namespace, so only the
	// head for this exact root kind can supply the nonce.
	head := `metadata_publication_heads h ON h.item_id=e.id`
	switch kind {
	case "album", "show", "season", "artist", "book", "collection":
		head = `artwork_entity_heads h ON h.entity_id=e.id AND h.kind=CASE e.kind
 WHEN 2 THEN 'show' WHEN 3 THEN 'season' WHEN 5 THEN 'artist' WHEN 6 THEN 'album' WHEN 8 THEN 'book' ELSE 'item' END`
	}
	values := append([]any{item}, args...)
	e := q.QueryRowContext(ctx, `SELECT e.id,e.library_id,e.public_id,h.incarnation,cl.library_id,k.name,k.playable,COALESCE(c.revision,1),e.info_desired_epoch,e.info_applied_epoch,e.info_source_blockers,e.info_availability_pending,e.info_blocked_children
 FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id
 JOIN catalog_kinds k ON k.id=e.kind JOIN `+head+`
 LEFT JOIN library_configuration c ON c.library_id=cl.library_id
 WHERE e.public_id=pid_blob(?) AND `+visible, values...).Scan(&ad.Identity.Item, &ad.Identity.Library, &public, &nonce, &library, &ad.Kind, &playable, &ad.ConfigurationRevision, &ad.Head.Desired, &ad.Head.Applied, &ad.Head.SourceBlockers, &ad.Head.AvailabilityPending, &ad.Head.BlockedChildren)
	if e != nil {
		return ad, e
	}
	if len(public) != 16 {
		return ad, readprep.ErrStale
	}
	copy(ad.Identity.PublicItem[:], public)
	raw, e := hex.DecodeString(nonce)
	if e != nil || len(raw) != 16 {
		return ad, readprep.ErrStale
	}
	copy(ad.ItemIncarnation[:], raw)
	ad.Owner, ad.Playable, ad.Children = owner, playable, ad.Kind == "album" || ad.Kind == "book"
	ad.SubtitleActor = subtitleActor
	ad.DiscSubtitlesAvailable = discSubtitles
	captured, _ := json.Marshal(v)
	// Include the retained slice elements as well as their scalar bytes. JSON
	// alone does not cover Go string headers for a large grant/label vector.
	ad.CaptureBytes = int64(len(captured) + len(subtitleActor) + 2048 + 24*(cap(v.Libraries)+cap(v.Restrictions.BlockedLabels)+cap(v.Restrictions.MemberDeniedLabels)))
	ad.DetailScope = DetailScope{server, library, item, v.Fence}
	var policy *int64
	if v.Restrictions.Active() {
		var n int64
		if e = q.QueryRowContext(ctx, `SELECT policy_epoch FROM catalog_facet_source_state WHERE singleton=1`).Scan(&n); e != nil {
			return ad, e
		}
		policy = &n
	}
	var subs int64
	if e = q.QueryRowContext(ctx, `SELECT COALESCE((SELECT revision FROM subtitle_collections WHERE item_id=?),1)`, ad.Identity.Item).Scan(&subs); e != nil {
		return ad, e
	}
	audience, e := json.Marshal(struct {
		Fence, Profile, Actor string
		Libraries             []string
		Policy                *int64
		Subtitles             int64
	}{v.Fence, v.Profile, subtitleActor, v.Libraries, policy, subs})
	if e != nil {
		return ad, e
	}
	hash := sha256.Sum256(audience)
	ad.Scope = DetailScope{server, library, item, base64.RawURLEncoding.EncodeToString(hash[:])}
	if ad.Children {
		db := s.db
		parent := ad.Identity.Item
		ad.ChildVisible = func(c context.Context, child int64) (bool, error) {
			clause, values := v.itemVisibilitySQL("e.id")
			var allowed bool
			e := fileInfoQuery(c, db).QueryRowContext(c, `SELECT EXISTS(SELECT 1 FROM catalog_entities e WHERE e.id=? AND e.parent_id=? AND `+clause+`)`, append([]any{child, parent}, values...)...).Scan(&allowed)
			return allowed, e
		}
	}
	return ad, nil
}

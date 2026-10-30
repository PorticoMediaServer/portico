package subtitles

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"strconv"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/mediaartifact"
)

type selectionPreparation struct {
	replace                  bool
	oldRender                string
	position, policyRevision int64
	ordinaryBurn             bool
	resource                 *Resource
}

func presentation(scope sessionScope, session, renderID string, position int64) *Presentation {
	url := "/v1/media/" + scope.grant
	mode := scope.mode
	if renderID == "ordinary" {
		url += "/master.m3u8"
		mode = "hls"
	} else if mode == "hls" {
		url += "/master.m3u8"
	}
	if mode == "remote" {
		mode = "direct"
	}
	return &Presentation{SessionID: session, Generation: scope.generation, StreamURL: url, Mode: mode, PositionUS: strconv.FormatInt(position, 10)}
}
func (s *Service) Select(ctx context.Context, p identity.Principal, item, session string, m SelectRequest) (PlaybackPlan, error) {
	return s.selectPlayback(ctx, p, item, session, m, false)
}

func (s *Service) selectPlayback(ctx context.Context, p identity.Principal, item, session string, m SelectRequest, cachePrepared bool) (PlaybackPlan, error) {
	if !validID(m.OperationID) || m.Generation < 1 || m.ExpectedRevision < 1 || (m.Mode != "off" && m.Mode != "track") {
		return PlaybackPlan{}, ErrInput
	}
	offset, e := Offset(m.OffsetUS)
	if e != nil {
		return PlaybackPlan{}, e
	}
	if m.Mode == "off" && (m.ResourceID != "" || m.ResourceRevision != 0 || offset != 0) || m.Mode == "track" && (!validID(m.ResourceID) || m.ResourceRevision < 1) {
		return PlaybackPlan{}, ErrInput
	}
	gated, p, e := s.tx(ctx, p, item)
	if e != nil {
		return PlaybackPlan{}, e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	scope, e := s.sessionTx(ctx, tx, p, item, session)
	if e != nil {
		return PlaybackPlan{}, e
	}
	if e = s.checkSelectionAcceptanceTx(ctx, tx, session); e != nil {
		return PlaybackPlan{}, e
	}
	digest := requestDigest([]any{session, m})
	var oldDigest, oldSession string
	e = tx.QueryRowContext(ctx, `SELECT request_digest,session_id FROM subtitle_selection_operations WHERE actor=? AND operation_id=?`, actor(p), m.OperationID).Scan(&oldDigest, &oldSession)
	if e == nil {
		if oldDigest != digest || oldSession != session {
			return PlaybackPlan{}, ErrOperation
		}
		gated.Rollback()
		return s.selectCommit(ctx, p, item, session, m, nil)
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return PlaybackPlan{}, e
	}
	if scope.generation != m.Generation {
		return PlaybackPlan{}, ErrConflict
	}
	if e = PinSessionTx(ctx, tx, session, scope.generation); e != nil {
		return PlaybackPlan{}, e
	}
	var revision int64
	var currentID string
	var currentRevision, currentOffset int64
	if e = tx.QueryRowContext(ctx, `SELECT revision,resource_id,resource_revision,offset_us FROM playback_subtitle_state WHERE session_id=? AND generation=?`, session, scope.generation).Scan(&revision, &currentID, &currentRevision, &currentOffset); e != nil {
		return PlaybackPlan{}, e
	}
	if revision != m.ExpectedRevision {
		return PlaybackPlan{}, ErrConflict
	}
	prepared := &selectionPreparation{}
	e = tx.QueryRowContext(ctx, `SELECT render_id FROM playback_subtitle_presentations WHERE session_id=?`, session).Scan(&prepared.oldRender)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return PlaybackPlan{}, e
	}
	var resource Resource
	var reader *mediaartifact.Reader
	needsCache := false
	if m.Mode == "track" {
		resource, e = scanResource(tx.QueryRowContext(ctx, `SELECT `+resourceColumns+` FROM subtitle_resources r JOIN subtitle_revisions v ON v.resource_id=r.id WHERE r.id=? AND r.item_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)) AND v.revision=?`, m.ResourceID, item, m.ResourceRevision))
		if e != nil {
			return PlaybackPlan{}, e
		}
		if err := s.checkDiscResourceSessionTx(ctx, tx, session, resource.ID); err != nil {
			return PlaybackPlan{}, err
		}
		if !visible(resource, p) || resource.SourceID != scope.source {
			return PlaybackPlan{}, identity.ErrUnauthorized
		}
		if resource.sourceSize != scope.size || resource.sourceModified != scope.modified || !scope.available {
			return PlaybackPlan{}, ErrConflict
		}
		var latest int64
		if e = tx.QueryRowContext(ctx, `SELECT current_revision FROM subtitle_resources WHERE id=?`, resource.ID).Scan(&latest); e != nil {
			return PlaybackPlan{}, e
		}
		if (resource.deleted || latest != resource.Revision) && (currentID != resource.ID || currentRevision != resource.Revision) {
			return PlaybackPlan{}, ErrConflict
		}
		if m.Rendering != "" {
			matched := false
			for _, d := range deliveryFormats(resource.Format) {
				if d.Format == m.Rendering {
					matched = true
				}
			}
			if !matched {
				return PlaybackPlan{}, ErrUnsupported
			}
		}
		if resource.Renderer == "burn_in" && m.Rendering == "" {
			if s.ChangeDeliveryTx == nil {
				return PlaybackPlan{}, ErrUnavailable
			}
			var enabled bool
			if e = tx.QueryRowContext(ctx, `SELECT transcoding_enabled,revision FROM playback_owner_policy WHERE singleton=1`).Scan(&enabled, &prepared.policyRevision); e != nil {
				return PlaybackPlan{}, e
			}
			if !enabled {
				return PlaybackPlan{}, ErrUnsupported
			}
			needsCache = resource.digest == ""
			// Acquire a physical object lease while still holding exact source/resource
			// authority. Garbage collection cannot delete this preparation's revision.
			if !needsCache {
				reader, e = s.objects.Open(ctx, mediaartifact.Object{Digest: resource.digest, Size: resource.size})
				if e != nil {
					return PlaybackPlan{}, e
				}
				defer reader.Close()
			}
		}
	}
	prepared.replace = resource.Renderer == "burn_in" && m.Rendering == "" || prepared.oldRender != "" || scope.mode == "hls" && currentOffset != offset
	if prepared.replace {
		position, e := strconv.ParseInt(m.PositionUS, 10, 64)
		if e != nil || position < 0 || strconv.FormatInt(position, 10) != m.PositionUS {
			return PlaybackPlan{}, ErrInput
		}
		prepared.position = position
	}
	if needsCache {
		// Admit the actual selection before any extraction, then release SQL.
		// Never borrow a detached cache worker's actor or another selection's lease.
		if cachePrepared {
			return PlaybackPlan{}, ErrConflict
		}
		selected := s.playbackSourceContext(ctx, p, item, session, scope, func(check context.Context, tx *sql.Tx) error {
			if err := s.checkDiscResourceSessionTx(check, tx, session, resource.ID); err != nil {
				return err
			}
			var selectionRevision int64
			if err := tx.QueryRowContext(check, `SELECT revision FROM playback_subtitle_state WHERE session_id=? AND generation=?`, session, m.Generation).Scan(&selectionRevision); err != nil {
				return err
			}
			if selectionRevision != m.ExpectedRevision {
				return ErrConflict
			}
			var currentRevision int64
			var deleted bool
			if err := tx.QueryRowContext(check, `SELECT current_revision,deleted FROM subtitle_resources WHERE id=? AND source_id=? AND item_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?))`, resource.ID, scope.source, item).Scan(&currentRevision, &deleted); err != nil {
				return err
			}
			if deleted || currentRevision != resource.Revision {
				return ErrConflict
			}
			return nil
		})
		defer closePlaybackSourceContext(selected)
		if e = checkPlaybackSourcePublicationTx(selected, tx); e != nil {
			return PlaybackPlan{}, e
		}
		if e = gated.Commit(); e != nil {
			return PlaybackPlan{}, e
		}
		if e = s.ensureBurnCached(selected, p, item, resource.ID, resource.Revision); e != nil {
			return PlaybackPlan{}, e
		}
		return s.selectPlayback(selected, p, item, session, m, true)
	}
	if e = gated.Commit(); e != nil {
		return PlaybackPlan{}, e
	}
	publicationContext := ctx
	// Remote representation evidence is distinct from .strm descriptor size/mtime.
	if resource.sourceEvidence != "" {
		if s.SourceInputs == nil {
			return PlaybackPlan{}, ErrUnavailable
		}
		inputContext := ctx
		if _, selected := PlaybackSourceInvocationFromContext(ctx); !selected {
			inputContext = s.playbackSourceContext(ctx, p, item, session, scope)
			defer closePlaybackSourceContext(inputContext)
		}
		publicationContext = inputContext
		input, e := s.SourceInputs.OpenSubtitleInput(inputContext, item, scope.source, session)
		if e != nil {
			return PlaybackPlan{}, e
		}
		same := input.Evidence() == resource.sourceEvidence
		valid := input.Validate(ctx)
		input.Close()
		if !same || valid != nil {
			return PlaybackPlan{}, ErrConflict
		}
	}
	if reader != nil {
		raw, e := io.ReadAll(io.LimitReader(reader, int64(AssetLimitBytes())+1))
		if e != nil {
			return PlaybackPlan{}, e
		}
		if len(raw) > AssetLimitBytes() {
			return PlaybackPlan{}, ErrCapacity
		}
		_, e = DecodeRenderAsset(raw)
		if e != nil {
			return PlaybackPlan{}, e
		}
		prepared.ordinaryBurn = true
		prepared.resource = &resource
	}

	if prepared.replace && s.PrepareDelivery != nil {
		publicationContext, e = s.PrepareDelivery(publicationContext)
		if e != nil {
			return PlaybackPlan{}, e
		}
	}
	out, e := s.selectCommit(publicationContext, p, item, session, m, prepared)
	return out, e
}

package subtitles

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/disc"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/mediaartifact"
)

// PlaybackPlan is the authoritative subtitle-only extension to the existing
// playback session. Source time remains the player's time; offsetUs moves cues.
type PlaybackPlan struct {
	Version         int           `json:"version"`
	SessionID       string        `json:"sessionId"`
	Generation      int           `json:"generation"`
	SourceID        string        `json:"sourceId"`
	Revision        int64         `json:"revision"`
	CatalogRevision int64         `json:"catalogRevision"`
	Renderer        string        `json:"renderer"`
	Mode            string        `json:"mode"`
	OffAvailable    bool          `json:"offAvailable"`
	OffsetUS        string        `json:"offsetUs"`
	Selected        *Resource     `json:"selected"`
	DocumentURL     string        `json:"documentUrl,omitempty"`
	Resources       []Resource    `json:"resources"`
	Discovered      []Discovery   `json:"discovered"`
	Presentation    *Presentation `json:"presentation,omitempty"`
	AppliedRevision int64         `json:"appliedRevision,omitempty"`
}

func PinSessionTx(ctx context.Context, tx *sql.Tx, session string, generation int) error {
	_, e := tx.ExecContext(ctx, `INSERT OR IGNORE INTO playback_subtitle_state(session_id,generation) VALUES(?,?)`, session, generation)
	return e
}

type sessionScope struct {
	source, grant  string
	generation     int
	size, modified int64
	available      bool
	mode           string
}

func sessionTx(ctx context.Context, tx *sql.Tx, p identity.Principal, item, session string) (sessionScope, error) {
	var v sessionScope
	e := tx.QueryRowContext(ctx, `SELECT s.asset_id,s.grant_token,s.generation,pin.size,pin.modified_ns,a.available,s.mode FROM playback_sessions s JOIN playback_source_pins pin ON pin.session_id=s.id AND pin.asset_id=s.asset_id JOIN catalog_assets a ON a.token=pin.asset_id WHERE s.id=? AND s.item_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)) AND s.session_hash=? AND s.account_id=? AND s.profile_id=? AND s.state NOT IN('stopped','ended','failed') AND s.expires_at>? AND a.size=pin.size AND a.modified_ns=pin.modified_ns`, session, item, p.Hash, p.AccountID, p.ProfileID, time.Now().UTC().Format(time.RFC3339)).Scan(&v.source, &v.grant, &v.generation, &v.size, &v.modified, &v.available, &v.mode)
	if errors.Is(e, sql.ErrNoRows) {
		e = identity.ErrUnauthorized
	}
	if e == nil {
		// Original stream indexes cannot be applied to a prepared MP4. P11B
		// deliberately does not advertise subtitle mapping for derivatives.
		var prepared bool
		e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM prepared_media_session_pins WHERE session_id=?)`, session).Scan(&prepared)
		if e == nil && prepared {
			e = ErrUnsupported
		}
	}
	if e == nil {
		e = disc.CheckPlaybackSelectionTx(ctx, tx, session)
	}
	return v, e
}

// Plan reads a presentation's subtitle plan in a read snapshot: it takes no
// write gate, so a scan or any other background write never delays it
// (NEW-38).
func (s *Service) Plan(ctx context.Context, p identity.Principal, item, session string) (PlaybackPlan, error) {
	snapshot, p, e := s.readTx(ctx, p, item)
	if e != nil {
		return PlaybackPlan{}, e
	}
	defer snapshot.Rollback()
	return s.PlanTx(ctx, snapshot.Tx(), p, item, session)
}
func (s *Service) PlanTx(ctx context.Context, tx *sql.Tx, p identity.Principal, item, session string) (PlaybackPlan, error) {
	out := PlaybackPlan{Version: 1, SessionID: session, Renderer: "external_text", Mode: "off", OffAvailable: true, OffsetUS: "0", Resources: []Resource{}, Discovered: []Discovery{}}
	p, e := s.authorize(ctx, tx, p, item)
	if e != nil {
		return out, e
	}
	scope, e := s.sessionTx(ctx, tx, p, item, session)
	if e != nil {
		return out, e
	}

	out.Generation = scope.generation
	out.SourceID = scope.source
	// A read never writes (NEW-38): a presentation nobody has selected a
	// subtitle for yet reads as the state a pin would create (revision 1,
	// nothing selected, no offset). The selection write pins it.
	var selected string
	var revision, offset int64
	var selectedRenderer string
	e = tx.QueryRowContext(ctx, `SELECT revision,resource_id,resource_revision,offset_us,renderer FROM playback_subtitle_state WHERE session_id=? AND generation=?`, session, scope.generation).Scan(&out.Revision, &selected, &revision, &offset, &selectedRenderer)
	if errors.Is(e, sql.ErrNoRows) {
		out.Revision, selected, revision, offset, e = 1, "", 0, 0, nil
	}
	if e != nil {
		return out, e
	}
	out.OffsetUS = strconv.FormatInt(offset, 10)
	catalog, e := s.listSessionTx(ctx, tx, p, item, scope.source, session)
	if e != nil {
		return out, e
	}
	out.CatalogRevision = catalog.Revision
	out.Resources = catalog.Resources
	out.Discovered = catalog.Discovered
	if selected != "" {
		r, e := scanResource(tx.QueryRowContext(ctx, `SELECT `+resourceColumns+` FROM subtitle_resources r JOIN subtitle_revisions v ON v.resource_id=r.id WHERE r.id=? AND r.item_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)) AND v.revision=? AND EXISTS(SELECT 1 FROM playback_subtitle_pins pin WHERE pin.session_id=? AND pin.resource_id=r.id AND pin.resource_revision=v.revision)`, selected, item, revision, session))
		if e != nil {
			return out, e
		}
		if err := s.checkDiscResourceSessionTx(ctx, tx, session, r.ID); err != nil {
			return out, err
		}
		if !visible(r, p) || r.SourceID != scope.source || r.sourceSize != scope.size || r.sourceModified != scope.modified {
			return out, identity.ErrUnauthorized
		}
		r.Pinned = true
		r.CanManage = manageable(r, p)
		r.Enabled = scope.available
		r.Reason = ""
		if !r.Enabled {
			r.Reason = "source_unavailable"
		}
		var current int64
		if e = tx.QueryRowContext(ctx, `SELECT current_revision FROM subtitle_resources WHERE id=?`, r.ID).Scan(&current); e != nil {
			return out, e
		}
		r.Retired = r.deleted || current != r.Revision
		if selectedRenderer != "" {
			r.Renderer = selectedRenderer
		}
		if scope.mode == "hls" && r.Renderer == "external_text" {
			var ordinal int
			err := tx.QueryRowContext(ctx, `SELECT 0 FROM playback_manifest_subtitles pin WHERE pin.session_id=? AND pin.resource_id=? AND pin.resource_revision=?`, session, r.ID, r.Revision).Scan(&ordinal)
			if err == nil {
				r.ManifestName = ManifestSubtitleName(r.Title, r.Language, ordinal)
			} else if !errors.Is(err, sql.ErrNoRows) {
				return out, err
			}
		}
		r.Deliveries = deliveries(MediaBaseURL(scope.grant, r.ID, r.Revision), r.Format)
		out.Selected = &r
		out.Mode = "track"
		out.Renderer = r.Renderer
		if r.Enabled && r.Renderer == "external_text" {
			out.DocumentURL = MediaBaseURL(scope.grant, r.ID, r.Revision)
		}
	}
	var renderID string
	var position int64
	e = tx.QueryRowContext(ctx, `SELECT render_id,position_us FROM playback_subtitle_presentations WHERE session_id=? AND generation=?`, session, scope.generation).Scan(&renderID, &position)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return out, e
	}
	if e == nil {
		out.Presentation = presentation(scope, session, renderID, position)
	}
	return out, nil
}

type SelectRequest struct {
	Rendering        string `json:"rendering,omitempty"`
	Recovery         bool   `json:"-"`
	OperationID      string `json:"operationId"`
	Generation       int    `json:"generation"`
	ExpectedRevision int64  `json:"expectedRevision"`
	Mode             string `json:"mode"`
	ResourceID       string `json:"resourceId,omitempty"`
	ResourceRevision int64  `json:"resourceRevision,omitempty"`
	OffsetUS         string `json:"offsetUs"`
	PositionUS       string `json:"positionUs,omitempty"`
}

func (s *Service) selectCommit(ctx context.Context, p identity.Principal, item, session string, m SelectRequest, prepared *selectionPreparation) (PlaybackPlan, error) {
	if !validID(m.OperationID) || m.Generation < 1 || m.ExpectedRevision < 1 || (m.Mode != "off" && m.Mode != "track") {
		return PlaybackPlan{}, ErrInput
	}
	offset, e := Offset(m.OffsetUS)
	if e != nil {
		return PlaybackPlan{}, e
	}
	if m.Mode == "off" && (m.ResourceID != "" || m.ResourceRevision != 0 || offset != 0) {
		return PlaybackPlan{}, ErrInput
	}
	if m.Mode == "track" && (!validID(m.ResourceID) || m.ResourceRevision < 1) {
		return PlaybackPlan{}, ErrInput
	}
	// Shares the immutable publication/GC lock, not playback's media I/O lock.
	s.publication.Lock()
	defer s.publication.Unlock()
	gated2, p, e := s.tx(ctx, p, item)
	if e != nil {
		return PlaybackPlan{}, e
	}
	tx := gated2.Tx()
	defer gated2.Rollback()
	scope, e := s.sessionTx(ctx, tx, p, item, session)
	if e != nil {
		return PlaybackPlan{}, e
	}
	if e = s.checkSelectionAcceptanceTx(ctx, tx, session); e != nil {
		return PlaybackPlan{}, e
	}
	accepting := selectionAcceptance(ctx)

	digest := requestDigest([]any{session, m})
	var priorDigest, priorSession string
	var applied int64
	e = tx.QueryRowContext(ctx, `SELECT request_digest,session_id,selection_revision FROM subtitle_selection_operations WHERE actor=? AND operation_id=?`, actor(p), m.OperationID).Scan(&priorDigest, &priorSession, &applied)
	if e == nil {
		if digest != priorDigest || session != priorSession {
			return PlaybackPlan{}, ErrOperation
		}
		out, e := s.PlanTx(ctx, tx, p, item, session)
		out.AppliedRevision = applied
		if e != nil {
			return out, e
		}
		return out, gated2.Commit()
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
	var currentRevision int64
	var currentID string
	var currentResourceRevision int64
	if e = tx.QueryRowContext(ctx, `SELECT revision,resource_id,resource_revision FROM playback_subtitle_state WHERE session_id=? AND generation=?`, session, m.Generation).Scan(&currentRevision, &currentID, &currentResourceRevision); e != nil {
		return PlaybackPlan{}, e
	}
	if currentRevision != m.ExpectedRevision {
		return PlaybackPlan{}, ErrConflict
	}
	if m.Mode == "track" {
		r, e := scanResource(tx.QueryRowContext(ctx, `SELECT `+resourceColumns+` FROM subtitle_resources r JOIN subtitle_revisions v ON v.resource_id=r.id WHERE r.id=? AND r.item_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)) AND v.revision=?`, m.ResourceID, item, m.ResourceRevision))
		if e != nil {
			return PlaybackPlan{}, e
		}
		if err := s.checkDiscResourceSessionTx(ctx, tx, session, r.ID); err != nil {
			return PlaybackPlan{}, err
		}
		if !visible(r, p) || r.SourceID != scope.source {
			return PlaybackPlan{}, identity.ErrUnauthorized
		}
		var latest int64
		if e = tx.QueryRowContext(ctx, `SELECT current_revision FROM subtitle_resources WHERE id=?`, r.ID).Scan(&latest); e != nil {
			return PlaybackPlan{}, e
		}
		// A retired pin can receive timing edits, but cannot be newly selected.
		alreadySelected := currentID == r.ID && currentResourceRevision == r.Revision
		if !alreadySelected && (r.deleted || r.Revision != latest) {
			return PlaybackPlan{}, ErrConflict
		}
		if !scope.available || r.sourceSize != scope.size || r.sourceModified != scope.modified {
			return PlaybackPlan{}, ErrConflict
		}
		if _, e = tx.ExecContext(ctx, `INSERT OR IGNORE INTO playback_subtitle_pins VALUES(?,?,?)`, session, r.ID, r.Revision); e != nil {
			return PlaybackPlan{}, e
		}
		if r.Renderer == "external_text" || m.Rendering == "text" {
			if _, e = tx.ExecContext(ctx, `INSERT OR IGNORE INTO playback_manifest_subtitles VALUES(?,?,?)`, session, r.ID, r.Revision); e != nil {
				return PlaybackPlan{}, e
			}
		}
	}
	nextGeneration := scope.generation
	renderer := "external_text"
	if m.Rendering != "" && m.Rendering != "text" {
		renderer = "client"
	}
	if prepared != nil && prepared.replace {
		if prepared.ordinaryBurn {
			renderer = "burn_in"
		}
		if prepared.ordinaryBurn {
			var enabled bool
			var revision int64
			if e = tx.QueryRowContext(ctx, `SELECT transcoding_enabled,revision FROM playback_owner_policy WHERE singleton=1`).Scan(&enabled, &revision); e != nil {
				return PlaybackPlan{}, e
			}
			if !enabled || revision != prepared.policyRevision {
				return PlaybackPlan{}, ErrConflict
			}
		}
		if accepting != nil {
			if e = accepting.BeforeGenerationTx(ctx, tx, session, scope.generation); e != nil {
				return PlaybackPlan{}, e
			}
		}
		if s.ChangeDeliveryTx != nil {
			if e = s.ChangeDeliveryTx(ctx, tx, p, session, prepared.resource); e != nil {
				return PlaybackPlan{}, e
			}
		}
		nextGeneration++
		grant := identity.Token()
		result, e := tx.ExecContext(ctx, `UPDATE playback_sessions SET generation=?,grant_token=?,grant_hash=? WHERE id=? AND generation=? AND state NOT IN('stopped','ended','failed')`, nextGeneration, grant, identity.Digest(grant), session, scope.generation)
		if e != nil {
			return PlaybackPlan{}, e
		}
		n, _ := result.RowsAffected()
		if n != 1 {
			return PlaybackPlan{}, ErrConflict
		}
		if accepting != nil {
			mapped, e := accepting.AfterGenerationTx(ctx, tx, session, nextGeneration, m.ResourceID)
			if e != nil {
				return PlaybackPlan{}, e
			}
			m.ResourceID = mapped
		}
		renderID := ""
		if prepared.ordinaryBurn {
			renderID = "ordinary"
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO playback_subtitle_presentations VALUES(?,?,?,?,?,?) ON CONFLICT(session_id) DO UPDATE SET render_id=excluded.render_id,generation=excluded.generation,position_us=excluded.position_us,policy_revision=excluded.policy_revision,created_at=excluded.created_at`, session, renderID, nextGeneration, prepared.position, prepared.policyRevision, time.Now().UTC().Format(time.RFC3339)); e != nil {
			return PlaybackPlan{}, e
		}
	}
	result, e := tx.ExecContext(ctx, `UPDATE playback_subtitle_state SET revision=revision+1,resource_id=?,resource_revision=?,offset_us=?,renderer=?,generation=? WHERE session_id=? AND generation=? AND revision=?`, m.ResourceID, m.ResourceRevision, offset, renderer, nextGeneration, session, m.Generation, m.ExpectedRevision)
	if e != nil {
		return PlaybackPlan{}, e
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return PlaybackPlan{}, ErrConflict
	}
	applied = currentRevision + 1
	if _, e = tx.ExecContext(ctx, `INSERT INTO subtitle_selection_operations VALUES(?,?,?,?,?,?)`, actor(p), m.OperationID, digest, session, applied, time.Now().UTC().Format(time.RFC3339)); e != nil {
		return PlaybackPlan{}, e
	}
	out, e := s.PlanTx(ctx, tx, p, item, session)
	if e != nil {
		return out, e
	}
	out.AppliedRevision = applied
	if accepting != nil {
		if e = accepting.AcceptTx(ctx, tx, out); e != nil {
			return PlaybackPlan{}, e
		}
	}
	if e = gated2.Commit(); e != nil {
		return out, e
	}
	if prepared != nil && prepared.replace {
		if s.RestartDelivery != nil {
			s.RestartDelivery(ctx, session, float64(prepared.position)/1e6)
		}
	}
	return out, nil
}

// OpenDocument requires the exact CURRENT selection and a live session pin.
// Keeping old bytes for physical readers never creates a new access grant.
func (s *Service) OpenDocument(ctx context.Context, p identity.Principal, item, session, id string, revision int64) (*mediaartifact.Reader, error) {
	if e := s.CheckDocument(ctx, p, item, session, id, revision); e != nil {
		return nil, e
	}
	if e := s.ensureSessionCached(ctx, p, item, session, id, revision); e != nil {
		return nil, e
	}
	gated3, p, e := s.readTx(ctx, p, item)
	if e != nil {
		return nil, e
	}
	tx := gated3.Tx()
	defer gated3.Rollback()
	scope, e := s.sessionTx(ctx, tx, p, item, session)
	if e != nil {
		return nil, e
	}
	if e = s.checkDiscResourceSessionTx(ctx, tx, session, id); e != nil {
		return nil, e
	}
	if !scope.available {
		return nil, ErrUnavailable
	}
	r, e := scanResource(tx.QueryRowContext(ctx, `SELECT `+resourceColumns+` FROM playback_subtitle_state st JOIN playback_subtitle_pins pin ON pin.session_id=st.session_id AND pin.resource_id=st.resource_id AND pin.resource_revision=st.resource_revision JOIN subtitle_resources r ON r.id=pin.resource_id JOIN subtitle_revisions v ON v.resource_id=r.id AND v.revision=pin.resource_revision WHERE st.session_id=? AND st.generation=? AND st.resource_id=? AND st.resource_revision=? AND r.item_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?))`, session, scope.generation, id, revision, item))
	if e != nil {
		return nil, identity.ErrUnauthorized
	}
	if !visible(r, p) || r.SourceID != scope.source || r.sourceSize != scope.size || r.sourceModified != scope.modified {
		return nil, identity.ErrUnauthorized
	}
	if r.sourceEvidence != "" {
		var expected string
		if e = tx.QueryRowContext(ctx, `SELECT evidence FROM subtitle_remote_sessions WHERE session_id=?`, session).Scan(&expected); e != nil || expected != r.sourceEvidence {
			return nil, identity.ErrUnauthorized
		}
	}
	// Opening under the authoritative transaction acquires the physical lease
	// before GC can retire the last pin. Authorization is checked again per read.
	object := mediaartifact.Object{Digest: r.digest, Size: r.size}
	if r.Renderer != "external_text" {
		if e = tx.QueryRowContext(ctx, `SELECT digest,size FROM subtitle_original_files WHERE resource_id=? AND revision=? AND part='text'`, id, revision).Scan(&object.Digest, &object.Size); e != nil {
			return nil, ErrUnsupported
		}
	}
	reader, e := s.objects.Open(ctx, object)
	if e != nil {
		return nil, e
	}
	if e = gated3.Commit(); e != nil {
		reader.Close()
		return nil, e
	}
	return reader, nil
}

// CheckDocument re-authorizes a document that is being sent, around every
// chunk of it. It only reads, so it runs in a read snapshot and takes no write
// gate: it neither waits behind a scan nor stands in front of one.
func (s *Service) CheckDocument(ctx context.Context, p identity.Principal, item, session, id string, revision int64) error {
	snapshot, p, e := s.readTx(ctx, p, item)
	if e != nil {
		return e
	}
	tx := snapshot.Tx()
	defer snapshot.Rollback()
	scope, e := s.sessionTx(ctx, tx, p, item, session)
	if e != nil {
		return e
	}
	if err := s.checkDiscResourceSessionTx(ctx, tx, session, id); err != nil {
		return err
	}
	if !scope.available {
		return ErrUnavailable
	}
	var ok bool
	e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM playback_subtitle_state st JOIN subtitle_resources r ON r.id=st.resource_id WHERE st.session_id=? AND st.generation=? AND st.renderer='external_text' AND st.resource_id=? AND st.resource_revision=? AND r.item_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)) AND (r.scope='shared' OR r.owner=?))`, session, scope.generation, id, revision, item, actor(p)).Scan(&ok)
	if e != nil {
		return e
	}
	if !ok {
		return identity.ErrUnauthorized
	}
	return nil
}

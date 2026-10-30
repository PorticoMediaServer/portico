package mediaanalysis

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"strconv"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

type Access struct {
	ServerID, LibraryID, ItemID, ViewerFence     string
	Authority, AccountID, ProfileID, SessionHash string
	Owner                                        bool
	Authorize                                    func(*sql.Tx) error
	captured                                     *capturedSource
}
type Target struct {
	SourceID        string `json:"sourceId"`
	SourceRevision  string `json:"sourceRevision"`
	MappingRevision string `json:"mappingRevision"`
	SessionID       string `json:"sessionId"`
	Generation      int64  `json:"generation"`
}
type Source struct {
	ID                                                  string `json:"id"`
	Revision                                            string `json:"revision"`
	MappingRevision                                     string `json:"mappingRevision"`
	DurationUS                                          string `json:"durationUS"`
	NeedsProbe                                          bool   `json:"needsProbe"`
	Evidence, ProbeEvidence, SourceBinding              string `json:"-"`
	ObjectID, RootID, Incarnation, Container            string `json:"-"`
	Configuration, Start, End, Duration, Size, Modified int64  `json:"-"`
}
type Scope struct {
	ServerID          string `json:"serverId"`
	LibraryID         string `json:"libraryId"`
	ItemID            string `json:"itemId"`
	ViewerFence       string `json:"viewerFence"`
	SessionID         string `json:"sessionId"`
	SessionGeneration int64  `json:"sessionGeneration"`
}

func (a Access) scope(t Target) Scope {
	return Scope{a.ServerID, a.LibraryID, a.ItemID, a.ViewerFence, t.SessionID, t.Generation}
}
func (s *Service) begin(ctx context.Context, a Access) (*dbwork.Write, error) {
	if a.Authorize == nil || a.AccountID == "" || a.ProfileID == "" || a.Authority == "" {
		return nil, identity.ErrUnauthorized
	}
	gated, e := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if e != nil {
		return nil, e
	}
	tx := gated.Tx()
	if e = a.Authorize(tx); e != nil {
		gated.Rollback()
		return nil, e
	}
	var library string
	if e = tx.QueryRowContext(ctx, `SELECT l.library_id FROM catalog_entities i JOIN catalog_libraries l ON l.id=i.library_id WHERE i.public_id=pid_blob(?)`, a.ItemID).Scan(&library); e != nil || library != a.LibraryID {
		gated.Rollback()
		if e == nil {
			e = identity.ErrUnauthorized
		}
		return nil, e
	}
	return gated, nil
}
func (s *Service) resolve(ctx context.Context, tx *sql.Tx, a Access, t Target) (Source, error) {
	return resolveSource(ctx, tx, a, t)
}

// resolveSource needs no service state: it is the selection and clock authority
// for a caller's transaction, shared by owner analysis and the viewer marker
// projection so both admit exactly the same source.
func resolveSource(ctx context.Context, tx *sql.Tx, a Access, t Target) (Source, error) {
	var v Source
	entity, err := entityid.Resolve(ctx, tx, a.ItemID)
	if err != nil {
		return v, sql.ErrNoRows
	}
	id := t.SourceID
	var pinSize, pinModified int64
	var sessionDuration float64
	if t.SessionID != "" {
		if t.Generation < 1 {
			return v, ErrInput
		}
		var generation int64
		var expires, state string
		e := tx.QueryRowContext(ctx, `SELECT ps.asset_id,ps.generation,ps.state,ps.expires_at,p.size,p.modified_ns,ps.duration FROM playback_sessions ps JOIN playback_source_pins p ON p.session_id=ps.id AND p.asset_id=ps.asset_id WHERE ps.id=? AND ps.item_id=? AND (ps.session_hash=? OR EXISTS(SELECT 1 FROM authorization_family_tokens original JOIN authorization_family_tokens caller ON caller.family_id=original.family_id JOIN authorization_session_families family ON family.id=caller.family_id WHERE original.token_hash=ps.session_hash AND caller.token_hash=? AND caller.retired=0 AND family.revoked=0 AND caller.generation=family.current_generation)) AND ps.account_id=? AND ps.profile_id=?`, t.SessionID, entity, a.SessionHash, a.SessionHash, a.AccountID, a.ProfileID).Scan(&id, &generation, &state, &expires, &pinSize, &pinModified, &sessionDuration)
		if e != nil {
			return v, e
		}
		expiry, e := time.Parse(time.RFC3339, expires)
		if e != nil || !expiry.After(time.Now()) || generation != t.Generation || state == "ended" || state == "stopped" || state == "failed" || t.SourceID != "" && id != t.SourceID {
			return v, ErrConflict
		}
		var derivative bool
		if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM prepared_media_session_pins WHERE session_id=?)`, t.SessionID).Scan(&derivative); e != nil {
			return v, e
		}
		// Matching duration is not a source-to-derivative clock map. Keep source
		// analysis in details, but never authorize original marker seeks against
		// prepared bytes until a versioned measured mapping is available.
		if derivative {
			return v, ErrClock
		}
	} else if t.Generation != 0 {
		return v, ErrInput
	}
	var start, end, duration float64
	var part int
	// Only server-owned, current inventory may project artifacts. A different
	// source is never silently selected when a playback session supplied one.
	e := tx.QueryRowContext(ctx, `SELECT a.token,o.id,o.revision,src.id,src.incarnation,src.generation,a.container,a.size,a.modified_ns,a.duration,link.start_seconds,COALESCE(link.end_seconds,a.duration),link.part_index FROM catalog_entities item JOIN catalog_libraries library ON library.id=item.library_id CROSS JOIN catalog_asset_links link ON link.entity_id=item.id CROSS JOIN catalog_assets a ON a.id=link.asset_id CROSS JOIN inventory_objects o INDEXED BY inventory_objects_asset ON o.asset_id=a.token JOIN library_sources src ON src.id=o.source_id WHERE item.public_id=pid_blob(?) AND library.library_id=? AND (?='' OR a.token=?) AND a.available=1 AND o.state='available' AND o.retired=0 AND o.root_incarnation=src.incarnation AND src.enabled=1 AND (?='' OR EXISTS(
 SELECT 1 FROM playback_source_pins pin
 WHERE pin.session_id=? AND (pin.binding='none' OR (
 pin.source_id=src.id AND pin.inventory_object_id=o.id AND pin.object_identity=o.object_identity AND pin.relative_path=o.relative_path
 AND pin.root=src.root AND pin.root_identity=src.root_identity AND src.identity_confirmed=1 AND src.health NOT IN('offline','root_changed','removing')))))
 ORDER BY link.part_index,a.token,src.id LIMIT 1`, a.ItemID, a.LibraryID, id, id, t.SessionID, t.SessionID).Scan(&v.ID, &v.ObjectID, &v.Revision, &v.RootID, &v.Incarnation, &v.Configuration, &v.Container, &v.Size, &v.Modified, &duration, &start, &end, &part)
	if e != nil {
		return v, e
	}
	if v.Duration, e = exactUS(duration); e != nil {
		return v, e
	}
	if v.Start, e = exactUS(start); e != nil {
		return v, e
	}
	if v.End, e = exactUS(end); e != nil {
		return v, e
	}
	if v.End < v.Start || v.End > v.Duration {
		return v, ErrConflict
	}
	var unknownBoundary bool
	if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM episode_asset_boundaries WHERE item_id=? AND asset_id=? AND status='unknown_multi_episode')`, entity, v.ID).Scan(&unknownBoundary); e != nil {
		return v, e
	}
	// A shared episode file without a supplied logical boundary cannot acquire
	// a guessed seek clock. P14 owns establishing that boundary.
	if unknownBoundary {
		return v, ErrClock
	}
	if t.SessionID != "" {
		duration, err := exactUS(sessionDuration)
		if err != nil || duration != v.End-v.Start {
			return v, ErrClock
		}
	}
	v.DurationUS = fmt.Sprint(v.End - v.Start)
	if v.Container == "strm" {
		e = tx.QueryRowContext(ctx, `SELECT evidence FROM analysis_probe_bindings WHERE object_id=? AND source_revision=?`, v.ObjectID, v.Revision).Scan(&v.ProbeEvidence)
		if e != nil && e != sql.ErrNoRows {
			return v, e
		}
		v.Evidence = v.ProbeEvidence
		if a.captured != nil {
			if a.captured.invocation != nil {
				if e = a.captured.invocation.checkRetainedTx(ctx, tx); e != nil {
					return v, e
				}
			}
			old := a.captured.source
			if old.ID != v.ID || old.ObjectID != v.ObjectID || old.Revision != v.Revision || old.Incarnation != v.Incarnation || old.Configuration != v.Configuration {
				return v, ErrConflict
			}
			v.Evidence = old.Evidence
		}
		v.NeedsProbe = v.ProbeEvidence == "" || v.ProbeEvidence != v.Evidence
		if t.SessionID != "" && a.captured != nil {
			if e = sessionEvidence(ctx, tx, t, v, v.Evidence); e != nil {
				return v, e
			}
		}
	}
	v.SourceBinding = token(v.Incarnation, fmt.Sprint(v.Configuration), v.Evidence)
	v.MappingRevision = token(a.LibraryID, a.ItemID, v.ID, v.Revision, fmt.Sprint(v.Start), fmt.Sprint(v.End), fmt.Sprint(part), v.Incarnation, fmt.Sprint(v.Configuration), v.Evidence)
	if t.SourceRevision != "" && t.SourceRevision != v.Revision || t.MappingRevision != "" && t.MappingRevision != v.MappingRevision {
		return v, ErrConflict
	}
	if t.SessionID != "" {
		if pinSize != v.Size || pinModified != v.Modified {
			return v, ErrConflict
		}
		// A catalog size/mtime pin alone cannot prove a physical root incarnation.
		// The integrated playback producer already records this stronger selection.
		var matching bool
		e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM playback_physical_source_pins WHERE session_id=? AND source_id=? AND incarnation=? AND configuration_generation=?)`, t.SessionID, v.RootID, v.Incarnation, v.Configuration).Scan(&matching)
		if e != nil {
			return v, e
		}
		if !matching {
			return v, ErrConflict
		}
	}
	return v, nil
}
func parseUS(s string) (int64, error) {
	if s == "" || len(s) > 19 || len(s) > 1 && s[0] == '0' {
		return 0, ErrInput
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, ErrInput
		}
	}
	n, e := strconv.ParseInt(s, 10, 64)
	if e != nil || n < 0 || n > (1<<53)-1 {
		return 0, ErrInput
	}
	return n, nil
}
func (s *Service) ResolveOwner(ctx context.Context, a Access, t Target) (Source, error) {
	if !a.Owner || a.Authority != "local" || t.SourceRevision == "" || t.MappingRevision == "" {
		return Source{}, identity.ErrUnauthorized
	}
	gated, e := s.begin(ctx, a)
	if e != nil {
		return Source{}, e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	v, e := s.resolve(ctx, tx, a, t)
	if e != nil {
		return v, e
	}
	return v, gated.Commit()
}

// AuthorizeTarget runs within the caller's mutation transaction, closing the
// resolve-to-queue/policy/control race without creating a second job authority.
func (s *Service) AuthorizeTarget(ctx context.Context, tx *sql.Tx, a Access, t Target) error {
	if !a.Owner || a.Authority != "local" || a.Authorize == nil || t.SourceRevision == "" || t.MappingRevision == "" {
		return identity.ErrUnauthorized
	}
	if e := a.Authorize(tx); e != nil {
		return e
	}
	_, e := s.resolve(ctx, tx, a, t)
	return e
}

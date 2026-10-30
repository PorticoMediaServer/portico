package playbackv1

import (
	"context"
	"strconv"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/streamlimits"
)

type ControlCommand struct {
	Action           string `json:"action"`
	PositionMs       *int64 `json:"positionMs,omitempty"`
	ExpectedRevision string `json:"expectedRevision"`
}
type SessionControls struct {
	SessionID  string `json:"sessionId"`
	Revision   string `json:"revision"`
	Generation int    `json:"generation"`
	State      string `json:"state"`
	PositionMs int64  `json:"positionMs"`
	// DurationMs is the length of the version being played, from the
	// catalogue's file facts; absent for a channel or an unknown length.
	DurationMs int64    `json:"durationMs,omitempty"`
	Actions    []string `json:"actions"`
}

func controlsOf(r row) SessionControls {
	actions := []string{}
	if r.ended == 0 {
		actions = []string{"pause", "resume", "seek", "stop"}
	}
	return SessionControls{SessionID: r.id, Generation: r.generation, Revision: strconv.Itoa(r.revision), State: r.state, PositionMs: r.position, Actions: actions}
}

// controlsWithDuration is controlsOf with the version's length.
func (s *Service) controlsWithDuration(ctx context.Context, r row) (SessionControls, error) {
	out := controlsOf(r)
	duration, err := s.sessionDuration(ctx, r)
	out.DurationMs = duration
	return out, err
}
func (s *Service) AdminControls(ctx context.Context, id string) (SessionControls, error) {
	r, err := loadRow(ctx, s.DB, id)
	if err != nil {
		return SessionControls{}, err
	}
	r = s.expireIfLapsed(ctx, r)
	return s.controlsWithDuration(ctx, r)
}

// AdminControl is deliberately limited to transport. It cannot change tracks,
// versions, policy or the viewer associated with a playback session.
func (s *Service) AdminControl(ctx context.Context, id string, in ControlCommand) (SessionControls, error) {
	r, err := loadRow(ctx, s.DB, id)
	if err != nil {
		return SessionControls{}, err
	}
	if in.ExpectedRevision == "" {
		return SessionControls{}, ErrRevisionRequired
	}
	var change Change
	switch in.Action {
	case "pause":
		change.State = "paused"
	case "resume":
		change.State = "playing"
	case "seek":
		if in.PositionMs == nil {
			return SessionControls{}, &FieldError{Path: "positionMs"}
		}
		change.Seek = &SeekRequest{PositionMs: *in.PositionMs, ID: "admin-" + strconv.Itoa(r.revision+1)}
	default:
		return SessionControls{}, &FieldError{Path: "action"}
	}
	// The edge has authorized an administrator. Apply the transport to the
	// session's device using the normal CAS, conversion seek and event pipeline.
	c := Caller{DeviceID: r.device, Principal: identity.Principal{Viewer: identity.Viewer{AccountID: r.account, ProfileID: r.profile, Authority: r.authority}}}
	_, err = s.Patch(ctx, c, id, in.ExpectedRevision, change)
	if err != nil {
		return SessionControls{}, err
	}
	return s.AdminControls(ctx, id)
}

type RemotePlayback struct {
	SessionControls
	Device AdminName   `json:"device"`
	Item   *AdminItem  `json:"item,omitempty"`
	Kind   string      `json:"kind"`
	Next   *QueueEntry `json:"next,omitempty"`
}
type RemoteControlsDocument struct {
	// Exact durable admission count, independent of the paged V1 controls rows.
	StreamLimit *StreamLimitDetail `json:"streamLimit,omitempty"`
	Items       []RemotePlayback   `json:"items"`
	NextCursor  string             `json:"nextCursor"`
}

// RemoteControls lists this account's active playback across its profiles.
// Other profiles offer only stop; ordinary transport and track changes keep
// their existing profile fence. The HTTP layer redacts hidden current titles.
func (s *Service) RemoteControls(ctx context.Context, c Caller, cursor string, limit int) (RemoteControlsDocument, error) {
	out := RemoteControlsDocument{Items: []RemotePlayback{}}
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 100 || len(cursor) > 160 {
		return out, &FieldError{Path: "limit"}
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id FROM playback_v1_sessions WHERE authority=? AND account_id=? AND ended_ms=0 AND lease_expires_ms>? AND id>? ORDER BY id LIMIT ?`, c.Principal.Authority, c.Principal.AccountID, s.now().UnixMilli(), cursor, limit+1)
	if err != nil {
		return out, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return out, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if len(ids) > limit {
		ids = ids[:limit]
		out.NextCursor = ids[len(ids)-1]
	}
	for _, id := range ids {
		r, e := loadRow(ctx, s.DB, id)
		if e != nil {
			return out, e
		}
		item := (*AdminItem)(nil)
		if r.item != "" {
			var title string
			if e = s.DB.QueryRowContext(ctx, `SELECT title FROM catalog_entities WHERE public_id=pid_blob(?)`, r.item).Scan(&title); e != nil {
				return out, e
			}
			item = &AdminItem{r.item, title}
		}
		device := AdminName{ID: r.device, Name: r.device}
		_ = s.DB.QueryRowContext(ctx, `SELECT name FROM identity_devices WHERE id=?`, r.device).Scan(&device.Name)
		controls, e := s.controlsWithDuration(ctx, r)
		if e != nil {
			return out, e
		}
		if !sameProfile(c.Principal, r) {
			controls.Actions = []string{"stop"}
		}
		remote := RemotePlayback{SessionControls: controls, Device: device, Item: item, Kind: r.kind}
		if r.queueID != "" {
			tx, done, e := dbwork.BeginRead(ctx, s.DB)
			if e != nil {
				return out, e
			}
			q, e := loadQueue(ctx, tx, r.queueID)
			if e == nil {
				l, e := s.readLayout(ctx, tx, q)
				if e == nil {
					if pos, _, ok := completionNext(l); ok {
						missing, e := l.loadExistingSonicRange(pos, 1)
						// Monitoring another profile cannot create its recommendation order.
						// An uncaptured music window is unknown, never a guessed next song.
						if e == nil && (len(missing) == 0 || !l.musicOnlySources()) {
							entries, e := s.window(ctx, l, c.Principal, pos, 1)
							if e == nil && len(entries) == 1 && entries[0].Available {
								remote.Next = &entries[0]
							}
						}
					}
				}
			}
			done()
		}
		out.Items = append(out.Items, remote)
	}
	detail, err := s.AccountStreamLimit(ctx, c)
	if err != nil {
		return out, err
	}
	// An absent/unlimited policy supplies no finite-capacity inference.
	if detail.Limit > 0 {
		out.StreamLimit = &detail
	}
	return out, nil
}

// StopAccountSession lets a viewer free their own account's stream slot even
// when the playing profile's title is hidden. It never discloses that title or
// grants transport, track or media access across profiles.
func (s *Service) StopAccountSession(ctx context.Context, c Caller, id string, selected ...*int) error {
	var generation *int
	if len(selected) > 0 {
		generation = selected[0]
	}
	if stopKey(ctx) != "" && generation == nil {
		return &FieldError{Path: "generation"}
	}
	// The selected occurrence is checked inside the accepting writer, after
	// current receipt authority and before any drain, fence or Stop effect.
	if stopKey(ctx) != "" || generation != nil {
		return s.stopWithReceipt(ctx, c, id, nil, generation, true)
	}

	r, err := loadRow(ctx, s.DB, id)
	if err != nil {
		return err
	}
	if r.authority != c.Principal.Authority || r.account != c.Principal.AccountID {
		return ErrNotFound
	}
	c.Principal.ProfileID = r.profile
	c.AuthorizeItem = nil
	return s.Stop(ctx, c, id, nil)
}

// AccountStreamLimit reads the same durable media slots as owner admission.
// It is used by the account-cap refusal and the same admitted own-account controls read.
func (s *Service) AccountStreamLimit(ctx context.Context, c Caller) (StreamLimitDetail, error) {
	out := StreamLimitDetail{Scope: "account"}
	if s.ControlsAuthorityTx == nil || s.DB == nil {
		return out, identity.ErrUnauthorized
	}
	tx, done, err := dbwork.BeginRead(ctx, s.DB)
	if err != nil {
		return out, err
	}
	defer done()
	if err = s.ControlsAuthorityTx(ctx, tx, c); err != nil {
		return out, err
	}
	streams, e := streamlimits.ReadTx(ctx, tx, c.Principal.AccountID)
	if e != nil {
		return out, e
	}
	if streams.EffectiveLimit != nil {
		out.Limit = int64(*streams.EffectiveLimit)
	}
	out.Playing, err = streamlimits.CountTx(ctx, tx, c.Principal.Authority, c.Principal.AccountID, true, s.now(), "", "")
	return out, err
}

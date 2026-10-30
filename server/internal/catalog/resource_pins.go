package catalog

import (
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/requestinput"
	"github.com/PorticoMediaServer/portico/server/internal/usermutation"
	"time"
)

type PinMutation struct {
	OperationID      string `json:"operationId"`
	ExpectedRevision int64  `json:"expectedRevision"`
	Pinned           bool   `json:"pinned"`
}
type PinReceipt struct {
	OperationID string `json:"operationId"`
	ResourceID  string `json:"resourceId"`
	PlaylistID  string `json:"playlistId,omitempty"`
	Revision    int64  `json:"revision"`
	PinRevision int64  `json:"pinRevision"`
	Pinned      bool   `json:"pinned"`
	Deleted     bool   `json:"deleted"`
}

func (s *Service) MutateSavedPin(a ResourceActor, kind, id string, m PinMutation, authorize func(*sql.Tx) error) (PinReceipt, error) {
	if err := requireCatalogMutationContext(s.Context(), m.OperationID); err != nil {
		return PinReceipt{}, err
	}
	out := PinReceipt{OperationID: m.OperationID, ResourceID: id, Pinned: m.Pinned}
	if !personalOperation.MatchString(m.OperationID) || m.ExpectedRevision < 0 {
		return out, requestinput.New("invalid pin operation")
	}
	gated, e := dbwork.Begin(s.Context(), s.db, dbwork.ClassFrom(s.Context(), dbwork.ClassInteractive))
	if e != nil {
		return out, e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	_, _, protocol := usermutation.RequestFrom(s.Context())
	priorReceipt, e := usermutation.CheckContext(s.Context(), tx, time.Now().UnixMilli())
	if e != nil {
		return out, e
	}
	if !protocol {
		if e = authorize(tx); e != nil {
			return out, e
		}
	}
	deleted := false
	role := ""
	if kind == "playlist" {
		role, out.Revision, deleted, e = playlistRole(tx, id, a)
		out.PlaylistID = id
	} else if kind == "collection" || kind == "view" {
		var actual string
		role, actual, out.Revision, deleted, e = savedResourceRole(tx, id, a)
		if actual != kind && e == nil {
			e = sql.ErrNoRows
		}
	} else {
		return out, requestinput.New("invalid resource kind")
	}
	if e != nil {
		if priorReceipt != nil && errors.Is(e, sql.ErrNoRows) {
			var exists bool
			query := `SELECT EXISTS(SELECT 1 FROM saved_resources WHERE id=?)`
			if kind == "playlist" {
				query = `SELECT EXISTS(SELECT 1 FROM catalog_playlists WHERE token=?)`
			}
			if err := tx.QueryRow(query, id).Scan(&exists); err != nil {
				return out, err
			}
			if !exists {
				out.PinRevision, out.Deleted, out.Pinned = priorReceipt.Revision, true, false
				return out, nil
			}
		}
		return out, e
	}
	if priorReceipt != nil {
		if deleted && role != "owner" {
			return out, sql.ErrNoRows
		}
		out.Deleted = deleted
		e = tx.QueryRow(`SELECT COALESCE((SELECT revision FROM saved_pins WHERE owner_key=? AND kind=? AND resource_id=?),0),COALESCE((SELECT pinned FROM saved_pins WHERE owner_key=? AND kind=? AND resource_id=?),0)`, actorKey(a), kind, id, actorKey(a), kind, id).Scan(&out.PinRevision, &out.Pinned)
		return out, e
	}
	if protocol {
		if e = authorize(tx); e != nil {
			return out, e
		}
	}
	if deleted {
		return out, sql.ErrNoRows
	}
	hash := operationHash(struct {
		Kind, ID string
		Mutation PinMutation
	}{kind, id, m})
	if !protocol {
		var prior, raw string
		e = tx.QueryRow(`SELECT request_hash,response FROM saved_resource_receipts WHERE owner_key=? AND operation_id=? AND created_at>=?`, actorKey(a), m.OperationID, time.Now().Add(-30*24*time.Hour).UTC().Format(time.RFC3339)).Scan(&prior, &raw)
		if e == nil {
			if prior != hash {
				return out, ErrOperationConflict
			}
			e = json.Unmarshal([]byte(raw), &out)
			return out, e
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return out, e
		}
		if e = checkRetired(tx, actorKey(a), "resource", m.OperationID, hash); e != nil {
			return out, e
		}
	}
	if e = tx.QueryRow(`SELECT COALESCE((SELECT revision FROM saved_pins WHERE owner_key=? AND kind=? AND resource_id=?),0)`, actorKey(a), kind, id).Scan(&out.PinRevision); e != nil {
		return out, e
	}
	if out.PinRevision != m.ExpectedRevision {
		return out, ErrPlaylistConflict
	}
	out.PinRevision++
	if _, e = tx.Exec(`INSERT INTO saved_pins VALUES(?,?,?,?,?) ON CONFLICT(owner_key,kind,resource_id) DO UPDATE SET pinned=excluded.pinned,revision=excluded.revision`, actorKey(a), kind, id, m.Pinned, out.PinRevision); e != nil {
		return out, e
	}
	if e = hintPinsChanged(tx, actorKey(a), "pins"); e != nil {
		return out, e
	}
	if protocol {
		if e = usermutation.SaveContext(s.Context(), tx, usermutation.Result{Status: "applied", TargetID: id, Revision: out.PinRevision}, time.Now().UnixMilli()); e != nil {
			return out, e
		}
		return out, gated.Commit()
	}
	if e = fenceOperation(tx, actorKey(a), "resource", m.OperationID, hash); e != nil {
		return out, e
	}
	rawBytes, _ := json.Marshal(out)
	if _, e = tx.Exec(`INSERT INTO saved_resource_receipts VALUES(?,?,?,?,?)`, actorKey(a), m.OperationID, hash, string(rawBytes), time.Now().UTC().Format(time.RFC3339)); e != nil {
		return out, e
	}
	return out, gated.Commit()
}

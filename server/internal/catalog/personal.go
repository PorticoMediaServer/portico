package catalog

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"github.com/PorticoMediaServer/portico/server/internal/requestinput"
	"strconv"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"github.com/PorticoMediaServer/portico/server/internal/personalstate"
	"github.com/PorticoMediaServer/portico/server/internal/usermutation"
	"regexp"
	"time"
)

var ErrPersonalConflict = errors.New("personal state changed; refresh before applying a new intent")
var ErrPersonalCapacity = errors.New("too many bulk jobs are in progress for this profile; retry when one finishes")
var ErrOperationConflict = errors.New("operationId was already used for a different request")
var ErrOperationExpired = errors.New("operation receipt expired; refresh before authoring a new intent")
var ErrPersonalReview = errors.New("the mutation ancestor is unavailable; review current personal state")
var ErrPersonalResolution = errors.New("personal state needs an explicit conflict resolution")
var personalOperation = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

type personalReader interface{ QueryRow(string, ...any) *sql.Row }

// personalID resolves a public item id to its integer entity id (a merged-away
// title's id resolves to the title that stayed). An unknown
// or malformed id reads as not found, like the old items lookup.
func personalID(q personalReader, item string) (int64, error) {
	var id int64
	if e := q.QueryRow(`SELECT id FROM catalog_entities WHERE public_id=pid_blob(?1)
 UNION ALL SELECT entity_id FROM catalog_merged_ids WHERE public_id=pid_blob(?1) LIMIT 1`, item).Scan(&id); e != nil {
		return 0, e
	}
	return id, nil
}

// positionMillis stores an API seconds position as integer milliseconds.
func positionMillis(seconds float64) int64 {
	return int64(math.Round(seconds * 1000))
}

func readPersonal(q personalReader, profile, item string) (PersonalState, error) {
	id, e := personalID(q, item)
	if e != nil {
		return PersonalState{Status: "current", Conflicts: []PersonalConflict{}}, e
	}
	return readPersonalByID(q, profile, id, personalstate.SQL("?", "?"))
}

func readPersonalByID(q personalReader, profile string, entityID int64, watched string) (PersonalState, error) {
	return readPersonalStateByID(q, profile, entityID, watched)
}

func readPersonalState(q personalReader, profile, item, watched string) (PersonalState, error) {
	id, e := personalID(q, item)
	if e != nil {
		return PersonalState{Status: "current", Conflicts: []PersonalConflict{}}, e
	}
	return readPersonalStateByID(q, profile, id, watched)
}

func readPersonalStateByID(q personalReader, profile string, entityID int64, watched string) (PersonalState, error) {
	out := PersonalState{Status: "current", Conflicts: []PersonalConflict{}}
	e := q.QueryRow(`SELECT watchlisted,favorite,not_interested,rating,revision,watched,last_played_at FROM personal_items WHERE profile_id=? AND item_id=?`, profile, entityID).Scan(&out.Watchlisted, &out.Favorite, &out.NotInterested, &out.Rating, &out.Revision, &out.Watched, &out.LastPlayedAt)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return out, e
	}
	if e = q.QueryRow(`SELECT COALESCE(`+watched+`,0)`, profile, entityID).Scan(&out.Watched); e != nil {
		return out, e
	}
	if e = q.QueryRow(`SELECT COALESCE((SELECT position FROM progress WHERE profile_id=? AND item_id=?),0)/1000.0,EXISTS(SELECT 1 FROM continue_dismissals d WHERE d.profile_id=? AND d.item_id=? AND d.playback_id=COALESCE((SELECT playback_id FROM progress WHERE profile_id=d.profile_id AND item_id=d.item_id),''))`, profile, entityID, profile, entityID).Scan(&out.ProgressSeconds, &out.ContinueDismissed); e != nil {
		return out, e
	}
	var raw string
	e = q.QueryRow(`SELECT COALESCE(json_group_array(json_object('id',c.id,'field',c.field,'baseRevision',c.base_revision,'baseValue',json(c.base_value),'choices',json((SELECT COALESCE(json_group_array(json_object('operationId',v.operation_id,'deviceId',v.device_id,'authoredAt',v.authored_at,'value',json(v.value))),'[]') FROM personal_explicit_events v WHERE v.profile_id=c.profile_id AND v.item_id=c.item_id AND v.field=c.field AND v.base_revision=c.base_revision)))),'[]') FROM personal_conflicts c WHERE c.profile_id=? AND c.item_id=? AND c.resolved=0`, profile, entityID).Scan(&raw)
	if e != nil {
		return out, e
	}
	if e = json.Unmarshal([]byte(raw), &out.Conflicts); e != nil {
		return out, e
	}
	if len(out.Conflicts) > 0 {
		out.Status = "needs-resolution"
	}
	return out, nil
}

func (s *Service) Personal(profile, item string) (PersonalState, error) {
	if dbwork.Snapshot(s.Context()) == nil {
		var out PersonalState
		err := dbwork.WithReadSnapshot(s.Context(), s.db, func(ctx context.Context) error {
			var err error
			out, err = s.WithContext(ctx).Personal(profile, item)
			return err
		})
		return out, err
	}
	return readPersonalState(s.read(), profile, item, personalstate.CompactSQL("?", "?"))
}

func storePersonal(tx *sql.Tx, profile, item string, out PersonalState) error {
	id, e := personalID(tx, item)
	if e != nil {
		return e
	}
	return storePersonalByID(tx, profile, id, out)
}

func storePersonalByID(tx *sql.Tx, profile string, entityID int64, out PersonalState) error {
	_, e := tx.Exec(`INSERT INTO personal_items(profile_id,item_id,watchlisted,favorite,not_interested,rating,revision,watched,last_played_at) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(profile_id,item_id) DO UPDATE SET watchlisted=excluded.watchlisted,favorite=excluded.favorite,not_interested=excluded.not_interested,rating=excluded.rating,revision=excluded.revision,watched=excluded.watched,last_played_at=excluded.last_played_at`, profile, entityID, out.Watchlisted, out.Favorite, out.NotInterested, out.Rating, out.Revision, out.Watched, out.LastPlayedAt)
	return e
}
func snapshotPersonal(tx *sql.Tx, profile, item string, out PersonalState) error {
	id, e := personalID(tx, item)
	if e != nil {
		return e
	}
	return snapshotPersonalByID(tx, profile, id, out)
}

func snapshotPersonalByID(tx *sql.Tx, profile string, entityID int64, out PersonalState) error {
	raw, _ := json.Marshal(out)
	_, e := tx.Exec(`INSERT OR IGNORE INTO personal_snapshots VALUES(?,?,?,?,?)`, profile, entityID, out.Revision, string(raw), time.Now().UTC().Format(time.RFC3339))
	return e
}
func operationHash(value any) string {
	raw, _ := json.Marshal(value)
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}

// A compact, non-sensitive retired-operation fence outlives the 30-day result
// receipt. Pruning a receipt never makes a delayed old operation new again.
func checkRetired(tx *sql.Tx, owner, domain, op, hash string) error {
	var old string
	e := tx.QueryRow(`SELECT request_hash FROM saved_operation_fences WHERE owner_key=? AND domain=? AND operation_id=?`, owner, domain, op).Scan(&old)
	if errors.Is(e, sql.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	if old != hash {
		return ErrOperationConflict
	}
	return ErrOperationExpired
}
func fenceOperation(tx *sql.Tx, owner, domain, op, hash string) error {
	_, e := tx.Exec(`INSERT OR IGNORE INTO saved_operation_fences VALUES(?,?,?,?,?)`, owner, domain, op, hash, time.Now().UTC().Format(time.RFC3339))
	return e
}

func fenceEvidenceByID(tx *sql.Tx, profile string, entityID int64) error {
	if _, e := tx.Exec(`INSERT INTO personal_evidence_fences(profile_id,item_id,playback_id) SELECT profile_id,item_id,playback_id FROM progress WHERE profile_id=? AND item_id=? ON CONFLICT(profile_id,item_id) DO UPDATE SET playback_id=excluded.playback_id`, profile, entityID); e != nil {
		return e
	}
	// Include accepted work that is not yet prepared and automatic continuations.
	_, e := tx.Exec(`INSERT INTO playback_personal_fences(profile_key,item_id,through_ordinal) VALUES(?,?,(SELECT COALESCE(max(ordinal),0) FROM playback_personal_intents)) ON CONFLICT(profile_key,item_id) DO UPDATE SET through_ordinal=excluded.through_ordinal`, profile, entityID)
	return e
}
func explicitResume(tx *sql.Tx, profile, item string, position float64) error {
	id, e := personalID(tx, item)
	if e != nil {
		return e
	}
	return explicitResumeByID(tx, profile, id, position)
}

func explicitResumeByID(tx *sql.Tx, profile string, entityID int64, position float64) error {
	millis := positionMillis(position)
	if e := fenceEvidenceByID(tx, profile, entityID); e != nil {
		return e
	}
	_, e := tx.Exec(`INSERT INTO progress(profile_id,item_id,position,playback_id) VALUES(?,?,?,'') ON CONFLICT(profile_id,item_id) DO UPDATE SET position=excluded.position`, profile, entityID, millis)
	if e != nil {
		return e
	}
	// Keep book/continue projections coherent without manufacturing history or
	// last-played activity for a manual action.
	if _, e = tx.Exec(`UPDATE book_resume SET position=? WHERE profile_id=? AND item_id=?`, millis, profile, entityID); e != nil {
		return e
	}
	_, e = tx.Exec(`INSERT INTO progress_activity(profile_id,library_id,item_id,updated_at,state) SELECT ?,cl.library_id,e.id,?,CASE WHEN ?=0 THEN 'ended' ELSE 'paused' END FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id WHERE e.id=? ON CONFLICT(profile_id,item_id) DO UPDATE SET state=excluded.state`, profile, time.Now().UTC().Format("2006-01-02T15:04:05.000Z"), millis, entityID)
	return e
}
func personalField(out PersonalState, field string) json.RawMessage {
	var v any
	switch field {
	case "continueDismissed":
		v = out.ContinueDismissed
	case "watchlisted":
		v = out.Watchlisted
	case "favorite":
		v = out.Favorite
	case "notInterested":
		v = out.NotInterested
	case "rating":
		v = out.Rating
	case "watched":
		v = struct {
			Watched  bool    `json:"watched"`
			Progress float64 `json:"progressSeconds"`
		}{out.Watched, out.ProgressSeconds}
	}
	b, _ := json.Marshal(v)
	return b
}
func applyPersonalField(out *PersonalState, field string, value json.RawMessage) error {
	switch field {
	case "continueDismissed":
		return json.Unmarshal(value, &out.ContinueDismissed)
	case "watchlisted":
		return json.Unmarshal(value, &out.Watchlisted)
	case "favorite":
		return json.Unmarshal(value, &out.Favorite)
	case "notInterested":
		return json.Unmarshal(value, &out.NotInterested)
	case "rating":
		return json.Unmarshal(value, &out.Rating)
	case "watched":
		var v struct {
			Watched  bool    `json:"watched"`
			Progress float64 `json:"progressSeconds"`
		}
		if e := json.Unmarshal(value, &v); e != nil {
			return e
		}
		out.Watched = v.Watched
		out.ProgressSeconds = v.Progress
	default:
		return requestinput.New("invalid personal field")
	}
	return nil
}
func mutationField(m *PersonalMutation) (string, json.RawMessage, error) {
	fields := 0
	field := ""
	var value any
	if m.ContinueDismissed != nil {
		fields++
		field = "continueDismissed"
		value = *m.ContinueDismissed
	}
	if m.Watchlisted != nil {
		fields++
		field = "watchlisted"
		value = *m.Watchlisted
	}
	if m.Favorite != nil {
		fields++
		field = "favorite"
		value = *m.Favorite
	}
	if m.NotInterested != nil {
		fields++
		field = "notInterested"
		value = *m.NotInterested
	}
	if len(m.Rating) > 0 {
		fields++
		field = "rating"
		var rating *float64
		if e := json.Unmarshal(m.Rating, &rating); e != nil {
			return "", nil, requestinput.New("invalid personal rating")
		}
		// A null value clears the rating; zero is a rated value.
		if rating != nil && (math.IsNaN(*rating) || math.IsInf(*rating, 0) || *rating < 0 || *rating > 10 || math.Trunc(*rating) != *rating) {
			return "", nil, requestinput.New("personal rating must be an integer from 0 to 10, or null")
		}
		value = rating
		m.Rating, _ = json.Marshal(rating)
	}
	if m.Watched != nil {
		fields++
		field = "watched"
		position := 0.
		if m.ProgressSeconds != nil {
			position = *m.ProgressSeconds
			if *m.Watched || math.IsNaN(position) || math.IsInf(position, 0) || position < 0 {
				return "", nil, requestinput.New("resume may only accompany Mark Unwatched")
			}
		}
		value = struct {
			Watched  bool    `json:"watched"`
			Progress float64 `json:"progressSeconds"`
		}{*m.Watched, position}
	} else if m.ProgressSeconds != nil {
		return "", nil, requestinput.New("resume requires an explicit watched action")
	}
	if m.Resolution != nil {
		if fields != 0 || m.Offline != nil {
			return "", nil, requestinput.New("resolution is a separate operation")
		}
		return "resolution", nil, nil
	}
	if fields != 1 {
		return "", nil, requestinput.New("set exactly one personal field")
	}
	raw, _ := json.Marshal(value)
	return field, raw, nil
}

// personalTarget says which entities a field may be written on. Every field
// belongs to a playable item. Not interested is a statement about a title, and
// a recommended title can be a show, an album or a book as well as a film: the
// taste engine maps an episode, a song or a book file to that same work, so
// the flag may sit on either. Anything else (a season, an artist, a
// collection) answers as absent, like an unknown id.
func personalTargetSupported(kind string, playable bool, field string) bool {
	return playable || field == "notInterested" && (kind == "show" || kind == "album" || kind == "book")
}

func personalTarget(tx *sql.Tx, entityID int64, field string) error {
	var kind string
	var playable bool
	if e := tx.QueryRow(`SELECT k.name,k.playable FROM catalog_entities e JOIN catalog_kinds k ON k.id=e.kind WHERE e.id=?`, entityID).Scan(&kind, &playable); e != nil {
		return e
	}
	if personalTargetSupported(kind, playable, field) {
		return nil
	}
	return sql.ErrNoRows
}

func (s *Service) SetPersonal(account, profile, item string, m PersonalMutation, authorize func(*sql.Tx) error) (PersonalState, error) {
	return s.setPersonal(account, profile, item, m, authorize, false)
}

func (s *Service) setPersonal(account, profile, item string, m PersonalMutation, authorize func(*sql.Tx) error, readingPersonal bool) (PersonalState, error) {
	return s.applyPersonal(account, profile, item, m, authorize, readingPersonal, nil)
}

// applyPersonal joins an explicit batch savepoint without opening a nested gated
// writer. Its outer owner commits the compact receipt with every accepted field.
func (s *Service) applyPersonal(account, profile, item string, m PersonalMutation, authorize func(*sql.Tx) error, readingPersonal bool, tx *sql.Tx) (PersonalState, error) {
	if err := requireCatalogMutationContext(s.Context(), m.OperationID); err != nil {
		return PersonalState{}, err
	}
	out := PersonalState{}
	if !personalOperation.MatchString(m.OperationID) || m.ExpectedRevision < 0 || m.ExpectedRevision > 9007199254740991 {
		return out, requestinput.New("invalid operationId or expectedRevision")
	}
	field, value, e := mutationField(&m)
	if e != nil {
		return out, e
	}
	if o := m.Offline; o != nil {
		_, err := time.Parse(time.RFC3339, o.AuthoredAt)
		if !personalOperation.MatchString(o.DeviceID) || !personalOperation.MatchString(o.DeviceMutationID) || o.Sequence < 1 || o.Sequence > 9007199254740991 || o.BaseRevision != m.ExpectedRevision || err != nil {
			return out, requestinput.New("invalid offline mutation evidence")
		}
	}
	hash := operationHash(struct {
		Item     string
		Mutation PersonalMutation
	}{item, m})
	owned := tx == nil
	commit := func() error { return nil }
	if owned {
		gated, err := dbwork.Begin(s.Context(), s.db, dbwork.ClassFrom(s.Context(), dbwork.ClassInteractive))
		if err != nil {
			return out, err
		}
		defer gated.Rollback()
		tx, commit = gated.Tx(), gated.Commit
	}
	_, _, protocol := usermutation.RequestFrom(s.Context())
	protocol = protocol && owned
	if protocol {
		prior, err := usermutation.CheckContext(s.Context(), tx, time.Now().UnixMilli())
		if err != nil {
			return out, err
		}
		if prior != nil {
			if _, err := personalID(tx, item); errors.Is(err, sql.ErrNoRows) {
				return PersonalState{Revision: prior.Revision, Status: "current", Conflicts: []PersonalConflict{}}, nil
			} else if err != nil {
				return out, err
			}
			if authorize != nil {
				if e = authorize(tx); e != nil {
					return out, e
				}
			}
			current, err := readPersonal(tx, profile, item)
			if errors.Is(err, sql.ErrNoRows) {
				return PersonalState{Revision: prior.Revision}, nil
			}
			return current, err
		}
	}
	if authorize != nil {
		if e = authorize(tx); e != nil {
			return out, e
		}
	}
	if o := m.Offline; o != nil {
		authored, _ := time.Parse(time.RFC3339, o.AuthoredAt)
		if authored.After(time.Now().Add(5*time.Minute)) || authored.Before(time.Now().Add(-30*24*time.Hour)) {
			return out, ErrPersonalReview
		}
	}
	entityID, e := entityid.Resolve(s.Context(), tx, item)
	if errors.Is(e, entityid.ErrNotFound) {
		return out, sql.ErrNoRows
	}
	if e != nil {
		return out, e
	}
	if readingPersonal {
		e = readingPersonalTarget(tx, entityID)
	} else {
		e = personalTarget(tx, entityID, field)
	}
	if e != nil {
		return out, e
	}
	cutoff := time.Now().UTC().Add(-30 * 24 * time.Hour).Format(time.RFC3339)
	if owned && !protocol {
		var prior, receipt string
		e = tx.QueryRow(`SELECT request_hash,response FROM personal_receipts WHERE account_id=? AND profile_id=? AND item_id=? AND operation_id=? AND created_at>=?`, account, profile, entityID, m.OperationID, cutoff).Scan(&prior, &receipt)
		if e == nil {
			if prior != hash {
				return out, ErrOperationConflict
			}
			e = json.Unmarshal([]byte(receipt), &out)
			return out, e
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return out, e
		}
		if e = checkRetired(tx, profile, "personal", m.OperationID, hash); e != nil {
			return out, e
		}
	}
	// Admission limits apply to logical requests at the API and job boundary;
	// per-item retry receipts are retention data, not an item-count quota.
	out, e = readPersonalByID(tx, profile, entityID, personalstate.SQL("?", "?"))
	if e != nil {
		return out, e
	}
	if e = snapshotPersonalByID(tx, profile, entityID, out); e != nil {
		return out, e
	}
	old := out
	var resolvedID string
	if field == "resolution" {
		r := m.Resolution
		if !personalOperation.MatchString(r.ConflictID) || r.ChoiceOperationID == "" {
			return out, requestinput.New("invalid conflict resolution")
		}
		if out.Revision != m.ExpectedRevision {
			return out, ErrPersonalConflict
		}
		var base string
		var baseRevision int64
		e = tx.QueryRow(`SELECT field,base_value,base_revision FROM personal_conflicts WHERE id=? AND profile_id=? AND item_id=? AND resolved=0`, r.ConflictID, profile, entityID).Scan(&field, &base, &baseRevision)
		if e != nil {
			return out, e
		}
		if r.ChoiceOperationID == "base" {
			value = json.RawMessage(base)
		} else {
			var choice string
			e = tx.QueryRow(`SELECT value FROM personal_explicit_events WHERE profile_id=? AND item_id=? AND field=? AND base_revision=? AND operation_id=?`, profile, entityID, field, baseRevision, r.ChoiceOperationID).Scan(&choice)
			if e != nil {
				return out, e
			}
			value = json.RawMessage(choice)
		}
		resolvedID = r.ConflictID
	} else {
		for _, c := range out.Conflicts {
			if c.Field == field {
				return out, ErrPersonalResolution
			}
		}
		if out.Revision != m.ExpectedRevision && m.Offline == nil {
			return out, ErrPersonalConflict
		}
	}
	if o := m.Offline; o != nil {
		var watermark int64
		if e = tx.QueryRow(`SELECT COALESCE((SELECT sequence FROM personal_device_sequences WHERE profile_id=? AND device_id=?),0)`, profile, o.DeviceID).Scan(&watermark); e != nil {
			return out, e
		}
		if o.Sequence <= watermark {
			return out, ErrOperationExpired
		}
		var priorOperation string
		e = tx.QueryRow(`SELECT operation_id FROM personal_explicit_events WHERE profile_id=? AND device_id=? AND device_mutation_id=?`, profile, o.DeviceID, o.DeviceMutationID).Scan(&priorOperation)
		if e == nil {
			return out, ErrOperationConflict
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return out, e
		}
		base := out
		if out.Revision != o.BaseRevision {
			var raw string
			if e = tx.QueryRow(`SELECT state FROM personal_snapshots WHERE profile_id=? AND item_id=? AND revision=? AND created_at>=?`, profile, entityID, o.BaseRevision, cutoff).Scan(&raw); errors.Is(e, sql.ErrNoRows) {
				return out, ErrPersonalReview
			} else if e != nil {
				return out, e
			}
			if e = json.Unmarshal([]byte(raw), &base); e != nil {
				return out, e
			}
		}
		var ancestorEvents int
		if e = tx.QueryRow(`SELECT count(*) FROM personal_explicit_events WHERE profile_id=? AND item_id=? AND field=? AND base_revision=?`, profile, entityID, field, o.BaseRevision).Scan(&ancestorEvents); e != nil {
			return out, e
		}
		if ancestorEvents >= 100 {
			return out, ErrPersonalReview
		}
		baseValue := personalField(base, field)
		var earlierValue, earlierDevice string
		e = tx.QueryRow(`SELECT value,device_id FROM personal_explicit_events WHERE profile_id=? AND item_id=? AND field=? AND base_revision=? ORDER BY created_at,operation_id LIMIT 1`, profile, entityID, field, o.BaseRevision).Scan(&earlierValue, &earlierDevice)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return out, e
		}
		conflict := e == nil && earlierDevice != o.DeviceID && earlierValue != string(value)
		currentValue := string(personalField(out, field))
		if conflict && currentValue != earlierValue {
			return out, ErrPersonalReview
		}
		if !conflict && currentValue != string(baseValue) && currentValue != string(value) {
			return out, ErrPersonalConflict
		}
		_, e = tx.Exec(`INSERT INTO personal_explicit_events VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, profile, entityID, m.OperationID, field, o.BaseRevision, string(baseValue), string(value), o.DeviceID, o.DeviceMutationID, o.Sequence, o.AuthoredAt, time.Now().UTC().Format(time.RFC3339))
		if e != nil {
			return out, e
		}
		if conflict {
			conflictID := operationHash([]string{profile, item, field, fmt.Sprint(o.BaseRevision)})
			if _, e = tx.Exec(`INSERT INTO personal_conflicts(id,profile_id,item_id,field,base_revision,base_value) VALUES(?,?,?,?,?,?)`, conflictID, profile, entityID, field, o.BaseRevision, string(baseValue)); e != nil {
				return out, e
			}
			value = baseValue // preserve the last uncontested field, never last-arrival-wins
		}
		if _, e = tx.Exec(`INSERT INTO personal_device_sequences VALUES(?,?,?) ON CONFLICT(profile_id,device_id) DO UPDATE SET sequence=excluded.sequence`, profile, o.DeviceID, o.Sequence); e != nil {
			return out, e
		}
	}
	if e = applyPersonalField(&out, field, value); e != nil {
		return out, e
	}
	if field == "continueDismissed" {
		if out.ContinueDismissed {
			_, e = tx.ExecContext(s.Context(), `INSERT INTO continue_dismissals(profile_id,item_id,playback_id) VALUES(?,?,COALESCE((SELECT playback_id FROM progress WHERE profile_id=? AND item_id=?),'')) ON CONFLICT(profile_id,item_id) DO UPDATE SET playback_id=excluded.playback_id`, profile, entityID, profile, entityID)
		} else {
			_, e = tx.ExecContext(s.Context(), `DELETE FROM continue_dismissals WHERE profile_id=? AND item_id=?`, profile, entityID)
		}
		if e != nil {
			return out, e
		}
	}
	if field == "watched" && !readingPersonal {
		if m.ProgressSeconds != nil {
			var duration float64
			if e = tx.QueryRow(`SELECT COALESCE(max(COALESCE(l.end_seconds,a.duration)-l.start_seconds),0) FROM catalog_asset_links l JOIN catalog_assets a ON a.id=l.asset_id WHERE l.entity_id=?`, entityID).Scan(&duration); e != nil {
				return out, e
			}
			if duration > 0 && out.ProgressSeconds > duration {
				return out, requestinput.New("resume exceeds the media duration")
			}
		}
		if e = explicitResumeByID(tx, profile, entityID, out.ProgressSeconds); e != nil {
			return out, e
		}
	} // Playback never writes ratings; rating edits must not freeze unrelated resume progress.
	if resolvedID != "" {
		if _, e = tx.Exec(`UPDATE personal_conflicts SET resolved=1 WHERE id=?`, resolvedID); e != nil {
			return out, e
		}
	}
	if string(personalField(old, field)) != string(personalField(out, field)) || m.Offline != nil || resolvedID != "" {
		out.Revision++
	}
	if field == "watched" {
		if e = personalstate.Write(tx, profile, entityID, out.Watched); e != nil {
			return out, e
		}
	}
	if e = storePersonalByID(tx, profile, entityID, out); e != nil {
		return out, e
	}
	out, e = readPersonalByID(tx, profile, entityID, personalstate.SQL("?", "?"))
	if e != nil {
		return out, e
	}
	if e = snapshotPersonalByID(tx, profile, entityID, out); e != nil {
		return out, e
	}
	if protocol {
		if e = usermutation.SaveContext(s.Context(), tx, usermutation.Result{Status: "applied", TargetID: item, Revision: out.Revision}, time.Now().UnixMilli()); e != nil {
			return out, e
		}
		return out, commit()
	}
	if !owned {
		return out, nil
	}
	response, _ := json.Marshal(out)
	if e = fenceOperation(tx, profile, "personal", m.OperationID, hash); e != nil {
		return out, e
	}
	if _, e = tx.Exec(`INSERT INTO personal_receipts(account_id,profile_id,item_id,operation_id,request_hash,response,created_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(account_id,profile_id,item_id,operation_id) DO UPDATE SET request_hash=excluded.request_hash,response=excluded.response,created_at=excluded.created_at`, account, profile, entityID, m.OperationID, hash, string(response), time.Now().UTC().Format(time.RFC3339)); e != nil {
		return out, e
	}
	return out, commit()
}

// receiptCleanupBatch bounds one receipt-retention transaction to a
// maintenance write plan.
const receiptCleanupBatch = 128

// CleanupPersonalReceipts removes receipts and snapshots older than 30 days, in
// bounded maintenance batches until none are left.
func (s *Service) CleanupPersonalReceipts() error {
	cutoff := time.Now().UTC().Add(-30 * 24 * time.Hour).Format(time.RFC3339)
	limit := ` LIMIT ` + strconv.Itoa(receiptCleanupBatch) + `)`
	statements := []string{}
	for _, table := range []string{"personal_receipts", "personal_batch_receipts", "playlist_receipts", "saved_resource_receipts", "reading_saved_group_receipts", "personal_activity_receipts", "personal_snapshots"} {
		statements = append(statements, `DELETE FROM `+table+` WHERE rowid IN(SELECT rowid FROM `+table+` WHERE created_at<?`+limit)
	}
	// Unresolved conflict evidence remains available regardless of age.
	statements = append(statements, `DELETE FROM personal_explicit_events WHERE rowid IN(SELECT e.rowid FROM personal_explicit_events e WHERE created_at<? AND NOT EXISTS(SELECT 1 FROM personal_conflicts c WHERE c.profile_id=e.profile_id AND c.item_id=e.item_id AND c.field=e.field AND c.base_revision=e.base_revision AND c.resolved=0)`+limit)
	for _, statement := range statements {
		for {
			result, e := dbwork.ExecWrite(context.Background(), s.db, dbwork.ClassMaintenance, statement, cutoff)
			if e != nil {
				return e
			}
			if n, _ := result.RowsAffected(); n < receiptCleanupBatch {
				break
			}
		}
	}
	return nil
}

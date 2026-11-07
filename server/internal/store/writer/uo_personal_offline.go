package writer

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strconv"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

type uoPersonalField uint8

const (
	uoPersonalWatched       uoPersonalField = 1
	uoPersonalWatchlist     uoPersonalField = 2
	uoPersonalFavorite      uoPersonalField = 3
	uoPersonalRating        uoPersonalField = 4
	uoPersonalNotInterested uoPersonalField = 5
)

func (f uoPersonalField) name() string {
	switch f {
	case uoPersonalWatched:
		return "watched"
	case uoPersonalWatchlist:
		return "watchlisted"
	case uoPersonalFavorite:
		return "favorite"
	case uoPersonalRating:
		return "rating"
	case uoPersonalNotInterested:
		return "not_interested"
	}
	return ""
}

// A closed scalar codec, not JSON or a saved primary model.
func (f uoPersonalField) validValue(s string) bool {
	if f.name() == "" {
		return false
	}
	if f != uoPersonalRating {
		return s == "0" || s == "1"
	}
	if s == "null" {
		return true
	}
	n, e := strconv.ParseInt(s, 10, 64)
	return e == nil && n >= 0 && n <= 10 && strconv.FormatInt(n, 10) == s
}

type uoPersonalOfflineInput struct {
	target                                  uoPersonalIntentInput
	field                                   uoPersonalField
	operation, device, mutation, authoredAt string
	deviceSequence, baseRevision            int64
	baseValue, value                        string
}
type uoPersonalOfflineEffect struct {
	revision, deviceSequence int64
	conflictID               string
	changed                  bool
}

func uoPersonalConflictID(profile, operation string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(profile+"\x00"+operation)))
}

// ApplyPersonalOfflineEvidence(603) accepts exactly the NEXT device sequence.
// A divergent field records one conflict and event, never silently overwrites
// the live field. Parent handles exact saved replay before this fresh effect.
func applyUOPersonalOffline(ctx context.Context, turn *commandTurn, q uoPersonalOfflineInput) (out uoPersonalOfflineEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if !q.target.policy.valid() || !uoPersonalText(q.operation, 128) || !uoPersonalText(q.device, 128) || !uoPersonalText(q.mutation, 128) || !uoPersonalText(q.authoredAt, 64) || !uoPersonalText(q.target.at, 64) || q.deviceSequence <= 0 || q.baseRevision < 0 || !q.field.validValue(q.baseValue) || !q.field.validValue(q.value) {
		return out, ErrInvalid
	}
	if _, err = readUOPersonalHead(ctx, turn, q.target.policy); err != nil {
		return out, err
	}
	// Input witness is rechecked even on a conflict-only path.
	if _, err = wc008DraftInteger(ctx, turn, "uo.personal.intent.subject", q.target.subjectID, q.target.publicID[:], q.target.libraryID, q.target.entityRevision, q.target.admissionRevision, q.target.library, uoFlagInteger(q.target.container)); err != nil {
		return out, err
	}
	var last int64
	row, err := wc008DraftQuery(ctx, turn, "uo.personal.offline.device", q.target.policy.profile, q.device)
	if err != nil {
		return out, err
	}
	err = row.Scan(&last)
	if errors.Is(err, sql.ErrNoRows) {
		last = 0
	} else if err != nil {
		return out, err
	}
	if last < 0 || last == math.MaxInt64 || q.deviceSequence != last+1 {
		return out, ErrConflict
	}
	revision, value, err := readUOPersonalScalar(ctx, turn, q.target, q.field)
	if err != nil {
		return out, err
	}
	if q.baseRevision > revision {
		return out, ErrConflict
	}
	out.revision = revision
	if q.baseRevision != revision && value != q.baseValue && value != q.value {
		out.conflictID = uoPersonalConflictID(q.target.policy.profile, q.operation)
		if _, err = wc008DraftInteger(ctx, turn, "uo.personal.offline.conflict", out.conflictID, q.target.policy.profile, q.target.subjectID, q.field.name(), q.baseRevision, q.baseValue); err != nil {
			return out, err
		}
	} else {
		q.target.expectedRevision = revision
		out.revision, out.changed, err = applyUOPersonalScalar(ctx, turn, q.target, q.field, q.value)
		if err != nil {
			return out, err
		}
	}
	if _, err = wc008DraftInteger(ctx, turn, "uo.personal.offline.event", q.target.policy.profile, q.target.subjectID, q.operation, q.field.name(), q.baseRevision, q.baseValue, q.value, q.device, q.mutation, q.deviceSequence, q.authoredAt, q.target.at); err != nil {
		return out, err
	}
	if last == 0 {
		_, err = wc008DraftInteger(ctx, turn, "uo.personal.offline.device_insert", q.target.policy.profile, q.device, q.deviceSequence)
	} else {
		_, err = wc008DraftInteger(ctx, turn, "uo.personal.offline.device_update", q.deviceSequence, q.target.policy.profile, q.device, last)
	}
	out.deviceSequence = q.deviceSequence
	return out, err
}
func readUOPersonalScalar(ctx context.Context, turn *commandTurn, q uoPersonalIntentInput, f uoPersonalField) (revision int64, value string, err error) {
	row, err := wc008DraftQuery(ctx, turn, "uo.personal.offline.current", q.policy.profile, q.subjectID, q.publicID[:])
	if err != nil {
		return 0, "", err
	}
	var watched, list, favorite, notInterested int64
	var rating sql.NullInt64
	err = row.Scan(&revision, &watched, &list, &favorite, &rating, &notInterested)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	} else if err != nil {
		return 0, "", err
	}
	n := watched
	switch f {
	case uoPersonalWatchlist:
		n = list
	case uoPersonalFavorite:
		n = favorite
	case uoPersonalNotInterested:
		n = notInterested
	case uoPersonalRating:
		if !rating.Valid {
			return revision, "null", nil
		}
		n = rating.Int64
	}
	value = strconv.FormatInt(n, 10)
	if !f.validValue(value) {
		return 0, "", ErrResolveRequired
	}
	return revision, value, nil
}
func applyUOPersonalScalar(ctx context.Context, turn *commandTurn, q uoPersonalIntentInput, f uoPersonalField, v string) (revision int64, changed bool, err error) {
	if !f.validValue(v) {
		return 0, false, ErrInvalid
	}
	if f == uoPersonalWatched {
		q.dismiss = false
		q.watched = v == "1"
		e, err := applyUOPersonalIntent(ctx, turn, q)
		return e.revision, err == nil, err
	}
	p := uoPersonalFlagsPatch{}
	switch f {
	case uoPersonalWatchlist:
		p.watchlisted = uoFlagValue{true, v == "1"}
	case uoPersonalFavorite:
		p.favorite = uoFlagValue{true, v == "1"}
	case uoPersonalNotInterested:
		p.notInterested = uoFlagValue{true, v == "1"}
	case uoPersonalRating:
		p.rating.set = true
		if v != "null" {
			n, _ := strconv.ParseInt(v, 10, 64)
			p.rating.value = sql.NullInt64{Int64: n, Valid: true}
		}
	default:
		return 0, false, ErrInvalid
	}
	e, err := applyUOPersonalFlags(ctx, turn, uoPersonalFlagsInput{profileKey: q.policy.profile, libraryKey: q.library, subjectID: q.subjectID, libraryID: q.libraryID, entityRevision: q.entityRevision, admissionRevision: q.admissionRevision, expectedRevision: q.expectedRevision, publicID: q.publicID, patch: p})
	return e.state.revision, e.changed, err
}

type uoPersonalResolveInput struct {
	target       uoPersonalIntentInput
	conflictID   string
	field        uoPersonalField
	baseRevision int64
	value        string
}

// ResolvePersonalConflict(604) CASes the live row and the exact unresolved
// profile/subject/field conflict in one turn. No conflict payload SELECT needed.
func applyUOPersonalResolve(ctx context.Context, turn *commandTurn, q uoPersonalResolveInput) (out uoPersonalOfflineEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if !uoPersonalText(q.conflictID, 64) || q.baseRevision < 0 || !q.field.validValue(q.value) {
		return out, ErrInvalid
	}
	if _, err = readUOPersonalHead(ctx, turn, q.target.policy); err != nil {
		return out, err
	}
	if _, err = wc008DraftInteger(ctx, turn, "uo.personal.offline.resolve", q.conflictID, q.target.policy.profile, q.target.subjectID, q.field.name(), q.baseRevision); err != nil {
		return out, err
	}
	out.revision, out.changed, err = applyUOPersonalScalar(ctx, turn, q.target, q.field, q.value)
	return out, err
}
func uoPersonalOfflineDefinitions() []stmt.Definition {
	return []stmt.Definition{
		uoPDef("uo.personal.offline.device", stmt.Read, `SELECT sequence FROM personal_device_sequences WHERE profile_id=? AND device_id=?`, uoPT("profile"), uoPT("device")),
		uoPDef("uo.personal.offline.device_insert", stmt.Write, `INSERT INTO personal_device_sequences(profile_id,device_id,sequence) VALUES(?,?,?) RETURNING sequence`, uoPT("profile"), uoPT("device"), uoPI("sequence")),
		uoPDef("uo.personal.offline.device_update", stmt.Write, `UPDATE personal_device_sequences SET sequence=? WHERE profile_id=? AND device_id=? AND sequence=? RETURNING sequence`, uoPI("sequence"), uoPT("profile"), uoPT("device"), uoPI("old")),
		uoPDef("uo.personal.offline.current", stmt.Read, `SELECT revision,watched,watchlisted,favorite,rating,not_interested FROM personal_items INDEXED BY personal_current_live WHERE profile_id=? AND item_id=? AND item_public_id=?`, uoPT("profile"), uoPI("subject"), uoPB("public")),
		uoPDef("uo.personal.offline.conflict", stmt.Write, `INSERT INTO personal_conflicts(id,profile_id,item_id,field,base_revision,base_value) VALUES(?,?,?,?,?,?) RETURNING 1`, uoPT("id"), uoPT("profile"), uoPI("subject"), uoPT("field"), uoPI("base"), uoPT("value")),
		uoPDef("uo.personal.offline.event", stmt.Write, `INSERT INTO personal_explicit_events(profile_id,item_id,operation_id,field,base_revision,base_value,value,device_id,device_mutation_id,sequence,authored_at,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?) RETURNING sequence`, uoPT("profile"), uoPI("subject"), uoPT("operation"), uoPT("field"), uoPI("base"), uoPT("base_value"), uoPT("value"), uoPT("device"), uoPT("mutation"), uoPI("sequence"), uoPT("authored"), uoPT("created")),
		uoPDef("uo.personal.offline.resolve", stmt.Write, `UPDATE personal_conflicts SET resolved=1 WHERE id=? AND profile_id=? AND item_id=? AND field=? AND base_revision=? AND resolved=0 RETURNING 1`, uoPT("conflict"), uoPT("profile"), uoPI("subject"), uoPT("field"), uoPI("base")),
	}
}

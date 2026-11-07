package read

import "database/sql"

// Fixed native facts only. The inbox reader must load them under current
// principal/scope authority, and repeat its exact publication fence. Revisions
// are allocated notification mutation ordinals, not timestamps or permissions.
type uoNoticeIdentity struct{ sequence, revision int64 }
type uoNoticeIndividualAck struct {
	exists             bool
	revision           int64
	readMS, archivedMS sql.NullInt64
}
type uoNoticeReadThrough struct {
	exists                     bool
	sequence, revision, readMS int64
}
type uoNoticeReadState struct{ readMS, archivedMS sql.NullInt64 }

// resolveUONoticeReadState is the pre-release schema-v1 readAt rule: individual
// read timestamps remain exact, while bulk-only reads use the current covering
// read-all timestamp. No per-principal bulk history or per-user producer writes.
// A producer restatement's newer revision invalidates old acknowledgement state.
// This values-only helper neither admits a read nor exposes an inbox endpoint.
func resolveUONoticeReadState(notice uoNoticeIdentity, individual uoNoticeIndividualAck, bulk uoNoticeReadThrough) (uoNoticeReadState, error) {
	validTime := func(v sql.NullInt64) bool {
		return v.Valid && v.Int64 >= 0 || !v.Valid && v.Int64 == 0
	}
	if notice.sequence <= 0 || notice.revision <= 0 ||
		!individual.exists && individual != (uoNoticeIndividualAck{}) ||
		individual.exists && (individual.revision <= 0 || !validTime(individual.readMS) || !validTime(individual.archivedMS)) ||
		!bulk.exists && bulk != (uoNoticeReadThrough{}) ||
		bulk.exists && (bulk.sequence <= 0 || bulk.revision <= 0 || bulk.readMS < 0) {
		return uoNoticeReadState{}, ErrInput
	}
	currentIndividual := individual.exists && individual.revision >= notice.revision
	covered := bulk.exists && notice.sequence <= bulk.sequence && notice.revision <= bulk.revision
	var state uoNoticeReadState
	if currentIndividual {
		state.archivedMS = individual.archivedMS
		if individual.readMS.Valid {
			// Even a later read-all keeps this individual's exact accepted time.
			state.readMS = individual.readMS
			return state, nil
		}
		if !covered || individual.revision > bulk.revision {
			// An explicit unread after bulk coverage remains unread. An unrelated
			// notice's acknowledgement never changes this bulk barrier.
			return state, nil
		}
	}
	if covered {
		state.readMS = sql.NullInt64{Int64: bulk.readMS, Valid: true}
	}
	return state, nil
}

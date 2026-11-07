package writer

import (
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// Fixed-size, observation-only fixture sidecars. No grant or caller callback
// crosses this seam. These facts are never fed back into admission.
type n1FixtureAdmission struct {
	Started, Returned       time.Time
	Request                 *governor.Request
	WALBefore               *WALObservation
	Granted                 *bool
	Refusal                 *governor.Refusal
	UsageBefore, UsageAfter *[4]governor.Amounts
	Error                   string
}
type n1AdmissionRecord struct {
	StartedNS   *int64               `json:"started_ns"`
	ReturnedNS  *int64               `json:"returned_ns"`
	Request     *governor.Request    `json:"actual_request"`
	WALBefore   *WALObservation      `json:"published_wal_before"`
	Granted     *bool                `json:"grant_returned"`
	Refusal     *governor.Refusal    `json:"actual_refusal"`
	UsageBefore *[4]governor.Amounts `json:"usage_before_try"`
	UsageAfter  *[4]governor.Amounts `json:"usage_after_try"`
	Error       string               `json:"error"`
	Scope       string               `json:"scope"`
}
type n1FixtureWALReturn struct {
	SetupEntered, Started, Returned time.Time
	Observation                     *WALObservation
	Error                           string
	err                             error
}
type n1WALReturnRecord struct {
	StartedNS   *int64          `json:"started_ns"`
	ReturnedNS  *int64          `json:"returned_ns"`
	Observation *WALObservation `json:"actual_publication"`
	Error       string          `json:"error"`
	Scope       string          `json:"scope"`
}

// Only the maintenance composition opts into production-equivalent owner
// publication. Ordinary protocol/memory fixtures retain their original path.
func (f *inventoryMemoryFixture) observeReturned(tx *transaction) error {
	if !f.maintenance || tx == nil {
		return nil
	}
	if f.postReturn != nil && f.postReturn.SetupEntered == tx.setupEnteredAt {
		return f.postReturn.err
	}
	if !tx.done || tx.physicalReturnedAt.IsZero() {
		return nil
	} // partial: no fabricated return/publication
	r := &n1FixtureWALReturn{SetupEntered: tx.setupEnteredAt, Started: time.Now()}
	f.postReturn = r
	r.Observation, r.err = observeWriterWALAt(f.path, f.wal, f.executor.now, &f.clockHighWater, tx.clockHighWater, tx.commitReturnedAt, tx.physicalReturnedAt)
	r.Returned, r.Error = time.Now(), checkpointError(r.err)
	return r.err
}
func n1BindAdmission(v *n1FixtureAdmission, origin, start, end time.Time) *n1AdmissionRecord {
	if v == nil || start.IsZero() || v.Started.Before(start) || v.Returned.IsZero() || v.Returned.After(end) {
		return nil
	}
	return &n1AdmissionRecord{n1NS(origin, v.Started), n1NS(origin, v.Returned), v.Request, v.WALBefore, v.Granted, v.Refusal, v.UsageBefore, v.UsageAfter, v.Error, "THIS fixture begin: actual pre-admission owner publication/request/decision and sampled usage; not public Evidence or before-custody proof"}
}
func n1BindWALReturn(v *n1FixtureWALReturn, origin, start, end time.Time) *n1WALReturnRecord {
	if v == nil || start.IsZero() || v.SetupEntered.Before(start) || v.Returned.IsZero() || v.Returned.After(end) {
		return nil
	}
	return &n1WALReturnRecord{n1NS(origin, v.Started), n1NS(origin, v.Returned), v.Observation, v.Error, "actual owner publication AFTER required native return/retirement using production observer and frame clock floors; advisory WALAfter stays separate"}
}

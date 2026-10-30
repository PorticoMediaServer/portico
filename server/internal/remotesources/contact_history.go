package remotesources

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/rcloneprogram"
	"github.com/PorticoMediaServer/portico/server/internal/remotemedia"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// contactKey is the exact association epoch: the source, the mount it admitted
// and the effective generation. A replacement mount at the same generation is a
// different key, so late evidence from the old association cannot discard or
// relabel the current one.
type contactKey struct {
	source     string
	mountID    string
	generation int64
}

// sourceContact is one association's coalesced contact evidence. It is held in
// memory between worker wakes, so a read, chunk or file never writes the
// database once per provider operation. Timestamps are Unix milliseconds and
// only move forward for an association.
type sourceContact struct {
	order     uint64
	successMs int64
	failureMs int64
	errorCode string
	checkMs   int64
}

type contactEntry struct {
	key     contactKey
	contact sourceContact
}

const (
	// contactFlushBatch bounds the associations one transaction persists.
	contactFlushBatch = 32
	// contactFlushBatches bounds the transactions one wake attempts.
	contactFlushBatches = 4
	// contactPendingMax bounds pending associations, including a batch that is
	// being written. In-flight capacity is reserved, so a failed batch requeues
	// without exceeding the bound.
	contactPendingMax = 256
)

// contactErrorCodes is the closed set of real completed provider failures that
// contact history may publish. Capacity, cancellation, retirement, local
// configuration, cursor, range and local program outcomes are not provider
// contact evidence and are never recorded.
var contactErrorCodes = map[string]bool{"offline": true, "credentials_required": true, "rate_limited": true, "quota_exceeded": true}

// noteSourceContact records one completed real provider operation for the
// worker wake to persist. It never writes the database and never stores prose,
// a locator or a body. A cancellation, retirement, local capacity or
// configuration outcome, or an unknown generation is ignored.
func (s *Service) noteSourceContact(source, mountID, generation string, at time.Time, err error) {
	if s == nil || source == "" || generation == "" {
		return
	}
	gen, e := strconv.ParseInt(generation, 10, 64)
	if e != nil || gen < 0 {
		return
	}
	code := ""
	if err != nil {
		code = contactErrorCode(err)
		if code == "" {
			return
		}
	}
	ms := at.UnixMilli()
	if ms <= 0 {
		return
	}
	s.recordContact(source, mountID, gen, ms, func(current *sourceContact) {
		if err == nil && ms > current.successMs {
			current.successMs = ms
		}
		if code != "" && ms >= current.failureMs {
			current.failureMs, current.errorCode = ms, code
		}
	})
}

// noteSourceTest records one completed explicit saved-source Test connection
// for the worker wake. Checked time and the completed outcome merge atomically
// under one contactsMu acquisition and one captured timestamp, so a flush cannot
// persist one half and drop the other. It returns the closed failure code, or ""
// for a real completed backend success. Only a real success or a guarded
// direct-provider failure returned reachable=false calls this; local/private-RC/
// setup/custody errors, cancelled or retired checks, cached outcomes and
// ordinary reads or scans never do.
func (s *Service) noteSourceTest(source, mountID, generation string, at time.Time, err error) string {
	if s == nil || source == "" || generation == "" {
		return ""
	}
	gen, e := strconv.ParseInt(generation, 10, 64)
	if e != nil || gen < 0 {
		return ""
	}
	code := ""
	if err != nil {
		code = contactErrorCode(err)
		if code == "" {
			return ""
		}
	}
	ms := at.UnixMilli()
	if ms <= 0 {
		return code
	}
	s.recordContact(source, mountID, gen, ms, func(current *sourceContact) {
		if ms > current.checkMs {
			current.checkMs = ms
		}
		if err == nil {
			if ms > current.successMs {
				current.successMs = ms
			}
		} else if ms >= current.failureMs {
			current.failureMs, current.errorCode = ms, code
		}
	})
	return code
}

// recordContact applies one observation to the exact association under the
// existing bounded pending set and oldest-pending order.
func (s *Service) recordContact(source, mountID string, generation, ms int64, apply func(*sourceContact)) {
	key := contactKey{source: source, mountID: mountID, generation: generation}
	s.contactsMu.Lock()
	if s.contacts == nil {
		s.contacts = map[contactKey]sourceContact{}
	}
	current, exists := s.contacts[key]
	if !exists && len(s.contacts)+s.contactsReserved >= contactPendingMax {
		// Bounded pending set, including in-flight batches: a new association
		// waits for the next wake. Existing associations keep coalescing.
		s.contactsMu.Unlock()
		return
	}
	if !exists {
		s.contactSeq++
		current.order = s.contactSeq
	}
	apply(&current)
	s.contacts[key] = current
	s.contactsMu.Unlock()
}

// contactErrorCode keeps only an explicit provider-domain or transport failure
// observed at a real provider operation. Unknown database, filesystem or local
// errors yield no contact fact; ErrorCode's default offline is deliberately not
// used here. The result is always a closed code, never provider or job text.
func contactErrorCode(err error) string {
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded),
		errors.Is(err, rcloneprogram.ErrServeStopped),
		errors.Is(err, storage.ErrBusy), errors.Is(err, remotemedia.ErrBusy),
		errors.Is(err, storage.ErrRemoteChanged), errors.Is(err, storage.ErrRemoteConfig),
		errors.Is(err, storage.ErrRemoteCursor), errors.Is(err, storage.ErrRemoteLimit),
		errors.Is(err, storage.ErrRemoteRange), errors.Is(err, storage.ErrRemoteBinary):
		return ""
	}
	var response *remotemedia.ResponseFailure
	if errors.As(err, &response) {
		switch response.Kind {
		case "throttled":
			return "rate_limited"
		case "quota":
			return "quota_exceeded"
		case "unavailable":
			return "offline"
		default:
			return ""
		}
	}
	switch {
	case errors.Is(err, storage.ErrRemoteCredentials), errors.Is(err, remotemedia.ErrDenied):
		return "credentials_required"
	case errors.Is(err, storage.ErrRemoteOffline), errors.Is(err, remotemedia.ErrUnavailable):
		return "offline"
	}
	return ""
}

// serveStopping reports a closed or cancelled serve runtime: every in-flight
// error then comes from retirement/shutdown, not from a provider answer.
func (s *Service) serveStopping() bool {
	s.serveMu.Lock()
	stopped := s.serveClosed || (s.serveLife != nil && s.serveLife.Err() != nil)
	s.serveMu.Unlock()
	return stopped
}

// flushSourceContacts writes oldest-pending batches while progress is possible
// and reports whether associations still wait. A failed batch is requeued and
// stops the loop so a broken database does not spin the wake.
func (s *Service) flushSourceContacts(ctx context.Context) bool {
	progressed := false
	for i := 0; i < contactFlushBatches; i++ {
		wrote, pending := s.flushContactBatch(ctx)
		if !wrote {
			return false
		}
		progressed = true
		if !pending {
			break
		}
	}
	if !progressed {
		return false
	}
	s.contactsMu.Lock()
	pending := len(s.contacts) > 0
	s.contactsMu.Unlock()
	return pending
}

// flushContactBatch persists the oldest pending associations. The association
// fence is the same kind-aware mount/source generation check the version
// projection uses, so an operation that finished after a replacement cannot
// write evidence.
func (s *Service) flushContactBatch(ctx context.Context) (wrote, pending bool) {
	s.contactsMu.Lock()
	if len(s.contacts) == 0 {
		s.contactsMu.Unlock()
		return false, false
	}
	entries := make([]contactEntry, 0, len(s.contacts))
	for key, contact := range s.contacts {
		entries = append(entries, contactEntry{key: key, contact: contact})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].contact.order != entries[j].contact.order {
			return entries[i].contact.order < entries[j].contact.order
		}
		if entries[i].key.source != entries[j].key.source {
			return entries[i].key.source < entries[j].key.source
		}
		return entries[i].key.mountID < entries[j].key.mountID
	})
	if len(entries) > contactFlushBatch {
		entries = entries[:contactFlushBatch]
	}
	for _, entry := range entries {
		delete(s.contacts, entry.key)
	}
	// Reserve the in-flight capacity until the write resolves, so concurrent
	// admissions cannot fill the pending cap and make a failed requeue exceed it.
	s.contactsReserved += len(entries)
	remaining := len(s.contacts) > 0
	s.contactsMu.Unlock()

	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassBackgroundMedia)
	if err != nil {
		s.requeueContacts(entries)
		return false, remaining
	}
	for _, entry := range entries {
		contact := entry.contact
		generation := strconv.FormatInt(entry.key.generation, 10)
		if _, err = gated.Tx().ExecContext(ctx, `INSERT INTO remote_source_contacts(source_id,mount_id,source_generation,last_success_ms,last_failure_ms,last_error_code,last_check_ms) SELECT ?,?,?,?,?,?,? WHERE EXISTS(SELECT 1 FROM remote_sources r WHERE r.id=? AND r.removed=0 AND r.mount_id=? AND (CASE WHEN r.kind IN('rclone','sftp','smb') THEN (SELECT CAST(m.generation AS TEXT) FROM mount_backend_configs m WHERE m.mount_id=r.mount_id) ELSE CAST(r.generation AS TEXT) END)=?) ON CONFLICT(source_id) DO UPDATE SET mount_id=excluded.mount_id,source_generation=excluded.source_generation,last_success_ms=CASE WHEN remote_source_contacts.mount_id=excluded.mount_id AND remote_source_contacts.source_generation=excluded.source_generation THEN MAX(remote_source_contacts.last_success_ms,excluded.last_success_ms) ELSE excluded.last_success_ms END,last_failure_ms=CASE WHEN remote_source_contacts.mount_id=excluded.mount_id AND remote_source_contacts.source_generation=excluded.source_generation THEN MAX(remote_source_contacts.last_failure_ms,excluded.last_failure_ms) ELSE excluded.last_failure_ms END,last_error_code=CASE WHEN remote_source_contacts.mount_id<>excluded.mount_id OR remote_source_contacts.source_generation<>excluded.source_generation THEN excluded.last_error_code WHEN excluded.last_failure_ms>0 AND excluded.last_failure_ms>=remote_source_contacts.last_failure_ms THEN excluded.last_error_code ELSE remote_source_contacts.last_error_code END,last_check_ms=CASE WHEN remote_source_contacts.mount_id=excluded.mount_id AND remote_source_contacts.source_generation=excluded.source_generation THEN MAX(remote_source_contacts.last_check_ms,excluded.last_check_ms) ELSE excluded.last_check_ms END`, entry.key.source, entry.key.mountID, generation, contact.successMs, contact.failureMs, contact.errorCode, contact.checkMs, entry.key.source, entry.key.mountID, generation); err != nil {
			gated.Rollback()
			s.requeueContacts(entries)
			return false, remaining
		}
	}
	if err = gated.Commit(); err != nil {
		s.requeueContacts(entries)
		return false, remaining
	}
	s.finishContactBatch(len(entries))
	return true, remaining
}

// finishContactBatch releases the reservation of a written batch.
func (s *Service) finishContactBatch(n int) {
	s.contactsMu.Lock()
	s.contactsReserved -= n
	if s.contactsReserved < 0 {
		s.contactsReserved = 0
	}
	s.contactsMu.Unlock()
}

// requeueContacts merges an unwritten batch back under its exact association
// keys and releases its reservation. The reservation kept the total at or below
// the pending cap while the batch was in flight.
func (s *Service) requeueContacts(entries []contactEntry) {
	s.contactsMu.Lock()
	if s.contacts == nil {
		s.contacts = map[contactKey]sourceContact{}
	}
	for _, entry := range entries {
		current, exists := s.contacts[entry.key]
		if !exists {
			s.contacts[entry.key] = entry.contact
			continue
		}
		if entry.contact.successMs > current.successMs {
			current.successMs = entry.contact.successMs
		}
		if entry.contact.failureMs > 0 && entry.contact.failureMs >= current.failureMs {
			current.failureMs, current.errorCode = entry.contact.failureMs, entry.contact.errorCode
		}
		if entry.contact.checkMs > current.checkMs {
			current.checkMs = entry.contact.checkMs
		}
		if entry.contact.order < current.order {
			current.order = entry.contact.order
		}
		s.contacts[entry.key] = current
	}
	s.contactsReserved -= len(entries)
	if s.contactsReserved < 0 {
		s.contactsReserved = 0
	}
	s.contactsMu.Unlock()
}

package writer

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strconv"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func ipArtStep(t *commandTurn, id stmt.ID) (statementStep, error) {
	if t == nil {
		return statementStep{}, ErrInvalid
	}
	s := statementStep{id, ipArtworkMaterialWork(id)}
	if v, ok := t.steps[id]; ok && v.statementStep == s {
		return s, nil
	}
	return statementStep{}, ErrInvalid
}
func ipArtRead(ctx context.Context, t *commandTurn, id stmt.ID, args ...any) (commandRow, error) {
	s, e := ipArtStep(t, id)
	if e != nil {
		return commandRow{}, e
	}
	return t.queryRow(ctx, s, args...)
}
func ipArtWrite(ctx context.Context, t *commandTurn, id stmt.ID, args ...any) error {
	s, e := ipArtStep(t, id)
	if e != nil {
		return e
	}
	r, e := t.exec(ctx, s, args...)
	if e != nil {
		return e
	}
	n, e := r.RowsAffected()
	if e != nil {
		return e
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}
func ipArtOne(ctx context.Context, t *commandTurn, id stmt.ID, args ...any) error {
	r, e := ipArtRead(ctx, t, id, args...)
	if e != nil {
		return e
	}
	var n int64
	if e = r.Scan(&n); e != nil {
		return e
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}
func ipArtAbsent(ctx context.Context, t *commandTurn, id stmt.ID, args ...any) error {
	e := ipArtOne(ctx, t, id, args...)
	if errors.Is(e, sql.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	return ErrConflict
}
func ipArtText(v string) any {
	if v == "" {
		return nil
	}
	return v
}
func ipArtNumber(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}
func ipArtOptionalNumber(v EvidenceInteger) any {
	if !v.Present {
		return nil
	}
	return v.Value
}
func ipArtOptionalText(v EvidenceText) any {
	if !v.Present {
		return nil
	}
	return v.Value
}
func ipArtFlag(v bool) int64 {
	if v {
		return 1
	}
	return 0
}
func ipArtNamespace(o ipArtworkDiscoveryOwner) (string, int64) {
	if o.entityID > 0 {
		return "entity", o.entityID
	}
	return "person", o.personID
}
func ipArtCommandName(k CommandID) string {
	switch k {
	case IPArtworkJobClaim:
		return "job_claim"
	case IPArtworkJobRenew:
		return "job_renew"
	case IPArtworkMaterialPublish:
		return "material_publish"
	case IPArtworkSelect:
		return "select"
	case IPArtworkClear:
		return "clear"
	case IPArtworkUploadAccept:
		return "upload_accept"
	case IPArtworkGCMark:
		return "gc_mark"
	case IPArtworkGCFinish:
		return "gc_finish"
	case IPArtworkReencode:
		return "reencode"
	}
	return ""
}

// Every text-bearing result is octet-preflighted before copy. The caller's
// admitted physical memory scope must fund these exact lengths plus fixed data.
func ipArtLookup(ctx context.Context, t *commandTurn, k CommandID, q ipArtworkMaterialRequest, d RequestDigest) (out ipArtworkMaterialOutcome, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	row, err := ipArtRead(ctx, t, ipAMOwner, q.Owner.id)
	if err != nil {
		return out, err
	}
	var owner receiptOwnerFacts
	var inc []byte
	if err = row.Scan(&inc, &owner.lastSequence, &owner.acknowledgedSequence); err != nil {
		return out, errors.Join(ErrOwnerUnavailable, err)
	}
	if len(inc) != 16 {
		return out, ErrResolveRequired
	}
	owner.exists = true
	copy(owner.incarnation[:], inc)
	i := q.Identity
	row, err = ipArtRead(ctx, t, ipAMReceiptOctets, q.Owner.id, i.Incarnation[:], i.Sequence)
	if err != nil {
		return out, err
	}
	var sizes [10]int64
	err = row.Scan(&sizes[0], &sizes[1], &sizes[2], &sizes[3], &sizes[4], &sizes[5], &sizes[6], &sizes[7], &sizes[8], &sizes[9])
	found := receiptFacts{}
	if errors.Is(err, sql.ErrNoRows) {
		out.decision, err = classifyReceipt(receiptIdentity{i, d}, k, owner, found)
		return out, err
	}
	if err != nil {
		return out, err
	}
	limits := [10]int64{256, 64, 7, 64, 64, 64, 16, 32, 32, 16}
	for n, size := range sizes {
		if size < 0 || size > limits[n] {
			return out, ErrResolveRequired
		}
	}
	if sizes[6] != 16 || sizes[7] != 32 || sizes[8] != 32 || (sizes[9] != 0 && sizes[9] != 16) {
		return out, ErrResolveRequired
	}
	row, err = ipArtRead(ctx, t, ipAMReceipt, q.Owner.id, i.Incarnation[:], i.Sequence)
	if err != nil {
		return out, err
	}
	r := ipArtworkMaterialResult{OwnerID: q.Owner.id}
	var operation, request, result, token []byte
	var kind int64
	err = row.Scan(&operation, &request, &result, &kind, &r.Revision, &r.AppliedMS, &r.Count, &r.Action, &r.LeaseGeneration, &r.LeaseUntilMS, &r.MaterialID, &r.Role, &r.Choice, &r.Selected, &r.Previous, &r.Blob, &r.SelectionRevision, &r.StorageRevision, &r.HistoryID, &token)
	if err != nil {
		return out, err
	}
	r.Kind = CommandID(kind)
	for n, v := range []string{r.MaterialID, r.Role, r.Choice, r.Selected, r.Previous, r.Blob} {
		if int64(len(v)) != sizes[n] {
			return out, ErrResolveRequired
		}
	}
	if len(operation) != 16 || len(request) != 32 || len(result) != 32 || int64(len(token)) != sizes[9] {
		return out, ErrResolveRequired
	}
	copy(r.Token[:], token)
	found.exists = true
	found.kind = r.Kind
	found.resultCount = r.Count
	found.identity.command = i
	copy(found.identity.command.OperationID[:], operation)
	copy(found.identity.digest[:], request)
	copy(found.resultDigest[:], result)
	out.decision, err = classifyReceipt(receiptIdentity{i, d}, k, owner, found)
	if err != nil || out.decision != receiptReplay {
		return out, err
	}
	expected, e := ipArtResultDigest(i, d, r)
	if e != nil || !bytes.Equal(expected[:], result) {
		return out, errors.Join(ErrResolveRequired, e)
	}
	out.result = r
	return out, nil
}

func ipArtFresh(ctx context.Context, t *commandTurn, k CommandID, q ipArtworkMaterialRequest, now int64) error {
	o := q.Owner
	if k == IPArtworkGCMark || k == IPArtworkGCFinish {
		return ipArtOne(ctx, t, ipAMMaintenance, o.id, o.incarnation[:], o.revision, o.entityID, o.personID)
	}
	if e := ipArtOne(ctx, t, ipAMFresh, o.id, o.incarnation[:], o.revision, o.entityID, o.personID, o.sourceIncarnation[:], o.sourceRevision, o.leaseOwner, o.leaseGeneration, o.leaseUntilMS, now); e != nil {
		return errors.Join(ErrConflict, e)
	}
	id := ipAMEntity
	subject := o.entityID
	if subject == 0 {
		id = ipAMPerson
		subject = o.personID
	}
	row, e := ipArtRead(ctx, t, id, q.Policy.Language, subject)
	if e != nil {
		return e
	}
	var inc []byte
	var rev, retired, language int64
	if e = row.Scan(&inc, &rev, &retired, &language); e != nil {
		return e
	}
	if !bytes.Equal(inc, o.sourceIncarnation[:]) || rev != o.sourceRevision || retired != 0 || language != 1 {
		return ErrConflict
	}
	row, e = ipArtRead(ctx, t, ipAMPolicy)
	if e != nil {
		return e
	}
	var policy, online int64
	if e = row.Scan(&policy, &online); e != nil {
		return e
	}
	if policy != q.Policy.Revision {
		return ErrConflict
	}
	// Offline owner selections/uploads still use the current policy revision;
	// provider acquisition/reencode additionally requires current online consent.
	if k == IPArtworkClear || k == IPArtworkUploadAccept || k == IPArtworkSelect && q.Selection.Choice == "locked" {
		return nil
	}
	// Upload is a typed native image job, never a permission flag. The body
	// subsequently matches provider/choice/owner/source/lease in ipAMJob before
	// any effect; a supplied "upload" label cannot convert another native job.
	if q.Policy.Provider == "upload" && q.Job.Choice == "locked" {
		return nil
	}
	if online != 1 {
		return ErrConflict
	}
	row, e = ipArtRead(ctx, t, ipAMProvider, q.Policy.Provider)
	if e != nil {
		return e
	}
	var enabled int64
	if e = row.Scan(&enabled); e != nil {
		return e
	}
	if enabled != 1 {
		return ErrConflict
	}
	return nil
}
func ipArtJobArgs(q ipArtworkMaterialRequest) []any {
	j := q.Job
	kind, subject := ipArtNamespace(q.Owner)
	return []any{j.ID, q.Owner.id, kind, subject, j.Role, j.Choice, j.ImageID, j.Origin, j.Attribution, j.Locale, q.Policy.Language, q.Policy.Provider, j.SourceFence, j.Actor, ipArtOptionalText(j.ObjectID), ipArtOptionalText(j.ObjectRevision), j.SelectionRevision, j.Revision, j.LeaseOwner, j.LeaseGeneration, j.LeaseUntilMS, j.State, ipArtOptionalNumber(j.NextMS)}
}
func ipArtJobFresh(ctx context.Context, t *commandTurn, q ipArtworkMaterialRequest, now int64) error {
	if e := ipArtOne(ctx, t, ipAMJob, ipArtJobArgs(q)...); e != nil {
		return errors.Join(ErrConflict, e)
	}
	if q.Job.State != 2 || q.Job.LeaseOwner != q.Owner.leaseOwner || q.Job.LeaseUntilMS <= now {
		return ErrConflict
	}
	// Material prepared for a stale/cleared/replaced role cannot become visible.
	kind, subject := ipArtNamespace(q.Owner)
	if q.Job.SelectionRevision == 0 {
		return ipArtAbsent(ctx, t, ipAMSelectionAbsent, kind, subject, q.Job.Role, q.Job.Choice)
	}
	s := q.Selection
	if s.Role != q.Job.Role || s.Choice != q.Job.Choice || !s.Before.Present || s.Before.Revision != q.Job.SelectionRevision {
		return ErrConflict
	}
	return ipArtCheckSelection(ctx, t, q.Owner, s.Role, s.Choice, s.Before)
}

// Body-only acceptance. finishCommand discards every result on any error; the
// frame owns rollback/drain. The original request includes EVERY typed witness
// (including identity), and is never rebuilt from a new lease or generated ID.
func ipArtworkMaterialApply(ctx context.Context, t *commandTurn, k CommandID, q ipArtworkMaterialRequest) (out ipArtworkMaterialOutcome, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if t == nil || t.transaction == nil || t.transaction.descriptor.ID != k {
		return out, ErrInvalid
	}
	defer t.transaction.guardPanic()
	if err = ipArtValidate(k, q); err != nil {
		return out, err
	}
	d, err := canonicalRequestDigest(k, 1, q.Identity, q)
	if err != nil {
		return out, err
	}
	out, err = ipArtLookup(ctx, t, k, q, d)
	if err != nil || out.decision != receiptFresh {
		return out, err
	}
	now := t.transaction.writer.now().UnixMilli()
	if now < 0 {
		return out, ErrInvalid
	}
	if err = ipArtFresh(ctx, t, k, q, now); err != nil {
		return out, err
	}
	r := ipArtworkMaterialResult{Kind: k, OwnerID: q.Owner.id, Revision: q.Owner.revision + 1, AppliedMS: now}
	switch k {
	case IPArtworkJobClaim, IPArtworkJobRenew:
		err = ipArtLease(ctx, t, k, q, now, &r)
	case IPArtworkMaterialPublish, IPArtworkReencode:
		err = ipArtMaterial(ctx, t, k, q, now, &r)
	case IPArtworkSelect, IPArtworkClear, IPArtworkUploadAccept:
		err = ipArtSelection(ctx, t, k, q, now, &r)
	case IPArtworkGCMark, IPArtworkGCFinish:
		err = ipArtGC(ctx, t, k, q, now, &r)
	default:
		err = ErrInvalid
	}
	if err != nil {
		return out, err
	}
	bump := int64(0)
	if r.HistoryID != 0 {
		bump = 1
	}
	o, i := q.Owner, q.Identity
	if err = ipArtWrite(ctx, t, ipAMOwnerCAS, i.Sequence, bump, o.id, o.incarnation[:], o.revision, i.Sequence-1, i.Sequence-1, o.entityID, o.personID); err != nil {
		return out, err
	}
	rd, err := ipArtResultDigest(i, d, r)
	if err != nil {
		return out, err
	}
	var token any
	if r.Token != ([16]byte{}) {
		token = r.Token[:]
	}
	err = ipArtWrite(ctx, t, ipAMSave, o.id, i.Incarnation[:], i.Sequence, i.OperationID[:], d[:], rd[:], ipArtCommandName(k), r.Revision, r.AppliedMS, r.Count, r.Action, r.LeaseGeneration, r.LeaseUntilMS, ipArtText(r.MaterialID), ipArtText(r.Role), ipArtText(r.Choice), ipArtText(r.Selected), ipArtText(r.Previous), ipArtText(r.Blob), ipArtNumber(r.SelectionRevision), ipArtNumber(r.StorageRevision), ipArtNumber(r.HistoryID), token)
	if err != nil {
		return out, err
	}
	out.result = r
	return out, nil
}
func ipArtLease(ctx context.Context, t *commandTurn, k CommandID, q ipArtworkMaterialRequest, now int64, r *ipArtworkMaterialResult) error {
	if e := ipArtOne(ctx, t, ipAMJob, ipArtJobArgs(q)...); e != nil {
		return e
	}
	j := q.Job
	until := now + q.Phase
	if until < now || until > q.Owner.leaseUntilMS {
		return ErrConflict
	}
	if k == IPArtworkJobClaim {
		if j.State == 4 || j.LeaseUntilMS > now || !j.NextMS.Present || j.NextMS.Value > now {
			return ErrConflict
		}
		if e := ipArtWrite(ctx, t, ipAMClaim, q.Owner.leaseOwner, until, j.ID, j.Revision, j.LeaseGeneration, now); e != nil {
			return e
		}
		r.LeaseGeneration = j.LeaseGeneration + 1
	} else {
		if j.State != 2 || j.LeaseOwner != q.Owner.leaseOwner || j.LeaseUntilMS <= now || until <= j.LeaseUntilMS {
			return ErrConflict
		}
		if e := ipArtWrite(ctx, t, ipAMRenew, until, j.ID, j.Revision, j.LeaseOwner, j.LeaseGeneration, j.LeaseUntilMS, now); e != nil {
			return e
		}
		r.LeaseGeneration = j.LeaseGeneration
	}
	r.LeaseUntilMS = until
	r.Count = 1
	return nil
}

func ipArtSlotArgs(m ipArtworkMaterial, n int) []any {
	s := m.Slots[n]
	return []any{m.ID, int64(n), s.Digest, s.MIME, s.Width, s.Height, s.Bytes, ipArtFlag(s.Outlier)}
}
func ipArtSlotKeys(ctx context.Context, t *commandTurn, id string) (keys []int64, err error) {
	defer func() { keys, err = finishCommand(ctx, commandLifetime(t), keys, err) }()
	step, err := ipArtStep(t, ipAMSlotKeys)
	if err != nil {
		return nil, err
	}
	rows, err := t.query(ctx, step, id, int64(-1), int64(5))
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		var key int64
		if err = rows.Scan(&key); err != nil {
			return nil, err
		}
		if key != int64(len(keys)) || len(keys) == 4 {
			return nil, ErrConflict
		}
		keys = append(keys, key)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return keys, nil
}

// Independent native verification is a bounded complete four-key manifest,
// including no-next. No opaque source manifest is substituted for native CAS.
func ipArtVerifyIntent(ctx context.Context, t *commandTurn, q ipArtworkMaterialRequest) (int64, error) {
	m := q.Material
	o := q.Owner
	row, e := ipArtRead(ctx, t, ipAMIntent, m.ID, o.id, o.incarnation[:], m.Generation, m.Revision, m.SourceDigest, m.MIME, m.Width, m.Height, m.LargeEdge, m.EncoderRevision, m.SlotCount, m.SlotCount)
	if e != nil {
		return 0, e
	}
	var state int64
	if e = row.Scan(&state); e != nil {
		return 0, e
	}
	keys, e := ipArtSlotKeys(ctx, t, m.ID)
	if e != nil {
		return 0, e
	}
	if int64(len(keys)) != m.SlotCount {
		return 0, ErrConflict
	}
	for _, key := range keys {
		if e = ipArtOne(ctx, t, ipAMSlotExact, ipArtSlotArgs(m, int(key))...); e != nil {
			return 0, e
		}
	}
	return state, nil
}
func ipArtOriginalBytes(m ipArtworkMaterial) int64 {
	if m.SlotCount == 4 {
		return m.Slots[3].Bytes
	}
	return 0
}
func ipArtSourceArgs(m ipArtworkMaterial, rev int64, previous bool) []any {
	s := ipArtworkMaterialSource{m.Slots[0].Digest, m.Slots[1].Digest, m.Slots[2].Digest, m.LargeEdge, ipArtFlag(m.Slots[0].Outlier), ipArtFlag(m.Slots[1].Outlier), m.EncoderRevision, ipArtOriginalBytes(m)}
	if previous {
		s = m.Previous
	}
	return []any{m.SourceDigest, m.MIME, m.Width, m.Height, s.Small, s.Large, s.Ambient, s.LargeEdge, s.SmallOutlier, s.LargeOutlier, s.EncoderRevision, s.OriginalBytes, rev}
}
func ipArtMaterial(ctx context.Context, t *commandTurn, k CommandID, q ipArtworkMaterialRequest, now int64, r *ipArtworkMaterialResult) error {
	if e := ipArtJobFresh(ctx, t, q, now); e != nil {
		return e
	}
	m, o := q.Material, q.Owner
	r.Action = q.Phase
	r.MaterialID = m.ID
	if q.Phase == ipArtPrepare {
		if m.Revision != 1 {
			return ErrConflict
		}
		// Refuse creation of a new custody/reference to material already deleting.
		if e := ipArtCheckReadyOrAbsent(ctx, t, m); e != nil {
			return e
		}
		if e := ipArtWrite(ctx, t, ipAMIntentCreate, m.ID, o.id, o.incarnation[:], m.Generation, m.SourceDigest, m.MIME, m.Width, m.Height, m.LargeEdge, m.EncoderRevision, m.SlotCount); e != nil {
			return e
		}
		for n := 0; n < int(m.SlotCount); n++ {
			if e := ipArtWrite(ctx, t, ipAMSlotCreate, ipArtSlotArgs(m, n)...); e != nil {
				return e
			}
		}
		if e := ipArtWrite(ctx, t, ipAMStageSlots, m.SlotCount, m.ID, m.SlotCount); e != nil {
			return e
		}
		r.Count = m.SlotCount
		return nil
	}
	state, e := ipArtVerifyIntent(ctx, t, q)
	if e != nil {
		return e
	}
	if q.Phase == ipArtInstalled {
		if state != ipArtPrepare {
			return ErrConflict
		}
		// Caller has verified all exact installed files under physical custody; this
		// state transition is not a file-existence proof or a governor capability.
		if e = ipArtWrite(ctx, t, ipAMInstall, m.ID, m.Generation, m.Revision); e != nil {
			return e
		}
		r.Count = m.SlotCount
		return nil
	}
	if state != ipArtInstalled {
		return ErrConflict
	}
	if e = ipArtPublishSource(ctx, t, k, q, now, r); e != nil {
		return e
	}
	if e = ipArtWrite(ctx, t, ipAMPublishIntent, m.ID, m.Generation, m.Revision); e != nil {
		return e
	}
	if e = ipArtWrite(ctx, t, ipAMCompleteJob, q.Job.ID, q.Job.Revision, q.Job.LeaseOwner, q.Job.LeaseGeneration, q.Job.LeaseUntilMS, now); e != nil {
		return e
	}
	r.Selected = m.SourceDigest
	return nil
}
func ipArtCheckReadyOrAbsent(ctx context.Context, t *commandTurn, m ipArtworkMaterial) error {
	e := ipArtAbsent(ctx, t, ipAMSourceAbsent, m.SourceDigest)
	if e != nil {
		if !errors.Is(e, ErrConflict) {
			return e
		}
		if e = ipArtOne(ctx, t, ipAMSourceReady, m.SourceDigest); e != nil {
			return e
		}
	}
	for n := 0; n < 3; n++ {
		s := m.Slots[n]
		e = ipArtAbsent(ctx, t, ipAMBlobAbsent, s.Digest)
		if e != nil {
			if !errors.Is(e, ErrConflict) {
				return e
			}
			if e = ipArtOne(ctx, t, ipAMBlob, s.Digest, s.MIME, s.Width, s.Height, s.Bytes); e != nil {
				return e
			}
		}
	}
	return nil
}
func ipArtPublishSource(ctx context.Context, t *commandTurn, k CommandID, q ipArtworkMaterialRequest, now int64, result *ipArtworkMaterialResult) error {
	m := q.Material
	result.StorageRevision = m.SourceRevision + 1
	result.Count = 1
	if e := ipArtCheckReadyOrAbsent(ctx, t, m); e != nil {
		return e
	}
	if k == IPArtworkReencode {
		if e := ipArtOne(ctx, t, ipAMSource, ipArtSourceArgs(m, m.SourceRevision, true)...); e != nil {
			return e
		}
		// Reencode preserves original physical custody and increments ONE source
		// revision. Readers inherit the source; no referencing title enumeration.
		if m.Previous.OriginalBytes != ipArtOriginalBytes(m) {
			return ErrConflict
		}
		desired := ipArtworkMaterialSource{m.Slots[0].Digest, m.Slots[1].Digest, m.Slots[2].Digest, m.LargeEdge, ipArtFlag(m.Slots[0].Outlier), ipArtFlag(m.Slots[1].Outlier), m.EncoderRevision, ipArtOriginalBytes(m)}
		if desired == m.Previous {
			result.StorageRevision = m.SourceRevision
			result.Count = 0
			return nil
		}
	}
	for n := 0; n < 3; n++ {
		s := m.Slots[n]
		if e := ipArtOne(ctx, t, ipAMBlob, s.Digest, s.MIME, s.Width, s.Height, s.Bytes); e != nil {
			if !errors.Is(e, sql.ErrNoRows) {
				return e
			}
			if e = ipArtAbsent(ctx, t, ipAMBlobAbsent, s.Digest); e != nil {
				return e
			}
			if e = ipArtWrite(ctx, t, ipAMBlobCreate, s.Digest, s.MIME, s.Width, s.Height, s.Bytes, strconv.FormatInt(now, 10)); e != nil {
				return e
			}
		}
	}
	if k == IPArtworkReencode {
		a := ipArtSourceArgs(m, m.SourceRevision, false)
		prev := m.Previous
		args := []any{a[4], a[5], a[6], a[7], a[8], a[9], a[10], m.SourceDigest, m.SourceRevision, prev.Small, prev.Large, prev.Ambient, prev.LargeEdge, prev.SmallOutlier, prev.LargeOutlier, prev.EncoderRevision, prev.OriginalBytes}
		return ipArtWrite(ctx, t, ipAMReencode, args...)
	}
	args := ipArtSourceArgs(m, 1, false)
	row, e := ipArtRead(ctx, t, ipAMSourceEquivalent, args[:12]...)
	if e != nil {
		return e
	}
	var current int64
	e = row.Scan(&current)
	if e == nil {
		if current <= 0 {
			return ErrConflict
		}
		result.StorageRevision = current
		result.Count = 0
		return nil
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	if e := ipArtAbsent(ctx, t, ipAMSourceAbsent, m.SourceDigest); e != nil {
		return e
	}
	args = ipArtSourceArgs(m, 1, false)
	args = append(args[:12], strconv.FormatInt(now, 10))
	return ipArtWrite(ctx, t, ipAMSourceCreate, args...)
}

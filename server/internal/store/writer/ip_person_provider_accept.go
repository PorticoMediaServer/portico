package writer

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
)

var ipPersonFields = [5]string{"name", "biography", "birth_date", "death_date", "birth_place"}

type ipPersonLock struct {
	present, locked, valuePresent, automaticPresent bool
	value, automatic                                string
}
type ipPersonCurrent struct {
	values                    [5]string
	sortName, biographySource string
}

func ipPersonCurrentValues(ctx context.Context, t *commandTurn, r ipPersonRequest, b *ipPersonDecode) (out ipPersonCurrent, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	key := []any{r.owner.personID, r.owner.token, r.owner.personRevision}
	var size int64
	if err = ipPersonScan(ctx, t, ipPPPersonOctets, key, &size); err != nil {
		return
	}
	if err = b.reserve(size); err != nil {
		return
	}
	err = ipPersonScan(ctx, t, ipPPPersonValues, key, &out.values[0], &out.sortName, &out.values[1], &out.values[2], &out.values[3], &out.values[4], &out.biographySource)
	if err != nil {
		return
	}
	actual := len(out.sortName) + len(out.biographySource)
	for _, v := range out.values {
		actual += len(v)
	}
	if int64(actual) != size {
		return out, ErrResolveRequired
	}
	return
}
func ipPersonLocks(ctx context.Context, t *commandTurn, person int64, b *ipPersonDecode) (out [5]ipPersonLock, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	step, err := ipPersonStep(t, ipPPFields)
	if err != nil {
		return out, err
	}
	rows, err := t.query(ctx, step, person)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var key, locked, vp, ap int64
		if err = rows.Scan(&key, &locked, &vp, &ap); err != nil {
			break
		}
		if key < 0 || key >= 5 || out[key].present || (locked != 0 && locked != 1) || (vp != 0 && vp != 1) || (ap != 0 && ap != 1) || (locked == 1 && vp != 1) {
			err = ErrResolveRequired
			break
		}
		out[key] = ipPersonLock{present: true, locked: locked == 1, valuePresent: vp == 1, automaticPresent: ap == 1}
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		return out, err
	}
	for i, l := range out {
		if !l.present {
			continue
		}
		args := []any{person, ipPersonFields[i]}
		var n int64
		if err = ipPersonScan(ctx, t, ipPPFieldOctets, args, &n); err != nil {
			return
		}
		if err = b.reserve(n); err != nil {
			return
		}
		if err = ipPersonScan(ctx, t, ipPPFieldValues, args, &l.value, &l.automatic); err != nil {
			return
		}
		if n != int64(len(l.value)+len(l.automatic)) || !ipPersonText(l.value, 16384, false) || !ipPersonText(l.automatic, 16384, false) || (!l.valuePresent && l.value != "") || (!l.automaticPresent && l.automatic != "") {
			return out, ErrResolveRequired
		}
		out[i] = l
	}
	return
}

// Projection helper only. W first proves receipt/current fences. Publication
// holds the exact person receipt, never a transient build or EN movie seal.
// Replay returns no change intent. This composed intent needs the reviewed W/PB
// durable seam in the SAME accepting transaction; this is not a new publisher.
func ipPersonAccept(ctx context.Context, t *commandTurn, r ipPersonRequest, b *ipPersonDecode) (out ipPersonResult, intent ipPersonSearchIntent, err error) {
	defer func() {
		v, e := finishCommand(ctx, commandLifetime(t), struct {
			r ipPersonResult
			i ipPersonSearchIntent
		}{out, intent}, err)
		out, intent, err = v.r, v.i, e
	}()
	s, err := ipPersonReadBuild(ctx, t, r)
	if err != nil {
		return out, intent, err
	}
	if s.state != 2 || s.rows != r.evidence.rows() || s.bytes != r.evidence.bytes {
		return out, intent, ErrConflict
	}
	var one int64
	if err = ipPersonScan(ctx, t, ipPPProof, []any{r.evidence.buildID, r.owner.id, r.identity.Incarnation[:], r.sealSequence, ipPersonManifest, r.evidence.commitment[:], s.rows, s.bytes}, &one); err != nil {
		return out, intent, errors.Join(ErrConflict, err)
	}
	if err = ipPersonValidateSealReceipt(ctx, t, r, b); err != nil {
		return out, intent, err
	}
	native, err := ipPersonReadEvidence(ctx, t, r, 1, b)
	if err != nil {
		return out, intent, err
	}
	d, size, err := ipPersonManifestDigest(r.owner.namespace, native)
	if err != nil {
		return out, intent, err
	}
	if native.rows() != s.rows || size != s.bytes || d != r.evidence.commitment {
		return out, intent, ErrConflict
	}
	current, err := ipPersonCurrentValues(ctx, t, r, b)
	if err != nil {
		return out, intent, err
	}
	locks, err := ipPersonLocks(ctx, t, r.owner.personID, b)
	if err != nil {
		return out, intent, err
	}
	o, n, h := r.owner, r.owner.namespace, native.header
	var mode int64
	if err = ipPersonScan(ctx, t, ipPPPolicyMode, []any{o.libraryID, n.provider, o.policyIncarnation[:], o.policyRevision}, &mode); err != nil {
		return out, intent, err
	}
	if mode != 1 && mode != 2 {
		return out, intent, ErrConflict
	}
	automatic := [5]string{h.name, h.biography, h.birthDate, h.deathDate, h.birthPlace}
	effective := current.values
	var changedOrigin [5]bool
	for i, l := range locks {
		if l.locked {
			effective[i] = l.value
		} else if mode == 2 || current.values[i] == "" {
			effective[i] = automatic[i]
			changedOrigin[i] = true
		}
	}
	if err = ipPersonOne(ctx, t, ipPPPublication, r.publicationID, o.personID, n.provider, n.providerID, r.evidence.id, o.id, o.id, r.identity.Incarnation[:], r.sealSequence, o.personRevision+1, h.observedAt); err != nil {
		return out, intent, err
	}
	source := o.source
	if source.revision == 0 {
		err = ipPersonOne(ctx, t, ipPPSourceInsert, o.personID, n.provider, source.role, n.providerID, source.incarnation[:], r.publicationID, r.evidence.id)
	} else {
		err = ipPersonOne(ctx, t, ipPPSourceUpdate, r.publicationID, r.evidence.id, o.personID, n.provider, source.role, n.providerID, source.incarnation[:], source.revision, ipPersonNullText(source.publication), ipPersonNullText(source.evidence))
	}
	if err != nil {
		return out, intent, err
	}
	for i, field := range ipPersonFields {
		if err = ipPersonOne(ctx, t, ipPPFieldUpdate, o.personID, field, effective[i], automatic[i], n.provider, h.observedAt); err != nil {
			return out, intent, err
		}
		if changedOrigin[i] {
			if err = ipPersonOne(ctx, t, ipPPOrigin, o.personID, field, n.provider, n.providerID, r.publicationID, r.evidence.id, h.sourceURL, h.observedAt, n.language, n.region, source.revision+1); err != nil {
				return out, intent, err
			}
		}
	}
	sortName := current.sortName
	if effective[0] != current.values[0] {
		sortName = effective[0]
	}
	biographySource := current.biographySource
	if changedOrigin[1] {
		biographySource = n.provider
	}
	if err = ipPersonOne(ctx, t, ipPPPersonUpdate, effective[0], sortName, effective[1], effective[2], effective[3], effective[4], biographySource, o.personID, o.token, o.personRevision); err != nil {
		return out, intent, err
	}
	out = ipPersonEvidenceResult(r, s.rows)
	out.revision = o.personRevision + 1
	out.publicationID = r.publicationID
	intent = ipPersonSearchIntent{o.personID, o.personRevision + 1, o.token, r.publicationID}
	return out, intent, nil
}

// Validate the FULL original person seal result using its native saved request
// digest and operation identity. A build FK/manifest match alone is not enough.
func ipPersonValidateSealReceipt(ctx context.Context, t *commandTurn, r ipPersonRequest, b *ipPersonDecode) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(t), struct{}{}, err) }()
	key := []any{r.owner.id, r.identity.Incarnation[:], r.sealSequence}
	var n int64
	if err = ipPersonScan(ctx, t, ipPPReceiptOctets, key, &n); err != nil {
		return err
	}
	if err = b.reserve(n); err != nil {
		return err
	}
	var op, request, digest, commit []byte
	var kind int64
	var ei, bi, pi, alg sql.NullString
	var ver, rows, size sql.NullInt64
	v := ipPersonResult{}
	if err = ipPersonScan(ctx, t, ipPPReceipt, key, &op, &request, &digest, &kind, &v.revision, &v.after, &v.count, &v.appliedMS, &ei, &bi, &pi, &alg, &ver, &commit, &rows, &size); err != nil {
		return err
	}
	if kind != int64(IPEvidenceSeal) || len(op) != 16 || len(request) != 32 || len(digest) != 32 || len(commit) != 32 || !ei.Valid || !bi.Valid || pi.Valid || !alg.Valid || !ver.Valid || !rows.Valid || !size.Valid || n != int64(len(ei.String)+len(bi.String)+len(alg.String)) || ei.String != r.evidence.id || bi.String != r.evidence.buildID || alg.String != ipPersonManifest || ver.Int64 != 1 || rows.Int64 != r.evidence.rows() || size.Int64 != r.evidence.bytes || !bytes.Equal(commit, r.evidence.commitment[:]) {
		return ErrResolveRequired
	}
	v.evidenceID, v.buildID, v.sealAlgorithm = ei.String, bi.String, alg.String
	v.sealVersion, v.sealRows, v.sealBytes = ver.Int64, rows.Int64, size.Int64
	copy(v.sealCommitment[:], commit)
	identity := CommandIdentity{Incarnation: r.identity.Incarnation, Sequence: r.sealSequence}
	copy(identity.OperationID[:], op)
	var q RequestDigest
	copy(q[:], request)
	d, err := ipPersonResultDigest(IPEvidenceSeal, identity, q, v)
	if err != nil || !bytes.Equal(d[:], digest) {
		return ErrResolveRequired
	}
	return nil
}

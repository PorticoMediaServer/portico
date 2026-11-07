package writer

import (
	"context"
	"errors"
	"math"
)

type ipPersonBuild struct{ revision, state, cursor, rows, bytes, charge int64 }

func ipPersonReadBuild(ctx context.Context, t *commandTurn, r ipPersonRequest) (s ipPersonBuild, err error) {
	defer func() { s, err = finishCommand(ctx, commandLifetime(t), s, err) }()
	o, n, v := r.owner, r.owner.namespace, r.evidence
	err = ipPersonScan(ctx, t, ipPPBuild, []any{v.buildID, v.id, o.id, r.identity.Incarnation[:], o.generation, o.generation, n.provider, n.providerID, v.rows(), v.bytes, ipPersonManifest, v.commitment[:]}, &s.revision, &s.state, &s.cursor, &s.rows, &s.bytes, &s.charge)
	if err != nil {
		return s, errors.Join(ErrConflict, err)
	}
	if s.revision != r.buildRevision || s.revision == math.MaxInt64 || s.rows != s.cursor || s.rows < 1 || s.rows > v.rows() || s.bytes <= 0 || s.bytes > v.bytes || s.charge != s.bytes {
		return s, ErrConflict
	}
	return
}
func ipPersonPrefix(v ipPersonEvidence, rows int64) ipPersonEvidence {
	n := int(rows - 1)
	if n < 0 {
		n = 0
	}
	a := n
	if a > len(v.aliases) {
		a = len(v.aliases)
	}
	x := n - a
	if x > len(v.externalIDs) {
		x = len(v.externalIDs)
	}
	v.aliases = v.aliases[:a]
	v.externalIDs = v.externalIDs[:x]
	return v
}
func ipPersonEvidenceResult(r ipPersonRequest, rows int64) ipPersonResult {
	return ipPersonResult{revision: r.owner.revision + 1, after: rows, count: rows, appliedMS: r.nowMS, evidenceID: r.evidence.id, buildID: r.evidence.buildID}
}
func ipPersonBegin(ctx context.Context, t *commandTurn, r ipPersonRequest) (out ipPersonResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if r.buildRevision != 0 || r.owner.phase != "claimed" {
		return out, ErrConflict
	}
	o, n, v := r.owner, r.owner.namespace, r.evidence
	h := v.header
	if err = ipPersonOne(ctx, t, ipPPBuildBorn, o.id, r.identity.Incarnation[:], o.revision, o.generation); err != nil {
		return out, err
	}
	_, size, e := ipPersonManifestDigest(n, ipPersonPrefix(v, 1))
	if e != nil {
		return out, e
	}
	if err = ipPersonOne(ctx, t, ipPPEvidenceInsert, v.id, n.subjectID, ipPersonParser, n.provider, n.providerID, n.language, n.region, n.shape, ipPersonHex(v.commitment), h.observedAt, h.sourceURL, h.name, h.biography, h.birthDate, h.deathDate, h.birthPlace); err != nil {
		return out, err
	}
	if err = ipPersonOne(ctx, t, ipPPBuildInsert, v.buildID, v.id, n.provider, n.providerID, o.id, r.identity.Incarnation[:], o.generation, o.generation, v.rows(), v.bytes, ipPersonManifest, v.commitment[:], size, size); err != nil {
		return out, err
	}
	return ipPersonEvidenceResult(r, 1), nil
}
func ipPersonAppend(ctx context.Context, t *commandTurn, r ipPersonRequest, b *ipPersonDecode) (out ipPersonResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	s, err := ipPersonReadBuild(ctx, t, r)
	if err != nil {
		return out, err
	}
	if s.state != 1 || s.cursor != r.after || r.through-r.after <= 0 || r.through-r.after > ipPersonChunk {
		return out, ErrConflict
	}
	native, err := ipPersonReadEvidence(ctx, t, r, 0, b)
	if err != nil {
		return out, err
	}
	got, size, err := ipPersonManifestDigest(r.owner.namespace, native)
	if err != nil {
		return out, err
	}
	want, wsize, err := ipPersonManifestDigest(r.owner.namespace, ipPersonPrefix(r.evidence, r.after))
	if err != nil {
		return out, err
	}
	if native.rows() != s.rows || size != s.bytes || wsize != size || got != want {
		return out, ErrConflict
	}
	v := r.evidence
	for row := r.after; row < r.through; row++ {
		i := int(row - 1)
		if i < len(v.aliases) {
			a := v.aliases[i]
			err = ipPersonOne(ctx, t, ipPPAliasInsert, v.id, int64(i), a.requestedID, a.canonicalID, a.alias, a.observedAt)
		} else {
			x := v.externalIDs[i-len(v.aliases)]
			err = ipPersonOne(ctx, t, ipPPExternalInsert, v.id, x.provider, x.id)
		}
		if err != nil {
			return out, err
		}
	}
	_, nextBytes, err := ipPersonManifestDigest(r.owner.namespace, ipPersonPrefix(v, r.through))
	if err != nil {
		return out, err
	}
	if nextBytes < s.bytes || nextBytes > v.bytes {
		return out, ErrConflict
	}
	if err = ipPersonOne(ctx, t, ipPPBuildAdvance, r.through, r.through, nextBytes, nextBytes, v.buildID, s.revision, s.cursor, s.rows, s.bytes); err != nil {
		return out, err
	}
	return ipPersonEvidenceResult(r, r.through), nil
}
func ipPersonSeal(ctx context.Context, t *commandTurn, r ipPersonRequest, b *ipPersonDecode) (out ipPersonResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	s, err := ipPersonReadBuild(ctx, t, r)
	if err != nil {
		return out, err
	}
	if s.state != 1 || s.rows != r.evidence.rows() || s.bytes != r.evidence.bytes {
		return out, ErrConflict
	}
	native, err := ipPersonReadEvidence(ctx, t, r, 0, b)
	if err != nil {
		return out, err
	}
	d, size, err := ipPersonManifestDigest(r.owner.namespace, native)
	if err != nil {
		return out, err
	}
	if native.rows() != s.rows || size != s.bytes || d != r.evidence.commitment {
		return out, ErrConflict
	}
	if err = ipPersonOne(ctx, t, ipPPEvidenceSeal, r.evidence.id); err != nil {
		return out, err
	}
	out = ipPersonEvidenceResult(r, s.rows)
	out.sealAlgorithm = ipPersonManifest
	out.sealVersion = 1
	out.sealCommitment = d
	out.sealRows = s.rows
	out.sealBytes = size
	return out, nil
}

// Independent bounded verification. Native header and every selected family
// are decoded after actual octet preflight in this SAME admitted snapshot.
// Protected Rows reach Err and Close; limit=max+1 proves no hidden tail. Alias
// raw ordinals must be contiguous; external IDs use the actual native tuple
// ordering. Non-admitted families must be absent. No counters or source digest
// are substituted for inspecting stored content.
func ipPersonReadEvidence(ctx context.Context, t *commandTurn, r ipPersonRequest, sealed int64, b *ipPersonDecode) (v ipPersonEvidence, err error) {
	defer func() { v, err = finishCommand(ctx, commandLifetime(t), v, err) }()
	n := r.owner.namespace
	id := r.evidence.id
	var size int64
	if err = ipPersonScan(ctx, t, ipPPHeaderOctets, []any{id}, &size); err != nil {
		return
	}
	if err = b.reserve(size); err != nil {
		return
	}
	h := ipPersonHeader{}
	err = ipPersonScan(ctx, t, ipPPHeader, []any{id, n.subjectID, n.provider, n.providerID, n.language, n.region, n.shape, ipPersonParser, ipPersonHex(r.evidence.commitment), sealed}, &h.name, &h.biography, &h.birthDate, &h.deathDate, &h.birthPlace, &h.observedAt, &h.sourceURL)
	if err != nil {
		return
	}
	if size != int64(len(h.name)+len(h.biography)+len(h.birthDate)+len(h.deathDate)+len(h.birthPlace)+len(h.observedAt)+len(h.sourceURL)) || !ipPersonValidHeader(h) {
		return v, ErrResolveRequired
	}
	v.header = h
	var one int64
	if err = ipPersonScan(ctx, t, ipPPOtherChildren, []any{id, id, id, id, id, id, id, id}, &one); err != nil {
		return v, errors.Join(ErrConflict, err)
	}
	keys, e := ipPersonAliasKeys(ctx, t, id, -1, ipPersonMaxChildren+1)
	if e != nil {
		return v, e
	}
	if len(keys) > ipPersonMaxChildren {
		return v, ErrQuantum
	}
	for i, key := range keys {
		if key != int64(i) {
			return v, ErrConflict
		}
		if err = ipPersonScan(ctx, t, ipPPAliasOctets, []any{id, key}, &size); err != nil {
			return
		}
		if err = b.reserve(size); err != nil {
			return
		}
		a := ipPersonAlias{}
		if err = ipPersonScan(ctx, t, ipPPAlias, []any{id, key}, &a.requestedID, &a.canonicalID, &a.alias, &a.observedAt); err != nil {
			return
		}
		if size != int64(len(a.requestedID)+len(a.canonicalID)+len(a.alias)+len(a.observedAt)) || a.canonicalID != n.providerID || !ipPersonText(a.requestedID, 256, true) || !ipPersonText(a.alias, 4096, false) || !ipPersonText(a.observedAt, 128, true) {
			return v, ErrResolveRequired
		}
		v.aliases = append(v.aliases, a)
	}
	v.externalIDs, err = ipPersonReadExternals(ctx, t, id, b)
	if err != nil {
		return
	}
	if len(v.aliases)+len(v.externalIDs) > ipPersonMaxChildren {
		return v, ErrQuantum
	}
	return
}
func ipPersonAliasKeys(ctx context.Context, t *commandTurn, id string, after, limit int64) (out []int64, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	if after < -1 || limit <= 0 || limit > ipPersonMaxChildren+1 {
		return nil, ErrInvalid
	}
	step, err := ipPersonStep(t, ipPPAliasKeys)
	if err != nil {
		return nil, err
	}
	rows, err := t.query(ctx, step, id, after, limit)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		var key int64
		if err = rows.Scan(&key); err != nil {
			return nil, err
		}
		if key <= after || int64(len(out)) >= limit {
			return nil, ErrConflict
		}
		out = append(out, key)
		after = key
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return
}
func ipPersonExternalSizes(ctx context.Context, t *commandTurn, id string, b *ipPersonDecode) (sizes []int64, err error) {
	defer func() { sizes, err = finishCommand(ctx, commandLifetime(t), sizes, err) }()
	s, err := ipPersonStep(t, ipPPExternalOctets)
	if err != nil {
		return nil, err
	}
	rows, err := t.query(ctx, s, id, int64(ipPersonMaxChildren+1))
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		var n int64
		if err = rows.Scan(&n); err != nil {
			return nil, err
		}
		if len(sizes) >= ipPersonMaxChildren {
			return nil, ErrQuantum
		}
		if err = b.reserve(n); err != nil {
			return nil, err
		}
		sizes = append(sizes, n)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return
}
func ipPersonReadExternals(ctx context.Context, t *commandTurn, id string, b *ipPersonDecode) (out []ipPersonExternalID, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(t), out, err) }()
	sizes, err := ipPersonExternalSizes(ctx, t, id, b)
	if err != nil {
		return nil, err
	}
	s, err := ipPersonStep(t, ipPPExternals)
	if err != nil {
		return nil, err
	}
	rows, err := t.query(ctx, s, id, int64(ipPersonMaxChildren+1))
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		if len(out) >= len(sizes) {
			return nil, ErrConflict
		}
		var x ipPersonExternalID
		if err = rows.Scan(&x.provider, &x.kind, &x.id); err != nil {
			return nil, err
		}
		if int64(len(x.provider)+len(x.kind)+len(x.id)) != sizes[len(out)] || !ipPersonText(x.provider, 128, true) || x.kind != "person" || !ipPersonText(x.id, 256, true) || (len(out) > 0 && !ipPersonExternalLess(out[len(out)-1], x)) {
			return nil, ErrResolveRequired
		}
		out = append(out, x)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(out) != len(sizes) {
		return nil, ErrConflict
	}
	return
}

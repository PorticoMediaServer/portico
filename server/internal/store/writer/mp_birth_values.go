package writer

// Historical registration/lifetime data, never a current disclosure capability.
// W derives these facts from its opaque current admission and original public
// registration before using one of the five concrete retained birth relations.
type mpPublicBirthScope struct {
	authority, account, profile, member, profileLifetime, installation, root, family, device string
	registration, issuedMS                                                                   int64
	keyDigest                                                                                [32]byte
}
type mpPublicBirthHeader struct {
	birthID   [16]byte
	identity  CommandIdentity
	request   RequestDigest
	scope     mpPublicBirthScope
	appliedMS int64
	digest    ResultDigest
}

func (s mpPublicBirthScope) valid() bool {
	return (s.authority == "local" || s.authority == "hosted") && mpPrepTexts(s.account, s.profile, s.family, s.device) && lowerHex(s.member, 32) && lowerHex(s.profileLifetime, 32) && lowerHex(s.installation, 32) && len(s.root) == 22 && mpPrepTexts(s.root) && s.registration > 0 && s.issuedMS > 0 && s.keyDigest != ([32]byte{})
}
func (h mpPublicBirthHeader) valid() bool {
	return h.birthID != ([16]byte{}) && h.identity.Incarnation != ([16]byte{}) && h.identity.OperationID != ([16]byte{}) && h.identity.Sequence == 1 && h.request != (RequestDigest{}) && h.scope.valid() && h.appliedMS >= h.scope.issuedMS
}
func mpEncodePublicBirth(e *requestEncoder, h mpPublicBirthHeader) {
	e.bytes(h.birthID[:])
	s := h.scope
	for _, v := range []string{s.authority, s.account, s.profile, s.member, s.profileLifetime, s.installation, s.root, s.family, s.device} {
		e.text(v)
	}
	e.number(uint64(s.registration))
	e.number(uint64(s.issuedMS))
	e.bytes(s.keyDigest[:])
}
func mpPublicBirthArgs(h mpPublicBirthHeader, kind CommandID) []any {
	s := h.scope
	return []any{h.birthID[:], h.identity.Incarnation[:], int64(kind), h.identity.OperationID[:], h.request[:], s.authority, s.account, s.profile, s.member, s.profileLifetime, s.installation, s.root, s.registration, s.family, s.device, s.keyDigest[:], s.issuedMS, h.appliedMS, h.digest[:]}
}

// Native prefix decoder shared by concrete codecs. It has no SQL/authority or
// result graph; each caller selects its own fixed relation and typed result.
type mpPublicBirthScan struct {
	header                                              mpPublicBirthHeader
	birth, incarnation, operation, request, key, digest []byte
	kind                                                int64
}

func (v *mpPublicBirthScan) destinations() []any {
	h, s := &v.header, &v.header.scope
	return []any{&v.birth, &v.incarnation, &v.kind, &v.operation, &v.request, &s.authority, &s.account, &s.profile, &s.member, &s.profileLifetime, &s.installation, &s.root, &s.registration, &s.family, &s.device, &v.key, &s.issuedMS, &h.appliedMS, &v.digest}
}
func (v *mpPublicBirthScan) finish(expected mpPublicBirthHeader, kind CommandID) (mpPublicBirthHeader, error) {
	h := v.header
	if len(v.birth) != 16 || len(v.incarnation) != 16 || len(v.operation) != 16 || len(v.request) != 32 || len(v.key) != 32 || len(v.digest) != 32 || v.kind != int64(kind) || expected.identity.Sequence != 1 {
		return mpPublicBirthHeader{}, ErrResolveRequired
	}
	copy(h.birthID[:], v.birth)
	copy(h.identity.Incarnation[:], v.incarnation)
	copy(h.identity.OperationID[:], v.operation)
	h.identity.Sequence = 1
	copy(h.request[:], v.request)
	copy(h.scope.keyDigest[:], v.key)
	copy(h.digest[:], v.digest)
	if !h.valid() || h.birthID != expected.birthID || h.identity != expected.identity || h.request != expected.request || h.digest != expected.digest {
		return mpPublicBirthHeader{}, ErrResolveRequired
	}
	return h, nil
}

package writer

// Shared result wire v1: common result prefix, uint16 version1, LP purpose,
// original identity16/sequence64/operation16/request32. Policy adds actual
// policy incarnation16, revision64/appliedMS64, enabled/priority/creditEnabled64,
// LP region/algorithm/refreshMode, then nullable credit cursor(number64+LPtext).
func preflightIPPolicyResult(v ipPolicyResult) error {
	if v.retired || v.incarnation == [16]byte{} || v.revision <= 0 || v.appliedMS < 0 ||
		(v.values.enabled != 0 && v.values.enabled != 1) || (v.values.creditEnabled != 0 && v.values.creditEnabled != 1) ||
		v.creditAfterItem.Valid != v.creditAfterProvider.Valid || !v.creditAfterItem.Valid && (v.creditAfterItem.Int64 != 0 || v.creditAfterProvider.String != "") {
		return ErrInvalid
	}
	if v.values.refreshMode != "fill_missing" && v.values.refreshMode != "replace_unlocked" {
		return ErrInvalid
	}
	_, err := CheckSealControlStrings([]string{v.values.region, v.values.algorithm, v.values.refreshMode, v.creditAfterProvider.String})
	return err
}

func writeIPResultIdentity(e *requestEncoder, purpose string, i CommandIdentity, q RequestDigest) {
	e.bytes([]byte(canonicalResultCodec))
	e.bytes([]byte{0, 1})
	e.text(purpose)
	e.bytes(i.Incarnation[:])
	e.number(uint64(i.Sequence))
	e.bytes(i.OperationID[:])
	e.bytes(q[:])
}

func writeIPPolicyResult(e *requestEncoder, i CommandIdentity, q RequestDigest, v ipPolicyResult) error {
	writeIPResultIdentity(e, "ip_policy_set", i, q)
	e.bytes(v.incarnation[:])
	e.number(uint64(v.revision))
	e.number(uint64(v.appliedMS))
	for _, n := range []int64{v.values.enabled, v.values.priority, v.values.creditEnabled} {
		e.number(uint64(n))
	}
	for _, s := range []string{v.values.region, v.values.algorithm, v.values.refreshMode} {
		e.text(s)
	}
	e.flag(v.creditAfterItem.Valid)
	if v.creditAfterItem.Valid {
		e.number(uint64(v.creditAfterItem.Int64))
		e.text(v.creditAfterProvider.String)
	}
	return e.err
}

type ipRefreshSavedResult struct {
	Kind  CommandID
	Value ipRefreshResult
}

// Refresh wire continues the same identity prefix with purpose ip_refresh and
// actual uint16 kind, then revision/generation/epoch64, LPphase, afterID/
// scheduled/waiting/appliedMS64. The whole original request binds page vs finish.
func preflightIPRefreshResult(v ipRefreshSavedResult) error {
	r := v.Value
	if r.retired || v.Kind != IPRefreshRequest && v.Kind != IPRefreshPage || r.revision <= 0 || r.generation <= 0 || r.epochMS < 0 ||
		r.afterID < 0 || r.scheduled < 0 || r.scheduled > ipRefreshRawLimit || (r.waiting != 0 && r.waiting != 1) || r.appliedMS < 0 ||
		(r.phase != "entities" && r.phase != "people" && r.phase != "complete") {
		return ErrInvalid
	}
	return nil
}

func writeIPRefreshResult(e *requestEncoder, i CommandIdentity, q RequestDigest, v ipRefreshSavedResult) error {
	writeIPResultIdentity(e, "ip_refresh", i, q)
	e.bytes([]byte{byte(v.Kind >> 8), byte(v.Kind)})
	r := v.Value
	for _, n := range []int64{r.revision, r.generation, r.epochMS} {
		e.number(uint64(n))
	}
	e.text(r.phase)
	for _, n := range []int64{r.afterID, r.scheduled, r.waiting, r.appliedMS} {
		e.number(uint64(n))
	}
	return e.err
}

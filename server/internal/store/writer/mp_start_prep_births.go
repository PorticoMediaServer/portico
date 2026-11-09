package writer

// MP-07 owns only the MP half of start preparation command 700. UO-23 (DESK-B)
// owns the user birth that mints the root and the CURRENT-root proof, and
// UO-01's root binding is under a blocking review. Therefore MP:
//
//   - never reads uo_root_binding.go or any UO root table,
//   - never derives, re-validates or caches a second root graph,
//   - never accepts a caller boolean, token or saved root reference as
//     current authority,
//   - never mints an Applied/receipt driver for the UO birth.
//
// mpStartPrepUOBirth is the ONE private seam. UO-23 constructs it in the SAME
// turn that admits the birth and hands MP the freshly minted root tuple plus the
// MP owner incarnation. MP records the tuple as historical data (the F schema
// gives it no lifetime-blocking FK) and refuses a zero or malformed witness
// before any row is written. When UO-23 lands it replaces this witness with the
// admitted root proof; this file is then the only place that changes.

// mpStartJobUserBirth is the UO-owned historical tuple. It is data, never a
// public receipt owner, current authority or root proof.
type mpStartJobUserBirth struct {
	root, member, profileLifetime string
}

func (v mpStartJobUserBirth) valid() bool {
	return len(v.root) == 22 && mpPrepTexts(v.root) && lowerHex(v.member, 32) && lowerHex(v.profileLifetime, 32)
}

// mpStartPrepUOBirth is the named private UO birth + current-root seam. There is
// no public constructor; only UO-23's same-turn birth can supply it.
type mpStartPrepUOBirth struct {
	birth       mpStartJobUserBirth
	incarnation [16]byte
}

func (b mpStartPrepUOBirth) valid() bool {
	return b.birth.valid() && b.incarnation != ([16]byte{})
}

// mpStartPrepRefuseUOBirth is the single refusal point for a missing UO birth.
// The frame is unqualified until UO-23 exists; MP does not fall back to a saved
// root reference, a historic owner reference or a synthetic driver.
func mpStartPrepRefuseUOBirth() error { return &Refusal{Code: RefusalUnqualified} }

// requireMPStartPrepUOBirth admits only the witness handed over by UO's birth
// in this same turn. It deliberately does not prove that the root is still
// current; that proof belongs to UO and is not available to this frame.
func requireMPStartPrepUOBirth(b mpStartPrepUOBirth, owner [16]byte, nowMS int64) error {
	if nowMS <= 0 || !b.valid() || b.incarnation != owner || b.incarnation == ([16]byte{}) {
		return mpStartPrepRefuseUOBirth()
	}
	return nil
}

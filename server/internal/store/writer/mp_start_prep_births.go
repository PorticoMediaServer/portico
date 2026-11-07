package writer

// Historical lifetime data supplied by UO/W after public birth admission.
// This tuple is data, never a public receipt owner or current authority.
type mpStartJobUserBirth struct{ root, member, profileLifetime string }

func (v mpStartJobUserBirth) valid() bool {
	return len(v.root) == 22 && mpPrepTexts(v.root) && lowerHex(v.member, 32) && lowerHex(v.profileLifetime, 32)
}

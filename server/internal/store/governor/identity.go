package governor

// Owns reports exact composition identity, not current grant authority, epoch,
// health or capacity. Wrappers, foreign issuers and nil/zero values are false.
func (a *Authority) Owns(g Governor) bool {
	owner, ok := g.(*ledger)
	return a != nil && a.ledger != nil && ok && owner != nil && owner == a.ledger
}

// OwnedBy reports the exact Authority pointer used to construct this pool.
// Identity grants no use/retirement capability and proves no resident lifetime.
func (s *Snapshots) OwnedBy(a *Authority) bool {
	return s != nil && s.authority != nil && a != nil && s.authority == a
}

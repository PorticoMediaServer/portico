package enrich

// tmdbCreditBuffer exclusively owns the temporary decoded credit array. Its
// backing must never be borrowed from an acquisition/cache body or a caller's
// ScreenCredit input. W copies only its chosen rows into the returned value.
// Clearing references ends this temporary lifetime; it is NOT physical memory
// reclamation, GV retirement or permission to refund any admitted holding.
type tmdbCreditBuffer struct{ rows []ScreenCredit }

func (b *tmdbCreditBuffer) prepare(subject ProviderRef, body []byte, layout tmdbMovieCounts) (PreparedScreenCredits, error) {
	if b == nil {
		return PreparedScreenCredits{}, ErrTMDBMovie
	}
	// Registered before decode: partial decode and selection refusals also drop
	// every candidate reference, including rows outside the selected cohort.
	defer func() {
		clear(b.rows[:cap(b.rows)])
		b.rows = nil
	}()
	if b.rows == nil || layout.cast < 0 || layout.crew < 0 || len(b.rows) != layout.cast+layout.crew {
		return PreparedScreenCredits{}, ErrTMDBMovie
	}
	if _, actual, err := readTMDBMovie(body, b.rows, layout); err != nil || actual != layout {
		return PreparedScreenCredits{}, ErrTMDBMovie
	}
	return PrepareScreenCredits(subject, b.rows)
}

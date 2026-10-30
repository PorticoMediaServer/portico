// Package dbwork owns Portico's database concurrency discipline: the validated
// SQLite resource policy, the single application-level write gate that
// serialises writers by semantic work class, the read and write helpers every
// package must use, typed BUSY/LOCKED retry, and the background-pressure signal
// bulk loops yield to.
//
// The shape is deliberate. SQLite serialises writers inside the file, so a
// large connection pool multiplies lock competition and per-connection page
// cache without creating write capacity. Portico therefore keeps a small
// validated pool for WAL readers and puts write ordering in Go, above SQLite,
// where a security fence or a playback control mutation can be selected ahead
// of a queued library scan batch.
package dbwork

// Class is the canonical priority ladder shared by the write gate, HTTP lane
// admission and background job ordering. One enum is the currency of the whole
// system so a route, a transaction and a job row all agree on what matters.
type Class int

const (
	// ClassSecurityFence covers revocation, session invalidation, restriction
	// publication and anything else whose delay leaves a principal authorised
	// for longer than policy allows.
	ClassSecurityFence Class = iota + 1
	// ClassProtectedCapture covers recording capture and other work that loses
	// unrecoverable content if it is delayed.
	ClassProtectedCapture
	// ClassEstablishedPlayback covers control of a session already playing:
	// progress, pause, seek, stop, renewal.
	ClassEstablishedPlayback
	// ClassPlaybackStart covers session creation and startup analysis, where a
	// viewer is waiting on a spinner.
	ClassPlaybackStart
	// ClassInteractive covers ordinary request-path mutations.
	ClassInteractive
	// ClassForegroundTransfer covers downloads and uploads a person started and
	// is watching.
	ClassForegroundTransfer
	// ClassBackgroundMedia covers scanning, metadata refresh, analysis and other
	// bulk catalogue work.
	ClassBackgroundMedia
	// ClassMaintenance covers retention, pruning, backups and optimisation.
	ClassMaintenance
)

// classNames is indexed by Class; index zero is the invalid class.
var classNames = [...]string{"", "security-fence", "protected-capture", "established-playback", "playback-start", "interactive", "foreground-transfer", "background-media", "maintenance"}

// Classes returns the ladder in strict priority order, highest first.
func Classes() []Class {
	return []Class{ClassSecurityFence, ClassProtectedCapture, ClassEstablishedPlayback, ClassPlaybackStart, ClassInteractive, ClassForegroundTransfer, ClassBackgroundMedia, ClassMaintenance}
}

func (c Class) String() string {
	if c < 1 || int(c) >= len(classNames) {
		return "interactive"
	}
	return classNames[c]
}

// Priority is the numeric rank; lower wins. It is the Class value itself so the
// ladder cannot drift out of step with the constants.
func (c Class) Priority() int {
	if c < 1 || int(c) >= len(classNames) {
		return int(ClassInteractive)
	}
	return int(c)
}

// Valid reports whether c names a declared class.
func (c Class) Valid() bool { return c >= 1 && int(c) < len(classNames) }

// ParseClass resolves a persisted or configured class name. An unknown name
// resolves to ClassInteractive with ok false so callers can classify loudly in
// tests without failing a request in production.
func ParseClass(name string) (Class, bool) {
	for i, candidate := range classNames {
		if i > 0 && candidate == name {
			return Class(i), true
		}
	}
	return ClassInteractive, false
}

// Background reports whether the class is bulk work that must yield to
// foreground traffic at every batch boundary.
func (c Class) Background() bool {
	return c.Priority() >= ClassBackgroundMedia.Priority()
}

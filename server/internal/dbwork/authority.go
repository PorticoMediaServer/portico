package dbwork

import (
	"sync/atomic"
)

// authorityGeneration moves whenever a committed write could have changed who
// may see or play what. Long-lived byte streams hold a verdict keyed on it, and
// the principal, restriction and library-grant caches are fenced on it, so a
// revocation anywhere in the process invalidates every one of them at once
// without anything polling the database.
//
// Two signals move it, and correctness does not depend on a caller having
// picked the right class:
//
//  1. A declared one: a write on the security-fence class, which is what every
//     revocation, epoch bump, enrolment and profile change in `internal/identity`
//     takes.
//  2. A textual one: a statement that names a table authority lives in, wherever
//     it is issued from and on whatever class. `authorityTables` in instrument.go
//     is deliberately over-inclusive, because a false bump costs a cache miss
//     and a missed bump costs a revoked session. A revocation path that takes
//     its class from a background loop is caught by this one.
//
// It used to move on a third: *every* committed write, coalesced to four times
// a second. The intent was a backstop for the same paths signal 2 exists for,
// and the effect was that the caches could not hold at all while the server was
// writing — which is whenever anyone is watching anything or a scan is running.
// Measured on the release tier, an ordinary viewer session moved the generation
// about a hundred and seventy times a second, so every request re-resolved its
// viewer from the database: eight statements and a pooled connection for an
// answer that had not changed. The two signals above are the pair the read-path
// audit specifies for exactly this cache, and the backstop they were a backstop
// for is signal 2.
//
// Playback heartbeats moved it under the old rule and do not under this one:
// they are the most frequent writes in the server and cannot change who may
// play what.
var authorityGeneration atomic.Uint64

// AuthorityGeneration is the current value. Equal values mean no
// authority-bearing write has committed in between.
func AuthorityGeneration() uint64 { return authorityGeneration.Load() }

// BumpAuthority is for the rare authority change that does not go through a
// gated write (a key rotation on disk, an in-memory policy swap).
func BumpAuthority() { authorityGeneration.Add(1) }

func bearsAuthority(class Class) bool {
	return class == ClassSecurityFence
}

// noteCommitted is called when a gated transaction commits on a class that did
// not declare itself authority-bearing. The observed driver already invalidates
// authority at statement time and again on commit if that transaction wrote an
// authority table. Nothing else needs invalidating here: ordinary progress and
// heartbeat transactions must leave the authority caches intact.
func noteCommitted(Class) {}

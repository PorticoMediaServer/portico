package storage

import "errors"

// ErrStorageOffline reports that the storage holding an accepted playback
// source is offline right now. Identity, lifetime, root and authorization are
// still valid, so the same request can be retried unchanged once the storage
// reconnects. It is deliberately distinct from ErrPlaybackSource so a client
// can say what is wrong instead of "unavailable"; callers must return it only
// after every other binding check passed, and never in a way that reveals a
// source the caller may not reach.
var ErrStorageOffline = errors.New("storage is offline")

// SourceHealthRefusal classifies a source's current health after the caller has
// validated the binding's identity, lifetime, root and authorization. Only
// offline is a retryable storage state; root_changed and removing keep the
// generic unavailable answer. Health is read at the point of use, never written
// per item.
func SourceHealthRefusal(health string) error {
	switch health {
	case "offline":
		return ErrStorageOffline
	case "root_changed", "removing":
		return ErrPlaybackSource
	}
	return nil
}

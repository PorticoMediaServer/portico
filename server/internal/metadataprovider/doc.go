// Provider adapter qualification is intentionally local and transport-only.
//
// Package tests exercise deterministic httptest origins for admission pacing,
// cancellation, Retry-After, size limits, redirect policy, sanitized errors,
// TVDB login coalescing and 401 renewal, series/episode identity and ordering,
// and MusicBrainz recording/release/merge validation. They do not contact live
// providers, prove credentials, select catalog matches, or persist provenance.
//
// MusicBrainz constructors share one process-wide transport admission gate.
// The application owns one TVDB instance for token and rate admission.
// A durable job scheduler must interpret
// retryable failures and RetryAfter; adapters do not perform hidden general
// retries. TVDB alone retries one unauthorized read after token renewal.
// MusicBrainz permits at most three validated same-origin/entity merge hops,
// with a total lookup deadline. Catalog/job integration is a separate step.
package metadataprovider

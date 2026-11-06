package enrich

import (
	"errors"
	"time"
)

// TMDBMode selects an explicitly budgeted pipeline partition. Trusted server
// composition selects it; this is not an HTTP/job option or a memory grant.
// These adopted planning partitions do not prove physical RAM sufficiency.
type TMDBMode uint8

const (
	TMDBTwoWorkers128MiB TMDBMode = iota + 1
	TMDBOneWorker80MiB
)

var ErrTMDBPolicy = errors.New("TMDB background staging policy unavailable")

// TMDBLimits is an informational copy of the adopted policy, not mutable
// authority. Source/history/images/receipts/control/WAL need separate admission.
// UnacceptedChargeBytes is server-wide logical evidence Q, NOT resident RAM.
type TMDBLimits struct {
	ResponseBytes, StoredContentBytes, UnacceptedChargeBytes int64
	ContentRows, ChargedStages, Workers                      int
	MemoryBytes                                              int64
}

// TMDBPolicy has no unlimited/implicit-default mode. Missing policy refuses
// background work; it is not a reason to fail server startup/local service.
type TMDBPolicy struct{ mode TMDBMode }

func NewTMDBPolicy(mode TMDBMode) (TMDBPolicy, error) {
	p := TMDBPolicy{mode: mode}
	if _, err := p.Limits(); err != nil {
		return TMDBPolicy{}, err
	}
	return p, nil
}

func (p TMDBPolicy) Limits() (TMDBLimits, error) {
	limits := TMDBLimits{
		ResponseBytes: 4 << 20, StoredContentBytes: 6 << 20,
		UnacceptedChargeBytes: 196 << 20, ContentRows: 151, ChargedStages: 4,
	}
	switch p.mode {
	case TMDBTwoWorkers128MiB:
		limits.Workers, limits.MemoryBytes = 2, 128<<20
	case TMDBOneWorker80MiB:
		limits.Workers, limits.MemoryBytes = 1, 80<<20
	default:
		return TMDBLimits{}, ErrTMDBPolicy
	}
	return limits, nil
}

// NewTMDBTransport puts acquisition and reuse INSIDE the selected reservation.
// Per worker: 48MiB acquisition/decoding/preparation; then 16MiB cache and
// 16MiB bookkeeping/headroom shared. Body copies/key metadata count inside
// those lines, not as an additional old64+64MiB pool. Actual lifetime/residency
// and the whole server budget still require W/GV physical qualification.
// TTL and timeout are explicit scheduling inputs, not new freshness authority.
func NewTMDBTransport(policy TMDBPolicy, admission NetworkAdmission, fetch Fetch, cacheTTL, requestTimeout time.Duration) (*Transport, error) {
	cfg, err := tmdbTransportConfig(policy, cacheTTL, requestTimeout)
	if err != nil {
		return nil, err
	}
	return NewTransport(cfg, admission, fetch)
}

func tmdbTransportConfig(policy TMDBPolicy, cacheTTL, requestTimeout time.Duration) (TransportConfig, error) {
	limits, err := policy.Limits()
	if err != nil {
		return TransportConfig{}, err
	}
	return TransportConfig{
		Provider: "tmdb", RequestsPerSecond: 40,
		MaxInFlight: limits.Workers, MaxPending: limits.Workers, MaxWaiters: limits.Workers,
		MaxResponseBytes: limits.ResponseBytes, CacheBytes: 16 << 20, CacheEntries: 256,
		CacheTTL: cacheTTL, RequestTimeout: requestTimeout,
	}, nil
}

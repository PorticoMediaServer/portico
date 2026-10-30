package httpapi

// Browsing queues absorb brief page/request bursts without increasing active
// database or CPU work. A fixed 48 waiting places refused most of a simultaneous
// 200-viewer burst even on a capable host. Budget this lane against effective
// physical/cgroup memory instead; admission still bounds each verified device
// separately and leaves all active-work limits unchanged.
//
// The 256 KiB planning allowance covers the server's 32 KiB header bound, its
// 64 KiB HTTP/2 receive window and request/goroutine overhead with headroom.
// This is not a hard RSS guarantee or a reservation shared with connection,
// database and media budgets. Retain the previous 48-place floor on unknown or
// exceptionally small hosts, and cap additional browsing headroom at 128 MiB.
func browsingQueueCapacity(memoryBytes int64) int {
	const (
		minimum       = 48
		maximum       = 512
		perWaiter     = 256 << 10
		maximumBudget = 128 << 20
	)
	if memoryBytes <= 0 {
		return minimum
	}
	budget := memoryBytes / 16
	if budget > maximumBudget {
		budget = maximumBudget
	}
	return max(minimum, min(maximum, int(budget/perWaiter)))
}

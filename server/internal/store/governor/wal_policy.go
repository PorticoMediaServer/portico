package governor

import "time"

// Phase1aWALPolicy is the ONE adopted phase 1a WAL policy. Formal qualification
// runs and the product composition use exactly this value. Screening fixtures
// may keep smaller values to stress a path, but must label them as such.
//
//   - Hard 512 MiB bounds physical WAL, and so crash-recovery replay (about 131k
//     frames) and disk footprint on small devices.
//   - Active pressure 256 MiB keeps the unflushed backlog under the dirty-page
//     throttle of a 2 GB box and is where same-ledger pressure recovery runs
//     before the next original (active WAL plus its closed append forecast).
//   - Soft debt 64 MiB schedules checkpoint work well above the few-MiB debt a
//     50 ms concurrent PASSIVE cadence leaves.
//   - Reserve 64 MiB is held back from Background and ScanMinimum work only, so
//     Foreground and security keep room for a largest (32 MiB) command forecast.
//
// ConcurrentPassive is on: the keeper runs ordinary concurrent PASSIVE, and
// CheckpointMaxHold is its separate maintenance window, never a writer window.
func Phase1aWALPolicy() WALPolicy {
	return WALPolicy{
		HardBytes:           512 << 20,
		ReserveBytes:        64 << 20,
		SoftDebtBytes:       64 << 20,
		ActivePressureBytes: 256 << 20,
		MaxSampleAge:        time.Second,
		ConcurrentPassive:   true,
		CheckpointMaxHold:   5 * time.Second,
	}
}

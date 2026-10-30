//go:build !linux && !darwin && !windows

package storage

func newInventoryWatcher(WatchRequest) (inventoryWatcher, error) { return nil, ErrWatchUnavailable }

//go:build race

package dbwork

// snapshotScale: see snapshot_scale.go. The race detector slows the
// transpiled SQLite by roughly an order of magnitude or more.
const snapshotScale = 20

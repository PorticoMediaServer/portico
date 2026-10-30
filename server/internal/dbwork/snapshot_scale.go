//go:build !race

package dbwork

// snapshotScale stretches snapshot lifetimes in race-detector builds, where
// SQLite itself runs instrumented and an ordinary page takes many times longer.
// Production builds use the design lifetimes exactly.
const snapshotScale = 1

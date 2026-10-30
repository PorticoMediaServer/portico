//go:build race

package catalog

// raceDetector is true in a -race build. The detector multiplies every database
// call by roughly an order of magnitude, so a test whose assertion is a latency
// budget is measuring the detector rather than the server. Those tests say so
// and skip; nothing that checks behaviour is skipped.
const raceDetector = true

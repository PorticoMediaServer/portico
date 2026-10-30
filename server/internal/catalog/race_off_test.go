//go:build !race

package catalog

// raceDetector is false in an ordinary build; see race_on_test.go.
const raceDetector = false

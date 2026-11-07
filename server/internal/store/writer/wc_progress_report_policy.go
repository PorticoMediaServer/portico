package writer

import (
	"database/sql"
	"math"
)

// Immutable policy data from trusted fixture/source composition, not a caller
// completion flag. No preference writer is installed in the measurement fixture.
// The formulas match catalog/personal_activity.go's existing completion rule.
type wcProgressPolicy struct {
	StartedPercent, PlayedPercent int64
	VideoCompletion               string
	CreditsStart                  sql.NullFloat64
}

func (p wcProgressPolicy) valid() bool {
	return p.StartedPercent >= 1 && p.StartedPercent <= 25 && p.PlayedPercent >= 75 && p.PlayedPercent <= 100 && (p.VideoCompletion == "earliest" || p.VideoCompletion == "threshold" || p.VideoCompletion == "credits") && (!p.CreditsStart.Valid && p.CreditsStart.Float64 == 0 || p.CreditsStart.Valid && p.CreditsStart.Float64 >= 0 && !math.IsNaN(p.CreditsStart.Float64) && !math.IsInf(p.CreditsStart.Float64, 0))
}
func (p wcProgressPolicy) completion(kind int64, position, duration float64, state string) (bool, error) {
	const maximumSeconds = float64(int64(1)<<40) / 1000
	if !p.valid() || math.IsNaN(position) || math.IsInf(position, 0) || position < 0 || position > maximumSeconds || math.IsNaN(duration) || math.IsInf(duration, 0) || duration < 0 || duration > maximumSeconds || (state != "playing" && state != "paused" && state != "ended") {
		return false, ErrInvalid
	}
	if duration > 0 {
		position = math.Min(position, duration)
	}
	video := kind == 1 || kind == 4
	thresholdHit := duration > 0 && position >= duration*float64(p.PlayedPercent)/100
	if kind == 9 {
		thresholdHit = duration > 0 && position > 0 && duration-position <= math.Min(30, duration/20)
	}
	creditsAvailable, creditsHit := false, false
	if video && p.VideoCompletion != "threshold" && (p.VideoCompletion == "credits" || !thresholdHit) {
		creditsAvailable = p.CreditsStart.Valid
		creditsHit = creditsAvailable && position >= p.CreditsStart.Float64
	}
	terminal := state == "ended" && (duration <= 0 || position >= duration-math.Min(1, duration/100))
	return terminal || ((!video || p.VideoCompletion != "credits" || !creditsAvailable) && thresholdHit) || creditsHit, nil
}

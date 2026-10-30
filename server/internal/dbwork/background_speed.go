package dbwork

import (
	"context"
	"database/sql"
)

type backgroundSpeedKey struct{}

const SpeedGentle = "gentle"
const SpeedNormal = "normal"
const SpeedFast = "fast"

func ValidBackgroundSpeed(speed string) bool {
	return speed == SpeedGentle || speed == SpeedNormal || speed == SpeedFast
}
func WithBackgroundSpeed(ctx context.Context, speed string) context.Context {
	if !ValidBackgroundSpeed(speed) {
		speed = SpeedNormal
	}
	return context.WithValue(ctx, backgroundSpeedKey{}, speed)
}
func BackgroundSpeed(ctx context.Context) string {
	speed, _ := ctx.Value(backgroundSpeedKey{}).(string)
	if !ValidBackgroundSpeed(speed) {
		return processBackgroundSpeed()
	}
	return speed
}

// ReadBackgroundSpeed consumes the same normalized value saved by the owner
// form and TOML import. Call once per bounded batch, outside a writer transaction.
func ReadBackgroundSpeed(ctx context.Context, db *sql.DB) string {
	var speed string
	if QueryRow(ctx, db, `SELECT background_speed FROM maintenance_settings WHERE singleton=1`).Scan(&speed) != nil || !ValidBackgroundSpeed(speed) {
		return SpeedNormal
	}
	return speed
}
func BackgroundConcurrency(ctx context.Context) int {
	switch BackgroundSpeed(ctx) {
	case SpeedGentle:
		return 2
	case SpeedFast:
		return 8
	}
	return 4
}

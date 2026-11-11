package db

import "time"

// MoodChoiceMade is when the profile's kept mood choice was last made (zero: none kept). Test only.
func (d *DB) MoodChoiceMade(profileID int64) time.Time {
	d.moods.mu.Lock()
	defer d.moods.mu.Unlock()
	if c := d.moods.choices[profileID]; c != nil {
		return c.made
	}
	return time.Time{}
}

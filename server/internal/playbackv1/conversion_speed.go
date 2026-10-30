package playbackv1

import "time"

// ConversionFreshFor excludes completed, throttled, abandoned and superseded
// producers. A missing observation is unknown, never zero-speed conversion.
const ConversionFreshFor = 5 * time.Second
const StuckBufferingAfter = 10 * time.Second

type ConversionObservation struct {
	Speed      float64 `json:"speed"`
	ObservedAt string  `json:"observedAt"`
	CadenceMs  int     `json:"cadenceMs"`
}

// All current converter components must keep up. Each row is emitted by an
// actual FFmpeg child, fenced to its exact media session and generation.
const conversionJoins = ` LEFT JOIN (
 SELECT session_id,generation,MIN(speed) speed,MIN(observed_ms) observed_ms
 FROM playback_conversion_observations,clock
 WHERE observed_ms BETWEEN clock.now-5000 AND clock.now
 GROUP BY session_id,generation
 ) conversion ON conversion.session_id=v.media_session_id AND conversion.generation=v.media_generation
 AND EXISTS(SELECT 1 FROM playback_sessions current WHERE current.id=v.media_session_id AND current.generation=v.media_generation AND current.state NOT IN('stopped','ended','failed'))`

const activityProblemCode = `CASE
 WHEN v.state='buffering' AND v.buffering_since_ms>0 AND v.buffering_since_ms<=clock.now-10000 THEN 'buffering'
 WHEN v.state IN ('playing','buffering') AND ` + activityConverting + `=1 AND conversion.speed<1 THEN 'behind'
 ELSE '' END`

const activityPriority = `CASE WHEN (` + activityProblemCode + `)<>'' THEN 0 WHEN ` + activityConverting + `=1 THEN 1 ELSE 2 END`

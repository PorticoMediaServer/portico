package apispec

import (
	"encoding/json"
	"fmt"
	"regexp"
)

var errorCode = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// ValidateErrorEnvelope checks error responses independently of a route's
// schema, so an overly permissive schema cannot hide a lost error code.
// Legacy retryable remains accepted during the documented migration.
func ValidateErrorEnvelope(status int, body []byte) error {
	if status < 400 {
		return nil
	}
	var envelope struct {
		Error struct {
			Code      string `json:"code"`
			Message   string `json:"message"`
			Retry     string `json:"retry"`
			Retryable *bool  `json:"retryable"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return fmt.Errorf("error envelope: %w", err)
	}
	e := envelope.Error
	if !errorCode.MatchString(e.Code) || e.Message == "" {
		return fmt.Errorf("error envelope needs code and message")
	}
	switch e.Retry {
	case "never", "same_request", "after_refresh", "after_reauth":
	case "":
		if e.Retryable == nil {
			return fmt.Errorf("error envelope needs retry policy")
		}
	default:
		return fmt.Errorf("invalid retry policy %q", e.Retry)
	}
	return nil
}

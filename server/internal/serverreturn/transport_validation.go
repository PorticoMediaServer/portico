package serverreturn

import (
	"errors"
	"regexp"
)

var ErrInvalid = errors.New("invalid server return")
var identifier = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
var secret = regexp.MustCompile(`^[A-Za-z0-9_-]{32,128}$`)

func Purpose(v string) bool      { return v == "sign-in" || claimPurpose(v) }

// RegisteredPurpose is what an ordinary registered return may carry. Setup has its own
// transport and names no registration (O-163), so it is never a registered return.
func RegisteredPurpose(v string) bool { return v == "sign-in" || v == "switch" || v == "change-owner" }
func claimPurpose(v string) bool { return v == "setup" || v == "switch" || v == "change-owner" }

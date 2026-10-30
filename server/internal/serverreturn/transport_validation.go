package serverreturn

import (
	"errors"
	"regexp"
)

var ErrInvalid = errors.New("invalid server return")
var identifier = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
var secret = regexp.MustCompile(`^[A-Za-z0-9_-]{32,128}$`)

func Purpose(v string) bool      { return v == "sign-in" || claimPurpose(v) }
func claimPurpose(v string) bool { return v == "setup" || v == "switch" || v == "change-owner" }

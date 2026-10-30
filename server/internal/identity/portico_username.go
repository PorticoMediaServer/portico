package identity

import "regexp"

var porticoUsername = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{1,30}[a-z0-9]$`)

func validPorticoUsername(v string) bool { return porticoUsername.MatchString(v) }

package auth

import (
	"slices"
	"time"
	"unicode/utf8"
)

// Policy is the owner's sign-in settings. Each field takes one of a short list of values.
type Policy struct {
	// StaySignedInDays is how long a sign-in lasts from its last use: 7, 30, 90 or 365.
	StaySignedInDays int `json:"staySignedInDays"`
	// PINRememberDays is how long a device may remember a profile's PIN: 0 (always ask), 7, 30 or 90.
	PINRememberDays int `json:"pinRememberDays"`
	// PasswordAttempts and PINAttempts say how strict wrong guesses are: relaxed, standard or strict.
	PasswordAttempts string `json:"passwordAttempts"`
	PINAttempts      string `json:"pinAttempts"`
	// PasswordMinLength is the shortest new password: 8, 10, 12 or 16. Existing passwords keep working.
	PasswordMinLength int `json:"passwordMinLength"`
	// ResetCodeMinutes is how long a member's reset code can be used: 15, 60 or 1440.
	ResetCodeMinutes int `json:"resetCodeMinutes"`
	// RequireTwoStep says who must use two-step sign-in: nobody, admins (the owner and
	// admins) or everyone.
	RequireTwoStep string `json:"requireTwoStep"`
	// RememberAccounts lets a device's sign-in screen list the accounts that chose to be remembered there.
	RememberAccounts bool `json:"rememberAccounts"`
	// EndedSessionsDays is how long an ended sign-in stays listed: 30, 90 or 365.
	EndedSessionsDays int `json:"endedSessionsDays"`
}

// DefaultPolicy is what a new server starts with.
func DefaultPolicy() Policy {
	return Policy{StaySignedInDays: 90, PINRememberDays: 30, PasswordAttempts: "standard", PINAttempts: "standard",
		PasswordMinLength: 8, ResetCodeMinutes: 60, RequireTwoStep: "nobody", RememberAccounts: true, EndedSessionsDays: 90}
}

var strictnesses = []string{"relaxed", "standard", "strict"}

// Problem names the first field holding a value it does not take, or "" if the policy will do.
func (p Policy) Problem() string {
	switch {
	case !slices.Contains([]int{7, 30, 90, 365}, p.StaySignedInDays):
		return "staySignedInDays"
	case !slices.Contains([]int{0, 7, 30, 90}, p.PINRememberDays):
		return "pinRememberDays"
	case !slices.Contains(strictnesses, p.PasswordAttempts):
		return "passwordAttempts"
	case !slices.Contains(strictnesses, p.PINAttempts):
		return "pinAttempts"
	case !slices.Contains([]int{8, 10, 12, 16}, p.PasswordMinLength):
		return "passwordMinLength"
	case !slices.Contains([]int{15, 60, 1440}, p.ResetCodeMinutes):
		return "resetCodeMinutes"
	case !slices.Contains([]string{"nobody", "admins", "everyone"}, p.RequireTwoStep):
		return "requireTwoStep"
	case !slices.Contains([]int{30, 90, 365}, p.EndedSessionsDays):
		return "endedSessionsDays"
	}
	return ""
}

func days(n int) time.Duration { return time.Duration(n) * 24 * time.Hour }

// SessionIdle is how long a sign-in lasts without being used. Each renewal starts it again.
func (p Policy) SessionIdle() time.Duration { return days(p.StaySignedInDays) }

// EndedSessionsKept is how long an ended sign-in stays in the session list.
func (p Policy) EndedSessionsKept() time.Duration { return days(p.EndedSessionsDays) }

// PINRemembered is how long a device may remember a PIN, or 0 for never.
func (p Policy) PINRemembered() time.Duration { return days(p.PINRememberDays) }

// ResetCodeLife is how long a member's reset code can be used.
func (p Policy) ResetCodeLife() time.Duration { return time.Duration(p.ResetCodeMinutes) * time.Minute }

// TwoStepRequired reports whether an account with this role must use two-step sign-in.
func (p Policy) TwoStepRequired(role string) bool {
	return p.RequireTwoStep == "everyone" || p.RequireTwoStep == "admins" && (role == "owner" || role == "admin")
}

// PasswordProblem says what is wrong with a new password, or "" if it will do. The one rule
// is its length: apps show a strength hint, but a weak password is the person's choice.
// 72 bytes is the most bcrypt reads.
func (p Policy) PasswordProblem(v string) string {
	switch {
	case len(v) > 72:
		return "Use a shorter password."
	case !utf8.ValidString(v) || utf8.RuneCountInString(v) < p.PasswordMinLength:
		return "Choose a password of at least " + map[int]string{8: "8", 10: "10", 12: "12", 16: "16"}[p.PasswordMinLength] + " characters."
	}
	return ""
}

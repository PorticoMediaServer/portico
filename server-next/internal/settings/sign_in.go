package settings

import "slices"

// SignInSettings is Settings › Sign-in. Only the owner changes it.
type SignInSettings struct {
	// StaySignedInDays is how long a sign-in lasts from its last use: 7, 30, 90 or 365.
	StaySignedInDays int `json:"staySignedInDays"`
	// RequireTwoStep says who must use two-step sign-in: "nobody", "admins" (the owner and
	// administrators) or "everyone". A person it applies to sets it up at their next sign-in
	// before anything else, and cannot turn it off.
	RequireTwoStep string `json:"requireTwoStep"`
}

// TwoStepRequired reports whether an account with this role must use two-step sign-in.
func (s SignInSettings) TwoStepRequired(role string) bool {
	return s.RequireTwoStep == "everyone" || s.RequireTwoStep == "admins" && (role == "owner" || role == "admin")
}

// SignIn is the sign-in settings document.
var SignIn = Define("sign-in", SignInSettings{StaySignedInDays: 90, RequireTwoStep: "nobody"}, func(s *SignInSettings) error {
	switch {
	case !slices.Contains([]int{7, 30, 90, 365}, s.StaySignedInDays):
		return &Invalid{Field: "staySignedInDays", Message: "Choose 7, 30, 90 or 365 days."}
	case !slices.Contains([]string{"nobody", "admins", "everyone"}, s.RequireTwoStep):
		return &Invalid{Field: "requireTwoStep", Message: "Choose nobody, admins or everyone."}
	}
	return nil
})

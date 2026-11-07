package securitypolicy

// ValidStreamMode preserves the existing account/invitation product envelope.
// An unlimited person setting still cannot bypass the separate owner ceiling.
func ValidStreamMode(mode string, value int) bool {
	return mode == "limit" && value >= 1 && value <= 1000 || (mode == "inherit" || mode == "unlimited") && value == 0
}

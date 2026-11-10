package streaming

// within reports whether a planned value respects a ceiling (zero: none).
func within(value, ceiling int) bool { return ceiling <= 0 || value <= ceiling }

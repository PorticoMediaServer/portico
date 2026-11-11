package api

// Google Cast.
func init() {
	safeToRepeat(map[string]string{
		"POST /v1/cast/codes":  "a lost answer leaves a pairing code nobody holds, which expires",
		"POST /v1/cast/redeem": "uses up its code: a repeat is refused",
	})
}

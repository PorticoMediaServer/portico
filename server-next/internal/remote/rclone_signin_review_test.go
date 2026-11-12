package remote

import "testing"

// Review desk A, for #2859: what rclone says that is NOT a refused sign-in but reads as one.
func TestReviewAWhatReadsAsARefusedSignIn(t *testing.T) {
	for _, tc := range []struct {
		line string
		want bool
	}{
		{`Failed to lsjson with 2 errors: last error was: couldn't list directory: 401 Unauthorized`, true},
		{`Failed to create file system for "portico:": couldn't fetch token: invalid_grant`, true},
		{`Failed to lsjson: googleapi: Error 403: User Rate Limit Exceeded. Rate of requests for user exceed configured project quota, userRateLimitExceeded`, false},
		{`Failed to lsjson: error in ListJSON: failed to read directory "Films/Area 403": connection reset by peer`, false},
		{`Failed to lsjson: dial tcp 203.0.113.9:403: connect: connection refused`, false},
	} {
		if got := signInRefused.MatchString(tc.line); got != tc.want {
			t.Errorf("read as a refused sign-in: %v, want %v: %s", got, tc.want, tc.line)
		}
	}
}

package remote

import "testing"

// What the bundled rclone 1.75.2 printed (its last line, date taken off) against the WebDAV
// stand-in and a small server answering by path: the two refused sign-ins, a plain 403, and
// three failures that are not a refused sign-in although they say 401 or 403.
func TestWhatRcloneSaysForARefusedSignIn(t *testing.T) {
	for _, tc := range []struct {
		line string
		want bool
	}{
		{`NOTICE: Failed to lsjson: couldn't list files: sign in: 401 Unauthorized`, true},
		{`CRITICAL: Failed to create file system for "portico:Movies": read metadata failed: sign in: 401 Unauthorized`, true},
		{`NOTICE: Failed to lsjson: couldn't list files: Forbidden: 403 Forbidden`, true},
		{`NOTICE: Failed to lsjson: couldn't list files: User Rate Limit Exceeded. Rate of requests for user exceed configured project quota.: 403 Forbidden`, false},
		{`CRITICAL: Failed to create file system for "portico:Area 403": read metadata failed: internal error: 500 Internal Server Error`, false},
		{`NOTICE: Failed to lsjson: couldn't list files: Propfind "http://127.0.0.1:403/dav/": dial tcp 127.0.0.1:403: connect: connection refused`, false},
	} {
		if got := signInRefused.MatchString(tc.line); got != tc.want {
			t.Errorf("read as a refused sign-in: %v, want %v: %s", got, tc.want, tc.line)
		}
	}
}

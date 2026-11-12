package remote

import (
	"errors"
	"testing"
)

// Review desk A, second look at the address rule (#2824). Text only: nothing is resolved or
// connected to.
func TestReviewAOtherWaysToWriteAnAddressOption(t *testing.T) {
	for _, tc := range []struct{ why, paste string }{
		{"user and password in the address", "[a]\ntype = webdav\nurl = http://u:p@169.254.169.254/dav\n"},
		{"capital scheme, IPv4 inside IPv6", "[a]\ntype = http\nurl = HTTP://[::ffff:169.254.169.254]/\n"},
		{"trailing dot", "[a]\ntype = sftp\nhost = 169.254.169.254.\n"},
		{"host and port with no scheme", "[a]\ntype = s3\nprovider = Other\naccess_key_id = k\nsecret_access_key = s\nendpoint = 169.254.169.254:80\n"},
		{"host and path with no scheme", "[a]\ntype = s3\nprovider = Other\naccess_key_id = k\nsecret_access_key = s\nendpoint = 169.254.169.254/latest\n"},
		{"a list of hosts", "[a]\ntype = hdfs\nnamenode = nn.example:8020,169.254.169.254:8020\n"},
		{"a SOCKS proxy", "[a]\ntype = sftp\nhost = files.example\nuser = u\npass = p\nsocks_proxy = 169.254.169.254:1080\n"},
		{"Files.com's site, which may be a whole domain", "[a]\ntype = filescom\nsite = metadata.google.internal\napi_key = k\n"},
	} {
		if _, err := ParseRcloneConfig(tc.paste); !errors.Is(err, ErrAddressRefused) {
			t.Errorf("%s: %v, want the address refused", tc.why, err)
		}
	}
}

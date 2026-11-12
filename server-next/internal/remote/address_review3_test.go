package remote

import "testing"

// Review desk A, for #2842. The text check now looks inside every option's value. A metadata
// service's short names are also ordinary words: a remote described as holding "metadata", or
// signed in to as a user of that name, is not at a link-local address and must not be refused
// as one.
func TestReviewAAnOrdinaryWordIsNotAnAddress(t *testing.T) {
	for _, paste := range []string{
		"[a]\ntype = sftp\nhost = nas.example\nuser = u\npass = p\nhost_keys = k\ndescription = film metadata and posters\n",
		"[a]\ntype = webdav\nurl = https://dav.example/files\nuser = metadata\npass = p\n",
		"[a]\ntype = s3\nprovider = Other\naccess_key_id = k\nsecret_access_key = s\nendpoint = https://s3.example\ndescription = instance-data archive\n",
	} {
		if _, err := ParseRcloneConfig(paste); err != nil {
			t.Errorf("%q: %v", paste, err)
		}
	}
}

package remotesources

import "testing"

func TestBackendCapabilitiesRetainAcceptedTransportLimits(t *testing.T) {
	for _, backend := range []string{"s3", "b2", "drive", "onedrive", "dropbox", "webdav", "http", "sftp", "smb"} {
		facts, err := sourceBackendCapabilities(backend, "nas.example")
		if err != nil || !facts.AdmittedOperationsBounded || !facts.HiddenRetriesWithinOperation || !facts.ThrottleReportsAsynchronous || facts.MetadataEqualityProvesBytes {
			t.Fatal(backend, facts, err)
		}
		native := backend == "sftp" || backend == "smb"
		if facts.NativeDirect != (backend == "smb") || facts.NativeHostnameCheckConnectGap || facts.HTTPProxyConfigured != (backend != "smb") || facts.HTTPSOpaque != !native || facts.SFTPConnectionPoolBounded != (backend == "sftp") || facts.SMBIdlePoolMayExceedOperations != (backend == "smb") {
			t.Fatal("backend facts do not match the enforcing transport", facts)
		}
		if native {
			literal, err := sourceBackendCapabilities(backend, "192.168.42.7")
			if err != nil || literal.NativeHostnameCheckConnectGap {
				t.Fatal("IP literal invented a DNS gap", literal, err)
			}
		}
	}
	if _, err := sourceBackendCapabilities("unknown", ""); err == nil {
		t.Fatal("unknown backend acquired capability facts")
	}
}

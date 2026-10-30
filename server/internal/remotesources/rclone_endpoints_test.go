package remotesources

import (
	"fmt"
	"testing"
)

func TestConfiguredEndpointsFollowEveryWrapperToActualOrigin(t *testing.T) {
	for _, wrapper := range []string{"alias", "crypt", "chunker", "hasher", "compress", "cache"} {
		raw := "[top]\ntype=" + wrapper + "\nremote=leaf:path\n[leaf]\ntype=webdav\nurl=https://nas.example:9443/private\n[unused]\ntype=http\nurl=http://127.0.0.1/\n"
		leaves, err := configuredBackends([]byte(raw), "top:")
		if err != nil || len(leaves) != 1 || leaves[0].Kind != "webdav" || len(leaves[0].Origins) != 1 || leaves[0].Origins[0] != "https://nas.example:9443/" {
			t.Fatal(wrapper, leaves, err)
		}
	}
	for _, wrapper := range []string{"union", "combine"} {
		members := `"a:path with spaces" b:bucket a:other`
		if wrapper == "combine" {
			members = `"one=a:path with spaces" two=b:bucket three=a:other`
		}
		raw := fmt.Sprintf("[top]\ntype=%s\nupstreams=%s\n[a]\ntype=webdav\nurl=http://nas.example:80/\n[b]\ntype=s3\nendpoint=https://s3.example:9443/\n", wrapper, members)
		leaves, err := configuredBackends([]byte(raw), "top:")
		if err != nil || len(leaves) != 3 || leaves[0].Kind != "webdav" || leaves[1].Kind != "s3" || leaves[2].Kind != "webdav" || leaves[0].Remote == leaves[2].Remote || webDAVLeafCount(leaves) != 2 {
			t.Fatal(wrapper, leaves, err)
		}
	}
}

func TestConfiguredEndpointsRefuseCyclesEscapesAndUnknownMembers(t *testing.T) {
	for _, raw := range []string{
		"[top]\ntype=crypt\nremote=top:\n",
		"[top]\ntype=union\nupstreams=leaf: missing:\n[leaf]\ntype=http\nurl=https://public.example/\n",
		"[top]\ntype=union\nupstreams=/owner/path leaf:\n[leaf]\ntype=s3\n",
		"[top]\ntype=alias\nremote=:http,url='http://127.0.0.1':\n",
		"[top]\ntype=http\nurl=http://owner:secret@host/\n",
		"[top]\ntype=crypt\nremote=leaf:\n[leaf]\ntype=sftp\nhost=host\nssh=arbitrary\n",
		"[top]\ntype=smb\nhost=host\nport=0\n",
		"[top]\ntype=union\nupstreams=\"unfinished\n",
	} {
		if leaves, err := configuredBackends([]byte(raw), "top:"); err == nil {
			t.Fatal("invalid graph admitted", leaves)
		}
	}
}

func TestConfiguredEndpointApprovalFactsNeverRetainPathOrQueryCredentials(t *testing.T) {
	leaves, err := configuredBackends([]byte("[top]\ntype=http\nurl=https://NAS.example/private?access_token=fixture-only\n"), "top:")
	if err != nil || len(leaves) != 1 || len(leaves[0].Origins) != 1 || leaves[0].Origins[0] != "https://nas.example:443/" {
		t.Fatal("endpoint not reduced to its real origin", err)
	}
}

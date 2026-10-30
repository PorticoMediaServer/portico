package dlna

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVideoDemuxAliasesDescribeAndServeTheSameOriginal(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, tc := range []struct{ container, suffix, mime string }{
		{"matroska", ".mkv", "video/x-matroska"},
		{"mpegts", ".ts", "video/mp2t"},
	} {
		t.Run(tc.container, func(t *testing.T) {
			f := newFixture(t)
			path := filepath.Join(t.TempDir(), "original"+tc.suffix)
			body := "unchanged original bytes"
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			token := f.items["heat"].Token
			f.c.Exec(`UPDATE catalog_assets SET path=?,size=?,modified_ns=?,container=?,video_codec='h264' WHERE token=?`, path, info.Size(), info.ModTime().UnixNano(), tc.container, token)
			xml := didl("http://home:8096", []object{{id: "item", res: &resource{token: token, container: tc.container, video: "h264"}}})
			url := mediaPath + token + tc.suffix
			if !strings.Contains(xml, url+"</res>") || !strings.Contains(xml, protocolInfo(tc.mime)) || !strings.Contains(sourceProtocols(), protocolInfo(tc.mime)) {
				t.Fatal("DIDL, suffix and ConnectionManager disagree", xml)
			}
			for _, method := range []string{http.MethodGet, http.MethodHead} {
				response := f.request(method, url, "192.168.1.20", "")
				if response.Code != http.StatusOK || response.Header().Get("Content-Type") != tc.mime || response.Header().Get("X-Content-Type-Options") != "nosniff" || method == http.MethodGet && response.Body.String() != body || method == http.MethodHead && response.Body.Len() != 0 {
					t.Fatal("served original differs from DIDL", method, response.Code, response.Header())
				}
			}
			if err = os.WriteFile(path, []byte(body+"changed"), 0o600); err != nil {
				t.Fatal(err)
			}
			if response := f.request(http.MethodGet, url, "192.168.1.20", ""); response.Code != http.StatusConflict {
				t.Fatal("changed source was served under its old observation", response.Code)
			}
		})
	}
}

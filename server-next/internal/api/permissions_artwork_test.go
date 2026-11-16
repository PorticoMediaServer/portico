package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http/httptest"
	"testing"
)

func TestKnownPublicPosterBytesCannotProbeTheArtworkStore(t *testing.T) {
	th := newTheatre(t)
	s := th.s
	data := []byte(pngPicture(t, 8, 12))
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	req := httptest.NewRequest("POST", "/v1/admin/items/"+th.film+"/artwork?role=poster", bytes.NewReader(data))
	req.Header.Set("Authorization", "Bearer "+th.owner.AccessToken)
	req.Header.Set("Content-Type", "application/octet-stream")
	res := serve(s, req)
	if res.Code != 201 {
		t.Fatalf("upload: HTTP %d", res.Code)
	}
	if w := call(t, s, "GET", "/v1/images/"+hash, "", nil, nil); w.Code != 404 {
		t.Errorf("known public poster content hash found on server: HTTP %d", w.Code)
	}
	item := get[Item](t, s, th.owner.AccessToken, "/v1/items/"+th.film)
	address := item.Images["poster"]
	if address == "/v1/images/"+hash {
		t.Error("response exposed original content hash")
	}
	if w := call(t, s, "GET", address, "", nil, nil); w.Code != 200 || w.Header().Get("Cache-Control") == "" {
		t.Errorf("issued image address: HTTP %d", w.Code)
	}
}

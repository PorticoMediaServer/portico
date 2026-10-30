package metadata

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"
)

func TestArtworkFullOriginKeepsExactAdmittedImageAndAuthority(t *testing.T) {
	for _, size := range []string{"w780", "w1280", "h632"} {
		raw := "https://image.tmdb.org/t/p/" + size + "/picture.jpg"
		if got := fullArtworkOrigin(raw, "tmdb"); got != "https://image.tmdb.org/t/p/original/picture.jpg" {
			t.Fatal(got)
		}
	}
	for _, raw := range []string{"https://image.tmdb.org/t/p/original/picture.jpg", "https://image.tmdb.org/t/p/unknown/picture.jpg", "https://image.tmdb.org/t/p/w0/picture.jpg", "https://image.tmdb.org/t/p/w780/", "http://image.tmdb.org/t/p/w780/picture.jpg", "https://image.tmdb.org.evil.invalid/t/p/w780/picture.jpg", "https://secret@image.tmdb.org/t/p/w780/picture.jpg", "https://image.tmdb.org/t/p/w780/../picture.jpg", "https://image.tmdb.org/t/p/w780/picture.jpg?token=secret", "https://image.tmdb.org/t/p/w780/picture.jpg#fragment"} {
		if got := fullArtworkOrigin(raw, "tmdb"); got != raw {
			t.Fatal("invalid or already original source was rewritten", got)
		}
	}
	raw := "https://image.tmdb.org/t/p/w780/picture.jpg"
	if fullArtworkOrigin(raw, "tvdb") != raw {
		t.Fatal("another provider was given TMDB authority")
	}
}

func TestArtworkAcquisitionUsesOriginalWhilePreviewKeepsProviderSize(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	s, _, _ := repairFixture(t)
	setArtworkPolicy(t, s, 2560)
	ctx, _, err := s.CaptureArtworkContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	body := editorJPEG(t, 2700, 1080)
	paths := []string{}
	s.artClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "image.tmdb.org" || r.Header.Get("Authorization") != "" {
			t.Fatal("origin or credential authority changed")
		}
		paths = append(paths, r.URL.Path)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(body)), Header: http.Header{}}, nil
	})}
	j := artworkWork{provider: "tmdb", origin: "https://image.tmdb.org/t/p/w780/picture.jpg"}
	m, err := s.acquireArtworkMaterial(ctx, j)
	if err != nil || m.large.width != 2560 || m.small.width != artworkSmallEdge || m.acceptedWidth != 2700 {
		t.Fatal("full acquisition retained provider thumbnail cap", m.large.width, m.small.width, err)
	}
	j.preview = true
	if _, err = s.acquireArtworkMaterial(ctx, j); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 || paths[0] != "/t/p/original/picture.jpg" || paths[1] != "/t/p/w780/picture.jpg" {
		t.Fatal("original/preview URL intent changed", paths)
	}
}

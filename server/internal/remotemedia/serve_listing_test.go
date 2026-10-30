package remotemedia

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestServeListingSnapshotNeverReturnsPartialChildren(t *testing.T) {
	child := `<d:response><d:href>/disc/clip.m2ts</d:href><d:propstat><d:prop><d:getcontentlength>8</d:getcontentlength><d:getetag>"etag"</d:getetag><d:getlastmodified>Fri, 23 Oct 2026 00:00:00 GMT</d:getlastmodified></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`
	body := `<d:multistatus xmlns:d="DAV:">` + child + `</d:multistatus>`
	for _, bad := range []string{"", "truncated", "oversized", "duplicate", "traversal"} {
		t.Run(bad, func(t *testing.T) {
			s, err := NewServeEndpoint(12345, "private", "secret", NewOperationBudget(context.Background(), 2))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			wire := body
			switch bad {
			case "truncated":
				wire = strings.TrimSuffix(body, `</d:multistatus>`)
			case "oversized":
				wire = `<d:multistatus xmlns:d="DAV:">` + strings.Repeat(" ", 4<<20) + `</d:multistatus>`
			case "duplicate":
				wire = `<d:multistatus xmlns:d="DAV:">` + child + child + `</d:multistatus>`
			case "traversal":
				wire = strings.ReplaceAll(body, "/disc/clip.m2ts", "/disc/../outside")
			}
			s.client.Transport = serveRoundTrip(func(r *http.Request) (*http.Response, error) {
				if r.Method != "PROPFIND" || r.URL.Path != "/disc/" || r.Header.Get("Depth") != "1" {
					t.Fatal("wrong listing request")
				}
				request, _ := io.ReadAll(r.Body)
				if strings.Contains(string(request), "allprop") || strings.Contains(string(request), "getcontenttype") {
					t.Fatal("expensive listing properties")
				}
				return &http.Response{StatusCode: 207, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(wire))}, nil
			})
			page, err := s.ListSnapshot(context.Background(), "disc", func(context.Context) error { return nil })
			if bad == "" && (err != nil || len(page.Entries) != 1) {
				t.Fatalf("%+v %v", page, err)
			}
			if bad != "" && (err == nil || len(page.Entries) != 0) {
				t.Fatalf("partial snapshot escaped: %+v %v", page, err)
			}
		})
	}
}

func TestServeStatHasExactMemberAndDoesNotInventProviderEvidence(t *testing.T) {
	s, err := NewServeEndpoint(12345, "private", "secret", NewOperationBudget(context.Background(), 2))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.client.Transport = serveRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.Method != "PROPFIND" || r.URL.Path != "/disc/file.iso" || r.Header.Get("Depth") != "0" {
			t.Fatal("stat escaped exact member")
		}
		wire := `<d:multistatus xmlns:d="DAV:"><d:response><d:href>/disc/file.iso</d:href><d:propstat><d:prop><d:getcontentlength>8</d:getcontentlength><d:getetag>"served"</d:getetag><d:getlastmodified>Fri, 23 Oct 2026 00:00:00 GMT</d:getlastmodified></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response></d:multistatus>`
		return &http.Response{StatusCode: 207, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(wire))}, nil
	})
	checks := 0
	entry, err := s.Stat(context.Background(), "disc/file.iso", func(context.Context) error { checks++; return nil })
	if err != nil {
		t.Fatal(err)
	}
	o, err := ServedFileObservation("source", "1", "disc", "file.iso", entry)
	if err != nil || o.Size != 8 || !o.ModifiedKnown || o.ServeETag != `"served"` || o.ProviderETag != "" || len(o.ProviderHashes) != 0 || o.ObjectID != "" || checks != 2 {
		t.Fatalf("invented evidence or lost fence: %+v %v %d", o, err, checks)
	}
	for _, modified := range []string{"", "Fri, 31 Dec 9999 23:59:59 GMT"} {
		entry.Modified = modified
		o, err = ServedFileObservation("source", "1", "disc", "file.iso", entry)
		// Section15 permits strongest-available observations. Missing time
		// stays unknown; the served precondition is not immutable identity.
		if err != nil || o.ModifiedKnown || o.ModifiedNS != 0 || o.ValidateForRange() != nil {
			t.Fatal("missing time invented evidence or blocked the admitted precondition", o, err)
		}
		o.ServeETag = ""
		if o.ValidateForRange() == nil {
			t.Fatal("missing served precondition admitted a range")
		}
		o.ModifiedNS = 1
		if o.Validate() == nil {
			t.Fatal("unknown time carried invented nanoseconds")
		}

	}
}

func TestServeStatMissingCapturedMemberSignalsChange(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusMultiStatus} {
		s, err := NewServeEndpoint(12345, "private", "secret", NewOperationBudget(context.Background(), 2))
		if err != nil {
			t.Fatal(err)
		}
		s.client.Transport = serveRoundTrip(func(r *http.Request) (*http.Response, error) {
			wire := `<d:multistatus xmlns:d="DAV:"><d:response><d:href>/disc/file.iso</d:href><d:status>HTTP/1.1 404 Not Found</d:status></d:response></d:multistatus>`
			return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(wire))}, nil
		})
		_, err = s.Stat(context.Background(), "disc/file.iso", func(context.Context) error { return nil })
		s.Close()
		if !errors.Is(err, ErrObservationChanged) {
			t.Fatal("missing captured member did not require rescan", status, err)
		}
	}
}

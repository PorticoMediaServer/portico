package remotemedia

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

const listingStart = `<d:multistatus xmlns:d="DAV:"><d:response><d:href>/disc/</d:href><d:propstat><d:prop><d:resourcetype><d:collection/></d:resourcetype></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`
const listingEnd = `</d:multistatus>`

func listingChild(name string) string {
	return `<d:response><d:href>/disc/` + name + `</d:href><d:propstat><d:prop><d:getcontentlength>8</d:getcontentlength><d:getetag>"served"</d:getetag></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>`
}

func TestServeListingStreamsBeyondOldPageBounds(t *testing.T) {
	// A generated pipe proves the consumer receives entries before EOF. No
	// complete provider response or child list is retained by the fixture.
	input, output := io.Pipe()
	done := make(chan error, 1)
	go func() {
		_, err := io.WriteString(output, listingStart)
		for i := 0; i < 25000 && err == nil; i++ {
			_, err = io.WriteString(output, strings.Repeat(" ", 48)+listingChild(fmt.Sprintf("file%05d.mkv", i)))
		}
		if err == nil {
			_, err = io.WriteString(output, listingEnd)
		}
		output.CloseWithError(err)
		done <- err
	}()
	defer input.Close()
	s, err := NewServeEndpoint(12345, "private", "secret", NewOperationBudget(context.Background(), 2))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.client.Transport = serveRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.Method != "PROPFIND" || r.Header.Get("Depth") != "1" || r.URL.Path != "/disc/" {
			t.Fatal("unconfined listing request")
		}
		return &http.Response{StatusCode: 207, Header: http.Header{}, Body: input}, nil
	})
	count, checks := 0, 0
	err = s.VisitSnapshot(context.Background(), "disc", func(context.Context) error { checks++; return nil }, func(entry DAVEntry) error {
		if entry.Relative != fmt.Sprintf("disc/file%05d.mkv", count) || entry.Size != 8 || entry.ETag != `"served"` {
			t.Fatal("changed child order/facts", entry, count)
		}
		count++
		return nil
	})
	if err != nil || count != 25000 || checks != 2 {
		t.Fatal("large complete listing", count, checks, err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
}

func TestServeListingStreamingRejectsIncompleteOrUnconfinedProof(t *testing.T) {
	child := listingChild("clip.m2ts")
	for _, test := range []struct {
		name, body string
	}{
		{"truncated", listingStart + child},
		{"second-root", listingStart + child + listingEnd + listingStart + listingEnd},
		{"missing-self", `<d:multistatus xmlns:d="DAV:">` + child + listingEnd},
		{"duplicate-self", listingStart + strings.TrimPrefix(listingStart, `<d:multistatus xmlns:d="DAV:">`) + listingEnd},
		{"traversal", listingStart + listingChild("../outside") + listingEnd},
		{"foreign-origin", listingStart + strings.ReplaceAll(child, "/disc/clip.m2ts", "https://outside.example/disc/clip.m2ts") + listingEnd},
		{"nested-child", listingStart + listingChild("folder/clip.m2ts") + listingEnd},
		{"huge-text", listingStart + strings.Repeat(" ", 128<<10) + listingEnd},
		{"huge-entry", listingStart + strings.ReplaceAll(child, "clip.m2ts", strings.Repeat("x", 128<<10)) + listingEnd},
		{"doctype", `<!DOCTYPE multistatus>` + listingStart + listingEnd},
		{"bad-status", listingStart + strings.ReplaceAll(child, "200 OK", "507 Insufficient Storage") + listingEnd},
		{"missing-size", listingStart + strings.ReplaceAll(child, "<d:getcontentlength>8</d:getcontentlength>", "") + listingEnd},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := DAV{config: DAVConfig{Root: "http://127.0.0.1:12345/"}}
			if err := d.visitServedSnapshot(context.Background(), strings.NewReader(test.body), "disc", func(DAVEntry) error { return nil }); err == nil {
				t.Fatal("invalid proof reached successful EOF")
			}
		})
	}
}

func TestServeListingPrivateSinkAndFinalAuthorityFailuresStayFailures(t *testing.T) {
	sentinel := errors.New("private snapshot refused")
	for _, test := range []string{"sink", "duplicate", "final-check", "cancel"} {
		t.Run(test, func(t *testing.T) {
			s, err := NewServeEndpoint(12345, "private", "secret", NewOperationBudget(context.Background(), 2))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			body := listingStart + listingChild("clip.m2ts") + listingEnd
			if test == "duplicate" {
				body = listingStart + listingChild("clip.m2ts") + listingChild("clip.m2ts") + listingEnd
			}
			s.client.Transport = serveRoundTrip(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 207, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			checks := 0
			seen := map[string]bool{}
			err = s.VisitSnapshot(ctx, "disc", func(context.Context) error {
				checks++
				if test == "final-check" && checks == 2 {
					return sentinel
				}
				return nil
			}, func(entry DAVEntry) error {
				if test == "sink" || seen[entry.Relative] {
					return sentinel
				}
				seen[entry.Relative] = true
				if test == "cancel" {
					cancel()
				}
				return nil
			})
			if test == "cancel" {
				if !errors.Is(err, context.Canceled) {
					t.Fatal("cancelled listing published", err)
				}
			} else if !errors.Is(err, sentinel) {
				t.Fatal("sink/authority failure became success", err)
			}
		})
	}
}

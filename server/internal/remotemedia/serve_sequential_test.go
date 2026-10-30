package remotemedia

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestServeFromStartHasDistinctProgressiveContract(t *testing.T) {
	for _, failure := range []string{"", "206", "range-header", "etag", "encoding", "length", "changed", "redirect"} {
		t.Run(failure, func(t *testing.T) {
			endpoint, err := NewServeEndpoint(12345, "private", "secret", NewOperationBudget(context.Background(), 1))
			if err != nil {
				t.Fatal(err)
			}
			defer endpoint.Close()
			calls := 0
			endpoint.client.Transport = serveRoundTrip(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Method != "GET" || r.Header.Get("Range") != "" || r.Header.Get("If-Match") != `"captured"` || r.Header.Get("Accept-Encoding") != "identity" {
					t.Fatal("sequential request became ranged/unconditioned")
				}
				response := &http.Response{StatusCode: 200, Header: http.Header{"Etag": {`"captured"`}}, ContentLength: 4, Body: io.NopCloser(strings.NewReader("book"))}
				switch failure {
				case "206":
					response.StatusCode = 206
				case "range-header":
					response.Header.Set("Content-Range", "bytes 0-3/4")
				case "etag":
					response.Header.Set("ETag", `"replacement"`)
				case "encoding":
					response.Header.Set("Content-Encoding", "gzip")
				case "length":
					response.ContentLength = -1
				case "changed":
					response.StatusCode = 412
				case "redirect":
					response.StatusCode = 302
				}
				return response, nil
			})
			body, err := endpoint.OpenFromStart(context.Background(), "book.pdf", `"captured"`, 4, func(context.Context) error { return nil })
			if failure != "" {
				if body != nil || err == nil {
					t.Fatal("invalid sequential response accepted", failure, err)
				}
				if failure == "changed" && !errors.Is(err, ErrObservationChanged) {
					t.Fatal(err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				defer body.Close()
				raw, err := io.ReadAll(body)
				if err != nil || string(raw) != "book" {
					t.Fatal(string(raw), err)
				}
			}
			if calls != 1 {
				t.Fatal("sequential transport replayed request", calls)
			}
		})
	}
}

func TestServeFromStartNeverInventsAbsentCondition(t *testing.T) {
	endpoint, _ := NewServeEndpoint(12345, "private", "secret", NewOperationBudget(context.Background(), 1))
	defer endpoint.Close()
	endpoint.client.Transport = serveRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Range") != "" || r.Header.Get("If-Match") != "" {
			t.Fatal("fabricated condition/range")
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), ContentLength: 0, Body: http.NoBody}, nil
	})
	body, err := endpoint.OpenFromStart(context.Background(), "book.pdf", "", 0, func(context.Context) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	body.Close()
}

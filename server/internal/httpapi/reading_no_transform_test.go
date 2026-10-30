package httpapi

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReadingNoTransformPreservesSourceValidator(t *testing.T) {
	for _, size := range []int{18, 2048} {
		for _, flush := range []bool{false, true} {
			w := httptest.NewRecorder()
			c := &compressingWriter{ResponseWriter: w}
			readingHeaders(c, "source-revision")
			c.Header().Set("Content-Type", "application/json")
			body := bytes.Repeat([]byte("x"), size)
			if _, err := c.Write(body); err != nil {
				t.Fatal(err)
			}
			if flush {
				c.Flush()
			}
			c.close()
			if w.Code != 200 || w.Header().Get("Content-Encoding") != "" || w.Header().Get("ETag") != `"source-revision"` || !bytes.Equal(w.Body.Bytes(), body) {
				t.Fatalf("size=%d flush=%v status=%d headers=%v", size, flush, w.Code, w.Header())
			}
		}
	}
	w := httptest.NewRecorder()
	c := &compressingWriter{ResponseWriter: w}
	readingHeaders(c, "source-revision")
	c.WriteHeader(http.StatusNotModified)
	c.close()
	if w.Code != 304 || w.Header().Get("ETag") != `"source-revision"` || w.Body.Len() != 0 {
		t.Fatal(w.Code, w.Header())
	}
}

func TestReadingRegisteredGzipProgressKeepsExactRevision(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, format := range []string{"pdf", "cbz"} {
		t.Run(format, func(t *testing.T) {
			kind := "book"
			if format == "cbz" {
				kind = "comic"
			}
			f := newReadingFixture(t, kind, format)
			h := New(f.d)
			m := readingOpened(t, f, h)
			path := "/v1/reading/sessions/" + m.SessionID + "/progress"
			request := func(revision string) *httptest.ResponseRecorder {
				r := httptest.NewRequest("GET", path, nil)
				r.Header.Set("Authorization", "Bearer "+f.f.owner.AccessToken)
				r.Header.Set("If-Match", `"`+revision+`"`)
				r.Header.Set("Accept-Encoding", "gzip")
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				return w
			}
			w := request(m.Revision)
			if w.Code != 200 || w.Body.String() != "{\"progress\":null}\n" || w.Header().Get("ETag") != `"`+m.Revision+`"` || w.Header().Get("Content-Encoding") != "" || !responseNoTransform(w.Header()) {
				t.Fatal(w.Code, w.Header(), w.Body.String())
			}
			if w = request(strings.Repeat("0", 64)); w.Code != 412 {
				t.Fatal("source revision guard changed", w.Code)
			}
		})
	}
}

func TestResponseNoTransformDirective(t *testing.T) {
	for _, tc := range []struct {
		values []string
		want   bool
	}{
		{[]string{"private, no-store, no-transform"}, true},
		{[]string{"private", " No-Transform "}, true},
		{[]string{`extension="a,no-transform,b", no-store`}, false},
		{[]string{`extension="a\\\"b,no-transform,c"`}, false},
		{[]string{"x-no-transform, no-transform-other"}, false},
		{[]string{`no-transform="yes"`}, false},
	} {
		h := http.Header{}
		for _, value := range tc.values {
			h.Add("Cache-Control", value)
		}
		if got := responseNoTransform(h); got != tc.want {
			t.Fatalf("%q: got %v", tc.values, got)
		}
	}
}

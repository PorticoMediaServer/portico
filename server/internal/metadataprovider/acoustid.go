package metadataprovider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Lookup uses only an application credential. Submission (and its user key) is
// deliberately not supported. Fingerprints and keys never enter URLs or errors.
type AcoustID struct {
	http *transport
	key  string
}
type AcousticMatch struct {
	RecordingID string
	Score       float64
}

func NewAcoustID(key string) (*AcoustID, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, nil
	}
	if len(key) > 256 || strings.ContainsAny(key, "\r\n\x00") {
		return nil, errors.New("invalid AcoustID application credential")
	}
	return &AcoustID{http: newTransport("acoustid", "https://api.acoustid.org/v2", "Portico/0.1 (https://getportico.tv)"), key: key}, nil
}
func (s *AcoustID) Lookup(ctx context.Context, fingerprint string, duration int) ([]AcousticMatch, error) {
	check, err := networkAdmission(ctx)
	if err != nil {
		return nil, err
	}
	if !ValidFingerprint(fingerprint) || duration < 1 || duration > 86400 {
		return nil, &Error{Provider: "acoustid", Code: "invalid_fingerprint"}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if err := s.http.acquire(ctx); err != nil {
		return nil, err
	}
	form := url.Values{"client": {s.key}, "duration": {strconv.Itoa(duration)}, "fingerprint": {fingerprint}, "meta": {"recordingids"}, "format": {"json"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.http.base+"/lookup", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, errors.New("invalid AcoustID request")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", s.http.userAgent)
	if err := check(); err != nil {
		return nil, err
	}
	resp, err := s.http.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &Error{Provider: "acoustid", Code: "unavailable"}
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		p := &Error{Provider: "acoustid", Status: resp.StatusCode, Code: "request_rejected"}
		if resp.StatusCode >= 500 {
			p.Code = "unavailable"
		}
		if resp.StatusCode == 429 || resp.StatusCode == 503 {
			p.Code = "rate_limited"
			p.RetryAfter = retryDelay(resp.Header.Get("Retry-After"), time.Now())
			s.http.deferRequests(ctx, p.RetryAfter)
		}
		if resp.StatusCode == 401 || resp.StatusCode == 403 {
			p.Code = "authentication"
		}
		return nil, p
	}
	const budget = 256 << 10
	if resp.ContentLength > budget {
		return nil, &Error{Provider: "acoustid", Code: "malformed"}
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, budget+1))
	if err != nil || len(raw) > budget {
		return nil, &Error{Provider: "acoustid", Code: "malformed"}
	}
	if err := check(); err != nil {
		return nil, err
	}
	var body struct {
		Status string `json:"status"`
		Error  struct {
			Code int `json:"code"`
		} `json:"error"`
		Results []struct {
			ID         string  `json:"id"`
			Score      float64 `json:"score"`
			Recordings []struct {
				ID string `json:"id"`
			} `json:"recordings"`
		} `json:"results"`
	}
	if json.Unmarshal(raw, &body) != nil || len(body.Results) > 25 {
		return nil, &Error{Provider: "acoustid", Code: "malformed"}
	}
	if body.Status != "ok" {
		code := "malformed"
		if body.Error.Code == 4 || body.Error.Code == 5 {
			code = "authentication"
		}
		if body.Error.Code == 14 {
			return nil, &Error{Provider: "acoustid", Code: "rate_limited", Status: 429, RetryAfter: time.Minute}
		}
		return nil, &Error{Provider: "acoustid", Code: code}
	}
	if body.Results == nil {
		return nil, &Error{Provider: "acoustid", Code: "malformed"}
	}
	out := []AcousticMatch{}
	seen := map[string]int{}
	for _, v := range body.Results {
		if !mbid.MatchString(v.ID) || math.IsNaN(v.Score) || math.IsInf(v.Score, 0) || v.Score < 0 || v.Score > 1 || len(v.Recordings) > 25 {
			return nil, &Error{Provider: "acoustid", Code: "malformed"}
		}
		for _, r := range v.Recordings {
			if !mbid.MatchString(r.ID) {
				return nil, &Error{Provider: "acoustid", Code: "malformed"}
			}
			id := strings.ToLower(r.ID)
			if n, ok := seen[id]; ok {
				out[n].Score = max(out[n].Score, v.Score)
			} else {
				if len(out) >= 25 {
					return nil, &Error{Provider: "acoustid", Code: "malformed"}
				}
				seen[id] = len(out)
				out = append(out, AcousticMatch{id, v.Score})
			}
		}
	}
	return out, nil
}

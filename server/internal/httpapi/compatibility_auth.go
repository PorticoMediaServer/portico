package httpapi

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Foreign authentication is normalized only on declared compatibility routes.
// The native credential edge still resolves the credential, scopes, principal,
// request budget and revocation. Identical legacy/modern copies are tolerated;
// conflicting or malformed proofs become an ambiguous native header and fail.
func normalizeCompatibilityCredential(r *http.Request) *http.Request {
	out := r.Clone(r.Context())
	u := *r.URL
	out.URL = &u
	values := []string{}
	bad := false
	for _, h := range r.Header.Values("Authorization") {
		if strings.HasPrefix(h, "Bearer ") {
			values = append(values, strings.TrimPrefix(h, "Bearer "))
			continue
		}
		fields, ok := compatibilityAuthFields(h)
		if !ok {
			bad = true
			continue
		}
		if token := fields["token"]; token != "" {
			values = append(values, token)
		}
	}
	for _, name := range []string{"X-Emby-Token", "X-MediaBrowser-Token"} {
		values = append(values, r.Header.Values(name)...)
		out.Header.Del(name)
	}
	for _, h := range r.Header.Values("X-Emby-Authorization") {
		fields, ok := compatibilityAuthFields(h)
		if !ok {
			bad = true
		} else if fields["token"] != "" {
			values = append(values, fields["token"])
		}
	}
	out.Header.Del("X-Emby-Authorization")
	query := u.Query()
	for key, vs := range query {
		if strings.EqualFold(key, "api_key") || strings.EqualFold(key, "apikey") {
			values = append(values, vs...)
			query.Del(key)
		}
	}
	out.URL.RawQuery = query.Encode()
	out.RequestURI = out.URL.RequestURI()
	out.Header.Del("Authorization")
	secret := ""
	for _, v := range values {
		if v == "" || len(v) > 256 || strings.ContainsAny(v, " \t\r\n") {
			bad = true
		}
		if secret != "" && secret != v {
			bad = true
		}
		secret = v
	}
	if bad {
		out.Header.Add("Authorization", "Bearer invalid.proof")
		out.Header.Add("Authorization", "Bearer ambiguous.proof")
	} else if secret != "" {
		out.Header.Set("Authorization", "Bearer "+secret)
	}
	return out
}
func compatibilityAuthFields(h string) (map[string]string, bool) {
	fields := map[string]string{}
	space := strings.IndexByte(h, ' ')
	if space < 0 || !strings.EqualFold(h[:space], "MediaBrowser") {
		return fields, false
	}
	rest := strings.TrimSpace(h[space+1:])
	for rest != "" {
		eq := strings.IndexByte(rest, '=')
		if eq < 1 {
			return fields, false
		}
		key := strings.ToLower(strings.TrimSpace(rest[:eq]))
		rest = strings.TrimSpace(rest[eq+1:])
		value := ""
		if strings.HasPrefix(rest, "\"") {
			end := 1
			escaped := false
			for ; end < len(rest); end++ {
				if rest[end] == '"' && !escaped {
					break
				}
				if rest[end] == '\\' && !escaped {
					escaped = true
				} else {
					escaped = false
				}
			}
			if end >= len(rest) {
				return fields, false
			}
			var err error
			value, err = strconv.Unquote(rest[:end+1])
			if err != nil {
				return fields, false
			}
			rest = strings.TrimSpace(rest[end+1:])
		} else {
			end := strings.IndexByte(rest, ',')
			if end < 0 {
				end = len(rest)
			}
			value = strings.TrimSpace(rest[:end])
			rest = strings.TrimSpace(rest[end:])
		}
		if _, exists := fields[key]; exists {
			return fields, false
		}
		fields[key] = value
		if rest == "" {
			break
		}
		if rest[0] != ',' {
			return fields, false
		}
		rest = strings.TrimSpace(rest[1:])
	}
	return fields, true
}

// Compatibility query keys are case-insensitive. Duplicate spellings are
// rejected rather than letting two layers interpret different values.
func compatibilityQuery(r *http.Request) (url.Values, bool) {
	out := url.Values{}
	for key, values := range r.URL.Query() {
		key = strings.ToLower(key)
		if _, ok := out[key]; ok || len(values) != 1 {
			return nil, false
		}
		out[key] = values
	}
	return out, true
}

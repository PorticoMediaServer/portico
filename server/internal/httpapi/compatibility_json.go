package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"sort"
)

// A media-source array can span native pages. It is written a page at a time,
// just like the enclosing item list, rather than being silently truncated or
// retained as a potentially unbounded per-item array.
type compatibilitySourcePages struct {
	first  []any
	cursor string
	fetch  func(string) (map[string]any, error)
}

func (p compatibilitySourcePages) writeJSON(w io.Writer) error {
	if _, err := io.WriteString(w, "["); err != nil {
		return err
	}
	values, cursor := p.first, p.cursor
	emitted := false
	for {
		for _, v := range values {
			if emitted {
				if _, err := io.WriteString(w, ","); err != nil {
					return err
				}
			}
			if err := writeCompatibilityJSON(w, v); err != nil {
				return err
			}
			emitted = true
		}
		if cursor == "" {
			break
		}
		page, err := p.fetch(cursor)
		if err != nil {
			return err
		}
		next := compatText(page["nextCursor"])
		if next == cursor {
			return io.ErrNoProgress
		}
		cursor = next
		values = foreignMediaSources(page["mediaSources"])
	}
	_, err := io.WriteString(w, "]")
	return err
}
func (d Dependencies) compatibilitySources(r *http.Request, id string, first any, cursor string) any {
	return compatibilitySourcePages{first: foreignMediaSources(first), cursor: cursor, fetch: func(cursor string) (map[string]any, error) {
		return d.integrationCall(r, "GET", "/v1/integrations/items/"+url.PathEscape(id)+"/media-info", url.Values{"cursor": {cursor}, "limit": {"16"}}, nil)
	}}
}
func writeCompatibilityJSON(w io.Writer, v any) error {
	switch value := v.(type) {
	case compatibilitySourcePages:
		return value.writeJSON(w)
	case map[string]any:
		if _, err := io.WriteString(w, "{"); err != nil {
			return err
		}
		keys := make([]string, 0, len(value))
		for key := range value {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for i, key := range keys {
			if i > 0 {
				if _, err := io.WriteString(w, ","); err != nil {
					return err
				}
			}
			raw, _ := json.Marshal(key)
			if _, err := w.Write(raw); err != nil {
				return err
			}
			if _, err := io.WriteString(w, ":"); err != nil {
				return err
			}
			if err := writeCompatibilityJSON(w, value[key]); err != nil {
				return err
			}
		}
		_, err := io.WriteString(w, "}")
		return err
	case []any:
		if _, err := io.WriteString(w, "["); err != nil {
			return err
		}
		for i, item := range value {
			if i > 0 {
				if _, err := io.WriteString(w, ","); err != nil {
					return err
				}
			}
			if err := writeCompatibilityJSON(w, item); err != nil {
				return err
			}
		}
		_, err := io.WriteString(w, "]")
		return err
	default:
		return json.NewEncoder(w).Encode(v)
	}
}

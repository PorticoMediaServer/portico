package operations

import (
	"bytes"
	"context"
	"database/sql"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ExportIncludePaths is an explicit per-export choice, never a saved default.
type ExportIncludePaths bool

func (ExportIncludePaths) JSONSchema() map[string]any { return map[string]any{"type": "boolean"} }
func (v *ExportIncludePaths) UnmarshalJSON(raw []byte) error {
	switch string(bytes.TrimSpace(raw)) {
	case "true":
		*v = true
	case "false":
		*v = false
	default:
		return ErrInvalid
	}
	return nil
}

type ExportPathCategory string

func (ExportPathCategory) JSONSchema() map[string]any {
	return map[string]any{"type": "string", "enum": []string{"library-configured-root", "library-resolved-root", "managed-mount-point"}}
}

type ExportPathFact struct {
	Category  ExportPathCategory `json:"category"`
	Reference string             `json:"reference"`
	Value     string             `json:"value"`
}
type ExportPathFacts struct {
	Limit int `json:"limit"`
	// HasMore means a 129th qualifying fact was actually examined.
	HasMore    bool `json:"hasMore"`
	ScanBudget int  `json:"scanBudget"`
	Examined   int  `json:"examined"`
	// ScanTruncated means the candidate budget ended; remaining qualifying facts are unknown.
	ScanTruncated bool `json:"scanTruncated"`
	// InvalidAmongExamined counts only invalid local path values we actually examined.
	InvalidAmongExamined int              `json:"invalidAmongExamined"`
	Items                []ExportPathFact `json:"items"`
}

const exportPathLimit = 128
const exportPathScanBudget = 512

// Each read walks the existing primary-key id index with a hard LIMIT. Source
// kind is classified inside CASE, so remote locators are never selected. The
// shared candidate budget bounds all three categories, including invalid rows.
// The caller invokes this only after authorized receipt lookup.
func readExportPathFacts(ctx context.Context, tx *sql.Tx) (*ExportPathFacts, error) {
	out := &ExportPathFacts{Limit: exportPathLimit, ScanBudget: exportPathScanBudget, Items: []ExportPathFact{}}
	queries := []struct {
		category ExportPathCategory
		sql      string
	}{
		{"library-configured-root", `SELECT CASE WHEN kind='local' THEN substr(configured_root,1,4097) ELSE NULL END FROM library_sources ORDER BY id LIMIT ?`},
		{"library-resolved-root", `SELECT CASE WHEN kind='local' THEN substr(root,1,4097) ELSE NULL END FROM library_sources ORDER BY id LIMIT ?`},
		{"managed-mount-point", `SELECT substr(mount_path,1,4097) FROM managed_mounts ORDER BY id LIMIT ?`},
	}
	for _, query := range queries {
		remaining := exportPathScanBudget - out.Examined
		if remaining == 0 {
			out.ScanTruncated = true
			return out, nil
		}
		rows, err := tx.QueryContext(ctx, query.sql, remaining)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var candidate sql.NullString
			if err = rows.Scan(&candidate); err != nil {
				rows.Close()
				return nil, err
			}
			out.Examined++
			if !candidate.Valid {
				continue
			}
			value := candidate.String
			if len(value) > 4096 || !utf8.ValidString(value) || !filepath.IsAbs(value) || strings.IndexFunc(value, unicode.IsControl) >= 0 {
				out.InvalidAmongExamined++
				continue
			}
			if len(out.Items) == exportPathLimit {
				out.HasMore = true
				rows.Close()
				return out, nil
			}
			out.Items = append(out.Items, ExportPathFact{query.category, "path-" + strconv.Itoa(len(out.Items)+1), value})
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		if out.Examined == exportPathScanBudget {
			// We deliberately do not issue an unbudgeted look-ahead. The budget's
			// exact exhaustion conservatively leaves the remainder unknown.
			out.ScanTruncated = true
			return out, nil
		}
	}
	return out, nil
}

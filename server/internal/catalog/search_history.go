package catalog

import (
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
)

// SearchHistoryLimit is the retained depth. Older entries fall off the tail on
// every accepted record, so the table never grows with query volume.
const SearchHistoryLimit = 20

type SearchHistoryEntry struct {
	Query     string `json:"query"`
	UpdatedAt string `json:"updatedAt"`
}
type SearchHistoryPage struct {
	Remembered bool                 `json:"remembered"`
	Entries    []SearchHistoryEntry `json:"entries"`
}

// RemembersSearchHistory reads the viewer's own effective preference. The search
// surface never infers this; the registry is the only authority.
func (s *Service) RemembersSearchHistory(v identity.Viewer) (bool, error) {
	values, _, _, e := operations.EffectivePreferences(s.read(), v, "")
	if e != nil {
		return false, e
	}
	return values.Bool("search.rememberHistory"), nil
}

// RecordSearch keeps one normalized entry per distinct query. A viewer who has
// turned remembering off records nothing, and reads nothing back.
func (s *Service) RecordSearch(v identity.Viewer, query string) error {
	remembered, e := s.RemembersSearchHistory(v)
	if e != nil || !remembered {
		return e
	}
	query = strings.Join(strings.Fields(query), " ")
	if query == "" {
		return nil
	}
	profile := identity.PersonalKey(v)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, e = dbwork.ExecWrite(s.Context(), s.db, dbwork.ClassFrom(s.Context(), dbwork.ClassInteractive), `INSERT INTO search_history SELECT ?,?,?,? WHERE NOT EXISTS(SELECT 1 FROM direct_profiles WHERE account_id=? AND id=? AND deleted=1 AND ?='local') ON CONFLICT(profile_key,normalized) DO UPDATE SET query=excluded.query,updated_at=excluded.updated_at`, profile, strings.ToLower(query), query, now, v.AccountID, v.ProfileID, v.Authority); e != nil {
		return e
	}
	_, e = dbwork.ExecWrite(s.Context(), s.db, dbwork.ClassFrom(s.Context(), dbwork.ClassInteractive), `DELETE FROM search_history WHERE profile_key=? AND normalized NOT IN(SELECT normalized FROM search_history WHERE profile_key=? ORDER BY updated_at DESC,normalized LIMIT ?)`, profile, profile, SearchHistoryLimit)
	return e
}

// SearchHistory returns the retained queries, newest first. With remembering off
// the list is empty regardless of what an earlier session left behind.
func (s *Service) SearchHistory(v identity.Viewer) (SearchHistoryPage, error) {
	out := SearchHistoryPage{Entries: []SearchHistoryEntry{}}
	remembered, e := s.RemembersSearchHistory(v)
	if e != nil {
		return out, e
	}
	out.Remembered = remembered
	if !remembered {
		return out, nil
	}
	rows, e := s.read().Query(`SELECT query,updated_at FROM search_history WHERE profile_key=? ORDER BY updated_at DESC,normalized LIMIT ?`, identity.PersonalKey(v), SearchHistoryLimit)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		var row SearchHistoryEntry
		if e = rows.Scan(&row.Query, &row.UpdatedAt); e != nil {
			return out, e
		}
		out.Entries = append(out.Entries, row)
	}
	return out, rows.Err()
}

// DeleteSearchHistory removes the one retained query whose canonical identity the
// viewer named: the same whitespace-collapsed, lowercased key RecordSearch stores, so
// no array position or name substring decides which row goes. It answers with the
// page the server now holds.
func (s *Service) DeleteSearchHistory(v identity.Viewer, query string) (SearchHistoryPage, error) {
	normalized := strings.ToLower(strings.Join(strings.Fields(query), " "))
	if normalized != "" {
		if _, e := dbwork.ExecWrite(s.Context(), s.db, dbwork.ClassFrom(s.Context(), dbwork.ClassInteractive),
			`DELETE FROM search_history WHERE profile_key=? AND normalized=?`, identity.PersonalKey(v), normalized); e != nil {
			return SearchHistoryPage{Entries: []SearchHistoryEntry{}}, e
		}
	}
	return s.SearchHistory(v)
}

// ClearSearchHistory removes every retained query for this viewer.
func (s *Service) ClearSearchHistory(v identity.Viewer) (SearchHistoryPage, error) {
	if _, e := dbwork.ExecWrite(s.Context(), s.db, dbwork.ClassFrom(s.Context(), dbwork.ClassInteractive), `DELETE FROM search_history WHERE profile_key=?`, identity.PersonalKey(v)); e != nil {
		return SearchHistoryPage{Entries: []SearchHistoryEntry{}}, e
	}
	return s.SearchHistory(v)
}

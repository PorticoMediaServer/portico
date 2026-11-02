package catalog

import (
	"encoding/json"
	"errors"
	"strings"
)

// Continue your books (Q24, DESIGN.md:158) is one row for books and audiobooks
// in progress: an audiobook from its listening activity, an ebook, comic or
// magazine from the Read service's own Continue (the canonical reading
// resume: latest saved version, unfinished, current publication). Both are
// ordered by when they were last used, newest first.

// ReadingResume is one title of the Read service's Continue, as Home needs it.
type ReadingResume struct {
	ItemID     string
	LibraryID  string
	ObservedAt int64
	Progress   ReadingProgress
}

// ReadingProgress is a reading title's latest saved position for this profile:
// the fraction read and, for paged formats, the zero-based page. An EPUB's
// saved section is a package path, not a chapter name, so it is not carried.
// Stale means the file changed since: the position is progress only, never a
// current place. Nothing is estimated.
type ReadingProgress struct {
	Progression float64 `json:"progression"`
	PageIndex   *int64  `json:"pageIndex,omitempty"`
	Stale       bool    `json:"stale,omitempty"`
}

// homeReadingResumes is the viewer's reading Continue within Home's libraries.
func (s *Service) homeReadingResumes(r HomeRequest, libraries []string) ([]ReadingResume, error) {
	if r.ReadingContinue == nil || r.ReadingPersonalAuthorize == nil || len(libraries) == 0 {
		return nil, nil
	}
	resumes, err := r.ReadingContinue()
	if err != nil {
		return nil, err
	}
	allowed := map[string]bool{}
	for _, library := range libraries {
		allowed[library] = true
	}
	out := make([]ReadingResume, 0, len(resumes))
	for _, resume := range resumes {
		if allowed[resume.LibraryID] {
			out = append(out, resume)
		}
	}
	return out, nil
}

// homeBooksSource is the row's candidate relation over the viewer's audiobook
// libraries (r.Libraries) and their book and comic libraries (reading).
func (s *Service) homeBooksSource(r HomeRequest, reading []string) (homeSource, error) {
	parts, args := []string{}, []any{}
	if len(r.Libraries) > 0 {
		visible, bound := r.Viewer.itemVisibilitySQL("candidate.entity")
		// Activity times are UTC text with milliseconds; ord is milliseconds so
		// both kinds of title share one order.
		parts = append(parts, `SELECT pid(parent.public_id) AS id,printf('%015d',CAST(round((julianday(max(candidate.ord))-2440587.5)*86400000) AS INTEGER)) AS ord FROM (`+homeContinueBase("continue_listening")+`) candidate
 CROSS JOIN catalog_entities i ON i.public_id=pid_blob(candidate.id)
 JOIN catalog_book_files bf ON bf.entity_id=i.id
 JOIN catalog_entities parent ON parent.id=bf.book_id
 WHERE `+visible+` AND i.kind=9 GROUP BY parent.id`)
		args = append(append(args, r.Profile, r.Profile, idsJSON(r.Libraries)), bound...)
	}
	resumes, err := s.homeReadingResumes(r, reading)
	if err != nil {
		return homeSource{}, err
	}
	if len(resumes) > 0 {
		type candidate struct {
			ID string `json:"id"`
			At int64  `json:"at"`
		}
		list := make([]candidate, 0, len(resumes))
		for _, resume := range resumes {
			list = append(list, candidate{resume.ItemID, resume.ObservedAt})
		}
		raw, _ := json.Marshal(list)
		parts = append(parts, `SELECT json_extract(value,'$.id') AS id,printf('%015d',json_extract(value,'$.at')) AS ord FROM json_each(?)`)
		args = append(args, string(raw))
	}
	if len(parts) == 0 {
		return candidateSource(nil), nil
	}
	return homeSource{base: `SELECT id,max(ord) AS ord FROM (` + strings.Join(parts, " UNION ALL ") + `) GROUP BY id`, args: args, selfRestricted: true}, nil
}

// homeBookEntries hydrates a Continue your books page in order: audiobooks
// through the listening projection, reading titles through Home's Reading
// projection, each carrying the saved position the Read service reported.
func (s *Service) homeBookEntries(r HomeRequest, ids []string) ([]ContentEntry, error) {
	if len(ids) == 0 {
		return []ContentEntry{}, nil
	}
	kinds := map[string]int{}
	rows, err := s.read().Query(`SELECT requested.value,e.kind FROM json_each(?) requested CROSS JOIN catalog_entities e ON e.public_id=pid_blob(requested.value)`, idsJSON(ids))
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		var kind int
		if err = rows.Scan(&id, &kind); err != nil {
			break
		}
		kinds[id] = kind
	}
	if e := rows.Err(); err == nil {
		err = e
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	audio, reading := []string{}, []string{}
	for _, id := range ids {
		if kinds[id] == 20 || kinds[id] == 21 {
			reading = append(reading, id)
		} else {
			audio = append(audio, id)
		}
	}
	byID := map[string]ContentEntry{}
	if len(audio) > 0 {
		entries, err := s.homeEngineEntries(r.Profile, audio)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			byID[entry.ID] = entry
		}
	}
	if len(reading) > 0 {
		projected, err := s.homeReadingPage(r.Viewer, reading, r.ReadingPersonalAuthorize)
		if errors.Is(err, ErrReadingProjectionChanged) {
			// A Reading library being rebuilt (a scan) leaves its titles out of
			// this page rather than failing Home; the next read has them.
			projected, err = map[string]ContentEntry{}, nil
		}
		if err != nil {
			return nil, err
		}
		resumes, err := s.homeReadingResumes(r, r.Libraries)
		if err != nil {
			return nil, err
		}
		progress := map[string]ReadingProgress{}
		for _, resume := range resumes {
			progress[resume.ItemID] = resume.Progress
		}
		for id, entry := range projected {
			if p, ok := progress[id]; ok {
				entry.ReadingProgress = &p
			}
			byID[id] = entry
		}
	}
	out := make([]ContentEntry, 0, len(ids))
	for _, id := range ids {
		if entry, ok := byID[id]; ok {
			out = append(out, entry)
		}
	}
	return out, nil
}

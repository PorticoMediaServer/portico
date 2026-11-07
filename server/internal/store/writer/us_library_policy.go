package writer

import (
	"database/sql"
	"encoding/json"
	"io"
	"slices"
	"strings"
)

// Match the existing direct account/profile raw-input envelope, independently
// of v1's smaller library cap. Neither limit is a limit on server users.
const usMaxPolicyLibraries = 1000
const usLibraryPolicyByteLimit = 2 + usMaxPolicyLibraries*(128+3)

type usLibraryAccess struct {
	all       bool
	libraries []string
}

// This is pure interpretation of CURRENT snapshot facts, never authentication,
// a reusable grant or evidence that a library exists. The owning reader/writer
// must first authenticate the current lifetimes, reserve the ACTUAL native
// octet_length preflight bytes in the same snapshot and admit decoder/map/output
// memory before calling. No cached policy can replace that current check.
func decodeUSLibraryAccess(role, memberJSON string, profileJSON sql.NullString) (usLibraryAccess, error) {
	if role != "owner" && role != "admin" && role != "member" {
		return usLibraryAccess{}, ErrDisclosureDenied
	}
	members, err := decodeUSLibraryIDs(memberJSON)
	if err != nil {
		return usLibraryAccess{}, err
	}
	all := role == "owner" || role == "admin"
	if !profileJSON.Valid {
		return usLibraryAccess{all: all, libraries: members}, nil
	}
	profiles, err := decodeUSLibraryIDs(profileJSON.String)
	if err != nil {
		return usLibraryAccess{}, err
	}
	// Explicit profile policies always narrow. SQL NULL inherits, while JSON
	// [] (or the retained JSON null spelling) is an explicit empty allow-list.
	if all {
		return usLibraryAccess{libraries: profiles}, nil
	}
	allowed := make(map[string]struct{}, len(members))
	for _, id := range members {
		allowed[id] = struct{}{}
	}
	intersection := make([]string, 0, min(len(members), len(profiles)))
	for _, id := range profiles {
		if _, ok := allowed[id]; ok {
			intersection = append(intersection, id)
		}
	}
	return usLibraryAccess{libraries: intersection}, nil
}

func decodeUSLibraryIDs(raw string) ([]string, error) {
	// A refusal happens before the JSON copy, decoder or collection allocation.
	// This is a hostile-value acceptance bound, not a memory reservation based
	// on the declared maximum of a database column.
	if len(raw) > usLibraryPolicyByteLimit {
		return nil, ErrDisclosureDenied
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	first, err := decoder.Token()
	if err != nil {
		return nil, ErrDisclosureDenied
	}
	if first == nil {
		if decoder.Decode(&struct{}{}) != io.EOF {
			return nil, ErrDisclosureDenied
		}
		return nil, nil
	}
	if first != json.Delim('[') {
		return nil, ErrDisclosureDenied
	}
	var ids []string
	seen := make(map[string]struct{})
	for decoder.More() {
		// Stop before allocating a 1,001st value, even for an array of tiny IDs.
		if len(ids) == usMaxPolicyLibraries {
			return nil, ErrDisclosureDenied
		}
		var id string
		if err := decoder.Decode(&id); err != nil || !usIdentifier(id) {
			return nil, ErrDisclosureDenied
		}
		if _, duplicate := seen[id]; duplicate {
			return nil, ErrDisclosureDenied
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	close, err := decoder.Token()
	if err != nil || close != json.Delim(']') || decoder.Decode(&struct{}{}) != io.EOF {
		return nil, ErrDisclosureDenied
	}
	return ids, nil
}

func (p usLibraryAccess) allows(library string) bool {
	return usIdentifier(library) && (p.all || slices.Contains(p.libraries, library))
}

func usIdentifier(v string) bool {
	if len(v) < 1 || len(v) > 128 {
		return false
	}
	for _, c := range v {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

package catalog

import "database/sql"

// homeExplanationCandidates removes only works that cannot have a positive
// personal weight. Keep all editions of a touched work: its canonical display
// entity and negative signals may come from a different candidate. The caller's
// bounded pool, ordering and recBase eligibility checks remain authoritative.
func (s *Service) homeExplanationCandidates(profile string, candidates []string) ([]string, error) {
	if len(candidates) == 0 {
		return candidates, nil
	}
	var inherited bool
	if err := s.read().QueryRow(`SELECT EXISTS(SELECT 1 FROM container_personal_state WHERE profile_id=? LIMIT 1)`, profile).Scan(&inherited); err != nil {
		return nil, err
	}
	// Container defaults can supply weight without any item-personal row. Keep
	// the original bounded relation whenever that inheritance is possible.
	if inherited {
		return candidates, nil
	}
	rows, err := s.read().Query(homeExplanationFactsSQL, idsJSON(candidates), profile, profile, profile)
	if err != nil {
		return nil, err
	}
	type fact struct{ id, work string }
	facts := make([]fact, 0, len(candidates))
	touched := make(map[string]bool)
	for rows.Next() {
		var id string
		var work sql.NullString
		var personal bool
		if err = rows.Scan(&id, &work, &personal); err != nil {
			break
		}
		if !work.Valid {
			continue
		}
		facts = append(facts, fact{id, work.String})
		if personal {
			touched[work.String] = true
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(facts))
	for _, f := range facts {
		if touched[f.work] {
			out = append(out, f.id)
		}
	}
	return out, nil
}

var homeExplanationFactsSQL = `SELECT candidate.value,` + workKeySQL("i") + `,
 EXISTS(SELECT 1 FROM personal_items p WHERE p.profile_id=?2 AND p.item_id=i.id)
 OR EXISTS(SELECT 1 FROM progress pg WHERE pg.profile_id=?3 AND pg.item_id=i.id)
 OR EXISTS(SELECT 1 FROM personal_watched_intents wi WHERE wi.profile_id=?4 AND wi.item_id=i.id)
 FROM json_each(?1) candidate CROSS JOIN catalog_entities i ON i.public_id=pid_blob(candidate.value)`

package catalog

// workspaceEpisodeCredits reads only the already-authorized episode selected by
// workspaceTarget. Item-scoped credits cannot be supplied by a hidden sibling
// episode. Portraits use the same canonical visible selection as the person
// route, so their version identifies the bytes that route will serve.
func (s *Service) workspaceEpisodeCredits(item string, viewer Viewer) ([]ShowCredit, error) {
	rows, err := s.read().Query(`SELECT p.token,p.name,role.label,dept.label,pc.ord
		FROM catalog_credits pc JOIN catalog_people p ON p.id=pc.person_id
		JOIN catalog_credit_labels role ON role.id=pc.role_id
		JOIN catalog_credit_labels dept ON dept.id=pc.department_id
		WHERE pc.entity_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?))
		ORDER BY CASE WHEN dept.label IN('Acting','Cast') THEN 0 ELSE 1 END,pc.ord,p.name,p.token`, item)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	credits := []ShowCredit{}
	ids := []string{}
	for rows.Next() {
		var credit ShowCredit
		if err = rows.Scan(&credit.ID, &credit.Name, &credit.Role, &credit.Department, &credit.Ordinal); err != nil {
			return nil, err
		}
		ids = append(ids, credit.ID)
		credits = append(credits, credit)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	portraits, err := s.portraitSelections(ids, viewer)
	if err != nil {
		return nil, err
	}
	for i := range credits {
		credits[i].PortraitURL = portraits[credits[i].ID].url()
	}
	return credits, nil
}

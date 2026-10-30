package catalog

import "database/sql"

type ReadingSaved struct {
	ItemID   string `json:"itemId"`
	Listed   bool   `json:"listed"`
	Revision int64  `json:"revision"`
}

func (s *Service) ReadingSaved(profile, item string, authorize func(*sql.Tx) error) (ReadingSaved, error) {
	state, err := s.readingPersonal(profile, item, authorize)
	if err != nil {
		return ReadingSaved{}, err
	}
	return ReadingSaved{ItemID: item, Listed: state.Watchlisted, Revision: state.Revision}, nil
}

// This shares the normal personal CAS/receipt domain with the explicit mark,
// but can author only membership in My List. No progress or history is changed.
func (s *Service) SetReadingSaved(account, profile, item, operation string, revision int64, listed bool, authorize func(*sql.Tx) error) (ReadingSaved, error) {
	if authorize == nil {
		return ReadingSaved{}, sql.ErrNoRows
	}
	state, err := s.setPersonal(account, profile, item, PersonalMutation{OperationID: operation, ExpectedRevision: revision, Watchlisted: &listed}, authorize, true)
	if err != nil {
		return ReadingSaved{}, err
	}
	return ReadingSaved{ItemID: item, Listed: state.Watchlisted, Revision: state.Revision}, nil
}

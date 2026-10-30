package catalog

import "database/sql"

type ReadingFavorite struct {
	ItemID   string `json:"itemId"`
	Favorite bool   `json:"favorite"`
	Revision int64  `json:"revision"`
}

func (s *Service) ReadingFavorite(profile, item string, authorize func(*sql.Tx) error) (ReadingFavorite, error) {
	state, err := s.readingPersonal(profile, item, authorize)
	if err != nil {
		return ReadingFavorite{}, err
	}
	return ReadingFavorite{ItemID: item, Favorite: state.Favorite, Revision: state.Revision}, nil
}

// This shares the normal personal CAS/receipt domain with the explicit mark,
// but can author only the Favorite flag. No progress or history is changed.
func (s *Service) SetReadingFavorite(account, profile, item, operation string, revision int64, favorite bool, authorize func(*sql.Tx) error) (ReadingFavorite, error) {
	if authorize == nil {
		return ReadingFavorite{}, sql.ErrNoRows
	}
	state, err := s.setPersonal(account, profile, item, PersonalMutation{OperationID: operation, ExpectedRevision: revision, Favorite: &favorite}, authorize, true)
	if err != nil {
		return ReadingFavorite{}, err
	}
	return ReadingFavorite{ItemID: item, Favorite: state.Favorite, Revision: state.Revision}, nil
}

package identity

import (
	"context"

	"golang.org/x/crypto/bcrypt"
)

// HashPassword uses the same two hashing slots as setup, sign-in and account
// changes. Call it outside any database transaction or snapshot.
func (s *Service) HashPassword(ctx context.Context, password string) ([]byte, error) {
	return s.hashPassword(ctx, password, bcrypt.GenerateFromPassword)
}

func (s *Service) hashPassword(ctx context.Context, password string, generate func([]byte, int) ([]byte, error)) ([]byte, error) {
	if !ValidDirectPassword(password) {
		return nil, ErrDirectInput
	}
	if err := s.takeHashSlot(ctx); err != nil {
		return nil, err
	}
	defer func() { <-s.hashSlots }()
	return generate([]byte(password), PasswordCost)
}

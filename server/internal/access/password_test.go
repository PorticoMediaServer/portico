package access

import (
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"golang.org/x/crypto/bcrypt"
)

// HashPassword applies the same cost and policy the direct identity service uses
// for a member password, so an invited account is indistinguishable from one the
// owner created by hand.
func HashPassword(password string) ([]byte, error) {
	if !identity.ValidDirectPassword(password) {
		return nil, invalid("password")
	}
	return bcrypt.GenerateFromPassword([]byte(password), identity.PasswordCost)
}

package operations

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestLocalAccountOptionsExcludeDisabledAndRequireOwner(t *testing.T) {
	s, _, auth := consoleFixture(t)
	_, err := s.DB.Exec(`INSERT INTO accounts(id,username,password_hash,profile_id) VALUES('member','Alice',X'00','member-primary'),('disabled','Disabled',X'00','disabled-primary');
 UPDATE direct_memberships SET disabled=1 WHERE account_id='disabled'`)
	if err != nil {
		t.Fatal(err)
	}
	options, err := s.LocalAccountOptions(context.Background(), auth)
	if err != nil || len(options) != 2 || options[0].Label != "Alice" || options[1].ID != "owner" {
		t.Fatalf("accounts: %+v %v", options, err)
	}
	denied := errors.New("denied")
	if _, err = s.LocalAccountOptions(context.Background(), func(context.Context, *sql.Tx, string) error { return denied }); !errors.Is(err, denied) {
		t.Fatal("account directory bypassed authority", err)
	}
}

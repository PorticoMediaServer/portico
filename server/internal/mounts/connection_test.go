package mounts

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func TestUnsavedRcloneConnectionRequiresOwnerBeforeCandidateOrProgram(t *testing.T) {
	s, input := fixture(t)
	prepared := 0
	s.PrepareSourceCandidate = func(context.Context, SourceCandidateInput, func(*sql.Tx) error) (*SourceCandidatePublication, error) {
		prepared++
		return nil, errors.New("must not prepare")
	}
	_, err := s.TestRcloneConnection(context.Background(), RcloneConnectionInput{Remote: input.Remote, Config: input.Config}, func(*sql.Tx) error { return identity.ErrUnauthorized })
	if !errors.Is(err, identity.ErrUnauthorized) || prepared != 0 {
		t.Fatal("owner refusal reached candidate admission", err, prepared)
	}
	for _, table := range []string{"managed_mounts", "mount_backend_configs", "mount_operations"} {
		var count int
		if err := s.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&count); err != nil || count != 0 {
			t.Fatal("connection test published durable state", table, count, err)
		}
	}
}

func TestUnsavedRcloneConnectionRejectsCommandConfigurationBeforeCandidate(t *testing.T) {
	s, _ := fixture(t)
	prepared := false
	s.PrepareSourceCandidate = func(context.Context, SourceCandidateInput, func(*sql.Tx) error) (*SourceCandidatePublication, error) {
		prepared = true
		return nil, ErrConfigInvalid
	}
	_, err := s.TestRcloneConnection(context.Background(), RcloneConnectionInput{Remote: "cloud:", Config: "[cloud]\ntype=sftp\nserver_command=private-command\n"}, func(*sql.Tx) error { return nil })
	if !errors.Is(err, ErrConfigInvalid) || prepared {
		t.Fatal("invalid command configuration reached candidate", err, prepared)
	}
}

func TestPublishedConfigurationTypesCannotWidenImportValidation(t *testing.T) {
	kinds := SupportedConfigurationKinds()
	if len(kinds) == 0 {
		t.Fatal("missing configuration type domain")
	}
	kinds[0] = "local"
	if err := validateConfig("[cloud]\ntype=local\n", "cloud:"); err == nil {
		t.Fatal("caller list mutation widened configuration access")
	}
	if err := validateConfig("[cloud]\ntype=s3\nenv_auth=true\n", "cloud:"); err == nil {
		t.Fatal("published type replaced field-level configuration guard")
	}
}

package metadata

import (
	"context"
	"database/sql"
	"github.com/PorticoMediaServer/portico/server/internal/requestinput"
)

// ErrLibraryLanguageInput answers a metadata language the library's agent does
// not offer: a code outside the agent's Languages, any language with the local
// agent, or any language for music and audiobook libraries.
var ErrLibraryLanguageInput = requestinput.New("Choose a metadata language offered for this library.")

// ApplyLibraryLanguageTx records the library's metadata language inside the
// caller's transaction, keeping the same two columns ApplyAdministrationPolicyTx
// keeps in step: screen_metadata_policies.language and the TMDB provider
// policy's language. It re-checks the library's kind and agent from the
// database so it is safe on its own, and returns ErrLibraryLanguageInput when
// the language is not offered. An empty language changes nothing.
func ApplyLibraryLanguageTx(ctx context.Context, tx *sql.Tx, library, language string) error {
	if language == "" {
		return nil
	}
	var kind, agent string
	if err := tx.QueryRowContext(ctx, `SELECT l.kind,a.agent FROM libraries l JOIN library_metadata_agents a ON a.library_id=l.id WHERE l.id=?`, library).Scan(&kind, &agent); err != nil {
		return err
	}
	if !LibraryLanguageOffered(kind, agent, language) {
		return ErrLibraryLanguageInput
	}
	if _, err := tx.ExecContext(ctx, `UPDATE screen_metadata_policies SET language=?,revision=revision+1 WHERE library_id=?`, language, library); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE metadata_provider_policies SET language=? WHERE library_id=? AND provider='tmdb'`, language, library); err != nil {
		return err
	}
	return nil
}

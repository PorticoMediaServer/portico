package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/wirejson"
)

// ExpectedRemoteSource binds an explicit folder selection to the remote
// connection the owner saw. It adds no authority to read the path itself.
type ExpectedRemoteSource struct {
	SourceID   string `json:"sourceId"`
	Generation int64  `json:"generation"`
}

// ExpectedRemoteSourceInput is a non-pointer, empty-omitted input. Only omission
// has the zero value: JSON null cannot collapse into the legacy nil fence.
// The small inner object has its own exact-name/duplicate checks; parent body
// parsing retains the API registry's strict decoding unchanged.
type ExpectedRemoteSourceInput json.RawMessage

func (ExpectedRemoteSourceInput) JSONSchema() map[string]any {
	return map[string]any{
		"type": "object", "additionalProperties": false,
		"required": []string{"sourceId", "generation"},
		"properties": map[string]any{
			"sourceId":   map[string]any{"type": "string", "minLength": 1, "maxLength": 256},
			"generation": map[string]any{"type": "integer", "minimum": 1, "maximum": 9007199254740991},
		},
	}
}

func parseExpectedRemoteSource(raw []byte) (*ExpectedRemoteSource, error) {
	fields, err := wirejson.Object(raw, "sourceId", "generation")
	if err != nil || len(fields) != 2 {
		return nil, ErrAdminQuery
	}
	var selected ExpectedRemoteSource
	if json.Unmarshal(raw, &selected) != nil || !validExpectedRemoteSource(&selected) {
		return nil, ErrAdminQuery
	}
	return &selected, nil
}

func (input *ExpectedRemoteSourceInput) UnmarshalJSON(raw []byte) error {
	if _, err := parseExpectedRemoteSource(raw); err != nil {
		return err
	}
	*input = append((*input)[:0], raw...)
	return nil
}

func (input ExpectedRemoteSourceInput) MarshalJSON() ([]byte, error) {
	if len(input) == 0 {
		return []byte("null"), nil
	}
	if _, err := parseExpectedRemoteSource(input); err != nil {
		return nil, err
	}
	return input, nil
}

func (input ExpectedRemoteSourceInput) Selection() (*ExpectedRemoteSource, error) {
	if len(input) == 0 {
		return nil, nil
	}
	return parseExpectedRemoteSource(input)
}

func validExpectedRemoteSource(expected *ExpectedRemoteSource) bool {
	return expected != nil && expected.SourceID != "" && len(expected.SourceID) <= 256 && utf8.ValidString(expected.SourceID) &&
		strings.IndexFunc(expected.SourceID, unicode.IsControl) < 0 && expected.Generation >= 1 && expected.Generation <= 9007199254740991
}

func checkExpectedRemoteSource(ctx context.Context, tx *sql.Tx, path string, expected *ExpectedRemoteSource) error {
	if expected == nil {
		return nil
	}
	if !validExpectedRemoteSource(expected) {
		return ErrAdminQuery
	}
	var id string
	var generation int64
	// Use the exact association expression the accepting INSERT/UPDATE uses,
	// in the same transaction. A new nearer prefix must not silently win.
	err := tx.QueryRowContext(ctx, `SELECT id,generation FROM remote_sources WHERE id=`+remoteSourceIDExpr, path, path).Scan(&id, &generation)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrSourceChanged
	}
	if err != nil {
		return err
	}
	if id != expected.SourceID || generation != expected.Generation {
		return ErrSourceChanged
	}
	return nil
}

// CreateFoldersExpectedSourceAuthorizedThen keeps the optional remote fence in
// the accepting library transaction, including its first and every later folder.
// Local callers retain the existing SourceGuard and nil-fence behavior.
func (s *Service) CreateFoldersExpectedSourceAuthorizedThen(ctx context.Context, name, kind string, folders []string, expected *ExpectedRemoteSource, authorize func(*sql.Tx) error, created func(*sql.Tx, Library) error) (Library, error) {
	if expected == nil {
		return s.CreateFoldersAuthorizedThen(ctx, name, kind, folders, authorize, created)
	}
	captured := *expected
	paths := append([]string(nil), folders...)
	return s.CreateFoldersAuthorizedThen(ctx, name, kind, paths, func(tx *sql.Tx) error {
		if authorize != nil {
			if err := authorize(tx); err != nil {
				return err
			}
		}
		for _, path := range paths {
			if err := checkExpectedRemoteSource(ctx, tx, path, &captured); err != nil {
				return err
			}
		}
		return nil
	}, created)
}

package livechannels

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"database/sql"
	"log"
	"os"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// sourceKeyFileName is the removed source key. It is only ever read once
// more, to migrate sealed source rows, and then deleted.
const sourceKeyFileName = "live-sources.v1.key"

// MigrateSealedToPlain is the one-time upgrade from the file-key era: sealed
// source URLs and credentials move to plain columns, and the key file is
// removed. Anything it cannot read is left for the next start; it never
// blocks startup. A row that fails to open under the old key is left sealed
// and fails at use, exactly as a wrong key did before.
// Startup-only: initializeLiveChannels runs before backup service creation.
func MigrateSealedToPlain(db *sql.DB, state string) {
	raw, err := os.ReadFile(storagepaths.Path(state, "keys", sourceKeyFileName))
	if err != nil || len(raw) != 32 {
		return
	}
	defer clear(raw)
	block, err := aes.NewCipher(raw)
	if err != nil {
		return
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return
	}
	ctx := context.Background()
	gated, err := dbwork.Begin(ctx, db, dbwork.ClassMaintenance)
	if err != nil {
		log.Printf("Sealed live-source migration deferred: %v", err)
		return
	}
	tx := gated.Tx()
	defer gated.Rollback()
	left := false
	// Durable rows (channel locators, remote configurations) keep the key while
	// any still doesn't open. A remote preview or save binding lives minutes;
	// one that doesn't open is dropped and the owner previews again.
	for _, table := range []struct {
		name, value, kind string
		ids               []string
		durable           bool
	}{
		{"live_channel_versions", "locator_sealed", "locator", []string{"generation_id", "channel_id"}, true},
		{"live_remote_configs", "sealed", "remote", []string{"source_id"}, true},
		{"live_remote_previews", "sealed", "preview", []string{"id"}, false},
		{"live_remote_save_bindings", "sealed", "remote-save", []string{"request_id"}, false},
	} {
		sealed, err := migrateSealedTable(tx, aead, table.name, table.ids, table.value, table.kind, table.durable)
		if err != nil {
			log.Printf("Sealed live-source migration (%s) deferred: %v", table.name, err)
			return
		}
		left = left || sealed
	}
	if err = gated.Commit(); err != nil {
		return
	}
	if left {
		// Removing the key would make those rows unreadable for good (O3 review).
		log.Printf("Some Live TV source rows don't open with %s; the key file is kept so a later start can migrate them", sourceKeyFileName)
		return
	}
	// Best effort: a leftover file is inert and migrates again next start.
	_ = os.Remove(storagepaths.Path(state, "keys", sourceKeyFileName))
}

// migrateSealedTable tries the old open on every row: success stores the
// plaintext, failure leaves the row for the next start and reports it (the
// key file must then stay). Locator rows bind channel and generation; the
// rest bind one id.
func migrateSealedTable(tx *sql.Tx, aead cipher.AEAD, table string, ids []string, value, kind string, durable bool) (bool, error) {
	var marker int
	if err := tx.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&marker); err != nil || marker == 0 {
		return false, err
	}
	// Identifiers come from the fixed table above, never from a caller.
	query := "SELECT "
	where := ""
	for i, id := range ids {
		if i > 0 {
			query += ","
			where += " AND "
		}
		query += `"` + id + `"`
		where += `"` + id + `"=?`
	}
	query += `,"` + value + `" FROM "` + table + `"`
	rows, err := tx.Query(query)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	type sealed struct {
		ids   []string
		value []byte
	}
	var pending []sealed
	for rows.Next() {
		var row sealed
		row.ids = make([]string, len(ids))
		holders := make([]any, len(ids)+1)
		for i := range row.ids {
			holders[i] = &row.ids[i]
		}
		holders[len(ids)] = &row.value
		if err = rows.Scan(holders...); err != nil {
			return false, err
		}
		pending = append(pending, row)
	}
	if err = rows.Err(); err != nil {
		return false, err
	}
	left := false
	for _, row := range pending {
		if len(row.value) == 0 {
			continue
		}
		aad := kind + ":" + row.ids[0]
		if kind == "locator" {
			aad = row.ids[1] + ":" + row.ids[0]
		}
		plain, err := openSealedValue(aead, row.value, aad)
		if err != nil {
			if plainLocator(row.value) {
				continue
			}
			if durable {
				left = true
				continue
			}
			keys := []any{}
			for _, id := range row.ids {
				keys = append(keys, id)
			}
			if _, err = tx.Exec(`DELETE FROM "`+table+`" WHERE `+where, keys...); err != nil {
				return false, err
			}
			continue
		}
		args := []any{plain}
		for _, id := range row.ids {
			args = append(args, id)
		}
		if _, err = tx.Exec(`UPDATE "`+table+`" SET "`+value+`"=? WHERE `+where, args...); err != nil {
			return false, err
		}
	}
	return left, nil
}

// openSealedValue opens one value in the old nonce-prefixed format.
func openSealedValue(aead cipher.AEAD, raw []byte, aad string) ([]byte, error) {
	n := aead.NonceSize()
	if len(raw) < n+16 || len(raw) > 128<<10 {
		return nil, ErrUnavailable
	}
	return aead.Open(nil, raw[:n], raw[n:], []byte(aad))
}

// plainLocator reports whether a stored value is already plaintext (valid
// UTF-8, as every plain source value is); a sealed value is a random nonce
// and ciphertext, which never is.
func plainLocator(raw []byte) bool {
	return len(raw) > 0 && utf8.Valid(raw)
}

package operations

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// Preference storage uses one typed value per key and one row per ordered list
// item. An empty list has a value row and no items: it is an explicit choice,
// distinct from removing an override. Authority/account/profile never collapse
// into a profile ID, and a device selector comes only from the verified session.
type preferenceAddress struct {
	authority, account, profile, scope, device string
}

func preferenceAddressFor(v identity.Viewer, scope, class, device string) (preferenceAddress, error) {
	a := preferenceAddress{authority: v.Authority, account: v.AccountID, profile: v.ProfileID, scope: scope}
	switch scope {
	case ScopeProfileServer:
	case ScopeDeviceClass:
		if !validDeviceClass(class) {
			return a, ErrInvalid
		}
		a.device = class
	case ScopeDevice:
		if device == "" {
			return a, invalidFields("scope")
		}
		a.device = device
	default:
		return a, ErrInvalid
	}
	return a, nil
}

func (a preferenceAddress) args() []any {
	return []any{a.authority, a.account, a.profile, a.scope, a.device}
}

const preferenceWhere = `authority=? AND account_id=? AND profile_id=? AND scope=? AND device_key=?`

func readPreferenceScope(q PreferenceQuery, a preferenceAddress) (int64, PreferenceValues, error) {
	var revision int64 = 1
	err := q.QueryRow(`SELECT revision FROM preference_scopes WHERE `+preferenceWhere, a.args()...).Scan(&revision)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, nil, err
	}
	values := PreferenceValues{}
	rows, err := q.Query(`SELECT key,kind,bool_value,number_value,text_value FROM preference_values WHERE `+preferenceWhere+` ORDER BY key`, a.args()...)
	if err != nil {
		return 0, nil, err
	}
	for rows.Next() {
		var key, kind string
		var b sql.NullBool
		var n sql.NullFloat64
		var s sql.NullString
		if err = rows.Scan(&key, &kind, &b, &n, &s); err != nil {
			rows.Close()
			return 0, nil, err
		}
		f, known := PreferenceFieldFor(key)
		if !known || !f.allows(a.scope) {
			continue
		}
		if f.Type != kind {
			rows.Close()
			return 0, nil, fmt.Errorf("invalid saved preference type: %s", key)
		}
		var raw any
		switch kind {
		case "boolean":
			raw = b.Bool
		case "integer", "number":
			raw = n.Float64
		case "string", "locale":
			raw = s.String
		case "string-list", "languageList":
			raw = []string{}
		}
		canonical, clamped, e := canonicalPreference(f, raw)
		if e != nil || clamped {
			rows.Close()
			return 0, nil, fmt.Errorf("invalid saved preference: %s", key)
		}
		values[key] = canonical
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, nil, err
	}
	rows, err = q.Query(`SELECT key,value FROM preference_list_items WHERE `+preferenceWhere+` ORDER BY key,position`, a.args()...)
	if err != nil {
		return 0, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var key, value string
		if err = rows.Scan(&key, &value); err != nil {
			return 0, nil, err
		}
		if list, ok := values[key].([]string); ok {
			values[key] = append(list, value)
		}
	}
	if err = rows.Err(); err != nil {
		return 0, nil, err
	}
	for key, value := range values {
		f, _ := PreferenceFieldFor(key)
		canonical, clamped, e := canonicalPreference(f, value)
		if e != nil || clamped {
			return 0, nil, fmt.Errorf("invalid saved preference: %s", key)
		}
		values[key] = canonical
	}
	return revision, values, nil
}

func writePreferenceScope(tx *sql.Tx, a preferenceAddress, revision, now int64, values PreferenceValues) error {
	args := append(a.args(), revision, now)
	if _, err := tx.Exec(`INSERT INTO preference_scopes(authority,account_id,profile_id,scope,device_key,revision,updated_ms) VALUES(?,?,?,?,?,?,?) ON CONFLICT(authority,account_id,profile_id,scope,device_key) DO UPDATE SET revision=excluded.revision,updated_ms=excluded.updated_ms`, args...); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM preference_values WHERE `+preferenceWhere, a.args()...); err != nil {
		return err
	}
	for key, value := range values {
		f, ok := PreferenceFieldFor(key)
		if !ok || !f.allows(a.scope) {
			continue
		}
		var b, n, s any
		switch v := value.(type) {
		case bool:
			b = v
		case int64:
			n = v
		case float64:
			n = v
		case string:
			s = v
		}
		args = append(a.args(), key, f.Type, b, n, s)
		if _, err := tx.Exec(`INSERT INTO preference_values(authority,account_id,profile_id,scope,device_key,key,kind,bool_value,number_value,text_value) VALUES(?,?,?,?,?,?,?,?,?,?)`, args...); err != nil {
			return err
		}
		if items, ok := value.([]string); ok {
			for i, item := range items {
				if _, err := tx.Exec(`INSERT INTO preference_list_items(authority,account_id,profile_id,scope,device_key,key,position,value) VALUES(?,?,?,?,?,?,?,?)`, append(a.args(), key, i, item)...); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

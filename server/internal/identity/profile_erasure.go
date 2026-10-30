package identity

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// Erasure uses each installed domain's existing tables and fences. Opaque revoked
// family/controller audit identities remain for replay defense. No physical media,
// source allocation or recorder lease is released by an identity transaction.
func eraseProfileRuntimeDataTx(ctx context.Context, tx *sql.Tx, v Viewer) error {
	exists := func(table string) (bool, error) {
		var yes bool
		e := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name=?)`, table).Scan(&yes)
		return yes, e
	}
	run := func(q string, args ...any) error { _, e := tx.ExecContext(ctx, q, args...); return e }
	// Live viewer preferences are optional until the live domain is installed.
	for _, table := range profileLiveErasure {
		if ok, e := exists(table); e != nil {
			return e
		} else if ok {
			if e = run(`DELETE FROM `+table+` WHERE authority=? AND account_id=? AND profile_id=?`, v.Authority, v.AccountID, v.ProfileID); e != nil {
				return e
			}
		}
	}
	key := PersonalKey(v)
	// Explicit reading annotations are personal content even when automatic history is paused.
	for _, table := range profileReadingErasure {
		if ok, e := exists(table); e != nil {
			return e
		} else if ok {
			if e = run(`DELETE FROM `+table+` WHERE owner_key=?`, key); e != nil {
				return e
			}
		}
	}
	// Reading positions and their removed-title copies are independently typed
	// personal state. Erasing a profile must remove both before late callbacks
	// are refused by the live identity fence.
	if ok, e := exists("reading_progress"); e != nil {
		return e
	} else if ok {
		if e = run(`DELETE FROM reading_progress WHERE owner_key=?`, key); e != nil {
			return e
		}
		if e = run(`DELETE FROM retained_title_state WHERE profile_id=?`, key); e != nil {
			return e
		}
	}
	for _, table := range profileRuntimeErasure {
		if ok, e := exists(table); e != nil {
			return e
		} else if ok {
			if e = run(`DELETE FROM `+table+` WHERE profile_id=?`, key); e != nil {
				return e
			}
		}
	}

	for _, table := range profilePositionErasure {
		if ok, e := exists(table); e != nil {
			return e
		} else if ok {
			column := "profile_key"
			if table == "book_resume" {
				column = "profile_id"
			}
			if e = run(`DELETE FROM `+table+` WHERE `+column+`=?`, key); e != nil {
				return e
			}
		}
	}
	if ok, e := exists("playback_personal_profile_fences"); e != nil {
		return e
	} else if ok {
		// Retain only a non-identifying ordinal fence. Late pre-deletion activity
		// cannot recreate the erased profile's progress after its data was removed.
		if e = run(`INSERT INTO playback_personal_profile_fences(profile_key,through_ordinal) VALUES(?,COALESCE((SELECT max(ordinal) FROM playback_personal_intents),0)) ON CONFLICT(profile_key) DO UPDATE SET through_ordinal=MAX(through_ordinal,excluded.through_ordinal)`, key); e != nil {
			return e
		}
	}
	raw, _ := json.Marshal([]string{v.Authority, v.AccountID, v.ProfileID})
	viewerKey := string(raw)
	if ok, e := exists("download_requests"); e != nil {
		return e
	} else if ok {
		if e = run(`DELETE FROM download_requests WHERE profile_key=?`, viewerKey); e != nil {
			return e
		}
		// The immutable Reading descriptor may include this profile's initial
		// location. Deliberate erasure retires the WHOLE descriptor, in every state;
		// it is never edited into a fresh document under an old receipt digest.
		if e = run(`UPDATE download_preparations SET publication_json='' WHERE profile_key=? AND authority=? AND account_id=? AND profile_id=?`, viewerKey, v.Authority, v.AccountID, v.ProfileID); e != nil {
			return e
		}
		if e = run(`DELETE FROM download_preparation_authority WHERE preparation_id IN(SELECT id FROM download_preparations WHERE profile_key=?)`, viewerKey); e != nil {
			return e
		}
		if e = run(`UPDATE download_preparations SET state='cancelled',reason='account_disabled',revision=revision+1 WHERE profile_key=? AND state IN('queued','running','paused','ready')`, viewerKey); e != nil {
			return e
		}
	}

	if ok, e := exists("console_documents"); e != nil {
		return e
	} else if ok {
		scopes := []string{viewerKey, "profile:" + viewerKey, "device:" + viewerKey + ":web", "device:" + viewerKey + ":mobile", "device:" + viewerKey + ":television", "report:" + viewerKey}
		for _, table := range []string{"console_documents", "console_receipts", "console_notifications", "console_reports"} {
			for _, scope := range scopes {
				if e = run(`DELETE FROM `+table+` WHERE scope=?`, scope); e != nil {
					return e
				}
			}
		}
		for _, table := range []string{"console_documents", "console_receipts"} {
			if e = run(`DELETE FROM `+table+` WHERE scope IN(SELECT 'session:'||t.token_hash FROM authorization_family_tokens t JOIN authorization_session_families f ON f.id=t.family_id WHERE f.authority=? AND f.account_id=? AND f.profile_id=?)`, v.Authority, v.AccountID, v.ProfileID); e != nil {
				return e
			}
		}
		if e = run(`DELETE FROM console_exports WHERE actor=?`, viewerKey); e != nil {
			return e
		}
	}
	// DVR is initialized by its runtime, not identity. This extension implements
	// the existing cancel_generation and rule revision protocols without owning
	// scheduling, retention or physical cleanup (P13). Completed files are kept
	// for explicit server-owner disposition; deletion never silently deletes them.
	if ok, e := exists("dvr_rules"); e != nil {
		return e
	} else if ok {
		args := []any{v.Authority, v.AccountID, v.ProfileID}
		now := time.Now().UnixMilli()
		if e = run(`DELETE FROM dvr_rule_work WHERE rule_id IN(SELECT id FROM dvr_rules WHERE authority=? AND account_id=? AND profile_id=?)`, args...); e != nil {
			return e
		}
		if e = run(`DELETE FROM live_source_dependencies WHERE kind='rule' AND id IN(SELECT id FROM dvr_rules WHERE authority=? AND account_id=? AND profile_id=?)`, args...); e != nil {
			return e
		}
		if e = run(`INSERT INTO dvr_owner_revisions(owner_key,revision) SELECT owner_key,1 FROM (SELECT owner_key FROM dvr_rules WHERE authority=? AND account_id=? AND profile_id=? UNION SELECT owner_key FROM dvr_recordings WHERE authority=? AND account_id=? AND profile_id=?) WHERE 1 ON CONFLICT(owner_key) DO UPDATE SET revision=revision+1`, v.Authority, v.AccountID, v.ProfileID, v.Authority, v.AccountID, v.ProfileID); e != nil {
			return e
		}
		if e = run(`UPDATE dvr_rules SET enabled=0,deleted=1,revision=revision+1,config_json='{}',updated_ms=? WHERE authority=? AND account_id=? AND profile_id=?`, now, v.Authority, v.AccountID, v.ProfileID); e != nil {
			return e
		}
		if e = run(`DELETE FROM dvr_receipts WHERE owner_key IN(SELECT owner_key FROM dvr_rules WHERE authority=? AND account_id=? AND profile_id=? UNION SELECT owner_key FROM dvr_recordings WHERE authority=? AND account_id=? AND profile_id=?)`, v.Authority, v.AccountID, v.ProfileID, v.Authority, v.AccountID, v.ProfileID); e != nil {
			return e
		}
		if e = run(`UPDATE live_reservations SET enabled=0 WHERE id IN(SELECT id FROM dvr_recordings WHERE authority=? AND account_id=? AND profile_id=?)`, args...); e != nil {
			return e
		}
		if e = run(`DELETE FROM live_source_dependencies WHERE kind='recording' AND id IN(SELECT id FROM dvr_recordings WHERE authority=? AND account_id=? AND profile_id=?)`, args...); e != nil {
			return e
		}
		if e = run(`UPDATE dvr_recordings SET state='cancelled',reason='profile-deleted',cancel_generation=cancel_generation+1,revision=revision+1,updated_ms=?,finished_ms=? WHERE authority=? AND account_id=? AND profile_id=? AND state NOT IN('completed','incomplete-playable','failed','cancelled','pending-delete','deleted')`, now, now, v.Authority, v.AccountID, v.ProfileID); e != nil {
			return e
		}
		if e = run(`UPDATE dvr_recordings SET keep=1,revision=revision+1,updated_ms=? WHERE authority=? AND account_id=? AND profile_id=? AND state IN('completed','incomplete-playable')`, now, v.Authority, v.AccountID, v.ProfileID); e != nil {
			return e
		}
	}
	return nil
}

package catalog

import (
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/requestinput"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// RecommendationsReset forgets what one profile's recommendations have
// learned. operationId is the idempotency key: a replay inside the receipt
// lifetime returns the first outcome instead of wiping what the profile has
// done since.
type RecommendationsReset struct {
	OperationID string `json:"operationId"`
}

type RecommendationsResetReceipt struct {
	OperationID string `json:"operationId"`
	// ClearedNotInterested is how many titles were marked not interested.
	ClearedNotInterested int64 `json:"clearedNotInterested"`
}

// ResetRecommendations clears the profile's "not interested" marks and its
// learned taste (compactcatalog.ResetRecProfile) in one transaction. History,
// ratings, favorites, the watchlist and watched state stay, and so
// does what they mean for which titles are offered: a watched film is still
// never recommended.
func (s *Service) ResetRecommendations(profile string, m RecommendationsReset, authorize func(*sql.Tx) error) (RecommendationsResetReceipt, error) {
	out := RecommendationsResetReceipt{OperationID: m.OperationID}
	if !personalOperation.MatchString(m.OperationID) || profile == "" {
		return out, requestinput.New("invalid operationId")
	}
	ctx := s.Context()
	gated, e := dbwork.Begin(ctx, s.db, dbwork.ClassFrom(ctx, dbwork.ClassInteractive))
	if e != nil {
		return out, e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	if authorize != nil {
		if e = authorize(tx); e != nil {
			return out, e
		}
	}
	hash := operationHash(struct {
		Action string
		RecommendationsReset
	}{"reset-recommendations", m})
	var prior, raw string
	e = tx.QueryRow(`SELECT request_hash,response FROM personal_activity_receipts WHERE profile_id=? AND operation_id=? AND created_at>=?`, profile, m.OperationID, time.Now().Add(-30*24*time.Hour).UTC().Format(time.RFC3339)).Scan(&prior, &raw)
	if e == nil {
		if prior != hash {
			return out, ErrOperationConflict
		}
		e = json.Unmarshal([]byte(raw), &out)
		return out, e
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return out, e
	}
	if e = checkRetired(tx, profile, "recommendations", m.OperationID, hash); e != nil {
		return out, e
	}
	// Each cleared mark is a personal-state change like any other: its
	// revision moves, and its trigger queues the taste job the reset settles.
	res, e := tx.ExecContext(ctx, `UPDATE personal_items SET not_interested=0,revision=revision+1 WHERE profile_id=? AND not_interested=1`, profile)
	if e != nil {
		return out, e
	}
	if out.ClearedNotInterested, e = res.RowsAffected(); e != nil {
		return out, e
	}
	if e = compactcatalog.ResetRecProfile(ctx, tx, profile, time.Now()); e != nil {
		return out, e
	}
	if e = fenceOperation(tx, profile, "recommendations", m.OperationID, hash); e != nil {
		return out, e
	}
	b, _ := json.Marshal(out)
	if _, e = tx.Exec(`INSERT INTO personal_activity_receipts VALUES(?,?,?,?,?)`, profile, m.OperationID, hash, string(b), time.Now().UTC().Format(time.RFC3339)); e != nil {
		return out, e
	}
	return out, gated.Commit()
}

package catalog

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/requestinput"
	"github.com/PorticoMediaServer/portico/server/internal/usermutation"
	"time"
)

const MaxPersonalBatch = 200

// PersonalBatchItem is one independent intent. expectedRevision is optional: a
// bulk action on a list the viewer is looking at is not a claim about every
// row's revision, so an absent value means "whatever this row is at now".
type PersonalBatchItem struct {
	ItemID           string `json:"itemId"`
	ExpectedRevision *int64 `json:"expectedRevision,omitempty"`
	Watched          *bool  `json:"watched,omitempty"`
	Favorite         *bool  `json:"favorite,omitempty"`
	Watchlisted      *bool  `json:"watchlisted,omitempty"`
	// NotInterested may also name a show, an album or a book: a title the
	// recommendations offer that is not itself playable.
	NotInterested *bool `json:"notInterested,omitempty"`
}
type PersonalBatchMutation struct {
	OperationID string              `json:"operationId"`
	Items       []PersonalBatchItem `json:"items"`
}
type PersonalBatchResult struct {
	ItemID   string         `json:"itemId"`
	OK       bool           `json:"ok"`
	Code     string         `json:"code,omitempty"`
	Message  string         `json:"message,omitempty"`
	Personal *PersonalState `json:"personal,omitempty"`
}
type PersonalBatchReceipt struct {
	ServerID          string                `json:"serverId"`
	ViewerFence       string                `json:"viewerFence"`
	OperationID       string                `json:"operationId"`
	Results           []PersonalBatchResult `json:"results"`
	Updated           int                   `json:"updated"`
	Failed            int                   `json:"failed"`
	ReceiptLifetimeMs int64                 `json:"receiptLifetimeMs"`
}

func personalFailureCode(e error) string {
	switch {
	case errors.Is(e, ErrPersonalConflict):
		return "personal_state_conflict"
	case errors.Is(e, ErrPersonalResolution):
		return "personal_needs_resolution"
	case errors.Is(e, ErrPersonalReview):
		return "personal_needs_review"
	case errors.Is(e, ErrOperationExpired):
		return "operation_expired"
	case errors.Is(e, ErrOperationConflict):
		return "operation_conflict"
	case errors.Is(e, ErrPersonalCapacity):
		return "personal_capacity"
	case errors.Is(e, sql.ErrNoRows):
		return "not_found"
	}
	return "invalid_request"
}

func personalBatchFields(item PersonalBatchItem) []struct {
	name     string
	mutation PersonalMutation
} {
	out := []struct {
		name     string
		mutation PersonalMutation
	}{}
	if item.Watchlisted != nil {
		out = append(out, struct {
			name     string
			mutation PersonalMutation
		}{"watchlisted", PersonalMutation{Watchlisted: item.Watchlisted}})
	}
	if item.Favorite != nil {
		out = append(out, struct {
			name     string
			mutation PersonalMutation
		}{"favorite", PersonalMutation{Favorite: item.Favorite}})
	}
	if item.Watched != nil {
		out = append(out, struct {
			name     string
			mutation PersonalMutation
		}{"watched", PersonalMutation{Watched: item.Watched}})
	}
	if item.NotInterested != nil {
		out = append(out, struct {
			name     string
			mutation PersonalMutation
		}{"notInterested", PersonalMutation{NotInterested: item.NotInterested}})
	}
	return out
}

// SetPersonalBatch applies each item independently under the single route's
// conflict rules. One rejected row never rolls back the rest. The batch as a
// whole carries the retry receipt, so a replay returns the first outcome rather
// than re-evaluating rows against revisions its own first attempt advanced.
func (s *Service) SetPersonalBatch(account, profile string, m PersonalBatchMutation, authorize func(*sql.Tx, string) error) (PersonalBatchReceipt, error) {
	if err := requireCatalogMutationContext(s.Context(), m.OperationID); err != nil {
		return PersonalBatchReceipt{}, err
	}
	out := PersonalBatchReceipt{OperationID: m.OperationID, Results: []PersonalBatchResult{}, ReceiptLifetimeMs: 2592000000}
	if !personalOperation.MatchString(m.OperationID) {
		return out, requestinput.New("invalid operationId")
	}
	if len(m.Items) == 0 || len(m.Items) > MaxPersonalBatch {
		return out, requestinput.New(fmt.Sprintf("a batch carries 1 to %d items", MaxPersonalBatch))
	}
	seen := map[string]bool{}
	for _, item := range m.Items {
		if item.ItemID == "" || len(item.ItemID) > 256 || seen[item.ItemID] {
			return out, requestinput.New("each batch item must name a distinct itemId")
		}
		seen[item.ItemID] = true
		if item.Watched == nil && item.Favorite == nil && item.Watchlisted == nil && item.NotInterested == nil {
			return out, requestinput.New("each batch item must set watched, favorite, watchlisted or notInterested")
		}
		if item.ExpectedRevision != nil && (*item.ExpectedRevision < 0 || *item.ExpectedRevision > 9007199254740991) {
			return out, requestinput.New("invalid expectedRevision")
		}
	}
	if _, _, protocol := usermutation.RequestFrom(s.Context()); protocol {
		return s.setPersonalBatchProtocol(account, profile, m, authorize)
	}
	hash := operationHash(m)
	cutoff := time.Now().UTC().Add(-30 * 24 * time.Hour).Format(time.RFC3339)
	var prior, stored string
	e := s.read().QueryRow(`SELECT request_hash,response FROM personal_batch_receipts WHERE profile_id=? AND operation_id=? AND created_at>=?`, profile, m.OperationID, cutoff).Scan(&prior, &stored)
	if e == nil {
		if prior != hash {
			return out, ErrOperationConflict
		}
		if e = json.Unmarshal([]byte(stored), &out); e != nil || authorize == nil {
			return out, e
		}
		gate, err := dbwork.BeginSnapshot(s.Context(), s.db)
		if err != nil {
			return PersonalBatchReceipt{}, err
		}
		defer gate.Rollback()
		if err = operations.ReauthorizeReceipt(out.Results, func(row PersonalBatchResult) string {
			return row.ItemID
		}, func(item string) error {
			return authorize(gate.Tx(), item)
		}, func(row *PersonalBatchResult) {
			*row = PersonalBatchResult{ItemID: row.ItemID, Code: "not_found", Message: sql.ErrNoRows.Error()}
		}); err != nil {
			return PersonalBatchReceipt{}, err
		}
		out.Updated, out.Failed = 0, 0
		for _, row := range out.Results {
			if row.OK {
				out.Updated++
			} else {
				out.Failed++
			}
		}
		return out, gate.Commit()
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return out, e
	}
	for _, item := range m.Items {
		var state PersonalState
		var failure error
		expected := int64(-1)
		if item.ExpectedRevision != nil {
			expected = *item.ExpectedRevision
		}
		for _, field := range personalBatchFields(item) {
			if expected < 0 {
				current, readErr := readPersonal(s.read(), profile, item.ItemID)
				if readErr != nil {
					failure = readErr
					break
				}
				expected = current.Revision
			}
			mutation := field.mutation
			mutation.OperationID = m.OperationID + "-" + operationHash([]string{item.ItemID, field.name})[:16]
			mutation.ExpectedRevision = expected
			state, failure = s.SetPersonal(account, profile, item.ItemID, mutation, func(tx *sql.Tx) error {
				if authorize == nil {
					return nil
				}
				return authorize(tx, item.ItemID)
			})
			if failure != nil {
				break
			}
			expected = state.Revision
		}
		if failure != nil {
			result := PersonalBatchResult{ItemID: item.ItemID, OK: false, Code: personalFailureCode(failure), Message: failure.Error()}
			// A title the viewer can't reach (absent, or hidden by its
			// restrictions) answers with nothing about it: echoing the stored
			// state would show a restricted profile its history on a hidden
			// title and tell it apart from one that doesn't exist (SEC-02).
			if !errors.Is(failure, sql.ErrNoRows) {
				if current, readErr := readPersonal(s.read(), profile, item.ItemID); readErr == nil {
					result.Personal = &current
				}
			}
			out.Results = append(out.Results, result)
			out.Failed++
			continue
		}
		applied := state
		out.Results = append(out.Results, PersonalBatchResult{ItemID: item.ItemID, OK: true, Personal: &applied})
		out.Updated++
	}
	response, _ := json.Marshal(out)
	_, e = dbwork.ExecWrite(s.Context(), s.db, dbwork.ClassFrom(s.Context(), dbwork.ClassInteractive), `INSERT INTO personal_batch_receipts VALUES(?,?,?,?,?) ON CONFLICT(profile_id,operation_id) DO UPDATE SET request_hash=excluded.request_hash,response=excluded.response,created_at=excluded.created_at`, profile, m.OperationID, hash, string(response), time.Now().UTC().Format(time.RFC3339))
	return out, e
}

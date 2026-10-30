package networking

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"github.com/PorticoMediaServer/portico/server/internal/claimweb"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
	"time"
)

type localWebClaim struct {
	ownerSwitchCount   *claimweb.Signed `json:"-"`
	Signed             claimweb.Signed  `json:"signed"`
	SetupStart         *claimweb.Signed `json:"setupStart,omitempty"`
	SetupTransactionID string           `json:"setupTransactionId,omitempty"`
	OwnerStart         *claimweb.Signed `json:"ownerStart,omitempty"`
	OwnerTransactionID string           `json:"ownerTransactionId,omitempty"`
	Code               string           `json:"code"`
	URL                string           `json:"url"`
	ExpiresAt          time.Time        `json:"expiresAt"`
}

func (h *ClaimHandler) beginWebClaim(ctx context.Context, v Intent, address string) (localWebClaim, error) {
	var out localWebClaim
	transport, ok := h.transport.(*HTTPTransport)
	if !ok {
		return out, ErrUnavailable
	}
	if v.Stage != Prepared {
		return out, ErrStale
	}
	// An unclaimed Local switch needs its distinct frozen owner start. The
	// ordinary claim path cannot create an unbound browser continuation.
	if v.Purpose == "switch" && v.CurrentOwnerAccountID == "" {
		return out, ErrInvalid
	}
	// Public proof and code survive a UI remount; private signing material never leaves its store.
	var saved string
	if e := h.store.db.QueryRowContext(ctx, `SELECT request FROM networking_web_claims WHERE operation_id=?`, v.OperationID).Scan(&saved); e == nil {
		if json.Unmarshal([]byte(saved), &out) == nil && time.Now().Before(out.ExpiresAt) {
			return out, nil
		}
		// The earlier request expired unanswered: sign a fresh one for the same
		// operation (Hosted lets a newer request replace an undecided one). Answering
		// stale here left a server that could never connect again (demo, 24 Sep).
		if _, e = dbwork.ExecWrite(ctx, h.store.db, dbwork.ClassInteractive, `DELETE FROM networking_web_claims WHERE operation_id=?`, v.OperationID); e != nil {
			return localWebClaim{}, e
		}
		out = localWebClaim{}
	}
	now := time.Now().UTC()
	account := v.AccountID
	if account == UnboundAccount {
		account = ""
	}
	q := claimweb.Request{Kind: "portico.claim.web.v2", Audience: transport.origin, OperationID: v.OperationID, ServerID: v.ServerID, AccountID: account, PublicKey: base64.RawURLEncoding.EncodeToString(v.PublicKey), LocalGeneration: v.LocalGeneration, Name: h.name(), Address: address, Purpose: v.Purpose, CurrentOwnerAccountID: v.CurrentOwnerAccountID, PreviousOwnerRole: v.PreviousOwnerRole, ReturnRegistrationID: v.ReturnRegistrationID, IssuedAt: now, ExpiresAt: now.Add(webClaimLifetime)}
	raw, _ := json.Marshal(q)
	signature, e := h.signer.Sign(ctx, v.Binding, raw)
	if e != nil {
		return out, e
	}
	out.Signed = claimweb.Signed{Payload: base64.RawURLEncoding.EncodeToString(raw), Signature: base64.RawURLEncoding.EncodeToString(signature)}
	if _, e = claimweb.Verify(out.Signed, transport.origin, now); e != nil {
		return out, ErrInvalid
	}
	var pending claimweb.Pending
	if e = transport.call(ctx, "pending", out.Signed, &pending, nil); e != nil {
		return out, e
	}
	fragment := url.Values{"code": {pending.Code}, "server": {q.Name}, "operation": {v.OperationID}}
	out.Code = pending.Code
	out.URL = transport.origin + "/claim#" + fragment.Encode()
	out.ExpiresAt = q.ExpiresAt
	encoded, _ := json.Marshal(out)
	_, e = dbwork.ExecWrite(ctx, h.store.db, dbwork.ClassInteractive, `INSERT INTO networking_web_claims(operation_id,request) VALUES(?,?) ON CONFLICT(operation_id) DO NOTHING`, v.OperationID, string(encoded))
	if e == nil {
		h.wakeClaimRecovery()
	}
	return out, e
}

// webClaimWatch is the one wait for Hosted's decision on a web claim. It
// belongs to the server, not to the page that asked for it: the claim installs
// (and the certificate is asked for) when the person approves, whether or not
// the page that opened the approval is still open. A page that waits again
// joins the same watch, and a late one reads the answer it left.
type webClaimWatch struct {
	done   chan struct{}
	answer []byte
}

// awaitWebClaim holds the page's request open until the watch has an answer:
// the final claim status, or `{state: approval_closed | approval_changed}`.
func (h *ClaimHandler) awaitWebClaim(w http.ResponseWriter, r *http.Request) {
	actor, e := h.authorize(r)
	if e != nil || actor.Guard == nil {
		h.failure(w, r, ErrClaimOwnerRequired)
		return
	}
	var body struct {
		Expected ClaimExpected `json:"expected"`
	}
	if e = readClaimBody(w, r, &body); e != nil {
		h.failure(w, r, e)
		return
	}
	watch, e := h.watchWebClaim(r.Context(), body.Expected, actor.Guard, actor.Resume)
	if e != nil {
		h.failure(w, r, e)
		return
	}
	// The longest purpose (setup) bounds how long a watcher can wait.
	deadline := time.Now().Add(claimweb.RequestLifetime("setup") + time.Minute)
	_ = http.NewResponseController(w).SetWriteDeadline(deadline)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(200)
	_, _ = w.Write([]byte(" "))
	_ = http.NewResponseController(w).Flush()
	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-watch.done:
			_, _ = w.Write(watch.answer)
			return
		case <-r.Context().Done():
			// The page went away; the watch carries on without it.
			return
		case <-ticker.C:
			if _, e = w.Write([]byte(" ")); e != nil {
				return
			}
			if e = http.NewResponseController(w).Flush(); e != nil {
				return
			}
		}
	}
}

// webClaimLifetime matches the signed request's own expiry (beginWebClaim).
const webClaimLifetime = 10 * time.Minute

// watchWebClaim returns the running watch for this operation, starting one
// (checked against the current claim under the owner's guard) when there is
// none. The guard is the owner's who asked: if that sign-in ends before the
// decision arrives, the approval is not applied.
func (h *ClaimHandler) watchWebClaim(ctx context.Context, expected ClaimExpected, guard ClaimRequestGuard, resume string) (*webClaimWatch, error) {
	if current, ok := h.webWatches.Load(expected.OperationID); ok {
		return current.(*webClaimWatch), nil
	}
	var saved string
	var local localWebClaim
	if e := h.store.db.QueryRowContext(ctx, `SELECT request FROM networking_web_claims WHERE operation_id=?`, expected.OperationID).Scan(&saved); e != nil || json.Unmarshal([]byte(saved), &local) != nil || !time.Now().Before(local.ExpiresAt) {
		return nil, ErrStale
	}
	e := h.runner.Do(ctx, func(ctx context.Context) error {
		guarded, e := guardedClaimRequest(ctx, h.store, guard)
		if e != nil {
			return e
		}
		_, e = h.expected(guarded, expected)
		return e
	})
	if e != nil {
		return nil, e
	}
	if _, ok := h.transport.(*HTTPTransport); !ok {
		return nil, stepFailure("hosted transport", ErrUnavailable)
	}
	watch := &webClaimWatch{done: make(chan struct{})}
	if current, loaded := h.webWatches.LoadOrStore(expected.OperationID, watch); loaded {
		return current.(*webClaimWatch), nil
	}
	if resume != "" {
		// Kept so a restart before the decision picks the wait up again.
		note, _ := json.Marshal(webClaimResume{expected, resume})
		if _, e := dbwork.ExecWrite(ctx, h.store.db, dbwork.ClassInteractive, `UPDATE networking_web_claims SET watch=? WHERE operation_id=?`, string(note), expected.OperationID); e != nil {
			log.Printf("The Portico Account approval wait was not saved for a restart: %v", e)
		}
	}
	supervise.Go("networking.claim.web-watch", func() {
		decided := false
		defer func() {
			if watch.answer == nil {
				watch.answer = webClaimAnswer("approval_closed")
			}
			close(watch.done)
			if !decided {
				// Hosted could not be asked (or the stream broke): the next wait asks again.
				h.webWatches.CompareAndDelete(expected.OperationID, watch)
				h.wakeClaimRecovery()
				return
			}
			// A page that asks again shortly afterwards reads this answer; after
			// the request's own expiry a new web approval starts a new watch.
			time.AfterFunc(time.Until(local.ExpiresAt)+time.Minute, func() { h.webWatches.CompareAndDelete(expected.OperationID, watch) })
		}()
		watch.answer, decided = h.runWebClaimWatch(expected, guard, local, resume)
	})
	return watch, nil
}

// webClaimResume is what a restart needs to wait on a pending approval again.
type webClaimResume struct {
	Expected ClaimExpected `json:"expected"`
	Owner    string        `json:"owner"`
}

// ResumeWebClaims waits again, after a server restart, on every web approval
// that was pending when the server stopped and has not expired. guard
// rebuilds the asking owner's guard from its ClaimRequestOwner.Resume note;
// the approval is applied only while that sign-in still holds, as before the
// restart. A claim that moved on meanwhile is left alone.
func (h *ClaimHandler) ResumeWebClaims(ctx context.Context, guard func(resume string) ClaimRequestGuard) {
	if h == nil || guard == nil {
		return
	}
	rows, e := h.store.db.QueryContext(ctx, `SELECT w.watch FROM networking_web_claims w JOIN networking_claim_intents i ON i.operation_id=w.operation_id WHERE w.watch<>'' AND i.stage IN('prepared','approved','finalizing','retrieving')`)
	if e != nil {
		log.Printf("Pending Portico Account approvals could not be read: %v", e)
		return
	}
	var pending []webClaimResume
	for rows.Next() {
		var raw string
		var note webClaimResume
		if rows.Scan(&raw) == nil && json.Unmarshal([]byte(raw), &note) == nil && note.Owner != "" {
			pending = append(pending, note)
		}
	}
	_ = rows.Close()
	for _, note := range pending {
		g := guard(note.Owner)
		if g == nil {
			if err := h.reconcileWebClaim(ctx, note.Expected.OperationID, nil, false); err != nil {
				log.Printf("Abandoned Portico Account approval could not be reconciled: %v", err)
			}
			continue
		}
		if _, e := h.watchWebClaim(ctx, note.Expected, g, note.Owner); e != nil {
			if err := h.reconcileWebClaim(ctx, note.Expected.OperationID, g, false); err != nil {
				log.Printf("Abandoned Portico Account approval could not be reconciled: %v", err)
			}
			if e != ErrStale {
				log.Printf("Waiting for the Portico Account approval could not resume: %v", e)
			}
		}
	}
}

func webClaimAnswer(state string) []byte {
	raw, _ := json.Marshal(map[string]string{"state": state})
	return raw
}

// runWebClaimWatch waits on Hosted's decision stream for one signed web claim
// and, when it is approved, installs the claim and wakes the certificate and
// remote-access work at once.
func (h *ClaimHandler) runWebClaimWatch(expected ClaimExpected, guard ClaimRequestGuard, local localWebClaim, resume string) ([]byte, bool) {
	ctx, cancel := context.WithDeadline(context.Background(), local.ExpiresAt)
	defer cancel()
	withdraw := func() {
		cleanup, stop := context.WithTimeout(context.Background(), time.Second*8)
		defer stop()
		if err := h.reconcileWebClaim(cleanup, expected.OperationID, guard, true); err != nil {
			log.Printf("Abandoned Portico Account approval could not be reconciled: %v", err)
		}
	}
	defer func() {
		if !time.Now().Before(local.ExpiresAt) {
			withdraw()
		}
	}()
	t := h.transport.(*HTTPTransport)
	raw, _ := json.Marshal(local.Signed)
	request, e := http.NewRequestWithContext(ctx, "POST", t.origin+"/v1/server-claims/events", bytes.NewReader(raw))
	if e != nil {
		return nil, false
	}
	request.Header.Set("Content-Type", "application/json")
	client := *t.client
	client.Timeout = 0
	response, e := client.Do(request)
	if e != nil {
		log.Printf("Waiting for the Portico Account approval failed: %v", stepFailure("hosted approval events", e))
		return nil, false
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		log.Printf("Waiting for the Portico Account approval failed: HTTP %d", response.StatusCode)
		return nil, false
	}
	var d struct {
		Status   string          `json:"status"`
		Envelope json.RawMessage `json:"approvalEnvelope"`
	}
	// Hosted keeps the stream alive with spaces; the decoder skips them.
	if e = json.NewDecoder(io.LimitReader(response.Body, 32<<10)).Decode(&d); e != nil {
		return nil, false
	}
	if d.Status != "approved" {
		withdraw()
		return webClaimAnswer("approval_closed"), true
	}
	var status ClaimStatus
	recorded := false
	apply := func() error {
		return h.runner.Do(ctx, func(ctx context.Context) error {
			ctx, e := guardedClaimRequest(ctx, h.store, guard)
			if e != nil {
				return e
			}
			v, e := h.expected(ctx, expected)
			if errors.Is(e, ErrStale) && recorded {
				// A retry finds this watch's own progress: the approval it recorded moved the
				// revision on. Continue that same operation, never a cancelled or reset one.
				v, e = h.approvedContinuation(ctx, expected)
			}
			if e != nil {
				return e
			}
			if v.Stage == Prepared {
				if _, e = h.store.Approve(ctx, v, d.Envelope); e != nil {
					return e
				}
			}
			recorded = true
			coordinator, e := NewCoordinator(v.OperationID, h.store.audience, h.store, h.transport, h.signer)
			if e != nil {
				return e
			}
			for range 4 {
				next, e := coordinator.Step(ctx)
				if e != nil {
					return e
				}
				if next.Stage == Installed && next.InstallationAcknowledged {
					if e = h.afterWebClaimInstalled(ctx, next, local, resume); e != nil {
						return e
					}
					status, e = h.snapshot(ctx)
					return e
				}
			}
			return ErrUnavailable
		})
	}
	e = retryWebApproval(ctx, apply)
	if e != nil {
		if errors.Is(e, ErrOwnerCollision) || errors.Is(e, ErrClaimOwnerRequired) || errors.Is(e, ErrStale) || errors.Is(e, ErrApprovalRequired) {
			withdraw()
		}
		log.Printf("Claim approval from the web could not be applied: %v", e)
		return webClaimAnswer("approval_changed"), true
	}
	out, e := json.Marshal(status)
	if e != nil {
		return webClaimAnswer("approval_changed"), true
	}
	return out, true
}

// approvedContinuation is the same claim operation after this server recorded its approval:
// the same local generation, still this server's active or pending claim, a revision at or
// past the one the watch began with, and a stage that only follows an approval.
func (h *ClaimHandler) approvedContinuation(ctx context.Context, want ClaimExpected) (Intent, error) {
	gated, e := h.store.tx(ctx)
	if e != nil {
		return Intent{}, e
	}
	defer gated.Rollback()
	tx := gated.Tx()
	id, e := readIdentity(ctx, tx)
	if e != nil {
		return Intent{}, e
	}
	if (!id.active.Valid || id.active.String != want.OperationID) && (!id.pending.Valid || id.pending.String != want.OperationID) {
		return Intent{}, ErrStale
	}
	v, e := loadIntentTx(ctx, tx, want.OperationID)
	if e != nil {
		return Intent{}, e
	}
	if v.LocalGeneration != want.LocalGeneration || v.Revision < want.Revision ||
		(v.Stage != Approved && v.Stage != Finalizing && v.Stage != Retrieving && v.Stage != Installed) {
		return Intent{}, ErrStale
	}
	return v.Intent, nil
}

// retryWebApproval applies an approval the person already gave, through transient Hosted
// failures (a timeout, 5xx, a dropped connection): quickly at first (1, 2, 4, 8 s, honouring
// Retry-After) for about two minutes, then every half minute until the claim request itself
// expires. One slow Hosted response must never leave setup stuck. A refusal is final at once.
func retryWebApproval(ctx context.Context, apply func() error) error {
	started := time.Now()
	for attempt := 0; ; attempt++ {
		e := apply()
		if e == nil || !transientWebApprovalFailure(e) {
			return e
		}
		next := RetryAt(time.Now(), time.Second, attempt, 3, ControlRetryAt(e))
		if time.Since(started) > webApprovalFastRetry {
			next = RetryAt(time.Now(), webApprovalSlowRetry, 0, 0, ControlRetryAt(e))
		}
		log.Printf("Claim approval from the web is waiting for Portico Services, trying again in %s: %v", time.Until(next).Round(time.Second), e)
		timer := time.NewTimer(time.Until(next))
		select {
		case <-ctx.Done():
			timer.Stop()
			return e
		case <-timer.C:
		}
	}
}

const (
	webApprovalFastRetry = 2 * time.Minute
	webApprovalSlowRetry = 30 * time.Second
)

func transientWebApprovalFailure(e error) bool {
	if errors.Is(e, ErrOwnerCollision) || errors.Is(e, ErrClaimOwnerRequired) || errors.Is(e, ErrStale) || errors.Is(e, ErrApprovalRequired) ||
		errors.Is(e, ErrCancelled) || errors.Is(e, ErrInvalid) || ControlRejected(e) || NeedsClaimReconciliation(e) {
		return false
	}
	var timeout interface{ Timeout() bool }
	return errors.Is(e, ErrUnavailable) || errors.As(e, &timeout) && timeout.Timeout()
}

// A genuine installed acknowledgement wakes its ordinary TLS and route work
// before optional return binding. The first bind can legitimately find no
// serving certificate row yet; that refusal must not leave both managers asleep.
func (h *ClaimHandler) afterWebClaimInstalled(ctx context.Context, next Intent, local localWebClaim, resume string) error {
	if next.Stage != Installed || !next.InstallationAcknowledged {
		return ErrStale
	}
	if h.certificates != nil {
		h.certificates.Wake()
	}
	if h.remote != nil {
		h.remote.Invalidate()
	}
	h.wakeClaimRecovery()
	if next.Purpose == "setup" && local.SetupStart != nil && h.setupReturnResolver != nil {
		if err := h.setupReturnResolver(ctx, resume, next.OperationID); err != nil && !errors.Is(err, ErrUnavailable) {
			return err
		}
	}
	return nil
}

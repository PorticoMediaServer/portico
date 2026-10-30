// Package idempotency stores replies in the same transaction as their mutations.
package idempotency

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/apikit/apierror"
)

type Store struct {
	Begin      func(context.Context) (Transaction, error)
	SweepBegin func(context.Context) (Transaction, error)
	Lifetime   time.Duration
}
type Transaction interface {
	Tx() *sql.Tx
	Commit() error
	Rollback() error
}
type Request struct {
	Method   string
	Path     string // concrete escaped path, never a route template
	RawQuery string
	Body     []byte
}
type Result struct {
	Status   int
	Body     []byte
	Headers  map[string]string
	Replayed bool
}

var keyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16,128}$`)

// Scope must contain the stable principal and device identity. The caller
// must authorize the request before invoking Execute, including on replays.
// Mutate must use only this transaction; external work is represented by an
// operation inserted here and performed after commit by its durable worker.
func (s Store) Execute(ctx context.Context, scope, key string, request Request, mutate func(*sql.Tx) (Result, error)) (Result, error) {
	if !keyPattern.MatchString(key) || scope == "" || len(scope) > 1024 || mutate == nil || s.Begin == nil || s.Lifetime < time.Second || s.Lifetime > 7*24*time.Hour || request.Method == "" || !strings.HasPrefix(request.Path, "/") || strings.ContainsAny(request.Path, "{}?#\r\n") {
		return Result{}, &apierror.Error{Code: "invalid_request"}
	}
	query, err := url.ParseQuery(request.RawQuery)
	if err != nil {
		return Result{}, &apierror.Error{Code: "invalid_request", Cause: err}
	}
	canonical, err := apikit.CanonicalJSON(request.Body)
	if err != nil {
		return Result{}, &apierror.Error{Code: "invalid_request", Cause: err}
	}
	digestInput, err := json.Marshal(struct {
		Method string          `json:"method"`
		Path   string          `json:"path"`
		Query  string          `json:"query"`
		Body   json.RawMessage `json:"body"`
	}{request.Method, request.Path, query.Encode(), canonical})
	if err != nil {
		return Result{}, err
	}
	hash := sha256.Sum256(digestInput)
	digest := hex.EncodeToString(hash[:])
	write, err := s.Begin(ctx)
	if err != nil {
		return Result{}, err
	}
	defer write.Rollback()
	tx := write.Tx()
	now := time.Now().Unix()
	// Expired receipts can be reused only for revision-fenced or set-state
	// mutations. The route's resource precondition is checked by mutate.
	if _, err = tx.ExecContext(ctx, `DELETE FROM api_idempotency WHERE scope=$1 AND key=$2 AND expires_at<=$3`, scope, key, now); err != nil {
		return Result{}, err
	}
	inserted, err := tx.ExecContext(ctx, `INSERT INTO api_idempotency(scope,key,digest,status,body,expires_at) VALUES($1,$2,$3,0,$4,$5) ON CONFLICT(scope,key) DO NOTHING`, scope, key, digest, []byte{}, now+int64(s.Lifetime/time.Second))
	if err != nil {
		return Result{}, err
	}
	count, err := inserted.RowsAffected()
	if err != nil {
		return Result{}, err
	}
	if count == 0 {
		var previous string
		var out Result
		var headers string
		if err = tx.QueryRowContext(ctx, `SELECT digest,status,body,headers FROM api_idempotency WHERE scope=$1 AND key=$2`, scope, key).Scan(&previous, &out.Status, &out.Body, &headers); err != nil {
			return out, err
		}
		if previous != digest {
			return Result{}, &apierror.Error{Code: "idempotency_key_reused"}
		}
		if out.Status == 0 {
			return Result{}, &apierror.Error{Code: "request_in_progress", RetryAfterSeconds: 1}
		}
		if err = json.Unmarshal([]byte(headers), &out.Headers); err != nil {
			return Result{}, err
		}
		out.Replayed = true
		return out, write.Commit()
	}
	out, err := mutate(tx)
	if err != nil {
		return Result{}, err
	}
	if out.Status < 200 || out.Status >= 300 || len(out.Body) > 512<<10 {
		return Result{}, errors.New("invalid idempotent result")
	}
	if len(out.Headers) > 16 {
		return Result{}, errors.New("too many idempotent response headers")
	}
	for name, value := range out.Headers {
		if name == "" || strings.ContainsAny(name, "\r\n:") || strings.ContainsAny(value, "\r\n") {
			return Result{}, errors.New("invalid idempotent response header")
		}
	}
	headerJSON, err := json.Marshal(out.Headers)
	if err != nil || len(headerJSON) > 16<<10 {
		return Result{}, errors.New("idempotent response headers too large")
	}
	if _, err = tx.ExecContext(ctx, `UPDATE api_idempotency SET status=$1,body=$2,headers=$3 WHERE scope=$4 AND key=$5`, out.Status, out.Body, string(headerJSON), scope, key); err != nil {
		return Result{}, err
	}
	return out, write.Commit()
}
func (s Store) Sweep(ctx context.Context, limit int) (int64, error) {
	if limit < 1 || limit > 1000 || s.SweepBegin == nil {
		return 0, errors.New("sweep limit must be between 1 and 1000")
	}
	write, err := s.SweepBegin(ctx)
	if err != nil {
		return 0, err
	}
	defer write.Rollback()
	result, err := write.Tx().ExecContext(ctx, `DELETE FROM api_idempotency WHERE (scope,key) IN (SELECT scope,key FROM api_idempotency WHERE expires_at<=$1 ORDER BY expires_at LIMIT $2)`, time.Now().Unix(), limit)
	if err != nil {
		return 0, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	return count, write.Commit()
}

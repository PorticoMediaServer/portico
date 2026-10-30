package identity

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"golang.org/x/crypto/bcrypt"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"strings"
	"time"
)

type PublicMove string

func (PublicMove) JSONSchema() map[string]any {
	return map[string]any{"type": "string", "enum": []string{"to_portico", "to_local"}}
}
func (s *Service) PublicAccountMove(ctx context.Context) (*PublicMove, error) {
	move, _, e := s.PublicAccountMoveWindow(ctx)
	return move, e
}

// PublicAccountMoveWindow is the announced move and, for a move to Portico Accounts, when the
// last waiting member's old sign-in stops working (zero when none has one). Apps word the
// offer from it ("within the next 3 days"), never from a fixed 30.
func (s *Service) PublicAccountMoveWindow(ctx context.Context) (*PublicMove, time.Time, error) {
	var stage string
	var until int64
	// A move is announced only while someone is still waiting to reconnect: once the last member
	// has (or none was ever waiting), no app offers the old sign-in again, and the old-password
	// path already refuses an account that is not waiting.
	e := s.db.QueryRowContext(ctx, `SELECT m.stage,COALESCE((SELECT max(w.old_password_until_ms) FROM identity_move_waiting w WHERE w.move_id=m.id AND w.state='waiting' AND w.old_password_hash IS NOT NULL),0) FROM identity_moves m WHERE m.stage IN('moving_to_portico','moving_to_local') AND EXISTS(SELECT 1 FROM identity_move_waiting w WHERE w.move_id=m.id AND w.state='waiting')`).Scan(&stage, &until)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, time.Time{}, nil
	}
	if e != nil {
		return nil, time.Time{}, e
	}
	v := PublicMove(strings.TrimPrefix(stage, "moving_"))
	var ends time.Time
	if v == "to_portico" && until > time.Now().UnixMilli() {
		ends = time.UnixMilli(until).UTC()
	}
	return &v, ends, nil
}

// Owner changes are made on the server's web page. Other apps may read the state.
func (s *Service) moveWebOwnerTx(ctx context.Context, tx *sql.Tx, bearer string) (directCaller, error) {
	c, e := s.moveOwnerTx(ctx, tx, bearer)
	if e != nil {
		return c, e
	}
	var platform string
	if e = tx.QueryRowContext(ctx, `SELECT d.platform FROM identity_device_families b JOIN identity_devices d ON d.id=b.device_id WHERE b.family_id=?`, c.familyID).Scan(&platform); e != nil {
		return c, e
	}
	if platform != "web" {
		return c, ErrForbidden
	}
	return c, nil
}

type MoveCollisionError struct {
	Username   string
	ServerName string
}

func (e *MoveCollisionError) Error() string {
	return fmt.Sprintf("That Portico Account is already on %s as %s.", e.ServerName, e.Username)
}
func (e *MoveCollisionError) Unwrap() error { return ErrMoveCollision }

type MovePasswordRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (MovePasswordRequest) String() string { return "[move password redacted]" }

type MoveLinkTicket struct {
	Ticket    string `json:"ticket"`
	ExpiresAt string `json:"expiresAt"`
}

func (MoveLinkTicket) String() string { return "[move linking ticket redacted]" }
func moveInstallation(ctx context.Context) string {
	q, _ := ctx.Value(issuingDeviceKey{}).(issuingDevice)
	return q.registration.InstallationID
}

// The old password proves only the right to link this Waiting account. It
// cannot admit a viewing or account session, and is never retained by the app.
func (s *Service) ProveMovePassword(ctx context.Context, q MovePasswordRequest) (out MoveLinkTicket, err error) {
	if err = s.RequireSignInKind(ctx, SignInPortico); err != nil {
		return
	}
	if len(q.Password) > 72 || len(q.Password) == 0 || !validInstallationID(moveInstallation(ctx)) {
		return out, ErrMoveCode
	}
	var id string
	var hash []byte
	var epoch int
	err = s.db.QueryRowContext(ctx, `SELECT a.id,w.old_password_hash,a.epoch FROM accounts a JOIN identity_move_waiting w ON w.account_id=a.id JOIN identity_moves m ON m.id=w.move_id WHERE a.username=? COLLATE NOCASE AND w.state='waiting' AND m.stage='moving_to_portico' AND w.old_password_until_ms>?`, strings.ToLower(strings.TrimSpace(q.Username)), time.Now().UnixMilli()).Scan(&id, &hash, &epoch)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrMoveCode
	}
	if err != nil {
		return
	}
	if err = s.takeHashSlot(ctx); err != nil {
		return
	}
	valid := bcrypt.CompareHashAndPassword(hash, []byte(q.Password)) == nil
	<-s.hashSlots
	if !valid {
		return out, ErrMoveCode
	}
	out.Ticket = Token()
	until := time.Now().Add(10 * time.Minute)
	out.ExpiresAt = until.UTC().Format(time.RFC3339)
	err = dbwork.WithWriteTx(ctx, s.db, dbwork.ClassSecurityFence, func(tx *sql.Tx) error {
		if e := RequireSignInKindTx(ctx, tx, SignInPortico); e != nil {
			return e
		}
		var current bool
		if e := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM accounts a JOIN identity_move_waiting w ON w.account_id=a.id WHERE a.id=? AND a.epoch=? AND w.old_password_hash=? AND w.old_password_until_ms>? AND w.state='waiting')`, id, epoch, hash, time.Now().UnixMilli()).Scan(&current); e != nil {
			return e
		}
		if !current {
			return ErrMoveCode
		}
		if _, e := tx.ExecContext(ctx, `DELETE FROM identity_move_link_tickets WHERE expires_ms<=? OR (account_id=? AND installation_id=?)`, time.Now().UnixMilli(), id, moveInstallation(ctx)); e != nil {
			return e
		}
		_, e := tx.ExecContext(ctx, `INSERT INTO identity_move_link_tickets(token_hash,account_id,installation_id,epoch,expires_ms) VALUES(?,?,?,?,?)`, Digest(out.Ticket), id, moveInstallation(ctx), epoch, until.UnixMilli())
		return e
	})
	if err != nil {
		out = MoveLinkTicket{}
	}
	return
}

type MoveCodePreviewRequest struct {
	Code string `json:"code"`
}

func (MoveCodePreviewRequest) String() string { return "[move code redacted]" }

type MoveCodePreview struct {
	Username  string     `json:"username"`
	SignIn    SignInKind `json:"signIn"`
	ExpiresAt string     `json:"expiresAt"`
}

func (s *Service) PreviewMoveCode(ctx context.Context, code string) (out MoveCodePreview, err error) {
	g, e := dbwork.BeginSnapshot(ctx, s.db)
	if e != nil {
		return out, e
	}
	defer g.Rollback()
	id, e := waitingCodeTx(ctx, g.Tx(), code)
	if e != nil {
		return out, e
	}
	var expires int64
	err = g.Tx().QueryRowContext(ctx, `SELECT a.username,m.to_kind,c.expires_ms FROM identity_move_codes c JOIN accounts a ON a.id=c.account_id JOIN identity_moves m ON m.id=c.move_id WHERE c.account_id=?`, id).Scan(&out.Username, &out.SignIn, &expires)
	out.ExpiresAt = time.UnixMilli(expires).UTC().Format(time.RFC3339)
	return
}

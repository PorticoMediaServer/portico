// Package hosted is the one place the server may talk to Portico Services, the hosted service
// behind Portico Accounts. The contract is Portico Services' API.md (portico-internal,
// hosted-services/hosted-next); trust and schedule beside this file are copies of the two
// folders that contract tells a server to copy as they are. Nothing else in the server names
// a Portico address.
//
// A server set up with local sign-in never calls anything here: every caller asks only on
// behalf of a server that uses Portico Accounts, or of a setup or switch that is choosing them.
package hosted

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/hosted/trust"
)

// ErrNotConnected: Portico Services gave no answer. Callers answer it as "Portico Services
// can't be reached".
var ErrNotConnected = errors.New("portico services can't be reached")

// ErrDeclined: the person declined the approval, or it expired, was made for another server
// or purpose, was used before, or does not check out. The server stays exactly as it was.
var ErrDeclined = errors.New("the approval was declined or has expired")

// ErrUnknownServer: Portico Services no longer knows this server (its owner removed it from
// their account). The server stops calling and tells its owner to link again or switch to
// local sign-in.
var ErrUnknownServer = errors.New("portico services does not know this server")

// BusyError: Portico Services answered "busy" or "slow down". Wait is how long it said to wait.
type BusyError struct{ Wait time.Duration }

func (e *BusyError) Error() string {
	return fmt.Sprintf("portico services is busy; retry in %s", e.Wait)
}

// ClockError: Portico Services refused a request because this server's clock is wrong. Off is
// how far this server's clock is ahead of Portico Services' (negative when it is behind).
type ClockError struct{ Off time.Duration }

func (e *ClockError) Error() string { return fmt.Sprintf("this server's clock is %s out", e.Off) }

// RefusedError: Portico Services answered and said no, for a reason that trying again will
// not change by itself. Code is its word for the reason ("owner_not_approved", "invalid");
// Detail is its whole answer, for the log.
type RefusedError struct {
	Status int
	Code   string
	Detail string
}

func (e *RefusedError) Error() string {
	return fmt.Sprintf("portico services refused the request: %d %s", e.Status, e.Code)
}

// Purpose is what an approval is for. The words are the contract's.
type Purpose string

const (
	// PurposeSetup makes the approving Portico Account the owner of a new server.
	PurposeSetup Purpose = trust.PurposeSetup
	// PurposeSwitch moves a server over to Portico Accounts, or links it again; the owner approves.
	PurposeSwitch Purpose = trust.PurposeSwitch
	// PurposeChangeOwner makes the approving account the server's new owner.
	PurposeChangeOwner Purpose = trust.PurposeChangeOwner
)

// ApprovalRequest asks Portico Services for an address where a person approves this server.
type ApprovalRequest struct {
	ServerID   string
	ServerName string
	Purpose    Purpose
	// ReturnURL is where the person's browser comes back to with the approval.
	ReturnURL string
}

// Approval is where to send the person, and until when that works.
type Approval struct {
	// ID names the approval when asking how it stands (PollApproval).
	ID  string
	URL string
	// Code is what a person types on a phone or another device to find this approval, shown
	// as XXXX-XXXX. The setup page shows it so the person can match it.
	Code string
	// PollToken is a secret this server made for the page that started the approval. Asking
	// how the approval stands needs it, so the approval comes back only to that page, never
	// to someone who merely saw the approval's address.
	PollToken string
	ExpiresAt time.Time
	// PollAfter is the shortest wait between two asks about this approval.
	PollAfter time.Duration
}

// Account is a Portico Account as the server sees it.
type Account struct {
	// ID is the account's id at Portico Services, stored as users.portico_account_id. It is
	// the identity: nothing else about an account is ever used to match one.
	ID          string
	Username    string
	DisplayName string
	// Audience is filled only by a verified sign-in note for a server-page return.
	Audience string
	// Email is filled by a sign-in, never by an approval. The server's owner and
	// administrators see it in People; the server does nothing else with it.
	Email string
	// PasswordChecked comes only from a verified sign-in note, never an approval.
	PasswordChecked bool
	// ProviderChecked is a verified fresh provider proof for a password-less account.
	ProviderChecked bool
}

// Picture is where one profile's picture can be read. See trust.Picture.
type Picture = trust.Picture

// Decision is an approval that came back, checked: who approved, for which server name, for
// what, and when. FinishApproval returns one only for an approval made for this server.
type Decision struct {
	Account    Account
	ServerName string
	Purpose    Purpose
	// IssuedAt is when the person approved. A setup or switch approval makes a new link, and
	// that link's numbers start at this moment.
	IssuedAt time.Time
}

// Service is what the server's request handlers ask of Portico Services. Only StartApproval
// and PollApproval use the network.
type Service interface {
	// StartApproval asks for an approval address. Nothing is saved on the server: the
	// approval that comes back carries everything the finish needs. It does not retry: a
	// person is waiting. Its failures are ErrNotConnected, *BusyError and *ClockError.
	StartApproval(ctx context.Context, req ApprovalRequest) (Approval, error)
	// PollApproval asks how an approval stands, for a person who approves on another device.
	// It answers the approval once it is given, to hand to FinishApproval, and "" while the
	// person has not answered. Declined, ended, not started by this server or asked about
	// with the wrong token is ErrDeclined.
	PollApproval(ctx context.Context, id, pollToken string) (returned string, err error)
	// FinishApproval checks an approval the person's browser or app brought back and says
	// who approved what. It asks nobody, so it works with Portico Services out of reach.
	// Anything that does not check out, names another server, has run out or was finished
	// before is ErrDeclined.
	FinishApproval(ctx context.Context, serverID, returned string) (Decision, error)
	// VerifyIdentity checks a sign-in note: Portico Services' word that this is that account,
	// speaking to this server, answering the challenge this server just gave. It asks nobody.
	// It grants nothing: the server decides from its own tables whether the account may come in.
	VerifyIdentity(serverID, challenge, returned string) (Account, []Picture, error)
}

// NotConnected is a Service that reaches nothing. Tests use it where Portico Services must
// not be asked.
type NotConnected struct{}

func (NotConnected) StartApproval(context.Context, ApprovalRequest) (Approval, error) {
	return Approval{}, ErrNotConnected
}
func (NotConnected) PollApproval(context.Context, string, string) (string, error) {
	return "", ErrNotConnected
}
func (NotConnected) FinishApproval(context.Context, string, string) (Decision, error) {
	return Decision{}, ErrNotConnected
}
func (NotConnected) VerifyIdentity(string, string, string) (Account, []Picture, error) {
	return Account{}, nil, ErrNotConnected
}

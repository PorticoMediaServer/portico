package trust

import (
	"crypto/ed25519"
	"encoding/json"
	"time"
)

// The kinds of document Portico Services signs. A document is only ever accepted as the
// kind the caller asked for, so one can never be passed off as another.
const (
	IdentityKind   = "portico.identity.v1" // the name released apps already send to servers
	NoteKind       = "portico.server-note"
	ApprovalKind   = "portico.server-approval"
	ServerListKind = "portico.server-list"
)

// Identity says "this is Portico Account A, speaking to server S, answering the challenge S
// just gave". It grants nothing: the server decides from its own tables whether the account
// may come in. It lasts five minutes. The fields up to ExpiresAt are the first service's,
// in its order.
type Identity struct {
	Kind        string `json:"kind"`
	AccountID   string `json:"accountId"`
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
	// Email is the account's confirmed address. The server shows it to its owner and
	// administrators in People and does nothing else with it: a server sends no email.
	Email     string    `json:"email"`
	ServerID  string    `json:"serverId"`
	Challenge string    `json:"challenge"`
	Nonce     string    `json:"nonce"`
	IssuedAt  time.Time `json:"issuedAt"`
	ExpiresAt time.Time `json:"expiresAt"`
	// Pictures are where the account's profile pictures on this server can be read. The
	// server keeps the addresses and hands them to apps; it never fetches a picture.
	Pictures []Picture `json:"pictures"`
}

// Picture is one profile's picture. ProfileID names a profile on this server; it is empty
// for the account's own picture, which the server shows for the account's main profile
// unless a picture for that profile's own id is also listed. The server uses a picture only
// for a profile that belongs to the account the note is about. The addresses never change
// and can be kept for good.
type Picture struct {
	ProfileID string `json:"profileId"`
	Small     string `json:"small"`
	Large     string `json:"large"`
}

// Note is what Portico Services has to tell one server. It holds everything the server has
// not yet acknowledged, so any one note makes the server current, and a note that arrives
// twice, late or after a newer one changes nothing.
//
// A note belongs to one link between a server and Services. The revision of a link starts
// at the moment its setup or switch approval was issued (Unix seconds), and the server sets
// its applied number to that when it finishes THAT approval, so every note of an earlier
// link, whoever still holds a copy, is below the new link's first number and is ignored. A
// change of owner is not a new link: it leaves the numbers alone on both sides.
//
// The server applies Left and Unlinked only when Revision is higher than the last it
// applied. A higher Fleet alone moves only the fleet number and the kept certificate.
type Note struct {
	Kind     string `json:"kind"`
	ServerID string `json:"serverId"`
	// Revision rises each time Services has something new for this server.
	Revision int64 `json:"revision"`
	// Fleet rises when there is news for every server. Today that is only a withdrawn
	// signing key, and the news itself is the certificate this note is signed with. It is
	// the Unix time the news was raised, so it too survives Services starting again empty.
	Fleet int64 `json:"fleet"`
	// Left are the accounts that left this server through their Portico Account or were
	// deleted. The server marks each one Left and deletes nothing.
	Left []string `json:"left"`
	// Unlinked says the owner removed this server from their Portico Account.
	Unlinked  bool      `json:"unlinked"`
	IssuedAt  time.Time `json:"issuedAt"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// NoteLifetime is how long a signed note may travel. Services signs a fresh one each time it
// delivers, so only a copy an app is carrying can grow old, and the daily check-in has a
// fresh one long before this.
const NoteLifetime = 7 * 24 * time.Hour

// The purposes an approval can have. They are the server's own words for them.
const (
	PurposeSetup       = "setup"        // the approving account becomes the owner of a new server
	PurposeSwitch      = "switch"       // the owner moves the server to Portico Accounts
	PurposeChangeOwner = "change_owner" // the approving account becomes the new owner
)

// Approval says "account A approved server S, under this name, for this purpose". The
// server keeps nothing between asking and finishing, so the approval carries all of it.
// Services gives one only for a request signed by the server's identity key, and it names
// that server, so an approval is no use anywhere else.
type Approval struct {
	Kind        string    `json:"kind"`
	ApprovalID  string    `json:"approvalId"`
	ServerID    string    `json:"serverId"`
	ServerName  string    `json:"serverName"`
	Purpose     string    `json:"purpose"`
	AccountID   string    `json:"accountId"`
	Username    string    `json:"username"`
	DisplayName string    `json:"displayName"`
	IssuedAt    time.Time `json:"issuedAt"`
	ExpiresAt   time.Time `json:"expiresAt"`
}

// ServerList is an account's servers with each one's identity and addresses, so an app
// never meets a Portico Accounts server it cannot recognise.
type ServerList struct {
	Kind      string `json:"kind"`
	AccountID string `json:"accountId"`
	// Version rises whenever the list changes. It is inside the signature so that an app
	// can tell a list is older than the one it holds: without it, a true list from before
	// a server was removed or linked again with a new key could be given to the app again.
	Version  int64          `json:"version"`
	Servers  []ListedServer `json:"servers"`
	IssuedAt time.Time      `json:"issuedAt"`
}

type ListedServer struct {
	ServerID  string   `json:"serverId"`
	PublicKey string   `json:"publicKey"`
	Name      string   `json:"name"`
	Role      string   `json:"role"`
	Owner     bool     `json:"owner"`
	Addresses []string `json:"addresses"`
}

// Verifier is what a server or an app keeps to check documents: the root it was built with
// and the withdrawn list from the newest certificate it has seen.
type Verifier struct {
	Root      ed25519.PublicKey
	Withdrawn []string
}

func open[T any](v Verifier, e Envelope, now time.Time, kind string, into *T) error {
	payload, err := e.Verify(v.Root, now, v.Withdrawn)
	if err != nil {
		return err
	}
	var head struct {
		Kind string `json:"kind"`
	}
	if json.Unmarshal(payload, &head) != nil || head.Kind != kind || json.Unmarshal(payload, into) != nil {
		return ErrTrust
	}
	return nil
}

// OpenIdentity checks a sign-in note for this server and the challenge it gave.
func (v Verifier) OpenIdentity(e Envelope, serverID, challenge string, now time.Time) (Identity, error) {
	var id Identity
	if err := open(v, e, now, IdentityKind, &id); err != nil {
		return Identity{}, err
	}
	if id.ServerID != serverID || id.Challenge != challenge || challenge == "" || id.AccountID == "" ||
		now.Before(id.IssuedAt.Add(-time.Minute)) || !now.Before(id.ExpiresAt) || id.ExpiresAt.Sub(id.IssuedAt) > 10*time.Minute {
		return Identity{}, ErrTrust
	}
	return id, nil
}

// OpenNote checks a note addressed to this server. The caller applies it only when its
// revision or fleet number is higher than the last it applied, and acknowledges it either
// way.
func (v Verifier) OpenNote(e Envelope, serverID string, now time.Time) (Note, error) {
	var n Note
	if err := open(v, e, now, NoteKind, &n); err != nil {
		return Note{}, err
	}
	if n.ServerID != serverID || n.Revision < 0 || n.Fleet < 0 || !now.Before(n.ExpiresAt) || n.ExpiresAt.Sub(n.IssuedAt) > NoteLifetime+time.Hour {
		return Note{}, ErrTrust
	}
	return n, nil
}

// Returned is an envelope as it travels in an address or a form field: its JSON in
// base64url. It is how an approval comes back to the server in the browser's address.
func Returned(e Envelope) string {
	raw, _ := json.Marshal(e)
	return encoding.EncodeToString(raw)
}

// ParseReturned reads what Returned wrote.
func ParseReturned(returned string) (Envelope, error) {
	var e Envelope
	raw, err := encoding.DecodeString(returned)
	if err != nil || len(raw) > 16<<10 || json.Unmarshal(raw, &e) != nil {
		return Envelope{}, ErrTrust
	}
	return e, nil
}

// OpenApproval checks an approval for this server and the purpose the caller is finishing,
// so an approval given for one thing can never finish another. The server also remembers
// the ApprovalID of each approval it has finished for as long as an approval lasts, and
// refuses a second use.
func (v Verifier) OpenApproval(e Envelope, serverID, purpose string, now time.Time) (Approval, error) {
	var a Approval
	if err := open(v, e, now, ApprovalKind, &a); err != nil {
		return Approval{}, err
	}
	if a.ServerID != serverID || a.Purpose != purpose || purpose == "" || a.AccountID == "" || a.ApprovalID == "" || !now.Before(a.ExpiresAt) {
		return Approval{}, ErrTrust
	}
	return a, nil
}

// OpenServerList checks an account's server list. held is the version of the list the app
// already holds (0 if it holds none): a list with a lower version is refused, however
// well signed, because it is from before a change the app has already seen. There is no
// end date on a list: an app re-reads it when the free check says it changed, not on a
// clock. Every listed server's id must be the digest of its key, so no mistake anywhere
// can tie an id to another key.
func (v Verifier) OpenServerList(e Envelope, accountID string, held int64, now time.Time) (ServerList, error) {
	var l ServerList
	if err := open(v, e, now, ServerListKind, &l); err != nil {
		return ServerList{}, err
	}
	if l.AccountID != accountID || l.Version < held || l.Version < 1 {
		return ServerList{}, ErrTrust
	}
	for _, server := range l.Servers {
		key, err := encoding.DecodeString(server.PublicKey)
		if err != nil {
			return ServerList{}, ErrTrust
		}
		if id, err := ServerID(key); err != nil || id != server.ServerID {
			return ServerList{}, ErrTrust
		}
	}
	return l, nil
}

// Newer says whether a certificate just seen replaces the one the caller keeps. A caller
// keeps the certificate with the highest revision, and its Revoked list is the Withdrawn
// list above.
func Newer(kept, seen *Certificate) bool {
	return seen != nil && (kept == nil || seen.Revision > kept.Revision)
}

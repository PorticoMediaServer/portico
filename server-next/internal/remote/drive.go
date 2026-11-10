package remote

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Google Drive is reached through rclone's Drive backend, signed in with the owner's own
// Google credentials (never a sign-in shared with other Portico servers): a service account
// key, or the owner's own OAuth client and a sign-in with it. Portico only ever reads Drive.

// The fixed Google addresses. Tests point them at a stand-in.
var (
	GoogleAuthURL  = "https://accounts.google.com/o/oauth2/v2/auth"
	GoogleTokenURL = "https://oauth2.googleapis.com/token"
)

// DriveRedirect is where Google sends the owner's browser after the sign-in. Nothing listens
// there: the page does not load, and the owner copies its address back into Portico. A
// desktop OAuth client accepts it without it being registered, and it works wherever the
// browser is.
const DriveRedirect = "http://127.0.0.1:53682/"

const driveScope = "https://www.googleapis.com/auth/drive.readonly"

// driveOptions are rclone's options for a Drive source.
func driveOptions(c Config, s Secret) (map[string]string, error) {
	options := map[string]string{"type": "drive", "scope": "drive.readonly"}
	switch {
	case s.DriveKey != "":
		options["service_account_credentials"] = s.DriveKey
	case s.DriveToken != "" && c.DriveClientID != "":
		options["client_id"], options["client_secret"], options["token"] = c.DriveClientID, s.DriveClientSecret, s.DriveToken
	default:
		return nil, ErrSignIn
	}
	return options, nil
}

// CheckServiceAccountKey checks that a pasted key is a Google service account key.
func CheckServiceAccountKey(key string) error {
	var k struct {
		Type        string `json:"type"`
		ClientEmail string `json:"client_email"`
		PrivateKey  string `json:"private_key"`
	}
	if json.Unmarshal([]byte(key), &k) != nil || k.Type != "service_account" || k.ClientEmail == "" || k.PrivateKey == "" {
		return errors.New("the key is not the JSON file Google gives for a service account")
	}
	return nil
}

// DriveSignIns are Google sign-ins the owner has started and not finished. They live in
// memory: a restart loses a sign-in under way, never a saved source.
type DriveSignIns struct {
	mu      sync.Mutex
	pending map[string]driveSignIn
}

type driveSignIn struct {
	clientID, clientSecret, verifier string
	expires                          time.Time
}

// Start begins a sign-in with the owner's OAuth client and returns its id and the address
// to open in the browser.
func (d *DriveSignIns) Start(clientID, clientSecret string) (id, address string) {
	verifier, state := token32(), token32()
	challenge := sha256.Sum256([]byte(verifier))
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.pending == nil {
		d.pending = map[string]driveSignIn{}
	}
	now := time.Now()
	for k, p := range d.pending {
		if now.After(p.expires) {
			delete(d.pending, k)
		}
	}
	d.pending[state] = driveSignIn{clientID: clientID, clientSecret: clientSecret, verifier: verifier, expires: now.Add(15 * time.Minute)}
	q := url.Values{"client_id": {clientID}, "redirect_uri": {DriveRedirect}, "response_type": {"code"}, "scope": {driveScope},
		"access_type": {"offline"}, "prompt": {"consent"}, "state": {state}, "code_challenge": {base64.RawURLEncoding.EncodeToString(challenge[:])},
		"code_challenge_method": {"S256"}}
	return state, GoogleAuthURL + "?" + q.Encode()
}

// Finish takes the address the browser was sent to, exchanges its code once at Google's
// token address, and returns the client id, its secret and the token as rclone keeps it. A
// sign-in is used once.
func (d *DriveSignIns) Finish(ctx context.Context, id, redirected string) (clientID, clientSecret, token string, err error) {
	d.mu.Lock()
	p, ok := d.pending[id]
	delete(d.pending, id)
	d.mu.Unlock()
	if !ok || time.Now().After(p.expires) {
		return "", "", "", errors.New("this sign-in is over: start it again")
	}
	back, err := url.Parse(strings.TrimSpace(redirected))
	if err != nil {
		return "", "", "", errors.New("paste the whole address the browser was sent to")
	}
	q := back.Query()
	if q.Get("state") != id {
		return "", "", "", errors.New("that address is from another sign-in")
	}
	if q.Get("error") != "" {
		return "", "", "", errors.New("Google did not allow the sign-in")
	}
	code := q.Get("code")
	if code == "" || len(code) > 4096 {
		return "", "", "", errors.New("paste the whole address the browser was sent to")
	}
	form := url.Values{"code": {code}, "client_id": {p.clientID}, "client_secret": {p.clientSecret}, "redirect_uri": {DriveRedirect},
		"grant_type": {"authorization_code"}, "code_verifier": {p.verifier}}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, GoogleTokenURL, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", "", fmt.Errorf("Google could not be reached: %w", err)
	}
	defer resp.Body.Close()
	var answer struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&answer) != nil ||
		answer.AccessToken == "" || answer.RefreshToken == "" {
		return "", "", "", errors.New("Google did not accept the sign-in: check the client id and secret")
	}
	if answer.TokenType == "" {
		answer.TokenType = "Bearer"
	}
	saved, _ := json.Marshal(map[string]string{"access_token": answer.AccessToken, "token_type": answer.TokenType, "refresh_token": answer.RefreshToken,
		"expiry": time.Now().Add(time.Duration(answer.ExpiresIn) * time.Second).UTC().Format(time.RFC3339)})
	return p.clientID, p.clientSecret, string(saved), nil
}

func token32() string {
	var b [32]byte
	rand.Read(b[:])
	return base64.RawURLEncoding.EncodeToString(b[:])
}

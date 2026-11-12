package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/metadata"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/fetch"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/opensubtitles"
)

// The owner's OpenSubtitles key and account on Server › Metadata, which subtitle search needs
// (definition area 7, rule 67; area 4, rule 8). They are sealed together in one row, and the
// key and password are never answered.

// openSubtitlesAccount is what is sealed for OpenSubtitles.
type openSubtitlesAccount struct {
	APIKey   string `json:"apiKey,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

func (a openSubtitlesAccount) complete() bool {
	return a.APIKey != "" && a.Username != "" && a.Password != ""
}

// subtitleCheck is what Test connection last found, kept in memory: after a restart or a
// change to the key or account, the connection is not tested yet.
type subtitleCheck struct {
	at        time.Time
	signedIn  bool
	problem   string
	retryAt   time.Time
	resetAt   time.Time
	allowance *opensubtitles.Allowance
}

// OpenSubtitlesStatus says where the key and account in force come from and how OpenSubtitles
// last answered. It never carries a key or password.
type OpenSubtitlesStatus struct {
	// KeySource and AccountSource are "owner" (saved on this page) or "none".
	KeySource     string `json:"keySource"`
	AccountSource string `json:"accountSource"`
	// Account is "none" (no username), "incomplete" (a username without a password or a key),
	// "not_checked", "signed_in" or "rejected".
	Account string `json:"account"`
	// Problem is what the last test met, or "": provider_key_rejected, provider_sign_in_failed,
	// provider_rate_limited, provider_quota_exhausted, provider_unreachable; or
	// provider_key_unreadable for a key and account saved that this server can no longer open.
	Problem   string `json:"problem,omitempty"`
	CheckedAt string `json:"checkedAt,omitempty"`
	RetryAt   string `json:"retryAt,omitempty"`
	// DownloadsRemaining and DownloadsAllowed are the account's downloads left today and in a
	// day, as the last test read them; -1 when not known.
	DownloadsRemaining int    `json:"downloadsRemaining"`
	DownloadsAllowed   int    `json:"downloadsAllowed"`
	DownloadsResetAt   string `json:"downloadsResetAt,omitempty"`
}

func instant(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// subtitleStatus is the status as the page shows it. Called with keys.mu held.
func (s *Server) subtitleStatus() OpenSubtitlesStatus {
	a, c := s.keys.subtitles, s.keys.checked
	out := OpenSubtitlesStatus{KeySource: "none", AccountSource: "none", Account: "none", DownloadsRemaining: -1, DownloadsAllowed: -1,
		Problem: c.problem, CheckedAt: instant(c.at), RetryAt: instant(c.retryAt), DownloadsResetAt: instant(c.resetAt)}
	if a.APIKey != "" {
		out.KeySource = "owner"
	}
	switch {
	case a.Username == "":
	case !a.complete():
		out.AccountSource, out.Account = "owner", "incomplete"
	case c.at.IsZero():
		out.AccountSource, out.Account = "owner", "not_checked"
	case c.signedIn:
		out.AccountSource, out.Account = "owner", "signed_in"
	case c.problem == "provider_sign_in_failed":
		out.AccountSource, out.Account = "owner", "rejected"
	default:
		out.AccountSource, out.Account = "owner", "not_checked"
	}
	if c.allowance != nil {
		out.DownloadsRemaining, out.DownloadsAllowed = c.allowance.Remaining, c.allowance.Allowed
	}
	if s.keys.unreadable[settingOpenSubtitles] && a == (openSubtitlesAccount{}) {
		out.Problem = "provider_key_unreadable"
	}
	return out
}

// saveSubtitleAccount saves the OpenSubtitles fields of a save of the page, if it has any.
func (s *Server) saveSubtitleAccount(ctx context.Context, req MetadataSettingsChange) error {
	if req.OpenSubtitlesAPIKey == nil && req.OpenSubtitlesUsername == nil && req.OpenSubtitlesPassword == nil {
		return nil
	}
	s.keys.mu.Lock()
	next := s.keys.subtitles
	s.keys.mu.Unlock()
	if req.OpenSubtitlesAPIKey != nil {
		next.APIKey = *req.OpenSubtitlesAPIKey
	}
	if req.OpenSubtitlesUsername != nil && *req.OpenSubtitlesUsername != next.Username {
		// The password belongs to the account it was saved with.
		next.Username, next.Password = *req.OpenSubtitlesUsername, ""
	}
	if req.OpenSubtitlesPassword != nil {
		next.Password = *req.OpenSubtitlesPassword
	}
	if next.Username == "" {
		next.Password = ""
	}
	sealed := ""
	if next != (openSubtitlesAccount{}) {
		raw, _ := json.Marshal(next)
		sealed = string(raw)
	}
	if err := s.saveKey(ctx, settingOpenSubtitles, sealed); err != nil {
		return err
	}
	s.keys.mu.Lock()
	defer s.keys.mu.Unlock()
	s.keys.subtitles, s.keys.checked = next, subtitleCheck{}
	delete(s.keys.unreadable, settingOpenSubtitles)
	s.useSubtitles()
	return nil
}

// useSubtitles gives subtitle search a client with the owner's key and account, or none until
// all three are saved. A new client is made for every save, so a refused password is tried again
// only once the owner changes it. Called with keys.mu held.
func (s *Server) useSubtitles() {
	a := s.keys.subtitles
	if !a.complete() {
		s.metadata.UseSubtitles(nil)
		return
	}
	s.metadata.UseSubtitles(opensubtitles.New(s.keys.subtitleHost, s.keys.subtitleEvery, a.APIKey, a.Username, a.Password))
}

// checkOpenSubtitles is Test connection: OpenSubtitles is asked now, with the saved key and
// account, whether they work and how many downloads are left. Without all three there is
// nothing to ask, and the status says what is missing.
func (s *Server) checkOpenSubtitles(r *http.Request, _ None) (OpenSubtitlesStatus, error) {
	s.keys.mu.Lock()
	a, host, every := s.keys.subtitles, s.keys.subtitleHost, s.keys.subtitleEvery
	if !a.complete() {
		defer s.keys.mu.Unlock()
		return s.subtitleStatus(), nil
	}
	s.keys.mu.Unlock()
	allowance, err := opensubtitles.New(host, every, a.APIKey, a.Username, a.Password).Check(r.Context())
	if r.Context().Err() != nil {
		return OpenSubtitlesStatus{}, r.Context().Err()
	}
	c := subtitleCheck{at: s.now()}
	var pause *fetch.SlowDown
	var used *opensubtitles.AllowanceUsed
	switch {
	case err == nil:
		c.signedIn, c.allowance = true, &allowance
	case errors.Is(err, opensubtitles.ErrKeyRefused):
		c.problem = "provider_key_rejected"
	case errors.Is(err, opensubtitles.ErrSignInRefused):
		c.problem = "provider_sign_in_failed"
	case errors.As(err, &pause):
		c.problem, c.retryAt = "provider_rate_limited", pause.Until
	case errors.As(err, &used):
		c.signedIn, c.problem, c.resetAt = true, "provider_quota_exhausted", used.Renews
	default:
		c.problem = "provider_unreachable"
		s.log.Info("OpenSubtitles did not answer Test connection", "error", err)
	}
	s.keys.mu.Lock()
	defer s.keys.mu.Unlock()
	if s.keys.subtitles == a { // not changed while it was asked
		s.keys.checked = c
	}
	return s.subtitleStatus(), nil
}

// UseSubtitleHost points the server's OpenSubtitles client at a stand-in, for testing.
func (s *Server) UseSubtitleHost(origin string) {
	s.keys.mu.Lock()
	defer s.keys.mu.Unlock()
	s.keys.subtitleHost, s.keys.subtitleEvery = origin, 0
	s.useSubtitles()
}

var _ metadata.SubtitleProvider = (*opensubtitles.Client)(nil)

package metadata

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/artwork"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/fetch"
)

// PictureStore keeps a picture's bytes under its hash: the server's picture store.
type PictureStore interface {
	Put(id string, data []byte) error
}

// maxPictureBytes is the largest picture fetched from a provider.
const maxPictureBytes = 10 << 20

var pictureRoles = map[string]db.ImageRole{"poster": db.ImagePoster, "backdrop": db.ImageBackdrop, "logo": db.ImageLogo,
	"thumb": db.ImageThumb, "banner": db.ImageBanner, "cover": db.ImageCover, "portrait": db.ImagePortrait}

// pictureRedirects are the providers' picture hosts whose addresses answer with a redirect,
// each with the hosts it may send a download on to. A redirect is followed only from one of
// these hosts to one of its own, over https, at most three times, and never to an address on
// this machine or the local network. Every other redirect is refused: TMDB's picture server
// answers directly. Cover Art Archive's cover address sends the download on to the Internet
// Archive's numbered download hosts.
var pictureRedirects = map[string][]string{
	"coverartarchive.org": {"archive.org", "*.archive.org"},
}

// redirectsOverHTTP and redirectsToPrivate let a test's stand-in, on a plain address on this
// machine, play the provider. They are never set outside tests.
var redirectsOverHTTP, redirectsToPrivate bool

// pictureTimeout is the longest one picture's download may take, its redirects included. A
// picture host that only times out costs a pass downAfter of these before the pass stops.
var pictureTimeout = 20 * time.Second

// pictureWait is the longest pause a picture host may ask for (a 429, or a 503 with
// Retry-After) that a download waits out; a longer one fails it, as a host in trouble.
const pictureWait = 30 * time.Second

var pictureHTTP = &http.Client{CheckRedirect: checkPictureRedirect, Transport: pictureTransport()}

// pictureHosts are the pacing clients the pictures are downloaded through, one per host the
// addresses name, so a host's ask to slow down holds back its own downloads and no other's.
var pictureHosts = struct {
	sync.Mutex
	by map[string]*fetch.Client
}{by: map[string]*fetch.Client{}}

func pictureHost(host string) *fetch.Client {
	pictureHosts.Lock()
	defer pictureHosts.Unlock()
	c := pictureHosts.by[host]
	if c == nil {
		c = fetch.NewWith("the picture server "+host, 0, pictureWait, pictureHTTP, maxPictureBytes)
		pictureHosts.by[host] = c
	}
	return c
}

// errLiveCallInTest refuses a picture download a test makes from anything but this machine.
// The rule: tests never reach a live provider; they use a stand-in on 127.0.0.1. It holds for
// every address, a provider's picture server and Cover Art Archive's fixed one alike: fetch
// refuses the address, and the connection to a host a redirect leads to is refused too.
var errLiveCallInTest = fetch.ErrLiveCallInTest

// thisMachine reports whether host names this machine: localhost or a loopback address. Any
// other name is not looked up.
func thisMachine(host string) bool {
	ip := net.ParseIP(host)
	return host == "localhost" || ip != nil && ip.IsLoopback()
}

// pictureTransport connects to a host a redirect may lead to only at an address it has just
// checked is on the Internet, so a name that answered a public address when the redirect was
// checked cannot answer a local one when the connection is made.
func pictureTransport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	var dialer net.Dialer
	t.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if testing.Testing() && (err != nil || !thisMachine(host)) {
			return nil, fmt.Errorf("%s: %w", address, errLiveCallInTest)
		}
		if err != nil || redirectsToPrivate || !redirectTarget(strings.ToLower(host)) {
			return dialer.DialContext(ctx, network, address)
		}
		addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		for _, a := range addresses {
			if !fetch.PublicAddress(a.IP) {
				return nil, fmt.Errorf("the picture's address redirected to %s, which is not on the Internet", host)
			}
		}
		for _, a := range addresses {
			conn, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(a.IP.String(), port))
			if dialErr == nil {
				return conn, nil
			}
			err = dialErr
		}
		if err == nil {
			err = fmt.Errorf("%s has no address", host)
		}
		return nil, err
	}
	return t
}

// redirectTarget reports whether host is one a provider's redirect may lead to.
func redirectTarget(host string) bool {
	for _, targets := range pictureRedirects {
		for _, a := range targets {
			if host == a || strings.HasPrefix(a, "*.") && strings.HasSuffix(host, a[1:]) {
				return true
			}
		}
	}
	return false
}

// checkPictureRedirect says whether a picture download may follow a redirect to req.
func checkPictureRedirect(req *http.Request, via []*http.Request) error {
	if len(via) > 3 {
		return errors.New("the picture's address redirected more than three times")
	}
	allowed, ok := pictureRedirects[strings.ToLower(via[0].URL.Hostname())]
	host := strings.ToLower(req.URL.Hostname())
	known := false
	for _, a := range allowed {
		if host == a || strings.HasPrefix(a, "*.") && strings.HasSuffix(host, a[1:]) {
			known = true
		}
	}
	switch {
	case !ok || !known:
		return fmt.Errorf("the picture's address redirected to %s, which is not its provider's", host)
	case req.URL.Scheme != "https" && !redirectsOverHTTP:
		return errors.New("the picture's address redirected to a plain http address")
	}
	// The host's address is checked when the connection is made: see pictureTransport.
	return nil
}

// fetchPicture downloads one picture a provider offers. Every app sees it from the server's
// own copy and never asks the provider. A host's failure that is its own (no answer, a 5xx, a
// 429 or a pause) is told apart by providerOutage, as an API's is.
func fetchPicture(ctx context.Context, address string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, pictureTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	data, err := pictureHost(strings.ToLower(req.URL.Host)).Do(req)
	var status *fetch.Status
	switch {
	case errors.Is(err, fetch.ErrNotFound) || errors.As(err, &status) && status.Code == http.StatusGone:
		return nil, fmt.Errorf("the picture's address answered that it has none: %w", errPictureGone)
	case errors.Is(err, fetch.ErrTooLarge):
		return nil, fmt.Errorf("the provider's picture is too large: %w", errPictureGone)
	case err != nil:
		return nil, err
	case !strings.HasPrefix(http.DetectContentType(data), "image/"):
		return nil, fmt.Errorf("the provider's picture is not a picture: %w", errPictureGone)
	}
	return data, nil
}

// ErrPictureHostDown is a picture pass stopped because the pictures' host failed itself on
// several in a row. Every picture not fetched is still recorded, for the next pass.
var ErrPictureHostDown = errors.New("the provider's picture server did not answer")

// errPictureGone is a picture's address that will not give a picture: the provider no longer
// has it, or what it serves is too large or not a picture. Anything else may work next time.
var errPictureGone = errors.New("no picture at this address")

// FetchPictures is the picture pass for a library: it fetches the provider's pictures that
// lookups and refreshes recorded, keeps each in the picture store in its two sizes and shows it, one at a
// time and at Background priority, so it never holds up a lookup or a person. It does at most
// limit and says how many it stored. An address that gives no picture is dropped; one that
// cannot be had now is passed over and tried at the next pass. A picture host in trouble (no
// answer, a 5xx, a 429) for downAfter pictures in a row ends the pass with ErrPictureHostDown,
// as the lookup's rule for a provider that is down: the rest stay recorded for the next pass,
// which every lookup and refresh run starts.
func (s *Service) FetchPictures(ctx context.Context, libraryID int64, limit int) (int, error) {
	if s.Pictures == nil {
		return 0, nil
	}
	stored, after, inARow := 0, int64(0), 0
	for stored < limit {
		page, err := s.DB.PicturesToFetch(ctx, libraryID, after, 50)
		if err != nil || len(page) == 0 {
			return stored, err
		}
		for _, p := range page {
			after = p.ID
			data, err := fetchPicture(ctx, p.SourceURL)
			if ctx.Err() != nil {
				return stored, ctx.Err()
			}
			if errors.Is(err, errPictureGone) {
				if err := s.DB.PictureGone(ctx, p.ID); err != nil {
					return stored, err
				}
				continue
			}
			if err != nil && providerOutage(err) {
				if inARow++; inARow >= downAfter {
					return stored, fmt.Errorf("%w: %w", ErrPictureHostDown, err)
				}
				continue
			}
			inARow = 0
			if err != nil {
				continue // refused now: the next pass asks again
			}
			sum := sha256.Sum256(data)
			hash := hex.EncodeToString(sum[:])
			if err := s.Pictures.Put(hash, data); errors.Is(err, artwork.ErrNotAPicture) {
				if err := s.DB.PictureGone(ctx, p.ID); err != nil {
					return stored, err
				}
				continue
			} else if err != nil {
				return stored, err
			}
			if err := s.DB.PictureFetched(ctx, p.ID, hash); err != nil {
				return stored, err
			}
			if stored++; stored >= limit {
				return stored, nil
			}
		}
	}
	return stored, nil
}

// storePictures records the provider's pictures of a title that it does not have yet: the
// first of each role, or with role set every one of that role. A lookup or a refresh (role
// "") records only their addresses and moves on, so a title's details never wait for its
// pictures: the picture pass (FetchPictures) fetches and shows them after. Find more (role set)
// is a person waiting for the answer, so its pictures are fetched now. No transaction is open
// while they are fetched, and a picture that cannot be fetched never fails what it came with.
func (s *Service) storePictures(ctx context.Context, itemID int64, pictures []Picture, role string, priority db.Priority) error {
	if s.Pictures == nil || len(pictures) == 0 {
		return nil
	}
	have, err := s.DB.ProviderPictureURLs(ctx, itemID)
	if err != nil {
		return err
	}
	firsts := map[string]bool{}
	var found []db.NewPicture
	for _, p := range pictures {
		code, known := pictureRoles[p.Role]
		switch {
		case !known || p.URL == "", role != "" && p.Role != role, role == "" && firsts[p.Role]:
			continue
		}
		firsts[p.Role] = true
		if have[p.URL] {
			continue
		}
		if role == "" {
			found = append(found, db.NewPicture{Role: code, SourceURL: p.URL, Width: p.Width, Height: p.Height, Language: p.Language})
			continue
		}
		data, err := fetchPicture(ctx, p.URL)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			continue
		}
		sum := sha256.Sum256(data)
		hash := hex.EncodeToString(sum[:])
		// Stored in its two sizes now. A picture this server cannot read is passed over.
		if err := s.Pictures.Put(hash, data); errors.Is(err, artwork.ErrNotAPicture) {
			continue
		} else if err != nil {
			return err
		}
		found = append(found, db.NewPicture{Role: code, Hash: hash, SourceURL: p.URL, Width: p.Width, Height: p.Height, Language: p.Language})
	}
	return s.DB.AddProviderPictures(ctx, priority, itemID, found)
}

// ErrNotMatched is the answer to Find more for a title with no match to ask the provider about.
var ErrNotMatched = errors.New("this title has no match to ask about")

// FindPictures is Find more: it asks the provider for every picture it has of a title in one
// role and keeps those the title does not have yet, to choose from.
func (s *Service) FindPictures(ctx context.Context, itemID, role string) error {
	if _, known := pictureRoles[role]; !known {
		return fmt.Errorf("%w: %s: not a role a provider has pictures for", db.ErrInvalid, role)
	}
	t, p, locale, err := s.target(ctx, itemID)
	if err != nil {
		return err
	}
	ref := Ref{Provider: p.Name(), Kind: providerKinds[t.Kind], ID: ownID(p, t.ExternalIDs)}
	if ref.ID == "" {
		return ErrNotMatched
	}
	record, err := p.Details(ctx, ref, locale)
	if err != nil {
		return err
	}
	return s.storePictures(ctx, t.ID, record.Pictures, role, db.User)
}

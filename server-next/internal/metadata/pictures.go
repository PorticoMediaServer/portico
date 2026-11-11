package metadata

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/artwork"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
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

var pictureClient = &http.Client{Timeout: 30 * time.Second, CheckRedirect: checkPictureRedirect}

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
	if redirectsToPrivate {
		return nil
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(req.Context(), host)
	if err != nil {
		return err
	}
	for _, a := range addresses {
		if !publicAddress(a.IP) {
			return fmt.Errorf("the picture's address redirected to %s, which is not on the Internet", host)
		}
	}
	return nil
}

// publicAddress reports whether ip is on the Internet: not this machine, the local network,
// a link-local address or an unspecified one.
func publicAddress(ip net.IP) bool {
	return !(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast())
}

// fetchPicture downloads one picture a provider offers. Every app sees it from the server's
// own copy and never asks the provider.
func fetchPicture(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	res, err := pictureClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the picture's address answered %d", res.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, maxPictureBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxPictureBytes || !strings.HasPrefix(http.DetectContentType(data), "image/") {
		return nil, errors.New("the provider's picture is too large or not a picture")
	}
	return data, nil
}

// storePictures fetches the provider's pictures of a title that it does not have yet, keeps
// them in the picture store and records them: the first of each role, or with role set every
// one of that role. No transaction is open while they are fetched. A picture that cannot be
// fetched now is tried again at the next refresh, and never fails the lookup it came with.
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

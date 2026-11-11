package metadata

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

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

var pictureClient = &http.Client{Timeout: 30 * time.Second}

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
		if err := s.Pictures.Put(hash, data); err != nil {
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

package librarychannels

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"
)

var ErrNoLibraries = errors.New("Add a permitted movie, television or anime library before creating a Library Channel.")

// Defaults returns an unsaved custom draft. Membership, ordering, fallback and
// quality defaults are owned here, not synthesized by an editor or player.
func (s *Store) Defaults(ctx context.Context, a Authority, timezone string) (Config, error) {
	if _, err := time.LoadLocation(timezone); timezone == "" || err != nil {
		return Config{}, ErrInvalid
	}
	templates, err := s.Templates(ctx, a)
	if err != nil {
		return Config{}, err
	}
	chosen := -1
	for i, t := range templates {
		if t.Applicable {
			chosen = i
			break
		}
	}
	// A custom channel may start with fewer than a template's three items. Such a
	// draft is explicitly previewed before saving; no duration or candidate is made up.
	if chosen < 0 {
		for i, t := range templates {
			if len(t.Config.Rules[0].Query.LibraryIDs) > 0 {
				chosen = i
				break
			}
		}
	}
	if chosen < 0 {
		return Config{}, ErrNoLibraries
	}
	var random [24]byte
	if _, err := rand.Read(random[:]); err != nil {
		return Config{}, unavailable(err)
	}
	c := templates[chosen].Config
	c.ID = hex.EncodeToString(random[:])
	c.Seed = c.ID
	c.TemplateID = ""
	c.Name = "New Library Channel"
	c.Timezone = timezone
	c.DefaultRuleID = digest(c.ID, "main")[:48]
	c.Rules[0].ID = c.DefaultRuleID
	c.Rules[0].Name = "Main programming"
	return c, Validate(c)
}

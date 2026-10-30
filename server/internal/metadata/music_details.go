package metadata

import (
	"errors"
	"regexp"
	"sort"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
)

var musicISRC = regexp.MustCompile(`^[A-Za-z]{2}[A-Za-z0-9]{3}[0-9]{7}$`)

func (b *mbEvidenceBudget) recordingDetails(v metadataprovider.Recording) error {
	if len(v.ISRCs) > 128 || len(v.Relations) > 128 {
		return errPublicationInput
	}
	for _, id := range v.ISRCs {
		if !musicISRC.MatchString(id) {
			return errPublicationInput
		}
	}
	for _, relation := range v.Relations {
		if relation.TypeID != "" && !mbIdentifier.MatchString(relation.TypeID) {
			return errPublicationInput
		}
		for _, text := range []string{relation.Type, relation.TargetType, relation.Direction} {
			if e := b.text(text, 256); e != nil {
				return e
			}
		}
		if len(relation.Attributes) > 128 {
			return errPublicationInput
		}
		for _, a := range relation.Attributes {
			if e := b.text(a, 512); e != nil {
				return e
			}
		}
		if relation.Artist != nil {
			if relation.TargetType != "artist" || !mbIdentifier.MatchString(relation.Artist.ID) {
				return errPublicationInput
			}
			for _, v := range []string{relation.Artist.Name, relation.Artist.SortName, relation.Artist.Disambiguation} {
				if e := b.text(v, 2048); e != nil {
					return e
				}
			}
		}
		if relation.Work != nil {
			if relation.TargetType != "work" || !mbIdentifier.MatchString(relation.Work.ID) {
				return errPublicationInput
			}
			for _, v := range []string{relation.Work.Title, relation.Work.Type, relation.Work.Disambiguation} {
				if e := b.text(v, 4096); e != nil {
					return e
				}
			}
		}
	}
	return nil
}
func normalizeMusicRecordingDetails(v *metadataprovider.Recording) {
	for n := range v.ISRCs {
		v.ISRCs[n] = strings.ToUpper(v.ISRCs[n])
	}
	sort.Strings(v.ISRCs)
	for n := range v.Relations {
		r := &v.Relations[n]
		r.TypeID = strings.ToLower(r.TypeID)
		if r.Artist != nil {
			r.Artist.ID = strings.ToLower(r.Artist.ID)
		}
		if r.Work != nil {
			r.Work.ID = strings.ToLower(r.Work.ID)
		}
	}
}
func validateMusicReleaseDetails(v metadataprovider.Release) error {
	b := mbEvidenceBudget{}
	if len(v.LabelInfo) > 128 {
		return errors.New("MusicBrainz labels exceed budget")
	}
	for _, s := range []string{v.Status, v.Packaging} {
		if e := b.text(s, 256); e != nil {
			return e
		}
	}
	if v.TextRepresentation != nil {
		if e := b.text(v.TextRepresentation.Language, 128); e != nil {
			return e
		}
		if e := b.text(v.TextRepresentation.Script, 128); e != nil {
			return e
		}
	}
	for _, l := range v.LabelInfo {
		if e := b.text(l.CatalogNumber, 512); e != nil {
			return e
		}
		if l.Label != nil {
			if l.Label.ID != "" && !mbIdentifier.MatchString(l.Label.ID) {
				return errPublicationInput
			}
			if e := b.text(l.Label.Name, 2048); e != nil {
				return e
			}
		}
	}
	if len(v.Genres) > 64 {
		return errors.New("MusicBrainz release genres exceed budget")
	}
	for _, g := range v.Genres {
		if strings.TrimSpace(g.Name) == "" {
			return errPublicationInput
		}
		if e := b.text(g.Name, 200); e != nil {
			return e
		}
	}
	return nil
}
func normalizeMusicReleaseDetails(v *metadataprovider.Release) {
	for n := range v.LabelInfo {
		if v.LabelInfo[n].Label != nil {
			v.LabelInfo[n].Label.ID = strings.ToLower(v.LabelInfo[n].Label.ID)
		}
	}
}

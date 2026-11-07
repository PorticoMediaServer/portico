package read

import (
	"crypto/sha256"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/readingcap"
)

const rsReadingNativePolicy readPolicyID = 40
const rsReadingMobiPolicy readPolicyID = 41
const rsReadingPublicationCap = 64

func init() {
	for _, recipe := range []struct {
		id   readPolicyID
		mobi bool
	}{{rsReadingNativePolicy, false}, {rsReadingMobiPolicy, true}} {
		mobi := recipe.mobi
		registerReadPolicy(readPolicyRecipe{id: recipe.id, domain: "reading-leaf", kinds: []int64{20, 21}, validate: func(q browseQuery, in BrowseInput, f browseFence) (string, error) {
			if in.policy.subject <= 0 || f.container != 0 {
				return "", ErrInput
			}
			source, err := q("rs.reading.source", in.policy.subject, in.Library, in.Kind)
			if err != nil {
				return "", err
			}
			if len(source) != 1 {
				return "", ErrDenied
			}
			r := source[0]
			libs, err := q("rs.reading.library", f.library)
			if err != nil {
				return "", err
			}
			if len(libs) != 1 || in.Kind == 20 && libs[0][0] != "book" || in.Kind == 21 && libs[0][0] != "comic" {
				return "", ErrDenied
			}
			if r[4].(int64) != 0 || r[7].(int64) != 1 {
				return "", ErrDenied
			}
			if r[6] == nil && f.allowUnrated == 0 || r[6] != nil && f.maximumAge >= 0 && r[6].(int64) > f.maximumAge {
				return "", ErrDenied
			}
			pubs, err := rsReadingPublications(q, in.policy.subject, in.Library, f.library)
			if err != nil {
				return "", err
			}
			if len(pubs) > rsReadingPublicationCap {
				return "", ErrUnqualified
			}
			eligible := false
			for _, p := range pubs {
				if rsReadingFormat(in.Kind, p[1].(string), p[2].(int64) != 0, mobi) {
					eligible = true
				}
			}
			if !eligible {
				return "", ErrDenied
			}
			// Complete capped publication/local eligibility is rechecked in both
			// fence snapshots; inventory availability also has final point proofs.
			return fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%#v|%#v|%t|%t|%t", r, pubs, mobi, readingcap.CB7(), readingcap.CBR())))), nil
		}})
	}
}
func rsReadingFormat(kind int64, format string, local, mobi bool) bool {
	if kind == 20 {
		return format == "epub" || format == "pdf" || mobi && (format == "mobi" || format == "azw3")
	}
	if kind != 21 {
		return false
	}
	return format == "cbz" || format == "pdf" || local && (format == "cb7" && readingcap.CB7() || format == "cbr" && readingcap.CBR())
}

func rsReadingPublications(q browseQuery, item, nativeLibrary int64, library string) ([][]any, error) {
	pubs, err := q("rs.reading.publications", item, int64(rsReadingPublicationCap+1))
	if err != nil {
		return nil, err
	}
	if len(pubs) > rsReadingPublicationCap {
		return nil, ErrUnqualified
	}
	for n, p := range pubs {
		local := int64(0)
		if p[1] == "cb7" && readingcap.CB7() || p[1] == "cbr" && readingcap.CBR() {
			key, err := q("rs.reading.asset_key", p[0], nativeLibrary)
			if err != nil {
				return nil, err
			}
			if len(key) != 1 {
				return nil, ErrStale
			}
			rows, err := q("rs.reading.local", key[0][0], library, key[0][1], key[0][2])
			if err != nil {
				return nil, err
			}
			if len(rows) > 0 {
				local = 1
			}
		}
		pubs[n] = append(p, local)
	}
	return pubs, nil
}

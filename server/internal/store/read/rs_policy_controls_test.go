package read

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
	"testing/synctest"
	"time"
)

func TestRSReadingCompleteCaps(t *testing.T) {
	for _, family := range []string{"publications", "library-sources"} {
		t.Run(family, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f, r := rsReadingFixture(t)
				add := func(n int) {
					if family == "library-sources" {
						id := fmt.Sprintf("cap-%03d", n)
						root := fmt.Sprintf("/media/cap-%03d", n)
						f.exec(t, `INSERT INTO library_sources(id,library_id,kind,root,configured_root,incarnation,health)VALUES(?,'movies','local',?,?,'inc','ready')`, id, root, root)
					} else {
						f.exec(t, `INSERT INTO catalog_assets(id,token,path,size,modified_ns,container,video_codec,audio_codec,width,height,duration,library_id) VALUES(?,?,?,1000,1,'pdf','','',0,0,0,1)`, n, fmt.Sprintf("cap-asset-%03d", n), fmt.Sprintf("/media/cap-%03d.pdf", n))
						f.exec(t, `INSERT INTO catalog_asset_links(entity_id,asset_id,library_id)VALUES(1101,?,1)`, n)
						f.exec(t, `INSERT INTO reading_publications(item_id,asset_id,format)VALUES(1101,?,'pdf')`, n)
					}
				}
				f.exec(t, `BEGIN`)
				for n := 2; n <= 64; n++ {
					add(n)
				}
				f.exec(t, `COMMIT`)
				in := ReadingFactsInput{View: f.input, Item: 1101}
				in.View.Kind = 20
				page, _, e := r.facts(t.Context(), foregroundRequest(), time.Second, f.output, in)
				if e != nil || page.ID != 1101 || !page.Available {
					t.Fatal("legal complete64 refused", family, page, e)
				}
				add(65)
				page, _, e = r.facts(t.Context(), foregroundRequest(), time.Second, f.output, in)
				if !errors.Is(e, ErrUnqualified) || !reflect.DeepEqual(page, ReadingFacts{}) {
					t.Fatal("65th row silently truncated", family, page, e)
				}
			})
		})
	}
}

func TestRSSubjectRejectsMismatchedPublishedTuple(t *testing.T) {
	in := BrowseInput{Library: 1, Kind: 6, policy: readPolicyBinding{recipe: rsDetailSubjectPolicy, subject: 1}}
	source := []any{int64(1), int64(1), int64(6), int64(0), nil, int64(1), int64(0), int64(8), nil, nil, int64(2), int64(2), int64(9), make([]byte, 16), int64(1), nil, nil, int64(1), int64(1), int64(1), int64(1)}
	for _, phase := range []string{"published", "folding"} {
		for _, mismatch := range []string{"members", "minimum-age", "allow-age", "known-age"} {
			t.Run(phase+"/"+mismatch, func(t *testing.T) {
				image := []any{phase, int64(1), int64(1), int64(1), int64(0), int64(8), nil, nil}
				switch mismatch {
				case "members":
					image[3] = int64(2)
				case "minimum-age":
					image[5] = int64(18)
				case "allow-age":
					image[6] = int64(7)
				case "known-age":
					image[7] = int64(8)
				}
				_, e := rsDetailSubjectEligibility(func(name string, args ...any) ([][]any, error) {
					switch name {
					case "rs.subject.eligibility":
						return [][]any{source}, nil
					case "rs.subject.publication":
						return [][]any{image}, nil
					}
					t.Fatal("unexpected source query", name)
					return nil, ErrInput
				}, in, browseFence{container: 1, maximumAge: 12, allowUnrated: 0})
				if !errors.Is(e, ErrUnqualified) {
					t.Fatal("installed admission image mismatch admitted", e)
				}
			})
		}
	}
}

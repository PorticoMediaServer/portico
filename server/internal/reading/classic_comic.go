package reading

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"io"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/archive7z"
)

// OpenClassicComic reads a CBR or CB7 through the bounded member reader and the
// same page model as CBZ. Pages come only from real image members. A root
// ComicInfo.xml is admitted only as bounded, scalar, exact metadata; absent,
// oversized, malformed or ambiguous XML leaves every fact unknown rather than
// inventing or partially publishing one.
func OpenClassicComic(ctx context.Context, format string, ra io.ReaderAt, size int64) (*Comic, error) {
	members, err := openMembers(ctx, format, ra, size)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(members.Members()))
	for _, member := range members.Members() {
		if !isImageMember(member.Name) {
			continue
		}
		names = append(names, member.Name)
	}
	if len(names) == 0 {
		_ = members.Close()
		return nil, ErrInvalid
	}
	sortMembersStable(names)
	c := &Comic{members: members, Names: names}
	if body, ok := memberNamed(members, "ComicInfo.xml"); ok && body.Size > 0 && body.Size <= archive7z.MaxComicInfo {
		var expanded int64
		raw, readErr := boundedMemberRead(ctx, body, &expanded)
		switch {
		case readErr != nil && ctx.Err() != nil:
			_ = members.Close()
			return nil, ctx.Err()
		case readErr != nil && !errors.Is(readErr, ErrLimit) && !errors.Is(readErr, ErrInvalid):
			// A source/archive failure is a physical/currentness error, not a
			// reason to guess a metadata fact.
			_ = members.Close()
			return nil, readErr
		case readErr == nil:
			if facts, ok := parseClassicComicInfo(raw); ok {
				applyClassicComicInfo(c, facts)
			}
		}
	}
	return c, nil
}

const maxClassicComicDepth = 64

type classicComicInfoFacts struct {
	title, writer, series, number, language, manga string
	formats                                        []string
	formatsScalar                                  bool
}

// parseClassicComicInfo accepts only one unnamespaced ComicInfo root. A fact is
// published only when its direct child is unnamespaced, appears exactly once,
// contains chardata and no nested markup. Foreign-namespaced elements are not
// ComicInfo fields; duplicates and nested values decline the affected fact.
func parseClassicComicInfo(raw []byte) (classicComicInfoFacts, bool) {
	type field struct {
		value  string
		count  int
		nested bool
	}
	fields := map[string]*field{}
	var formatText strings.Builder
	formats := []string{}
	formatsScalar := true
	rootSeen, rootDone := false, false
	depth := 0
	current := ""
	var text strings.Builder
	decoder := xml.NewDecoder(bytes.NewReader(raw))
	decoder.Strict = true
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return classicComicInfoFacts{}, false
		}
		switch t := token.(type) {
		case xml.StartElement:
			if rootDone {
				return classicComicInfoFacts{}, false
			}
			if depth >= 1 && depth+1 > maxClassicComicDepth {
				return classicComicInfoFacts{}, false
			}
			switch {
			case depth == 0:
				if rootSeen || rootDone || t.Name.Space != "" || t.Name.Local != "ComicInfo" {
					return classicComicInfoFacts{}, false
				}
				rootSeen = true
				depth = 1
			case depth == 1 && t.Name.Space == "":
				current = t.Name.Local
				text.Reset()
				if current == "Format" {
					formatText.Reset()
				} else {
					f := fields[current]
					if f == nil {
						f = &field{}
						fields[current] = f
					}
					f.count++
				}
				depth = 2
			case depth >= 1:
				// Nested markup inside a direct child is not a scalar fact.
				if current == "Format" && depth == 2 {
					formatsScalar = false
				} else if current != "" {
					if f := fields[current]; f != nil {
						f.nested = true
					}
				}
				depth++
			default:
				return classicComicInfoFacts{}, false
			}
		case xml.CharData:
			if depth == 0 {
				// Only whitespace may surround the root. "<ComicInfo>…</ComicInfo>junk"
				// is not a valid ComicInfo document.
				if len(bytes.TrimSpace(t)) != 0 {
					return classicComicInfoFacts{}, false
				}
				continue
			}
			if depth >= 2 && current != "" {
				text.Write(t)
			}
		case xml.Comment:
			if rootDone {
				return classicComicInfoFacts{}, false
			}
		case xml.ProcInst:
			if rootDone {
				return classicComicInfoFacts{}, false
			}
			if !rootSeen && strings.EqualFold(t.Target, "xml") {
				continue
			}
			if !rootSeen {
				return classicComicInfoFacts{}, false
			}
		case xml.Directive:
			return classicComicInfoFacts{}, false
		case xml.EndElement:
			if depth == 0 {
				return classicComicInfoFacts{}, false
			}
			if depth == 1 {
				depth = 0
				rootDone = true
				current = ""
				continue
			}
			if depth < 1 {
				return classicComicInfoFacts{}, false
			}
			depth--
			if depth == 1 && current != "" {
				value := strings.TrimSpace(text.String())
				if current == "Format" {
					formats = append(formats, value)
				} else if f := fields[current]; f != nil && f.count == 1 && !f.nested {
					f.value = value
				}
				current = ""
			}
		}
	}
	if !rootSeen || !rootDone {
		return classicComicInfoFacts{}, false
	}
	single := func(name string, limit int) string {
		f := fields[name]
		if f == nil || f.count != 1 || f.nested || f.value == "" || len(f.value) > limit {
			return ""
		}
		return f.value
	}
	return classicComicInfoFacts{
		title:         single("Title", 4096),
		writer:        single("Writer", 4096),
		series:        single("Series", 4096),
		number:        single("Number", 64),
		language:      single("LanguageISO", 64),
		manga:         single("Manga", 64),
		formats:       formats,
		formatsScalar: formatsScalar,
	}, true
}

func applyClassicComicInfo(c *Comic, facts classicComicInfoFacts) {
	if facts.title != "" {
		c.Metadata.Title = facts.title
	}
	if facts.writer != "" {
		c.Metadata.Author = facts.writer
	}
	if facts.language != "" {
		c.Metadata.Language = facts.language
	}
	c.Series, c.SeriesNumber = facts.series, facts.number
	if facts.manga == "YesAndRightToLeft" {
		c.Metadata.Direction = "rtl"
	}
	c.Metadata.PublicationTypeField = "ComicInfo/Format"
	c.Metadata.PublicationTypeRaw, c.Metadata.PublicationTypeComplete = boundedTypeEvidence(facts.formats)
	c.Metadata.PublicationTypeComplete = c.Metadata.PublicationTypeComplete && facts.formatsScalar
	if c.Metadata.PublicationTypeComplete && len(facts.formats) == 1 {
		c.Metadata.ExplicitPublicationType = explicitPublicationType(facts.formats)
	}
}

func memberNamed(a memberArchive, name string) (archiveMember, bool) {
	for _, member := range a.Members() {
		if member.Name == name {
			return member, true
		}
	}
	return archiveMember{}, false
}

func sortMembersStable(names []string) {
	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && naturalLess(names[j], names[j-1]); j-- {
			names[j], names[j-1] = names[j-1], names[j]
		}
	}
}

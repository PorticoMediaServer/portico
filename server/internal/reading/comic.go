package reading

import (
	"bytes"
	"context"
	"encoding/xml"
	"image"
	_ "image/jpeg"
	"image/png"
	"io"
	"sort"
	"strings"
	"unicode"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

type Image struct {
	MIME          string
	Width, Height int
}
type Page struct {
	Index int
	Name  string
	Bytes []byte
	Image Image
}
type Comic struct {
	// pageReader is an accepted direct-folder member reader; nil keeps archive behavior.
	pageReader func(ctx context.Context, name string, limit int64) ([]byte, error)
	archive    *archive
	// members is the classic-archive reader (CBR/CB7); when set, pages come from it.
	members  memberArchive
	Names    []string
	Metadata Metadata
	// Series and SeriesNumber are the package's own ComicInfo series facts.
	Series, SeriesNumber string
}

func OpenComic(ctx context.Context, r io.ReaderAt, size int64) (*Comic, error) {
	a, err := openArchive(r, size)
	if err != nil {
		return nil, err
	}
	return openComicArchive(ctx, a)
}
func OpenSequentialComic(ctx context.Context, source SequentialSource) (*Comic, error) {
	a, err := openSequentialArchive(ctx, source)
	if err != nil {
		return nil, err
	}
	return openComicArchive(ctx, a)
}
func openComicArchive(ctx context.Context, a *archive) (*Comic, error) {
	c := &Comic{archive: a}
	for _, name := range a.order {
		switch extension(name) {
		case ".jpg", ".jpeg", ".png", ".webp":
			c.Names = append(c.Names, name)
		}
	}
	if len(c.Names) == 0 {
		return nil, ErrInvalid
	}
	sort.Slice(c.Names, func(i, j int) bool { return naturalLess(c.Names[i], c.Names[j]) })
	if a.files["ComicInfo.xml"] != nil {
		b, err := a.read(ctx, "ComicInfo.xml", 256<<10)
		if err != nil {
			return nil, err
		}
		var info struct {
			XMLName                           xml.Name
			Title, Writer, Manga, LanguageISO string
			Series, Number                    string
			Format                            []struct {
				XMLName xml.Name
				Value   string `xml:",chardata"`
				Inner   string `xml:",innerxml"`
			} `xml:"Format"`
		}
		if err = decodeXML(b, &info); err != nil {
			return nil, err
		}
		c.Metadata.Title, c.Metadata.Author, c.Metadata.Language = info.Title, info.Writer, info.LanguageISO
		c.Series, c.SeriesNumber = info.Series, info.Number
		c.Metadata.PublicationTypeField = "ComicInfo/Format"
		if info.XMLName.Space == "" && info.XMLName.Local == "ComicInfo" {
			values := []string{}
			scalar := true
			for _, field := range info.Format {
				if field.XMLName.Space == "" && field.XMLName.Local == "Format" {
					values = append(values, field.Value)
					scalar = scalar && !strings.Contains(field.Inner, "<")
				}
			}
			c.Metadata.PublicationTypeRaw, c.Metadata.PublicationTypeComplete = boundedTypeEvidence(values)
			c.Metadata.PublicationTypeComplete = c.Metadata.PublicationTypeComplete && scalar
			if c.Metadata.PublicationTypeComplete && len(values) == 1 {
				c.Metadata.ExplicitPublicationType = explicitPublicationType(values)
			}
		}
		if len(c.Metadata.Title) > 4096 || len(c.Metadata.Author) > 4096 || len(c.Metadata.Language) > 64 {
			return nil, ErrLimit
		}
		if info.Manga == "YesAndRightToLeft" {
			c.Metadata.Direction = "rtl"
		}
	}
	return c, nil
}

// readMember serves one page through whichever bounded archive backs this comic.
func (c *Comic) readMember(ctx context.Context, name string, limit int64) ([]byte, error) {
	if c.pageReader != nil {
		return c.pageReader(ctx, name, limit)
	}
	if c.archive != nil {
		return c.archive.read(ctx, name, limit)
	}
	if c.members == nil {
		return nil, ErrInvalid
	}
	for _, member := range c.members.Members() {
		if member.Name != name {
			continue
		}
		var expanded int64
		out, err := boundedMemberRead(ctx, member, &expanded)
		if err != nil {
			return nil, err
		}
		if int64(len(out)) > limit {
			return nil, ErrLimit
		}
		return out, nil
	}
	return nil, ErrInvalid
}

func inspectImage(b []byte) (Image, error) {
	c, kind, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		return Image{}, ErrInvalid
	}
	if kind != "jpeg" && kind != "png" && kind != "webp" {
		return Image{}, ErrInvalid
	}
	if c.Width < 1 || c.Height < 1 || c.Width > 32768 || c.Height > 32768 || int64(c.Width)*int64(c.Height) > 40000000 {
		return Image{}, ErrLimit
	}
	return Image{MIME: "image/" + kind, Width: c.Width, Height: c.Height}, nil
}

// One transform at a time bounds the worst-case decoded image memory. Waiting
// and every archive/source read remain owned by the request's cancellation.
var imageWork = make(chan struct{}, 1)

func (c *Comic) Page(ctx context.Context, index, width int) (Page, error) {
	if index < 0 || index >= len(c.Names) || (width != 0 && (width < 32 || width > 2048)) {
		return Page{}, ErrInvalid
	}
	name := c.Names[index]
	// The direct-folder member reader takes precedence when the publication was
	// opened as an admitted image folder; otherwise the archive path applies.
	b, err := c.readMember(ctx, name, MaxMember)
	if err != nil {
		return Page{}, err
	}
	info, err := inspectImage(b)
	if err != nil {
		return Page{}, err
	}
	if width != 0 && width < info.Width {
		select {
		case imageWork <- struct{}{}:
			defer func() { <-imageWork }()
		case <-ctx.Done():
			return Page{}, ctx.Err()
		}
		img, _, err := image.Decode(bytes.NewReader(b))
		if err != nil {
			return Page{}, ErrInvalid
		}
		if err = ctx.Err(); err != nil {
			return Page{}, err
		}
		height := max(1, int(int64(info.Height)*int64(width)/int64(info.Width)))
		out := image.NewRGBA(image.Rect(0, 0, width, height))
		draw.ApproxBiLinear.Scale(out, out.Bounds(), img, img.Bounds(), draw.Src, nil)
		var encoded bytes.Buffer
		if err = png.Encode(&encoded, out); err != nil {
			return Page{}, err
		}
		b = encoded.Bytes()
		info = Image{"image/png", width, height}
	}
	if err := ctx.Err(); err != nil {
		return Page{}, err
	}
	if width != 0 && len(b) > 16<<20 {
		return Page{}, ErrLimit
	}
	return Page{index, name, b, info}, nil
}

func naturalLess(a, b string) bool {
	aa, bb := strings.ToLower(a), strings.ToLower(b)
	for len(aa) > 0 && len(bb) > 0 {
		if aa[0] >= '0' && aa[0] <= '9' && bb[0] >= '0' && bb[0] <= '9' {
			i, j := 0, 0
			for i < len(aa) && unicode.IsDigit(rune(aa[i])) {
				i++
			}
			for j < len(bb) && unicode.IsDigit(rune(bb[j])) {
				j++
			}
			x, y := strings.TrimLeft(aa[:i], "0"), strings.TrimLeft(bb[:j], "0")
			if len(x) != len(y) {
				return len(x) < len(y)
			}
			if x != y {
				return x < y
			}
			if i != j {
				return i < j
			}
			aa, bb = aa[i:], bb[j:]
			continue
		}
		if aa[0] != bb[0] {
			return aa[0] < bb[0]
		}
		aa, bb = aa[1:], bb[1:]
	}
	if aa != bb {
		return aa < bb
	}
	return a < b
}

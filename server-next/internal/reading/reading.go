// Package reading opens books and comics for the apps' readers. Nothing is "open" on the
// server: opening a book reads its file's own list of parts once and hands the app an address
// that carries its own permission, under which each part, page or the whole file is read from
// the library file when asked for, a part at a time. A book's file is reached only through
// libraryfile, by the path the scanner recorded; inside an archive only the entries the book's
// own lists name can be asked for.
package reading

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/libraryfile"
)

// The formats this server opens, by the file's extension. A book in any other format the
// scanner lists (CBR, CB7, MOBI, AZW3) says it cannot be opened yet, and can be saved as a file.
const (
	FormatEPUB = "epub"
	FormatPDF  = "pdf"
	FormatCBZ  = "cbz"
)

// Why a book cannot be opened. Each is a sentence the app shows as it is.
var (
	ErrFormatLater = errors.New("This server can't open this kind of book yet.")
	ErrRemote      = errors.New("This server can't open books kept on remote storage yet.")
	ErrUnavailable = errors.New("This book's file can't be reached.")
)

// grantLifetime is how long a book's address works. It ends sooner with the sign-in that asked
// for it. An app whose address has ended asks for the book again, which gives it a new one,
// and carries on at the same page.
const grantLifetime = 4 * time.Hour

// Service opens books.
type Service struct {
	db  *db.DB
	key []byte
	now func() time.Time
	// FromHome says whether a request came from the home network. With it set, a book's
	// address is held to its account's home-only limit; viewing hours are held either way.
	FromHome func(*http.Request) bool

	mu    sync.Mutex
	books map[string]*epub // EPUB package documents already read, by file and fingerprint
	// checked remembers, for a few seconds, that an address's profile may still see its book,
	// so a page turn costs no database read. See allowed.
	checked map[string]checked
}

// Reader is who asked for a book: the sign-in, the stamp of the token it asked with, and the
// profile. A book's address is good only while all three still hold.
type Reader struct {
	SessionID int64
	Stamp     db.TokenStamp
	db.Viewer
}

// New makes the service. key signs the addresses it hands out; it is kept by the server so
// an address still works after a restart.
func New(d *db.DB, key string, now func() time.Time) *Service {
	return &Service{db: d, key: []byte(key), now: now, books: map[string]*epub{}, checked: map[string]checked{}}
}

// Opened is a book as its reader needs it.
type Opened struct {
	Version     db.BookVersion
	Format      string
	Fingerprint string
	// Problem says why the book cannot be read on this server; the fields below it are then empty.
	Problem error
	// MediaURL is the address its parts, pages and file are read under.
	MediaURL  string
	SaveURL   string
	Direction string
	Parts     []Part
	Spine     []string
	Contents  []Contents
	Pages     []string
}

// Format is a book file's format, from its extension.
func Format(fileName string) string {
	return strings.TrimPrefix(strings.ToLower(path.Ext(fileName)), ".")
}

// Fingerprint names one exact file of a book: its size and when it was last changed. A file
// that is replaced has a new one; one that is only moved keeps it, as the scanner does.
func Fingerprint(size, modifiedNS int64) string {
	sum := sha256.Sum256(fmt.Appendf(nil, "%d:%d", size, modifiedNS))
	return hex.EncodeToString(sum[:8])
}

// Choose picks the version to open: the one asked for, or else the first that can be read,
// EPUB before CBZ before PDF, since an EPUB fits any screen.
func Choose(book db.Book, version string) (db.BookVersion, bool) {
	if version != "" {
		for _, v := range book.Versions {
			if v.PublicID == version {
				return v, true
			}
		}
		return db.BookVersion{}, false
	}
	for _, format := range []string{FormatEPUB, FormatCBZ, FormatPDF} {
		for _, v := range book.Versions {
			if Format(v.FileName) == format && v.Path != "" {
				return v, true
			}
		}
	}
	if len(book.Versions) == 0 {
		return db.BookVersion{}, false
	}
	return book.Versions[0], true
}

// Open reads what a reader needs to know about one version of a book before it asks for any
// part of it. A book that cannot be read says why in Problem, and can still be saved.
func (s *Service) Open(ctx context.Context, who Reader, book db.Book, v db.BookVersion) Opened {
	out := Opened{Version: v, Format: Format(v.FileName), Fingerprint: Fingerprint(v.Size, v.ModifiedNS)}
	if v.Path != "" {
		g := grant{fileID: v.FileID, itemID: book.ID, reader: who, expires: s.now().Add(grantLifetime).Unix()}
		copy(g.fingerprint[:], fingerprintBytes(v.Size, v.ModifiedNS))
		out.MediaURL = "/v1/media/" + s.sign(g)
		if book.MaySave {
			out.SaveURL = out.MediaURL + "/source?save=1"
		}
	}
	switch {
	case out.Format != FormatEPUB && out.Format != FormatPDF && out.Format != FormatCBZ:
		out.Problem = ErrFormatLater
	case v.Path == "":
		out.Problem = ErrRemote
	}
	if out.Problem != nil || out.Format == FormatPDF {
		return out
	}
	err := s.withArchive(ctx, v, func(a *archive) error {
		if out.Format == FormatCBZ {
			c, err := readComic(a)
			if err == nil {
				out.Pages, out.Direction = c.pages, c.direction
			}
			return err
		}
		e, err := s.epub(v, a)
		if err == nil {
			out.Direction, out.Spine, out.Contents = e.direction, e.spine, e.contents
			out.Parts = e.parts
		}
		return err
	})
	if err != nil {
		out.Problem, out.Pages = err, nil
	}
	return out
}

// epub returns an EPUB's package, read once and then remembered for as long as the file is
// unchanged, so each part asked for does not read it again.
func (s *Service) epub(v db.BookVersion, a *archive) (*epub, error) {
	key := fmt.Sprint(v.FileID, ":", Fingerprint(v.Size, v.ModifiedNS))
	s.mu.Lock()
	e := s.books[key]
	s.mu.Unlock()
	if e != nil {
		return e, nil
	}
	e, err := readEPUB(a)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	if len(s.books) >= 64 {
		clear(s.books)
	}
	s.books[key] = e
	s.mu.Unlock()
	return e, nil
}

// withArchive opens a version's file through libraryfile and reads its list of entries. The
// file must still be the one the scanner recorded.
func (s *Service) withArchive(ctx context.Context, v db.BookVersion, fn func(*archive) error) error {
	folders, err := s.db.LibraryFolders(ctx)
	if err != nil {
		return err
	}
	f, err := libraryfile.Open(v.Path, folders)
	if err != nil {
		return ErrUnavailable
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Size() != v.Size || info.ModTime().UnixNano() != v.ModifiedNS {
		return ErrChanged
	}
	a, err := openArchive(f, info.Size())
	if err != nil {
		return err
	}
	return fn(a)
}

// ErrChanged says the file is no longer the one the reader was given; the app opens the book again.
var ErrChanged = errors.New("This book changed.")

// grant is what a book's address carries: which book and file, which exact copy of it, who
// asked (the sign-in, its token's stamp and the profile), and until when. It is signed, so it
// cannot be altered or made up. Every request through it checks that the sign-in still holds
// and that the profile may still see the book, so an address dies with its sign-in and with the
// profile's right to the book, and one that leaks is worth no more than the sign-in it came from.
type grant struct {
	fileID      int64
	itemID      int64
	fingerprint [8]byte
	reader      Reader
	expires     int64
}

const grantLen = 8*8 + 8 // seven numbers, the fingerprint and the expiry

func fingerprintBytes(size, modifiedNS int64) []byte {
	raw, _ := hex.DecodeString(Fingerprint(size, modifiedNS))
	return raw
}

// grantPrefix starts every book address, which no playback address can contain.
const grantPrefix = "b."

// IsGrant reports whether a media address belongs to a book.
func IsGrant(token string) bool { return strings.HasPrefix(token, grantPrefix) }

func (s *Service) sign(g grant) string {
	payload := make([]byte, 0, grantLen+16)
	for _, n := range []int64{g.fileID, g.itemID, g.reader.SessionID, g.reader.Stamp.Boot, g.reader.Stamp.Seq, g.reader.UserID,
		g.reader.ProfileID} {
		payload = binary.BigEndian.AppendUint64(payload, uint64(n))
	}
	payload = append(payload, g.fingerprint[:]...)
	payload = binary.BigEndian.AppendUint64(payload, uint64(g.expires))
	return grantPrefix + base64.RawURLEncoding.EncodeToString(append(payload, s.mac(payload)...))
}

func (s *Service) mac(payload []byte) []byte {
	m := hmac.New(sha256.New, s.key)
	m.Write([]byte("book address\x00"))
	m.Write(payload)
	return m.Sum(nil)[:16]
}

// parse checks a book address. One that was altered, made up or has ended is not a grant.
func (s *Service) parse(token string) (grant, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(token, grantPrefix))
	if !IsGrant(token) || err != nil || len(raw) != grantLen+16 || !hmac.Equal(raw[grantLen:], s.mac(raw[:grantLen])) {
		return grant{}, false
	}
	n := func(i int) int64 { return int64(binary.BigEndian.Uint64(raw[i*8:])) }
	g := grant{fileID: n(0), itemID: n(1), reader: Reader{SessionID: n(2), Stamp: db.TokenStamp{Boot: n(3), Seq: n(4)},
		Viewer: db.Viewer{UserID: n(5), ProfileID: n(6)}}, expires: n(8)}
	copy(g.fingerprint[:], raw[56:64])
	return g, s.now().Unix() < g.expires
}

// checkFor is how long the answer to "may this profile still see this book" is reused. A
// profile that loses a book stops reading it within this time.
const checkFor = 5 * time.Second

type checked struct {
	maySave bool
	until   time.Time
}

// allowed reports whether an address may still be used, and whether its profile may save the
// file. The sign-in is checked every time, in memory: ended, signed out, or changed since the
// address was given (another profile chosen, access or role changed) all end the address. The
// profile's right to the book, and to download, and the sign-in itself (the in-memory note of
// an ended sign-in is dropped after 16 minutes; an address lives for hours), are read from the
// database at most once every few seconds per address.
func (s *Service) allowed(ctx context.Context, token string, g grant) (ok, maySave bool, err error) {
	if g.reader.SessionID == 0 || s.db.TokenRefused(g.reader.SessionID, g.reader.Stamp) {
		return false, false, nil
	}
	now := s.now()
	s.mu.Lock()
	c, found := s.checked[token]
	s.mu.Unlock()
	if found && now.Before(c.until) {
		return true, c.maySave, nil
	}
	visible, maySave, err := s.db.BookAccess(ctx, g.reader.Viewer, g.reader.SessionID, g.itemID, g.fileID, now)
	if err != nil || !visible {
		return false, false, err
	}
	s.mu.Lock()
	if len(s.checked) >= 10_000 {
		clear(s.checked)
	}
	s.checked[token] = checked{maySave: maySave, until: now.Add(checkFor)}
	s.mu.Unlock()
	return true, maySave, nil
}

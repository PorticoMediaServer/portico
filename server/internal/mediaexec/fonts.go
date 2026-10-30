package mediaexec

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// Text subtitles are drawn by libass, which finds fonts through fontconfig.
// A confined decoder sees no host font directories, and a minimal host (or
// the production FFmpeg bundle's static fontconfig, whose compiled-in config
// path does not exist) has no usable configuration at all: every glyph was
// silently dropped and burned-in subtitles never appeared. Every decoder
// therefore gets a fontconfig file of Portico's own, naming the host's font
// directories (when there are any) and bundled Latin, CJK, Thai and Devanagari
// fallback faces, so common scripts also work on a minimal host.

//go:embed fonts/DejaVuSans.ttf
var bundledFont []byte

//go:embed fonts/LICENSE
var bundledFontLicense []byte

// Unmodified SIL OFL faces supply CJK, Thai and Devanagari glyphs on minimal
// hosts. Embedded subtitle attachments take precedence through libass fontsdir.
// Noto CJK revision f8d157532fbfaeda587e826d4cd5b21a49186f7c;
// Google Fonts revision 66928797f271fed5ee428544e7229685e2d90127.
//
//go:embed fonts/NotoSansCJKjp-Regular.otf
var notoCJKFont []byte

//go:embed fonts/NotoCJK-LICENSE
var notoCJKLicense []byte

//go:embed fonts/NotoSansThai.ttf
var notoThaiFont []byte

//go:embed fonts/NotoSansThai-LICENSE
var notoThaiLicense []byte

//go:embed fonts/NotoSansDevanagari.ttf
var notoDevanagariFont []byte

//go:embed fonts/NotoSansDevanagari-LICENSE
var notoDevanagariLicense []byte

var cjkTag = sync.OnceValue(func() string {
	sum := sha256.Sum256(notoCJKFont)
	return `"` + hex.EncodeToString(sum[:16]) + `"`
})

// CJKFont is the bundled Chinese, Japanese and Korean face and a strong ETag
// for it. Browsers that draw styled subtitles themselves fetch this one copy
// instead of carrying a second CJK font of their own.
func CJKFont() ([]byte, string) { return notoCJKFont, cjkTag() }

// hostFontDirs are read-only font sources a decoder may use when present:
// the distribution's configuration, symlinked rules and fonts on Linux, the
// system's fonts on macOS. Debian/Ubuntu conf.d links target /usr/share/fontconfig.
var hostFontDirs = []string{"/etc/fonts", "/usr/share/fontconfig", "/usr/share/fonts", "/usr/local/share/fonts", "/System/Library/Fonts", "/Library/Fonts"}

// FontPack is the materialized font set: Dir holds the bundled face and
// Config (FONTCONFIG_FILE); Host lists existing font/configuration sources.
type FontPack struct {
	Dir, Config string
	Host        []string
}

var fontPackMu sync.Mutex

// fontRoot is where the pack is written (tests point it elsewhere).
var fontRoot = os.TempDir

// Fonts writes the pack once per content (a directory named by its hash) and
// rewrites it when something removed it (a temporary-file cleaner).
func Fonts() (FontPack, error) {
	fontPackMu.Lock()
	defer fontPackMu.Unlock()
	var host []string
	sources := append([]string(nil), hostFontDirs...)
	if runtime.GOOS == "windows" {
		root := os.Getenv("SystemRoot")
		if filepath.IsAbs(root) && !strings.ContainsAny(root, "\n\r\x00") {
			sources = append(sources, filepath.Join(root, "Fonts"))
		}
	}
	for _, d := range sources {
		if info, e := os.Stat(d); e == nil && info.IsDir() {
			host = append(host, d)
		}
	}
	sum := sha256.New()
	sum.Write(bundledFont)
	want := map[string][]byte{"DejaVuSans.ttf": bundledFont, "LICENSE": bundledFontLicense}
	fallbacks := []struct {
		name string
		data []byte
	}{{"NotoSansCJKjp-Regular.otf", notoCJKFont}, {"NotoCJK-LICENSE", notoCJKLicense}, {"NotoSansThai.ttf", notoThaiFont}, {"NotoSansThai-LICENSE", notoThaiLicense}, {"NotoSansDevanagari.ttf", notoDevanagariFont}, {"NotoSansDevanagari-LICENSE", notoDevanagariLicense}}
	for _, font := range fallbacks {
		want[font.name] = font.data
		sum.Write(font.data)
	}
	sum.Write([]byte(strings.Join(host, "\n")))
	dir := filepath.Join(fontRoot(), "portico-fonts-"+hex.EncodeToString(sum.Sum(nil))[:16])
	if !filepath.IsAbs(dir) {
		return FontPack{}, errors.New("font directory is not absolute")
	}
	var conf strings.Builder
	conf.WriteString("<?xml version=\"1.0\"?>\n<!DOCTYPE fontconfig SYSTEM \"urn:fontconfig:fonts.dtd\">\n<fontconfig>\n")
	for _, d := range host {
		if d == "/usr/share/fontconfig" {
			continue // Fontconfig rule symlink targets; not a font directory.
		}
		if d == "/etc/fonts" {
			// The distribution's rules (aliases, language preferences) when it has them.
			conf.WriteString("  <include ignore_missing=\"yes\">/etc/fonts/fonts.conf</include>\n")
			continue
		}
		conf.WriteString("  <dir>" + fontXMLPath(d) + "</dir>\n")
	}
	conf.WriteString("  <dir>" + fontXMLPath(dir) + "</dir>\n  <cachedir>" + fontXMLPath(filepath.Join(os.TempDir(), "portico-fontconfig")) + "</cachedir>\n</fontconfig>\n")
	pack := FontPack{Dir: dir, Config: filepath.Join(dir, "fonts.conf"), Host: host}
	want["fonts.conf"] = []byte(conf.String())
	for name, body := range want {
		if have, e := os.ReadFile(filepath.Join(dir, name)); e == nil && bytes.Equal(have, body) {
			continue
		}
		if e := os.MkdirAll(dir, 0o755); e != nil {
			return FontPack{}, e
		}
		tmp, e := os.CreateTemp(dir, ".write-")
		if e != nil {
			return FontPack{}, e
		}
		_, werr := tmp.Write(body)
		cerr := tmp.Close()
		if werr == nil {
			werr = cerr
		}
		if werr == nil {
			werr = os.Chmod(tmp.Name(), 0o644)
		}
		if werr == nil {
			werr = os.Rename(tmp.Name(), filepath.Join(dir, name))
		}
		if werr != nil {
			_ = os.Remove(tmp.Name())
			return FontPack{}, werr
		}
	}
	return pack, nil
}

func fontXMLPath(path string) string {
	var out bytes.Buffer
	_ = xml.EscapeText(&out, []byte(filepath.ToSlash(path)))
	return out.String()
}

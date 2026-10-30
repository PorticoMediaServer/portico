package reading

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha1" // Required by IDPF font obfuscation; not an authority/security hash.
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

func fixtureObfuscatedEPUB(t *testing.T, identifier, font, encryption string, rights bool) []byte {
	t.Helper()
	raw := fixtureEPUB(t, `<html><body><p>Original text.</p></body></html>`)
	z, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	var entries [][2]string
	for _, f := range z.File {
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(r)
		r.Close()
		if err != nil {
			t.Fatal(err)
		}
		content := string(b)
		if f.Name == "OEBPS/content.opf" {
			content = strings.Replace(content, "urn:uuid:test", identifier, 1)
			content = strings.Replace(content, "</manifest>", `<item id="publisher-font" href="fonts/publisher.ttf" media-type="font/ttf"/></manifest>`, 1)
		}
		entries = append(entries, [2]string{f.Name, content})
	}
	entries = append(entries, [2]string{"OEBPS/fonts/publisher.ttf", font}, [2]string{"META-INF/encryption.xml", encryption})
	if rights {
		entries = append(entries, [2]string{"META-INF/rights.xml", `<rights/>`})
	}
	return fixtureZIP(t, entries)
}

func fixtureEncryption(algorithm, reference string) string {
	return fmt.Sprintf(`<encryption xmlns="%s" xmlns:enc="%s"><enc:EncryptedData><enc:EncryptionMethod Algorithm="%s"/><enc:CipherData><enc:CipherReference URI="%s"/></enc:CipherData></enc:EncryptedData></encryption>`, ocfNamespace, xmlencNamespace, algorithm, reference)
}

func TestEPUBOriginalObfuscatedFontsAndEncryptionAreEnumeratedUnchanged(t *testing.T) {
	font := fixturePublisherFont(t)
	identifier := "urn:uuid:4875335a-10c2-4850-aa65-000000009058"
	for _, algorithm := range []string{idpfObfuscation, adobeObfuscation} {
		t.Run(algorithm, func(t *testing.T) {
			var key []byte
			length := 1040
			if algorithm == idpfObfuscation {
				hash := sha1.Sum([]byte(identifier))
				key = hash[:]
			} else {
				var err error
				key, err = hex.DecodeString(strings.ReplaceAll(strings.TrimPrefix(identifier, "urn:uuid:"), "-", ""))
				if err != nil {
					t.Fatal(err)
				}
				length = 1024
			}
			obfuscated := bytes.Clone(font)
			for i := 0; i < min(length, len(obfuscated)); i++ {
				obfuscated[i] ^= key[i%len(key)]
			}
			if algorithm == idpfObfuscation && !bytes.HasPrefix(obfuscated, []byte{0xff, 0xd8}) {
				t.Fatal("fixture must exercise a genuine opaque font with a JPEG-like prefix")
			}
			encryption := fixtureEncryption(algorithm, "OEBPS/fonts/publisher.ttf")
			raw := fixtureObfuscatedEPUB(t, identifier, string(obfuscated), encryption, false)
			for _, sequential := range []bool{false, true} {
				var e *EPUB
				var err error
				if sequential {
					opens := 0
					e, err = OpenSequentialEPUB(context.Background(), sequentialFixture(raw, &opens))
				} else {
					e, err = OpenEPUB(context.Background(), bytes.NewReader(raw), int64(len(raw)))
				}
				if err != nil {
					t.Fatal(err)
				}
				for _, expected := range []struct{ path, mime, bytes string }{{"META-INF/encryption.xml", "application/xml", encryption}, {"OEBPS/fonts/publisher.ttf", "font/ttf", string(obfuscated)}} {
					got, mime, err := e.Asset(context.Background(), expected.path)
					if err != nil || mime != expected.mime || string(got) != expected.bytes {
						t.Fatalf("opaque original asset changed/failed: %s %s %v", expected.path, mime, err)
					}
					found := false
					for _, resource := range e.Resources {
						if resource.Path == expected.path {
							found = resource.MIME == expected.mime && resource.Size == int64(len(got))
						}
					}
					if !found {
						t.Fatal("original size/MIME missing from enumeration", expected.path)
					}
				}
			}
		})
	}
}

func TestEPUBObfuscationRejectsProtectedUnsafeAndAmbiguousDeclarations(t *testing.T) {
	valid := fixtureEncryption(idpfObfuscation, "OEBPS/fonts/publisher.ttf")
	data := valid[strings.Index(valid, "<enc:EncryptedData>"):strings.Index(valid, "</encryption>")]
	assets := map[string]string{"OEBPS/fonts/publisher.ttf": "font/ttf", "OEBPS/chapter.xhtml": "application/xhtml+xml"}
	for _, declaration := range []string{
		fixtureEncryption("http://www.w3.org/2001/04/xmlenc#aes256-cbc", "OEBPS/fonts/publisher.ttf"),
		fixtureEncryption(idpfObfuscation, "https://origin.invalid/private.ttf"),
		fixtureEncryption(idpfObfuscation, "//origin.invalid/private.ttf"),
		fixtureEncryption(idpfObfuscation, "../secret.ttf"),
		fixtureEncryption(idpfObfuscation, "OEBPS/chapter.xhtml"),
		fixtureEncryption(idpfObfuscation, "OEBPS/fonts/missing.ttf"),
		fixtureEncryption(idpfObfuscation, "OEBPS/fonts/publisher.ttf#alias"),
		strings.Replace(valid, "</encryption>", data+"</encryption>", 1),
		strings.Replace(valid, "<enc:CipherData>", `<enc:KeyInfo/><enc:CipherData>`, 1),
		strings.Replace(valid, "/><enc:CipherData>", `/><enc:EncryptionMethod Algorithm="`+idpfObfuscation+`"/><enc:CipherData>`, 1),
		strings.Replace(valid, "<enc:CipherReference", `<enc:CipherReference xml:base="https://origin.invalid/"`, 1),
		strings.Replace(valid, xmlencNamespace, "urn:spoofed-encryption", 1),
		strings.Replace(valid, "<enc:CipherData>", "<enc:CipherData><enc:Transforms/>", 1),
		"<!DOCTYPE encryption>" + valid,
		`<encryption xmlns="` + ocfNamespace + `"/>`,
		valid + valid,
		valid[:len(valid)-1],
	} {
		if err := validateFontObfuscation([]byte(declaration), assets); err == nil {
			t.Fatal("unsafe obfuscation accepted", declaration)
		}
	}
	// The allowlist is enforced at publication open, not just in a standalone parser.
	for _, rights := range []bool{false, true} {
		declaration := valid
		if !rights {
			declaration = fixtureEncryption("urn:protected", "OEBPS/fonts/publisher.ttf")
		}
		raw := fixtureObfuscatedEPUB(t, "urn:uuid:test", "opaque", declaration, rights)
		if _, err := OpenEPUB(context.Background(), bytes.NewReader(raw), int64(len(raw))); !errors.Is(err, ErrInvalid) {
			t.Fatal("protected publication admitted", err)
		}
	}
	large := valid + strings.Repeat(" ", 256<<10)
	raw := fixtureObfuscatedEPUB(t, "urn:uuid:test", "opaque", large, false)
	if _, err := OpenEPUB(context.Background(), bytes.NewReader(raw), int64(len(raw))); !errors.Is(err, ErrLimit) {
		t.Fatal("unbounded encryption XML admitted", err)
	}
}

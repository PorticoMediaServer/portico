package httpapi

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha1" // IDPF font obfuscation, not an authority/security hash.
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/readingtest"
)

func TestReadingPrivatePublisherAssetsRemainOriginalAndAuthorityBound(t *testing.T) {
	font, err := os.ReadFile("../../../packages/reader-core/assets/Literata[opsz,wght].ttf")
	if err != nil || fmt.Sprintf("%x", sha256.Sum256(font)) != "b41138c9373112f32abb589cc22e8674b06ed4048b0c513be922bdd26f274440" {
		t.Fatal("checked lawful publisher font unavailable", err)
	}
	identifier := "urn:uuid:4875335a-10c2-4850-aa65-000000009058"
	key := sha1.Sum([]byte(identifier))
	for i := 0; i < 1040; i++ {
		font[i] ^= key[i%len(key)]
	}
	if !bytes.HasPrefix(font, []byte{0xff, 0xd8}) {
		t.Fatal("actual obfuscated font must have the coincidental raster prefix")
	}
	css := `@import "./palette.css"; @font-face { font-family: OriginalPublisher; src: url('../fonts/publisher.ttf') format('truetype'); } @media screen { p { font-family: OriginalPublisher; } }`
	encryption := `<encryption xmlns="urn:oasis:names:tc:opendocument:xmlns:container" xmlns:enc="http://www.w3.org/2001/04/xmlenc#"><enc:EncryptedData><enc:EncryptionMethod Algorithm="http://www.idpf.org/2008/embedding"/><enc:CipherData><enc:CipherReference URI="EPUB/fonts/publisher.ttf"/></enc:CipherData></enc:EncryptedData></encryption>`
	original := readingtest.EPUB()
	z, err := zip.NewReader(bytes.NewReader(original), int64(len(original)))
	if err != nil {
		t.Fatal(err)
	}
	var raw bytes.Buffer
	out := zip.NewWriter(&raw)
	write := func(name string, b []byte, method uint16) {
		t.Helper()
		w, err := out.CreateHeader(&zip.FileHeader{Name: name, Method: method})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(b); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range z.File {
		input, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(input)
		input.Close()
		if err != nil {
			t.Fatal(err)
		}
		if f.Name == "EPUB/package.opf" {
			text := strings.Replace(string(b), "urn:uuid:382df65f-89d0-496e-ab18-6670e4d27ad4", identifier, 1)
			b = []byte(strings.Replace(text, "</manifest>", `<item id="font" href="fonts/publisher.ttf" media-type="font/ttf"/><item id="css" href="styles/main.css" media-type="text/css"/><item id="palette" href="styles/palette.css" media-type="text/css"/></manifest>`, 1))
		}
		if f.Name == "EPUB/chapter.xhtml" {
			b = []byte(strings.Replace(string(b), "</head>", `<link rel="stylesheet" href="styles/main.css"/></head>`, 1))
		}
		if f.Name == "mimetype" {
			// Preserve the actual EPUB-required first, stored member header.
			w, err := out.CreateRaw(&f.FileHeader)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = w.Write(b); err != nil {
				t.Fatal(err)
			}
		} else {
			write(f.Name, b, zip.Deflate)
		}
	}
	want := map[string][]byte{"EPUB/fonts/publisher.ttf": font, "EPUB/styles/main.css": []byte(css), "EPUB/styles/palette.css": []byte(`p { color: #231f20; }`), "META-INF/encryption.xml": []byte(encryption)}
	for name, b := range want {
		write(name, b, zip.Deflate)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	r := newReadingFixture(t, "book", "epub", raw.Bytes())
	session, publication, err := r.s.Prepare(context.Background(), r.p, r.item, r.source)
	if err != nil {
		t.Fatal(err)
	}
	for name, b := range want {
		w := r.bytesRequest(t, "GET", "/assets/"+name, session, publication.Selection.Revision, r.f.owner.AccessToken, nil)
		if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), b) || w.Header().Get("Cache-Control") != "private, no-store, no-transform" {
			t.Fatalf("asset changed or rejected %s: status=%d size=%d/%d", name, w.Code, w.Body.Len(), len(b))
		}
		found := false
		for _, resource := range publication.Resources {
			if resource.Path == name {
				found = resource.Size == int64(w.Body.Len()) && strings.Split(w.Header().Get("Content-Type"), ";")[0] == resource.MIME
			}
		}
		if !found {
			t.Fatal("enumerated original size/MIME mismatch", name)
		}
	}
	if w := r.bytesRequest(t, "GET", "/assets/EPUB/fonts/publisher.ttf", session, publication.Selection.Revision, "", nil); w.Code != 401 {
		t.Fatal("opaque font crossed identity boundary", w.Code)
	}
	if err := r.f.id.LogoutToken(context.Background(), r.f.owner.AccessToken); err != nil {
		t.Fatal(err)
	}
	if w := r.bytesRequest(t, "GET", "/assets/META-INF/encryption.xml", session, publication.Selection.Revision, r.f.owner.AccessToken, nil); w.Code == 200 || w.Body.String() == encryption {
		t.Fatal("revoked family received cached publication assets", w.Code)
	}
}

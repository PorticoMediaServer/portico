package reading

import (
	"bytes"
	"encoding/xml"
	"io"
	"strings"
)

const (
	ocfNamespace     = "urn:oasis:names:tc:opendocument:xmlns:container"
	xmlencNamespace  = "http://www.w3.org/2001/04/xmlenc#"
	idpfObfuscation  = "http://www.idpf.org/2008/embedding"
	adobeObfuscation = "http://ns.adobe.com/pdf/enc#RC"
)

// Only standard font obfuscation is admitted. This is not a DRM/decryption
// implementation: the original bounded XML and opaque font bytes are returned
// unchanged, for the reader engine to deobfuscate before decoding the font.
// See https://www.w3.org/TR/epub-33/#sec-font-obfuscation-specifying .
func validateFontObfuscation(b []byte, assets map[string]string) error {
	d := xml.NewDecoder(bytes.NewReader(b))
	var stack []xml.Name
	root, done := false, false
	seen := map[string]bool{}
	algorithm, reference := "", ""
	methods, ciphers, references := 0, 0, 0
	for {
		token, err := d.Token()
		if err == io.EOF {
			if !done || len(stack) != 0 || len(seen) == 0 {
				return ErrInvalid
			}
			return nil
		}
		if err != nil {
			return ErrInvalid
		}
		switch t := token.(type) {
		case xml.StartElement:
			if done {
				return ErrInvalid
			}
			var attribute string
			switch len(stack) {
			case 0:
				if root || t.Name != (xml.Name{Space: ocfNamespace, Local: "encryption"}) {
					return ErrInvalid
				}
				root = true
			case 1:
				if t.Name != (xml.Name{Space: xmlencNamespace, Local: "EncryptedData"}) {
					return ErrInvalid
				}
				algorithm, reference = "", ""
				methods, ciphers, references = 0, 0, 0
				attribute = "Id"
			case 2:
				if t.Name.Space != xmlencNamespace {
					return ErrInvalid
				}
				switch t.Name.Local {
				case "EncryptionMethod":
					methods++
					attribute = "Algorithm"
				case "CipherData":
					ciphers++
				default:
					return ErrInvalid
				}
			case 3:
				if stack[2].Local != "CipherData" || t.Name != (xml.Name{Space: xmlencNamespace, Local: "CipherReference"}) {
					return ErrInvalid
				}
				references++
				attribute = "URI"
			default:
				return ErrInvalid
			}
			value, err := obfuscationAttribute(t.Attr, attribute)
			if err != nil {
				return err
			}
			switch attribute {
			case "Algorithm":
				algorithm = value
			case "URI":
				reference = value
			}
			if methods > 1 || ciphers > 1 || references > 1 {
				return ErrInvalid
			}
			stack = append(stack, t.Name)
		case xml.EndElement:
			if len(stack) == 0 {
				return ErrInvalid
			}
			if len(stack) == 2 {
				if methods != 1 || ciphers != 1 || references != 1 || algorithm != idpfObfuscation && algorithm != adobeObfuscation || strings.ContainsAny(reference, "?#") {
					return ErrInvalid
				}
				// META-INF references are rooted at the publication container,
				// not relative to META-INF or the package document directory.
				name, err := resolveReference("_", reference)
				if err != nil || seen[name] || !fontMIME(assets[name]) {
					return ErrInvalid
				}
				seen[name] = true
				if len(seen) > MaxEntries {
					return ErrLimit
				}
			}
			stack = stack[:len(stack)-1]
			if len(stack) == 0 {
				done = true
			}
		case xml.CharData:
			if len(bytes.TrimSpace(t)) != 0 {
				return ErrInvalid
			}
		case xml.ProcInst:
			if root || t.Target != "xml" {
				return ErrInvalid
			}
		case xml.Directive:
			return ErrInvalid
		}
	}
}

func obfuscationAttribute(attrs []xml.Attr, allowed string) (string, error) {
	value, found := "", false
	for _, a := range attrs {
		if a.Name.Space == "xmlns" || a.Name.Space == "" && a.Name.Local == "xmlns" {
			continue
		}
		if allowed == "" || a.Name.Space != "" || a.Name.Local != allowed || found {
			return "", ErrInvalid
		}
		value, found = a.Value, true
	}
	if (allowed == "Algorithm" || allowed == "URI") && (!found || value == "") {
		return "", ErrInvalid
	}
	return value, nil
}

func fontMIME(mime string) bool {
	switch mime {
	case "font/otf", "font/ttf", "font/woff", "font/woff2", "application/font-sfnt", "application/vnd.ms-opentype":
		return true
	}
	return false
}

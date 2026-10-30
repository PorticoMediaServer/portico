package reading

import (
	"bytes"
	"strings"
	"unicode/utf8"
)

// validateCSS checks references without changing publisher bytes or enumerated
// sizes. The renderer owns sanitization and Blob URL rewriting. Escaped tokens
// and indirect image functions are deliberately outside this bounded subset.
func validateCSS(base string, b []byte, assets map[string]string) error {
	for _, c := range b {
		if c == '\\' || c == 127 || c < 32 && !cssSpace(c) {
			return ErrInvalid
		}
	}
	p := cssValidator{base: base, b: b, assets: assets}
	var stack []byte
	namespaceAllowed, namespaceSeen, importPrelude := true, false, false
	for p.i < len(b) {
		if err := p.space(); err != nil {
			return err
		}
		if p.i == len(b) {
			break
		}
		c := b[p.i]
		// Namespace rules precede qualified/block rules. Keep an import's
		// optional media prelude separate until its semicolon, without altering
		// any bytes or joining tokens across comments.
		if len(stack) == 0 && !importPrelude && c != '@' {
			namespaceAllowed = false
		}
		switch {
		case c == '\'' || c == '"':
			if _, err := p.quoted(); err != nil {
				return err
			}
		case c == '@':
			p.i++
			name := p.ident()
			switch name {
			case "namespace":
				if len(stack) != 0 || !namespaceAllowed || importPrelude {
					return ErrInvalid
				}
				if err := p.namespace(); err != nil {
					return err
				}
				namespaceSeen = true
			case "import":
				if len(stack) == 0 {
					if namespaceSeen || importPrelude {
						return ErrInvalid
					}
					importPrelude = true
				}
				if err := p.space(); err != nil || p.i == len(b) {
					return ErrInvalid
				}
				var reference string
				var err error
				if b[p.i] == '\'' || b[p.i] == '"' {
					reference, err = p.quoted()
				} else if p.ident() == "url" {
					reference, err = p.url()
				} else {
					return ErrInvalid
				}
				if err != nil || !p.reference(reference, true) {
					return ErrInvalid
				}
			case "charset":
				if len(stack) == 0 {
					if namespaceSeen || importPrelude {
						return ErrInvalid
					}
					importPrelude = true
				}
			case "font-face", "media", "supports", "page", "layer", "container", "keyframes", "-webkit-keyframes", "counter-style":
				if len(stack) == 0 {
					namespaceAllowed = false
				}
			default:
				return ErrInvalid
			}
		case cssIdent(c):
			name := p.ident()
			switch name {
			case "expression", "behavior", "-ms-behavior", "-moz-binding", "progid", "alphaimageloader":
				return ErrInvalid
			}
			if err := p.space(); err != nil {
				return err
			}
			if p.i < len(b) && b[p.i] == '(' {
				switch name {
				case "url":
					reference, err := p.url()
					if err != nil || !p.reference(reference, false) {
						return ErrInvalid
					}
				case "image-set", "-webkit-image-set", "image", "src", "paint", "attr":
					// These can interpret strings or element values as URLs.
					return ErrInvalid
				}
			}
		default:
			if c == '<' {
				return ErrInvalid
			}
			p.i++
			switch c {
			case ';':
				if len(stack) == 0 {
					importPrelude = false
				}
			case '{', '(', '[':
				if c == '{' && len(stack) == 0 && importPrelude {
					return ErrInvalid
				}
				stack = append(stack, c)
				if len(stack) > 128 {
					return ErrLimit
				}
			case '}', ')', ']':
				if len(stack) == 0 || !cssPair(stack[len(stack)-1], c) {
					return ErrInvalid
				}
				stack = stack[:len(stack)-1]
			}
		}
	}
	if len(stack) != 0 || importPrelude {
		return ErrInvalid
	}
	return nil
}

type cssValidator struct {
	base   string
	b      []byte
	i      int
	assets map[string]string
}

func cssSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '\f' }
func cssIdent(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c >= 128
}
func cssPair(a, b byte) bool {
	return a == '{' && b == '}' || a == '(' && b == ')' || a == '[' && b == ']'
}

func (p *cssValidator) space() error {
	for p.i < len(p.b) {
		if cssSpace(p.b[p.i]) {
			p.i++
			continue
		}
		if p.i+1 < len(p.b) && p.b[p.i] == '/' && p.b[p.i+1] == '*' {
			start := p.i
			end := bytes.Index(p.b[start+2:], []byte("*/"))
			if end < 0 {
				return ErrInvalid
			}
			p.i += end + 4
			// No comment may disguise a token by joining two identifier parts.
			if start > 0 && p.i < len(p.b) && cssIdent(p.b[start-1]) && cssIdent(p.b[p.i]) {
				return ErrInvalid
			}
			continue
		}
		break
	}
	return nil
}

func (p *cssValidator) ident() string {
	start := p.i
	for p.i < len(p.b) && cssIdent(p.b[p.i]) {
		p.i++
	}
	return strings.ToLower(string(p.b[start:p.i]))
}

func (p *cssValidator) quoted() (string, error) {
	quote := p.b[p.i]
	p.i++
	start := p.i
	for p.i < len(p.b) {
		c := p.b[p.i]
		if c == quote {
			value := string(p.b[start:p.i])
			p.i++
			return value, nil
		}
		if c == '\n' || c == '\r' || c == '\f' || c == '<' {
			return "", ErrInvalid
		}
		p.i++
	}
	return "", ErrInvalid
}

func (p *cssValidator) url() (string, error) {
	if err := p.space(); err != nil || p.i == len(p.b) || p.b[p.i] != '(' {
		return "", ErrInvalid
	}
	p.i++
	if err := p.space(); err != nil || p.i == len(p.b) {
		return "", ErrInvalid
	}
	var reference string
	if p.b[p.i] == '\'' || p.b[p.i] == '"' {
		var err error
		reference, err = p.quoted()
		if err != nil {
			return "", err
		}
	} else {
		start := p.i
		for p.i < len(p.b) && p.b[p.i] != ')' && !cssSpace(p.b[p.i]) {
			c := p.b[p.i]
			if c == '<' || c == '(' || c == '\'' || c == '"' || c == '/' && p.i+1 < len(p.b) && p.b[p.i+1] == '*' {
				return "", ErrInvalid
			}
			p.i++
		}
		reference = string(p.b[start:p.i])
	}
	if err := p.space(); err != nil || p.i == len(p.b) || p.b[p.i] != ')' {
		return "", ErrInvalid
	}
	p.i++
	return reference, nil
}

// namespace accepts the quoted identifier form only. Its string is not a
// resource: no URL parser, archive lookup, approval, or network request runs.
func (p *cssValidator) namespace() error {
	if err := p.space(); err != nil || p.i == len(p.b) {
		return ErrInvalid
	}
	if p.b[p.i] != '\'' && p.b[p.i] != '"' {
		start := p.i
		p.ident()
		prefix := p.b[start:p.i]
		if len(prefix) == 0 || len(prefix) > 64 || !utf8.Valid(prefix) {
			return ErrInvalid
		}
		for i, c := range prefix {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= 128 || c == '_' || c == '-' || i > 0 && c >= '0' && c <= '9') {
				return ErrInvalid
			}
		}
		if prefix[0] == '-' && (len(prefix) == 1 || prefix[1] >= '0' && prefix[1] <= '9') {
			return ErrInvalid
		}
		if err := p.space(); err != nil || p.i == len(p.b) {
			return ErrInvalid
		}
	}
	if p.b[p.i] != '\'' && p.b[p.i] != '"' {
		return ErrInvalid
	}
	identifier, err := p.quoted()
	if err != nil || len(identifier) > 2048 || !utf8.ValidString(identifier) {
		return ErrInvalid
	}
	if err = p.space(); err != nil || p.i == len(p.b) || p.b[p.i] != ';' {
		return ErrInvalid
	}
	p.i++
	return nil
}

func (p *cssValidator) reference(reference string, imported bool) bool {
	if reference == "" || strings.TrimSpace(reference) != reference {
		return false
	}
	name, err := resolveReference(p.base, reference)
	if err != nil {
		return false
	}
	mime := p.assets[name]
	if imported {
		return mime == "text/css"
	}
	switch mime {
	case "image/jpeg", "image/png", "image/webp", "font/otf", "font/ttf", "font/woff", "font/woff2", "application/font-sfnt", "application/vnd.ms-opentype":
		return true
	}
	return false
}

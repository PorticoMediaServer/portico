package remotemedia

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var ErrDAVToken = errors.New("WebDAV sync token is no longer valid")
var ErrDAVUnsupported = errors.New("WebDAV synchronization is not supported")
var ErrDAVResponse = errors.New("WebDAV response is invalid or exceeds its bounds")

// DAVConfig is PRIVATE persistence. It must never be serialized into an API or
// catalog projection. Approval records are captured when an owner validates it.
type DAVConfig struct {
	Root, Username, Password string
	InsecureLocal            bool
	Approval                 Approval
}
type DAV struct {
	config DAVConfig
	policy Policy
}
type DAVEntry struct {
	Relative, ETag, ContentType, Modified string
	Size                                  int64
	Directory, Deleted                    bool
}
type DAVPage struct {
	Entries []DAVEntry
	Token   string
	More    bool
}

const davProps = `<d:prop><d:resourcetype/><d:getcontentlength/><d:getetag/><d:getlastmodified/><d:getcontenttype/><d:sync-token/></d:prop>`

func ValidateDAVConfig(ctx context.Context, c DAVConfig, resolver Resolver) (DAVConfig, error) {
	u, err := parseURL(c.Root)
	if err != nil || u.RawQuery != "" || strings.Contains(c.Username, ":") || strings.ContainsAny(c.Username+c.Password, "\r\n\x00") {
		return c, ErrPolicy
	}
	policy := Policy{Resolver: resolver}
	a, err := policy.Approve(ctx, c.Root)
	if err != nil {
		return c, err
	}
	if u.Scheme != "https" {
		if !c.InsecureLocal {
			return c, ErrPolicy
		}
		for _, raw := range a.Addresses {
			ip, e := netip.ParseAddr(raw)
			if e != nil || public(ip) || hardBlocked(ip) {
				return c, ErrPolicy
			}
		}
	}
	c.Root = strings.TrimSuffix(u.String(), "/") + "/"
	c.Approval = a
	return c, nil
}
func NewDAV(c DAVConfig, resolver Resolver) (*DAV, error) {
	u, err := parseURL(c.Root)
	if err != nil || u.RawQuery != "" || c.Approval.Root == "" || len(c.Approval.Addresses) == 0 {
		return nil, ErrPolicy
	}
	if u.Scheme != "https" && !c.InsecureLocal {
		return nil, ErrPolicy
	}
	return &DAV{c, Policy{Approvals: []Approval{c.Approval}, Resolver: resolver}}, nil
}
func cleanDAVRelative(v string) bool {
	return len(v) <= 8192 && utf8.ValidString(v) && !strings.ContainsAny(v, "\\\x00%") && strings.IndexFunc(v, unicode.IsControl) < 0 && (v == "" || !strings.HasPrefix(v, "/") && path.Clean(v) == v && v != "." && v != ".." && !strings.HasPrefix(v, "../"))
}
func (d *DAV) locator(relative string) (string, error) {
	if !cleanDAVRelative(relative) {
		return "", ErrPolicy
	}
	root, err := url.Parse(d.config.Root)
	if err != nil {
		return "", ErrPolicy
	}
	root.Path = strings.TrimSuffix(root.Path, "/") + "/" + relative
	root.RawPath = ""
	return root.String(), nil
}
func (d *DAV) client(ctx context.Context, relative string) (*Client, error) {
	locator, err := d.locator(relative)
	if err != nil {
		return nil, err
	}
	return NewAuthenticated(ctx, locator, d.policy, d.config.Username, d.config.Password)
}
func (d *DAV) Href(href, directory string) (string, error) {
	return d.href(href, directory, false)
}
func (d *DAV) href(href, directory string, stat bool) (string, error) {
	if href == "" || len(href) > 8192 || !utf8.ValidString(href) || strings.IndexFunc(href, unicode.IsControl) >= 0 {
		return "", ErrDAVResponse
	}
	lower := strings.ToLower(href)
	for _, escape := range []string{"%2f", "%5c", "%00"} {
		if strings.Contains(lower, escape) {
			return "", ErrDAVResponse
		}
	}
	base, _ := url.Parse(d.config.Root)
	target, err := url.Parse(href)
	if err != nil || target.User != nil || target.RawQuery != "" || target.Fragment != "" {
		return "", ErrDAVResponse
	}
	// Check before ResolveReference: URL resolution normalizes traversal away.
	if strings.ContainsAny(target.Path, "\\%") || strings.IndexFunc(target.Path, unicode.IsControl) >= 0 {
		return "", ErrDAVResponse
	}
	for _, segment := range strings.Split(target.Path, "/") {
		if segment == "." || segment == ".." {
			return "", ErrDAVResponse
		}
	}
	requested, _ := d.locator(directory)
	requestURL, _ := url.Parse(requested)
	if !stat || directory == "" {
		requestURL.Path = strings.TrimSuffix(requestURL.Path, "/") + "/"
	}
	target = requestURL.ResolveReference(target)
	if origin(target) != origin(base) {
		return "", ErrDAVResponse
	}
	raw := strings.TrimSuffix(target.Path, "/")
	root := strings.TrimSuffix(base.Path, "/")
	if raw != root && !strings.HasPrefix(raw, root+"/") {
		return "", ErrDAVResponse
	}
	relative := strings.TrimPrefix(strings.TrimPrefix(raw, root), "/")
	if !cleanDAVRelative(relative) {
		return "", ErrDAVResponse
	}
	if stat && relative != directory || relative != directory && path.Dir(relative) != emptyDir(directory) {
		return "", ErrDAVResponse
	}
	return relative, nil
}
func emptyDir(s string) string {
	if s == "" {
		return "."
	}
	return s
}

type davProp struct {
	Type struct {
		Collection *struct{} `xml:"DAV: collection"`
	} `xml:"DAV: resourcetype"`
	Size        string `xml:"DAV: getcontentlength"`
	ETag        string `xml:"DAV: getetag"`
	Modified    string `xml:"DAV: getlastmodified"`
	ContentType string `xml:"DAV: getcontenttype"`
	Token       string `xml:"DAV: sync-token"`
}
type davResponse struct {
	Href      string `xml:"DAV: href"`
	Status    string `xml:"DAV: status"`
	Propstats []struct {
		Prop   davProp `xml:"DAV: prop"`
		Status string  `xml:"DAV: status"`
	} `xml:"DAV: propstat"`
	Error struct {
		Limit *struct{} `xml:"DAV: number-of-matches-within-limits"`
	} `xml:"DAV: error"`
}

func davStatus(v string) int {
	fields := strings.Fields(v)
	if len(fields) < 2 || !strings.HasPrefix(fields[0], "HTTP/") {
		return 0
	}
	n, _ := strconv.Atoi(fields[1])
	return n
}

type boundedDAVTokens struct {
	decoder       *xml.Decoder
	depth, tokens int
}

func (b *boundedDAVTokens) Token() (xml.Token, error) {
	token, err := b.decoder.Token()
	if err != nil {
		return token, err
	}
	b.tokens++
	if b.tokens > 250000 {
		return nil, ErrDAVResponse
	}
	switch t := token.(type) {
	case xml.StartElement:
		b.depth++
		if b.depth > 24 || len(t.Attr) > 32 {
			return nil, ErrDAVResponse
		}
	case xml.EndElement:
		b.depth--
	case xml.Directive:
		return nil, ErrDAVResponse // no DTD/entity declarations
	case xml.ProcInst:
		if t.Target != "xml" {
			return nil, ErrDAVResponse
		}
	}
	return token, nil
}
func (d *DAV) parse(r io.Reader, directory string, sync bool) (DAVPage, error) {
	return d.parseResponse(r, directory, sync, false)
}
func (d *DAV) parseResponse(r io.Reader, directory string, sync, stat bool) (DAVPage, error) {
	limited := &io.LimitedReader{R: r, N: 4<<20 + 1}
	dec := xml.NewTokenDecoder(&boundedDAVTokens{decoder: xml.NewDecoder(limited)})
	out := DAVPage{Entries: []DAVEntry{}}
	seen := map[string]bool{}
	root := false
	closed := false
	for {
		token, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return DAVPage{}, ErrDAVResponse
		}
		if limited.N <= 0 {
			return DAVPage{}, ErrDAVResponse
		}
		switch t := token.(type) {
		case xml.StartElement:
			if !root {
				if t.Name.Space != "DAV:" || t.Name.Local != "multistatus" {
					return DAVPage{}, ErrDAVResponse
				}
				root = true
				continue
			}
			if closed {
				return DAVPage{}, ErrDAVResponse
			}
			if t.Name.Space != "DAV:" {
				if dec.Skip() != nil {
					return DAVPage{}, ErrDAVResponse
				}
				continue
			}
			switch t.Name.Local {
			case "response":
				if len(seen) >= 4096 {
					return DAVPage{}, ErrDAVResponse
				}
				var wire davResponse
				if dec.DecodeElement(&wire, &t) != nil {
					return DAVPage{}, ErrDAVResponse
				}
				rel, e := d.href(wire.Href, directory, stat)
				if e != nil || seen[rel] {
					return DAVPage{}, ErrDAVResponse
				}
				seen[rel] = true
				status := davStatus(wire.Status)
				if stat && rel == directory && status == 404 {
					return DAVPage{}, ErrObservationChanged
				}
				if rel == directory && status == 507 && sync && wire.Error.Limit != nil {
					out.More = true
					continue
				}
				entry := DAVEntry{Relative: rel}
				if status == 404 && sync && rel != directory {
					entry.Deleted = true
					out.Entries = append(out.Entries, entry)
					continue
				}
				if status != 0 && status != 200 {
					return DAVPage{}, ErrUnavailable
				}
				found := false
				sizeKnown := false
				for _, p := range wire.Propstats {
					code := davStatus(p.Status)
					if code == 404 {
						continue
					}
					if code != 200 {
						return DAVPage{}, ErrUnavailable
					}
					found = true
					if p.Prop.Type.Collection != nil {
						entry.Directory = true
					}
					if p.Prop.Size != "" {
						n, e := strconv.ParseInt(strings.TrimSpace(p.Prop.Size), 10, 64)
						if e != nil || n < 0 {
							return DAVPage{}, ErrDAVResponse
						}
						entry.Size = n
						sizeKnown = true
					}
					if p.Prop.ETag != "" {
						if len(p.Prop.ETag) > 1024 {
							return DAVPage{}, ErrDAVResponse
						}
						entry.ETag = p.Prop.ETag
					}
					entry.Modified = p.Prop.Modified
					entry.ContentType = p.Prop.ContentType
					if rel == directory && p.Prop.Token != "" {
						if len(p.Prop.Token) > 8192 {
							return DAVPage{}, ErrDAVResponse
						}
						out.Token = p.Prop.Token
					}
				}
				if !found || !entry.Directory && !sizeKnown {
					return DAVPage{}, ErrDAVResponse
				}
				if len(entry.Modified) > 128 || len(entry.ContentType) > 512 {
					return DAVPage{}, ErrDAVResponse
				}
				out.Entries = append(out.Entries, entry)
			case "sync-token":
				var token string
				if dec.DecodeElement(&token, &t) != nil || len(token) > 8192 {
					return DAVPage{}, ErrDAVResponse
				}
				out.Token = token
			default:
				if dec.Skip() != nil {
					return DAVPage{}, ErrDAVResponse
				}
			}
		case xml.CharData:
			if strings.TrimSpace(string(t)) != "" {
				return DAVPage{}, ErrDAVResponse
			}
		case xml.EndElement:
			if t.Name.Space == "DAV:" && t.Name.Local == "multistatus" {
				closed = true
			}
		}
	}
	if !root || !closed || limited.N <= 0 || out.More && out.Token == "" {
		return DAVPage{}, ErrDAVResponse
	}
	return out, nil
}
func (d *DAV) request(ctx context.Context, relative, method, depth string, body []byte, sync bool) (DAVPage, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	client, err := d.client(ctx, relative)
	if err != nil {
		return DAVPage{}, err
	}
	defer client.Close()
	response, err := client.Request(ctx, method, body, http.Header{"Depth": {depth}, "Content-Type": {"application/xml; charset=utf-8"}})
	if err != nil {
		return DAVPage{}, err
	}
	defer response.Body.Close()
	if sync && (response.StatusCode == 405 || response.StatusCode == 501) {
		return DAVPage{}, ErrDAVUnsupported
	}
	if sync && response.StatusCode == 403 {
		raw, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
		if bytes.Contains(raw, []byte("valid-sync-token")) {
			return DAVPage{}, ErrDAVToken
		}
	}
	if sync && response.StatusCode == 507 {
		return DAVPage{}, ErrDAVUnsupported
	}
	if response.StatusCode != 207 {
		return DAVPage{}, safeResponseError(response)
	}
	if response.Header.Get("Content-Encoding") != "" {
		return DAVPage{}, ErrDAVResponse
	}
	return d.parseResponse(response.Body, relative, sync, depth == "0" && !sync)
}
func (d *DAV) List(ctx context.Context, directory string) (DAVPage, error) {
	return d.request(ctx, directory, "PROPFIND", "1", []byte(`<d:propfind xmlns:d="DAV:">`+davProps+`</d:propfind>`), false)
}
func (d *DAV) Stat(ctx context.Context, relative string) (DAVEntry, error) {
	p, err := d.request(ctx, relative, "PROPFIND", "0", []byte(`<d:propfind xmlns:d="DAV:">`+davProps+`</d:propfind>`), false)
	if err != nil {
		return DAVEntry{}, err
	}
	if len(p.Entries) != 1 || p.Entries[0].Relative != relative {
		return DAVEntry{}, ErrDAVResponse
	}
	return p.Entries[0], nil
}
func (d *DAV) Sync(ctx context.Context, directory, token string) (DAVPage, error) {
	if len(token) > 8192 {
		return DAVPage{}, ErrDAVToken
	}
	var escaped bytes.Buffer
	_ = xml.EscapeText(&escaped, []byte(token))
	body := `<d:sync-collection xmlns:d="DAV:"><d:sync-token>` + escaped.String() + `</d:sync-token><d:sync-level>1</d:sync-level>` + davProps + `<d:limit><d:nresults>256</d:nresults></d:limit></d:sync-collection>`
	return d.request(ctx, directory, "REPORT", "0", []byte(body), true)
}
func (d *DAV) OpenVersion(ctx context.Context, relative, scope, object string) (*VersionedClient, error) {
	locator, err := d.locator(relative)
	if err != nil {
		return nil, err
	}
	binding, err := ExactLocatorBinding(locator, scope, object)
	if err != nil {
		return nil, err
	}
	return DiscoverAuthenticatedVersion(ctx, locator, d.policy, d.config.Username, d.config.Password, scope, object, binding)
}
func (d *DAV) CheckRange(ctx context.Context, relative string) error {
	c, err := d.OpenVersion(ctx, relative, "setup", "sample")
	if err != nil {
		return err
	}
	c.Close()
	return nil
}

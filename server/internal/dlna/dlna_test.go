package dlna

import (
	"context"
	"encoding/xml"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == "--portico-storage-helper" {
		if storage.Helper(os.Stdin, os.Stdout) != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

var home = []netip.Prefix{netip.MustParsePrefix("192.168.1.0/24")}

type fixture struct {
	t       *testing.T
	c       *catalogtest.Catalog
	server  *Server
	enabled atomic.Bool
	items   map[string]catalogtest.Item
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	c := catalogtest.Open(t)
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, c: c, items: map[string]catalogtest.Item{}}
	f.enabled.Store(true)
	f.server = &Server{DB: c.DB, Storage: storage.New(binary), Version: "1.2.3",
		ServerID: func() string { return "server-1" }, Name: func() string { return "Den & Co" },
		Settings: func(context.Context) (Settings, error) { return Settings{Enabled: f.enabled.Load(), Home: home}, nil }}
	films := c.Library("films", "Films & <Co>", "movie", "/films")
	f.items["heat"] = c.Movie(films, "/films/Heat.mkv", "Heat & Dust", 1995)
	f.items["alien"] = c.Movie(films, "/films/Alien.mp4", "Alien", 1979)
	f.items["remote"] = c.Movie(films, "/films/Remote.strm", "Remote", 2001)
	tv := c.Library("tv", "TV", "tv", "/tv")
	show := c.Show(tv, "The Wire", 2002)
	season := c.Season(show, 1)
	f.items["show"], f.items["season"] = show, season
	for i := 1; i <= 3; i++ {
		f.items["episode"+strconv.Itoa(i)] = c.Episode(show, season, i, "/tv/wire/s01e0"+strconv.Itoa(i)+".mkv")
	}
	music := c.Library("music", "Music", "music", "/music")
	artist := c.Artist(music, "Band")
	album := c.Album(artist, "Record", 2001)
	f.items["artist"], f.items["album"] = artist, album
	f.items["song2"] = c.Song(album, 2, "/music/two.flac", "Two")
	f.items["song1"] = c.Song(album, 1, "/music/one.flac", "One")
	books := c.Library("books", "Books", "audiobook", "/books")
	f.items["book"] = c.Book(books, "Novel", "Writer")
	f.items["part"] = c.BookFile(f.items["book"], 1, "/books/novel-1.m4b")
	f.items["anonymous"] = c.Book(books, "Anonymous Tales", "")
	c.BookFile(f.items["anonymous"], 1, "/books/anon-1.m4b")
	// The scanner's listening pass names author groups; the fixture does it here.
	c.Exec(`INSERT INTO listening_book_groups(id,library_id,kind,name,name_key) VALUES('0a1b2c','books','author','Writer','writer')`)
	c.Drain()
	return f
}

func (f *fixture) request(method, path, peer, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.RemoteAddr = peer + ":40000"
	local := &net.TCPAddr{IP: net.ParseIP("192.168.1.2"), Port: 8096}
	r = r.WithContext(context.WithValue(r.Context(), http.LocalAddrContextKey, local))
	w := httptest.NewRecorder()
	f.server.ServeHTTP(w, r)
	return w
}

func TestDisabledServesNothing(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := newFixture(t)
	f.enabled.Store(false)
	for _, path := range []string{"/dlna/device.xml", "/dlna/scpd/cds.xml", "/dlna/media/" + f.items["heat"].Token} {
		if w := f.request("GET", path, "192.168.1.20", ""); w.Code != http.StatusNotFound {
			t.Fatalf("%s while off: %d", path, w.Code)
		}
	}
	// Turning it on applies to the next request, with no restart.
	f.enabled.Store(true)
	if w := f.request("GET", "/dlna/device.xml", "192.168.1.20", ""); w.Code != http.StatusOK {
		t.Fatalf("device.xml once on: %d", w.Code)
	}
}

func TestOnlyTheHomeNetwork(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := newFixture(t)
	for _, peer := range []string{"203.0.113.9", "10.0.0.5", "127.0.0.1"} {
		if w := f.request("GET", "/dlna/device.xml", peer, ""); w.Code != http.StatusForbidden {
			t.Fatalf("peer %s: %d", peer, w.Code)
		}
		if w := f.request("GET", "/dlna/media/"+f.items["heat"].Token, peer, ""); w.Code != http.StatusForbidden {
			t.Fatalf("media to %s: %d", peer, w.Code)
		}
	}
	// A proxy on the home network can relay a remote client; DLNA never goes
	// through one.
	r := httptest.NewRequest("GET", "/dlna/device.xml", nil)
	r.RemoteAddr = "192.168.1.30:5000"
	r.Header.Set("X-Forwarded-For", "203.0.113.9")
	w := httptest.NewRecorder()
	f.server.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("forwarded request: %d", w.Code)
	}
}

func TestDeviceDescription(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := newFixture(t)
	w := f.request("GET", "/dlna/device.xml", "192.168.1.20", "")
	body := w.Body.String()
	var doc struct {
		Device struct {
			DeviceType   string `xml:"deviceType"`
			FriendlyName string `xml:"friendlyName"`
			UDN          string `xml:"UDN"`
			Services     []struct {
				ServiceType string `xml:"serviceType"`
				ControlURL  string `xml:"controlURL"`
			} `xml:"serviceList>service"`
		} `xml:"device"`
	}
	if err := xml.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatalf("%v\n%s", err, body)
	}
	if doc.Device.DeviceType != deviceType || doc.Device.FriendlyName != "Den & Co" || doc.Device.UDN != "uuid:"+DeviceUUID("server-1") || len(doc.Device.Services) != 3 {
		t.Fatalf("description: %+v", doc.Device)
	}
	if DeviceUUID("server-1") != DeviceUUID("server-1") || DeviceUUID("server-1") == DeviceUUID("server-2") || len(DeviceUUID("x")) != 36 {
		t.Fatal("device UUID is not a stable per-server id")
	}
	for name := range serviceDescriptions {
		if w := f.request("GET", "/dlna/scpd/"+name, "192.168.1.20", ""); w.Code != 200 || xml.Unmarshal(w.Body.Bytes(), new(struct{})) != nil {
			t.Fatalf("%s: %d", name, w.Code)
		}
	}
}

type didlObject struct {
	XMLName    xml.Name
	ID         string `xml:"id,attr"`
	Parent     string `xml:"parentID,attr"`
	ChildCount string `xml:"childCount,attr"`
	Title      string `xml:"title"`
	Class      string `xml:"class"`
	Album      string `xml:"album"`
	Artist     string `xml:"artist"`
	Art        string `xml:"albumArtURI"`
	Res        []struct {
		SampleRate string `xml:"sampleFrequency,attr"`
		BitDepth   string `xml:"bitsPerSample,attr"`
		Channels   string `xml:"nrAudioChannels,attr"`
		Protocol   string `xml:"protocolInfo,attr"`
		Duration   string `xml:"duration,attr"`
		Resolution string `xml:"resolution,attr"`
		URL        string `xml:",chardata"`
	} `xml:"res"`
}

type browseResult struct {
	objects         []didlObject
	returned, total int
}

func (f *fixture) soap(service, action, args string) *httptest.ResponseRecorder {
	name := map[string]string{contentDirectoryType: "cds", connectionManagerType: "cms", registrarType: "mrr"}[service]
	body := `<?xml version="1.0"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><u:` + action + ` xmlns:u="` + service + `">` + args + `</u:` + action + `></s:Body></s:Envelope>`
	r := httptest.NewRequest("POST", "/dlna/control/"+name, strings.NewReader(body))
	r.RemoteAddr = "192.168.1.20:40000"
	r.Header.Set("SOAPACTION", `"`+service+"#"+action+`"`)
	r = r.WithContext(context.WithValue(r.Context(), http.LocalAddrContextKey, &net.TCPAddr{IP: net.ParseIP("192.168.1.2"), Port: 8096}))
	w := httptest.NewRecorder()
	f.server.ServeHTTP(w, r)
	return w
}

func (f *fixture) browse(id, flag string, start, count int) browseResult {
	f.t.Helper()
	w := f.soap(contentDirectoryType, "Browse", "<ObjectID>"+xmlText(id)+"</ObjectID><BrowseFlag>"+flag+"</BrowseFlag><Filter>*</Filter><StartingIndex>"+strconv.Itoa(start)+"</StartingIndex><RequestedCount>"+strconv.Itoa(count)+"</RequestedCount><SortCriteria></SortCriteria>")
	if w.Code != 200 {
		f.t.Fatalf("Browse %s %s: %d %s", id, flag, w.Code, w.Body.String())
	}
	var envelope struct {
		Result         string `xml:"Body>BrowseResponse>Result"`
		NumberReturned int    `xml:"Body>BrowseResponse>NumberReturned"`
		TotalMatches   int    `xml:"Body>BrowseResponse>TotalMatches"`
	}
	if err := xml.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		f.t.Fatal(err)
	}
	var doc struct {
		Objects []didlObject `xml:",any"`
	}
	if err := xml.Unmarshal([]byte(envelope.Result), &doc); err != nil {
		f.t.Fatalf("DIDL: %v\n%s", err, envelope.Result)
	}
	if len(doc.Objects) != envelope.NumberReturned {
		f.t.Fatalf("NumberReturned %d for %d objects", envelope.NumberReturned, len(doc.Objects))
	}
	return browseResult{doc.Objects, envelope.NumberReturned, envelope.TotalMatches}
}

func titles(objects []didlObject) string {
	var out []string
	for _, o := range objects {
		out = append(out, o.Title)
	}
	return strings.Join(out, "|")
}

func TestBrowseEveryLibrary(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := newFixture(t)
	// The owner's library order, then by name.
	root := f.browse("0", "BrowseDirectChildren", 0, 0)
	if titles(root.objects) != "Books|Films & <Co>|Music|TV" || root.total != 4 {
		t.Fatalf("root: %s (%d)", titles(root.objects), root.total)
	}
	library := map[string]didlObject{}
	for _, o := range root.objects {
		library[o.Title] = o
	}
	films := f.browse(library["Films & <Co>"].ID, "BrowseDirectChildren", 0, 0)
	// A remote .strm title has no file this server can serve, so it is left out.
	if titles(films.objects) != "Alien|Heat & Dust" || films.total != 2 {
		t.Fatalf("films: %s (%d)", titles(films.objects), films.total)
	}
	heat := films.objects[1]
	if heat.XMLName.Local != "item" || heat.Class != "object.item.videoItem.movie" || heat.Parent != "lfilms" || len(heat.Res) != 1 {
		t.Fatalf("movie: %+v", heat)
	}
	res := heat.Res[0]
	if res.URL != "http://192.168.1.2:8096/dlna/media/"+f.items["heat"].Token+".mkv" || res.Protocol != "http-get:*:video/x-matroska:"+contentFeatures || res.Duration != "1:30:00.000" || res.Resolution != "1920x1080" {
		t.Fatalf("movie resource: %+v", res)
	}
	if heat.Art != "http://192.168.1.2:8096/dlna/art/"+f.items["heat"].Public {
		t.Fatalf("artwork: %q", heat.Art)
	}

	shows := f.browse(library["TV"].ID, "BrowseDirectChildren", 0, 0)
	if titles(shows.objects) != "The Wire" || shows.objects[0].XMLName.Local != "container" {
		t.Fatalf("shows: %+v", shows.objects)
	}
	seasons := f.browse(shows.objects[0].ID, "BrowseDirectChildren", 0, 0)
	if titles(seasons.objects) != "Season 1" || seasons.objects[0].ChildCount != "3" {
		t.Fatalf("seasons: %+v", seasons.objects)
	}
	episodes := f.browse(seasons.objects[0].ID, "BrowseDirectChildren", 0, 0)
	if titles(episodes.objects) != "Episode 1|Episode 2|Episode 3" {
		t.Fatalf("episodes: %s", titles(episodes.objects))
	}

	artists := f.browse(library["Music"].ID, "BrowseDirectChildren", 0, 0)
	albums := f.browse(artists.objects[0].ID, "BrowseDirectChildren", 0, 0)
	songs := f.browse(albums.objects[0].ID, "BrowseDirectChildren", 0, 0)
	if titles(artists.objects) != "Band" || titles(albums.objects) != "Record" || titles(songs.objects) != "One|Two" {
		t.Fatalf("music: %s / %s / %s", titles(artists.objects), titles(albums.objects), titles(songs.objects))
	}
	if s := songs.objects[0]; s.Class != "object.item.audioItem.musicTrack" || s.Album != "Record" || s.Artist != "Band" || !strings.HasPrefix(s.Res[0].Protocol, "http-get:*:audio/flac:") {
		t.Fatalf("song: %+v", s)
	}

	// Authors first, then books no author holds.
	books := f.browse(library["Books"].ID, "BrowseDirectChildren", 0, 0)
	if titles(books.objects) != "Writer|Anonymous Tales" || books.total != 2 {
		t.Fatalf("audiobooks: %s (%d)", titles(books.objects), books.total)
	}
	byAuthor := f.browse(books.objects[0].ID, "BrowseDirectChildren", 0, 0)
	parts := f.browse(byAuthor.objects[0].ID, "BrowseDirectChildren", 0, 0)
	if titles(byAuthor.objects) != "Novel" || titles(parts.objects) != "Part 1" || !strings.HasPrefix(parts.objects[0].Res[0].Protocol, "http-get:*:audio/mp4:") {
		t.Fatalf("books: %s / %+v", titles(byAuthor.objects), parts.objects)
	}
}

func TestBrowsePagingAndMetadata(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := newFixture(t)
	season := "e" + f.items["season"].Public
	page := f.browse(season, "BrowseDirectChildren", 1, 1)
	if titles(page.objects) != "Episode 2" || page.total != 3 || page.returned != 1 {
		t.Fatalf("page: %s %d/%d", titles(page.objects), page.returned, page.total)
	}
	if past := f.browse(season, "BrowseDirectChildren", 5, 10); past.returned != 0 || past.total != 3 {
		t.Fatalf("past the end: %d/%d", past.returned, past.total)
	}
	for id, parent := range map[string]string{
		"e" + f.items["episode2"].Public: season,
		season:                           "e" + f.items["show"].Public,
		"e" + f.items["show"].Public:     "ltv",
		"e" + f.items["book"].Public:     "", // its author group
		"ltv":                            "0",
		"0":                              "-1",
	} {
		got := f.browse(id, "BrowseMetadata", 0, 0)
		if got.returned != 1 || got.objects[0].ID != id || parent != "" && got.objects[0].Parent != parent || parent == "" && !strings.HasPrefix(got.objects[0].Parent, "a") {
			t.Fatalf("metadata %s: %+v", id, got.objects)
		}
	}
	if got := f.browse("e"+f.items["show"].Public, "BrowseMetadata", 0, 0); got.objects[0].ChildCount != "1" {
		t.Fatalf("show childCount: %+v", got.objects[0])
	}
	for _, id := range []string{"e" + f.items["remote"].Public, "eAAAAAAAAAAAAAAAAAAAAA", "lmissing", "x1", "e12"} {
		if w := f.soap(contentDirectoryType, "Browse", "<ObjectID>"+id+"</ObjectID><BrowseFlag>BrowseMetadata</BrowseFlag><StartingIndex>0</StartingIndex><RequestedCount>0</RequestedCount>"); w.Code != 500 || !strings.Contains(w.Body.String(), "<errorCode>701</errorCode>") {
			t.Fatalf("%s: %d %s", id, w.Code, w.Body.String())
		}
	}
}

func TestControlRequestsAreBounded(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := newFixture(t)
	for name, w := range map[string]*httptest.ResponseRecorder{
		"unknown action": f.soap(contentDirectoryType, "Search", ""),
		"wrong service":  f.soap(connectionManagerType, "Browse", ""),
		"bad index":      f.soap(contentDirectoryType, "Browse", "<ObjectID>0</ObjectID><BrowseFlag>BrowseDirectChildren</BrowseFlag><StartingIndex>-1</StartingIndex><RequestedCount>0</RequestedCount>"),
		"nested":         f.soap(contentDirectoryType, "Browse", "<ObjectID><x>0</x></ObjectID>"),
		"oversized":      f.soap(contentDirectoryType, "Browse", "<ObjectID>"+strings.Repeat("a", maxControlBytes)+"</ObjectID>"),
	} {
		if w.Code != 500 || !strings.Contains(w.Body.String(), "UPnPError") {
			t.Fatalf("%s: %d %s", name, w.Code, w.Body.String())
		}
	}
	w := f.soap(registrarType, "IsAuthorized", "<DeviceID></DeviceID>")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "<Result>1</Result>") {
		t.Fatalf("IsAuthorized: %s", w.Body.String())
	}
	if w := f.soap(connectionManagerType, "GetProtocolInfo", ""); !strings.Contains(w.Body.String(), "http-get:*:video/mp4:") {
		t.Fatalf("GetProtocolInfo: %s", w.Body.String())
	}
	if w := f.soap(contentDirectoryType, "GetSystemUpdateID", ""); w.Code != 200 || !strings.Contains(w.Body.String(), "<Id>") {
		t.Fatalf("GetSystemUpdateID: %s", w.Body.String())
	}
	// A subscription is acknowledged and never called back.
	r := httptest.NewRequest("SUBSCRIBE", "/dlna/event/cds", nil)
	r.RemoteAddr = "192.168.1.20:40000"
	r.Header.Set("NT", "upnp:event")
	r.Header.Set("CALLBACK", "<http://192.168.1.20:9/>")
	sub := httptest.NewRecorder()
	f.server.ServeHTTP(sub, r)
	if sub.Code != 200 || !strings.HasPrefix(sub.Header().Get("SID"), "uuid:") {
		t.Fatalf("SUBSCRIBE: %d %v", sub.Code, sub.Header())
	}
}

func TestMediaIsServedByTokenWithRanges(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := newFixture(t)
	path := filepath.Join(t.TempDir(), "Heat.mkv")
	if err := os.WriteFile(path, []byte("0123456789abcdef"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	f.c.Exec(`UPDATE catalog_assets SET path=?,size=?,modified_ns=? WHERE token=?`, path, info.Size(), info.ModTime().UnixNano(), f.items["heat"].Token)
	r := httptest.NewRequest("GET", "/dlna/media/"+f.items["heat"].Token+".mkv", nil)
	r.RemoteAddr = "192.168.1.20:40000"
	r.Header.Set("Range", "bytes=4-7")
	w := httptest.NewRecorder()
	f.server.ServeHTTP(w, r)
	if w.Code != http.StatusPartialContent || w.Body.String() != "4567" || w.Header().Get("Content-Type") != "video/x-matroska" ||
		w.Header()["contentFeatures.dlna.org"][0] != contentFeatures || w.Header()["transferMode.dlna.org"][0] != "Streaming" {
		t.Fatalf("range: %d %q %v", w.Code, w.Body.String(), w.Header())
	}
	head := f.request("HEAD", "/dlna/media/"+f.items["heat"].Token, "192.168.1.20", "")
	if head.Code != 200 || head.Header().Get("Content-Length") != "16" || head.Header().Get("Accept-Ranges") != "bytes" {
		t.Fatalf("HEAD: %d %v", head.Code, head.Header())
	}
	// Only catalogued files of live titles, by token: never a path or a
	// remote source.
	for _, name := range []string{"../../etc/passwd", f.items["remote"].Token, "unknown", "%2e%2e"} {
		if w := f.request("GET", "/dlna/media/"+name, "192.168.1.20", ""); w.Code != http.StatusNotFound {
			t.Fatalf("%s: %d", name, w.Code)
		}
	}
	f.c.Delete(f.items["heat"].ID)
	if w := f.request("GET", "/dlna/media/"+f.items["heat"].Token, "192.168.1.20", ""); w.Code != http.StatusNotFound {
		t.Fatalf("deleted title's file: %d", w.Code)
	}
}

func TestStreamEndsWhenSwitchedOff(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	f := newFixture(t)
	r := httptest.NewRequest("GET", "/dlna/media/x", nil)
	r.RemoteAddr = "192.168.1.20:40000"
	g := &guardedSource{ReadSeeker: strings.NewReader("abc"), server: f.server, request: r, checked: time.Now().Add(-recheckEvery)}
	f.enabled.Store(false)
	if _, err := g.Read(make([]byte, 3)); err != errWithdrawn {
		t.Fatalf("read after switch-off: %v", err)
	}
}

func TestSearchAnswers(t *testing.T) {
	uuid := DeviceUUID("server-1")
	search := "M-SEARCH * HTTP/1.1\r\nHOST: 239.255.255.250:1900\r\nMAN: \"ssdp:discover\"\r\nMX: 9\r\nST: %s\r\n\r\n"
	for st, want := range map[string]int{"ssdp:all": 6, "upnp:rootdevice": 1, "uuid:" + uuid: 1, deviceType: 1, contentDirectoryType: 1, "urn:schemas-upnp-org:device:MediaRenderer:1": 0} {
		got, mx, ok := parseSearch([]byte(strings.Replace(search, "%s", st, 1)))
		if !ok || got != st || mx != 5 || len(answers(got, uuid)) != want {
			t.Fatalf("%s: %q %d %v %d", st, got, mx, ok, len(answers(got, uuid)))
		}
	}
	for _, bad := range []string{"NOTIFY * HTTP/1.1\r\nNT: upnp:rootdevice\r\n\r\n", "M-SEARCH * HTTP/1.1\r\nST: ssdp:all\r\n\r\n", "garbage"} {
		if _, _, ok := parseSearch([]byte(bad)); ok {
			t.Fatalf("accepted %q", bad)
		}
	}
	a := Advertisement{UUID: uuid, Server: "linux/1.0 UPnP/1.0 Portico/1"}
	response := string(searchResponse(a, "http://192.168.1.2:8096/dlna/device.xml", deviceType, time.Unix(0, 0)))
	for _, line := range []string{"HTTP/1.1 200 OK\r\n", "CACHE-CONTROL: max-age=1800\r\n", "EXT:\r\n", "LOCATION: http://192.168.1.2:8096/dlna/device.xml\r\n", "ST: " + deviceType + "\r\n", "USN: uuid:" + uuid + "::" + deviceType + "\r\n"} {
		if !strings.Contains(response, line) {
			t.Fatalf("response lacks %q:\n%s", line, response)
		}
	}
	bye := string(notifyPacket(a, "", "uuid:"+uuid, "ssdp:byebye"))
	if !strings.Contains(bye, "NTS: ssdp:byebye\r\n") || !strings.Contains(bye, "USN: uuid:"+uuid+"\r\n") || strings.Contains(bye, "LOCATION") {
		t.Fatalf("byebye:\n%s", bye)
	}
	s := ssdpSocket{addresses: []netip.Prefix{netip.MustParsePrefix("10.0.0.2/24"), netip.MustParsePrefix("192.168.1.2/24")}}
	if got := s.location(netip.MustParseAddr("192.168.1.40"), 8096); got != "http://192.168.1.2:8096/dlna/device.xml" {
		t.Fatalf("location: %s", got)
	}
	if _, _, ok := listenerPort("127.0.0.1:8096"); ok {
		t.Fatal("a loopback-only listener is advertised")
	}
}

func TestSearchBudget(t *testing.T) {
	l := &limiter{}
	if !l.allow(100) || l.allow(30) || !l.allow(20) {
		t.Fatal("answer budget not enforced")
	}
}

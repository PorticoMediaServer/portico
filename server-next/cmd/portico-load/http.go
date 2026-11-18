package main

import (
	"bytes"
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/api"
	"github.com/PorticoMediaServer/portico/server-next/internal/seed"
)

// Wire is what the run over HTTP measured, against a real server process.
type Wire struct {
	People        int               `json:"people"`
	Streams       int               `json:"streams"`
	Seconds       float64           `json:"seconds"`
	Requests      map[string]Timing `json:"requests"`
	Failures      map[string]int    `json:"failures"`
	StreamStarts  Timing            `json:"streamStarts"`
	Chunks        Timing            `json:"mebibyteReads"`
	Stalls        int               `json:"chunksOverOneSecond"`
	NotDirect     int               `json:"streamsNotPlayedDirectly"`
	Converting    int               `json:"convertingStreams"`
	LateSegments  int               `json:"convertedSegmentsLate"`
	ConvertSpeed  []float64         `json:"conversionSpeedsTimesRealTime"`
	ConvertStarts Timing            `json:"conversionStarts"`
	StreamedMiB   int64             `json:"mibStreamed"`
	ServerCPU     float64           `json:"serverCpuCoresOnAverage"`
	ServerPeakRSS int64             `json:"serverPeakRssBytes"`
	DriverCPU     float64           `json:"driverCpuCoresOnAverage"`
	Bars          []Bar             `json:"bars"`
}

// wire is one server reached over HTTP, and what has been measured of it.
type wire struct {
	base     string
	password string
	mu       sync.Mutex
	times    map[string][]time.Duration
	failures map[string]int
}

// person is one device signed in, on one profile.
type person struct {
	w          *wire
	token      string
	restricted bool
	sees       map[string]bool // the libraries the profile may see
}

// httpCrowd is an ordinary busy evening at a real server, through its own routes: people
// streaming films by direct play, each player reading a mebibyte a second with byte ranges
// and reporting where it is every ten seconds, and the rest browsing with a second or two
// between requests, half of them on a restricted profile. Every person is a device of its
// own, signed in with a password. The server was seeded before it started; this sets it up,
// adds a library of real films for the streams, and plays.
func httpCrowd(ctx context.Context, base, setupCodeFile, media, convertMedia, document string, pid, people, streams, converting int, length time.Duration) Wire {
	w := &wire{base: strings.TrimRight(base, "/"), password: randomText(), times: map[string][]time.Duration{}, failures: map[string]int{}}
	code, err := os.ReadFile(setupCodeFile)
	if err != nil {
		log.Fatal(err)
	}
	var owner api.SignInResponse
	w.must("POST", "/v1/setup/owner", "", api.SetupOwnerRequest{SetupCode: strings.TrimSpace(string(code)), Username: "owner", Password: w.password,
		SetupRemote: api.SetupRemote{RemoteAccess: "off"}, Device: device("owner")}, &owner)
	admin := w.choose(owner, owner.Profiles[0].ID)
	w.must("PATCH", "/v1/admin/playback/stream-limits", admin, api.StreamLimitsEdit{Total: new(int), PerPerson: new(int)}, nil)
	open, restricted := w.profiles(admin, owner.Profiles[0].ID)
	films := w.films(admin, "Load films", media, min(streams, 20))
	var conversions []string
	if converting > 0 {
		conversions = w.films(admin, "Load conversions", convertMedia, converting)
	}
	capabilities := profileDocument(document)
	libraries := w.libraries(admin)
	// A server that has just started on a seeded database builds its search index first, and
	// says so with 503: the evening starts once search answers.
	for deadline := time.Now().Add(15 * time.Minute); ; time.Sleep(2 * time.Second) {
		status, raw := w.send("GET", "/v1/search?q=the", admin, nil)
		if status == http.StatusOK {
			break
		}
		if status != http.StatusServiceUnavailable || time.Now().After(deadline) {
			log.Fatalf("search answered %d: %s", status, raw)
		}
	}
	log.Printf("set up: %d profiles, %d films to stream; %d people for %s", len(open)+len(restricted), len(films), people, length)
	// Each kind of request alone first, one after another, so what a request costs is told
	// apart from waiting behind 199 others.
	for n, profile := range []string{open[0], restricted[0]} {
		if p := w.signIn(fmt.Sprint("alone-", n), profile, n == 1); p != nil {
			p.alone(ctx, libraries)
		}
	}

	cpu, mine := cpuSeconds(pid), cpuSeconds(os.Getpid())
	ctx, cancel := context.WithTimeout(ctx, length)
	defer cancel()
	started := time.Now()
	var wg sync.WaitGroup
	var streamed, notDirect, stalls, late int64
	var starts, chunks, convertStarts []time.Duration
	var speeds []float64
	var mu sync.Mutex
	for n := range people {
		wg.Go(func() {
			r := rand.New(rand.NewPCG(uint64(n), 9))
			time.Sleep(time.Duration(r.Int64N(int64(20 * time.Second)))) // people arrive over the first seconds
			name := fmt.Sprint("person-", n)
			if n < streams {
				p := w.signIn(name, pick(r, open), false)
				if p == nil {
					return
				}
				// The first `converting` players declare nothing (the baseline device: H.264 8-bit
				// only) and play the HEVC 10-bit film, so the server converts the picture.
				item, declared := films[n%len(films)], capabilities
				if n < converting {
					item, declared = conversions[n], nil
				}
				s := p.play(ctx, r, item, declared)
				mu.Lock()
				streamed += s.bytes
				switch {
				case n < converting:
					late, convertStarts = late+int64(s.stalls), append(convertStarts, s.start)
					speeds = append(speeds, s.speed)
				case !s.direct:
					notDirect++
				default:
					stalls, chunks, starts = stalls+int64(s.stalls), append(chunks, s.chunks...), append(starts, s.start)
				}
				mu.Unlock()
				return
			}
			profile, isRestricted := pick(r, open), n%2 == 1
			if isRestricted {
				profile = pick(r, restricted)
			}
			if p := w.signIn(name, profile, isRestricted); p != nil {
				p.browse(ctx, r, libraries)
			}
		})
	}
	wg.Wait()
	elapsed := time.Since(started)
	out := Wire{People: people, Streams: streams, Seconds: elapsed.Seconds(), Requests: map[string]Timing{}, Failures: w.failures,
		StreamStarts: summarise(starts), Chunks: summarise(chunks), Stalls: int(stalls), NotDirect: int(notDirect), StreamedMiB: streamed / mib,
		Converting: converting, LateSegments: int(late), ConvertSpeed: speeds, ConvertStarts: summarise(convertStarts),
		ServerCPU: (cpuSeconds(pid) - cpu) / elapsed.Seconds(), ServerPeakRSS: serverPeakRSS(pid),
		DriverCPU: (cpuSeconds(os.Getpid()) - mine) / elapsed.Seconds()}
	var pages, counts []time.Duration
	for name, samples := range w.times {
		out.Requests[name] = summarise(append([]time.Duration(nil), samples...))
		kind, _, _ := strings.Cut(name, " (")
		switch {
		case strings.HasPrefix(name, "count") && strings.HasSuffix(name, "(restricted)"):
			counts = append(counts, samples...)
		case slices.Contains([]string{"home", "first page", "next page", "genre page", "title", "related", "search"}, kind):
			pages = append(pages, samples...)
		}
	}
	page, count, start := summarise(pages), summarise(counts), summarise(starts)
	out.Bars = []Bar{
		{"everyday pages p99", "at most 100 ms", page.P99, "ms", page.P99 <= 100},
		{"restricted counts p99", "at most 300 ms", count.P99, "ms", count.P99 <= 300},
		{"stream starts p99", "at most 1,000 ms", start.P99, "ms", start.P99 <= 1000},
		{"mebibyte reads over a second", "0", float64(stalls), "reads", stalls == 0},
		{"streams not played directly", "0", float64(notDirect), "streams", notDirect == 0},
		{"converted segments late", "0", float64(late), "segments", late == 0},
		{"failed requests", "0", float64(sum(w.failures)), "requests", sum(w.failures) == 0},
	}
	return out
}

// profiles fills the owner's account to its eight profiles: five that see everything and
// three held back, one each by rating, by library and by a blocked label.
func (w *wire) profiles(admin, main string) (open, restricted []string) {
	open = []string{main}
	var labels api.TagPage
	w.must("GET", "/v1/tags?kind=label&limit=10", admin, nil, &labels)
	var libraries struct{ Items []api.AdminLibrary }
	w.must("GET", "/v1/admin/libraries", admin, nil, &libraries)
	var shows []string
	for _, lib := range libraries.Items {
		if lib.Kind == "shows" {
			shows = append(shows, lib.ID)
		}
	}
	holds := []map[string]any{{"maxRatingAge": 12}, {"allLibraries": false, "libraries": shows}, {"blockedLabels": []string{labels.Items[0].ID}}}
	for n := range 7 {
		var p api.Profile
		w.must("POST", "/v1/profiles", admin, api.ProfileCreate{Name: fmt.Sprint("Profile ", n+1)}, &p)
		if n < 4 {
			open = append(open, p.ID)
			continue
		}
		w.must("PUT", "/v1/profiles/"+p.ID+"/restrictions", admin, holds[n-4], nil)
		restricted = append(restricted, p.ID)
	}
	return open, restricted
}

// films copies the real film into a library of its own, n times, scans it and answers the
// titles. The seeded libraries' folders do not exist here, so nothing else is scanned.
func (w *wire) films(admin, library, media string, n int) []string {
	folder, err := os.MkdirTemp(filepath.Dir(media), "films-")
	if err != nil {
		log.Fatal(err)
	}
	raw, err := os.ReadFile(media)
	if err != nil {
		log.Fatal(err)
	}
	for i := range n {
		name := fmt.Sprintf("%s %02d (2020)", library, i+1)
		if err := os.MkdirAll(filepath.Join(folder, name), 0o755); err != nil {
			log.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(folder, name, name+filepath.Ext(media)), raw, 0o644); err != nil {
			log.Fatal(err)
		}
	}
	off := false
	var lib api.AdminLibrary
	w.must("POST", "/v1/admin/libraries", admin, api.LibraryCreate{Name: library, Kind: "movies", Folders: []string{folder},
		Options: &api.LibraryOptions{Watch: &off}}, &lib)
	w.must("POST", "/v1/admin/libraries/"+lib.ID+"/scan", admin, api.ScanRequest{}, nil)
	for deadline := time.Now().Add(5 * time.Minute); ; time.Sleep(time.Second) {
		var page api.ItemPage
		w.must("GET", "/v1/items?kind=movie&limit=100&library="+lib.ID, admin, nil, &page)
		if len(page.Items) == n {
			ids := make([]string, n)
			for i, it := range page.Items {
				ids[i] = it.ID
			}
			return ids
		}
		if time.Now().After(deadline) {
			log.Fatalf("the film library lists %d titles after five minutes, not %d", len(page.Items), n)
		}
	}
}

// libraries are the seeded libraries a person browses, with the kind each one lists.
func (w *wire) libraries(admin string) []api.AdminLibrary {
	var page struct{ Items []api.AdminLibrary }
	w.must("GET", "/v1/admin/libraries", admin, nil, &page)
	var out []api.AdminLibrary
	for _, lib := range page.Items {
		if !strings.HasPrefix(lib.Name, "Load ") {
			out = append(out, lib)
		}
	}
	return out
}

// signIn is a device signing in with the password and choosing a profile.
func (w *wire) signIn(name, profile string, restricted bool) *person {
	var in api.SignInResponse
	if !w.call("sign-in", "POST", "/v1/auth/sign-in", "", api.SignInRequest{Username: "owner", Password: w.password, Device: device(name)}, &in) {
		return nil
	}
	var session api.Session
	if !w.call("choose profile", "POST", "/v1/profiles/"+profile+"/select", in.Session.AccessToken, api.ProfileSelect{}, &session) {
		return nil
	}
	p := &person{w: w, token: session.AccessToken, restricted: restricted, sees: map[string]bool{}}
	var mine api.Libraries
	if !w.call("libraries", "GET", "/v1/libraries", p.token, nil, &mine) {
		return nil
	}
	for _, lib := range mine.Items {
		p.sees[lib.ID] = true
	}
	return p
}

func (w *wire) choose(in api.SignInResponse, profile string) string {
	var session api.Session
	w.must("POST", "/v1/profiles/"+profile+"/select", in.Session.AccessToken, api.ProfileSelect{}, &session)
	return session.AccessToken
}

// alone sends each kind of browsing request 15 times, one at a time, over the libraries the
// profile may see, timed under "alone <kind>".
func (p *person) alone(ctx context.Context, libraries []api.AdminLibrary) {
	who := " (unrestricted)"
	if p.restricted {
		who = " (restricted)"
	}
	r := rand.New(rand.NewPCG(1, 2))
	var ids, words []string
	for i := range 15 {
		var page api.ItemPage
		var lib api.AdminLibrary
		for lib = pick(r, libraries); !p.sees[lib.ID]; lib = pick(r, libraries) {
		}
		list := "/v1/items?limit=50&library=" + lib.ID + "&kind=" + seed.LeafKind(lib.Kind).String() + "&sort=title"
		var home struct{ Rows []struct{ ID string } }
		p.get(ctx, "alone home"+who, "/v1/home", &home)
		for _, row := range home.Rows {
			p.get(ctx, "alone home row "+row.ID+who, "/v1/home/rows/"+url.PathEscape(row.ID)+"?limit=40", nil)
		}
		p.get(ctx, "alone first page"+who, list, &page)
		p.get(ctx, "alone count"+who, list+"&total=true&limit=1", nil)
		genre := list + "&genre=" + url.QueryEscape(seed.Genres(lib.Kind)[i%len(seed.Genres(lib.Kind))])
		p.get(ctx, "alone genre page"+who, genre, nil)
		p.get(ctx, "alone count genre"+who, genre+"&total=true&limit=1", nil)
		for _, it := range page.Items {
			word, _, _ := strings.Cut(it.Title, " ")
			ids, words = append(ids, it.ID), append(words, word)
		}
		if len(ids) > 0 {
			id := pick(r, ids)
			p.get(ctx, "alone title"+who, "/v1/items/"+id, nil)
			p.get(ctx, "alone related"+who, "/v1/items/"+id+"/related", nil)
			p.get(ctx, "alone search"+who, "/v1/search?q="+url.QueryEscape(pick(r, words)), nil)
		}
	}
}

// browse is one person moving about the seeded libraries until ctx ends.
func (p *person) browse(ctx context.Context, r *rand.Rand, all []api.AdminLibrary) {
	who := " (unrestricted)"
	if p.restricted {
		who = " (restricted)"
	}
	var libraries []api.AdminLibrary // a profile held to some libraries browses only those
	for _, lib := range all {
		if p.sees[lib.ID] {
			libraries = append(libraries, lib)
		}
	}
	var next string          // the list this person is scrolling down
	var seen, words []string // titles met while browsing, and words of their names to search for
	for ctx.Err() == nil {
		lib := pick(r, libraries)
		kind := seed.LeafKind(lib.Kind).String()
		if lib.Kind == "shows" && r.IntN(2) == 0 {
			kind = "show"
		} else if lib.Kind == "music" && r.IntN(5) < 3 {
			kind = "album"
		}
		list := "/v1/items?limit=50&library=" + lib.ID + "&kind=" + kind + "&sort=" + pick(r, []string{"title", "title", "added", "year"})
		var page api.ItemPage
		switch n := r.IntN(100); {
		case n < 15:
			p.get(ctx, "home"+who, "/v1/home", nil)
		case n < 35: // open a library: the first page, then its size
			if p.get(ctx, "first page"+who, list, &page) {
				next = list + "&cursor=" + url.QueryEscape(page.NextCursor)
			}
			p.get(ctx, "count"+who, list+"&total=true&limit=1", nil)
		case n < 60:
			if next == "" {
				continue
			}
			if p.get(ctx, "next page"+who, next, &page) && page.NextCursor != "" {
				next = next[:strings.LastIndex(next, "&cursor=")] + "&cursor=" + url.QueryEscape(page.NextCursor)
			}
		case n < 70:
			filtered := list + "&genre=" + url.QueryEscape(pick(r, seed.Genres(lib.Kind)))
			p.get(ctx, "genre page"+who, filtered, &page)
			p.get(ctx, "count genre"+who, filtered+"&total=true&limit=1", nil)
		case n < 85:
			if len(seen) == 0 {
				continue
			}
			id := pick(r, seen)
			p.get(ctx, "title"+who, "/v1/items/"+id, nil)
			p.get(ctx, "related"+who, "/v1/items/"+id+"/related", nil)
		case n < 95:
			if len(words) == 0 {
				continue
			}
			p.get(ctx, "search"+who, "/v1/search?q="+url.QueryEscape(pick(r, words)), nil)
		default:
			if len(seen) == 0 {
				continue
			}
			p.w.call("tap"+who, "PUT", "/v1/items/"+pick(r, seen)+"/state", p.token, map[string]bool{"favorite": r.IntN(2) == 0}, nil)
		}
		for _, it := range page.Items {
			word, _, _ := strings.Cut(it.Title, " ")
			if len(seen) < 200 {
				seen, words = append(seen, it.ID), append(words, word)
			} else {
				at := r.IntN(len(seen))
				seen[at], words[at] = it.ID, word
			}
		}
		select {
		case <-ctx.Done():
		case <-time.After(time.Second + time.Duration(r.Int64N(int64(time.Second)))):
		}
	}
}

func (p *person) get(ctx context.Context, name, path string, out any) bool {
	if ctx.Err() != nil {
		return false
	}
	return p.w.call(name, "GET", path, p.token, nil, out)
}

// played is what one stream saw.
type played struct {
	start  time.Duration
	chunks []time.Duration
	stalls int
	bytes  int64
	direct bool
	speed  float64 // a converted stream's first 30 seconds, in times real time
}

// play is one player: it says what it can do, starts the film, reads a mebibyte of it every
// second with a byte range as a player buffering at 8 Mbit/s does, reports where it is
// every ten seconds, and stops when ctx ends.
func (p *person) play(ctx context.Context, r *rand.Rand, item string, capabilities json.RawMessage) played {
	var out played
	if capabilities != nil && !p.w.call("capabilities", "PUT", "/v1/devices/current/capabilities", p.token, capabilities, nil) {
		return out
	}
	p.w.call("playback options", "GET", "/v1/items/"+item+"/playback-options", p.token, nil, nil)
	began := time.Now()
	var session api.PlaybackSession
	for key := randomText(); ; {
		status, raw := p.w.send("POST", "/v1/playback/sessions", p.token, api.PlaybackStart{ItemID: item}, "Idempotency-Key", key)
		if status == http.StatusAccepted && time.Since(began) < time.Minute {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		if status != http.StatusCreated || json.Unmarshal(raw, &session) != nil {
			p.w.fail("start", fmt.Sprint(status, " ", string(raw)))
			return out
		}
		break
	}
	out.direct = session.Presentation.Mode == "direct"
	media := session.Presentation.URL
	if !strings.HasPrefix(media, "http") {
		media = p.w.base + media
	}
	if !out.direct {
		p.playHLS(ctx, session, media, began, &out)
		return out
	}
	var size, offset int64 = 0, -1
	position := time.Now()
	report := time.NewTicker(10 * time.Second)
	defer report.Stop()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for ctx.Err() == nil {
		if offset < 0 || offset >= size {
			offset = 0
		}
		asked := time.Now()
		got, total, err := readRange(ctx, media, offset)
		if err != nil {
			if ctx.Err() == nil {
				p.w.fail("media read", err.Error())
			}
			break
		}
		took := time.Since(asked)
		if out.bytes == 0 {
			out.start = time.Since(began) // until the first mebibyte is in hand
		}
		out.chunks, out.bytes, size, offset = append(out.chunks, took), out.bytes+got, total, offset+got
		if took > time.Second {
			out.stalls++
		}
		select {
		case <-ctx.Done():
		case <-report.C:
			ms := time.Since(position).Milliseconds()
			p.w.call("progress", "PUT", "/v1/playback/sessions/"+session.ID+"/progress", p.token, api.PlaybackProgress{State: "playing", PositionMs: ms}, nil)
			<-tick.C
		case <-tick.C:
		}
	}
	ms := time.Since(position).Milliseconds()
	p.w.send("DELETE", "/v1/playback/sessions/"+session.ID, p.token, api.PlaybackStop{PositionMs: &ms, ProgressToken: session.ProgressToken})
	return out
}

// playHLS is a player of a converted stream: it fills 30 seconds as fast as the server makes
// them, then fetches each segment as playback nears it, counting every segment that arrives
// after the moment it was due on screen, and reporting progress every ten seconds.
func (p *person) playHLS(ctx context.Context, session api.PlaybackSession, master string, began time.Time, out *played) {
	base := master[:strings.LastIndex(master, "/")+1]
	media := master
	if text, ok := p.fetchText(ctx, master); !ok {
		return
	} else {
		for _, line := range strings.Split(text, "\n") {
			if line = strings.TrimSpace(line); strings.HasSuffix(line, ".m3u8") && !strings.HasPrefix(line, "#") {
				media = base + line // the first rendition: the original's size
				break
			}
		}
	}
	text, ok := p.fetchText(ctx, media)
	if !ok {
		return
	}
	base = media[:strings.LastIndex(media, "/")+1]
	var parts []string
	var at []float64 // where each segment starts
	var clock float64
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "#EXT-X-MAP:"):
			if uri := attribute(line, "URI"); uri != "" {
				parts, at = append(parts, uri), append(at, -1)
			}
		case strings.HasPrefix(line, "#EXTINF:"):
			length, _ := strconv.ParseFloat(strings.TrimSuffix(strings.SplitN(line[len("#EXTINF:"):], ",", 2)[0], ","), 64)
			at = append(at, clock)
			clock += length
		case line != "" && !strings.HasPrefix(line, "#"):
			parts = append(parts, line)
		}
	}
	const ahead = 30.0
	var playing time.Time // when the picture started; moved on by each wait
	report := time.Now()
	for i, part := range parts {
		if ctx.Err() != nil {
			break
		}
		if !playing.IsZero() && at[i] > ahead {
			// A player holds about 30 seconds: it asks for more as playback nears the end of that.
			select {
			case <-ctx.Done():
			case <-time.After(time.Until(playing.Add(time.Duration((at[i] - ahead) * float64(time.Second))))):
			}
		}
		n, ok := p.fetchSegment(ctx, base+part)
		if !ok {
			break
		}
		out.bytes += n
		if at[i] < 0 {
			continue
		}
		if playing.IsZero() {
			playing, out.start = time.Now(), time.Since(began) // the first picture
			continue
		}
		if due := playing.Add(time.Duration(at[i] * float64(time.Second))); time.Now().After(due) {
			out.stalls++
			playing = playing.Add(time.Since(due)) // the picture waited
		}
		if out.speed == 0 && at[i] >= ahead {
			out.speed = at[i] / time.Since(playing).Seconds()
		}
		if time.Since(report) > 10*time.Second {
			report = time.Now()
			ms := time.Since(playing).Milliseconds()
			p.w.call("progress", "PUT", "/v1/playback/sessions/"+session.ID+"/progress", p.token, api.PlaybackProgress{State: "playing", PositionMs: ms}, nil)
		}
	}
	p.w.send("DELETE", "/v1/playback/sessions/"+session.ID, p.token, api.PlaybackStop{ProgressToken: session.ProgressToken})
}

func (p *person) fetchText(ctx context.Context, address string) (string, bool) {
	req, _ := http.NewRequestWithContext(ctx, "GET", address, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		p.w.fail("playlist", fmt.Sprint(resp.StatusCode, " ", string(raw)))
		return "", false
	}
	return string(raw), true
}

// fetchSegment waits for a segment the server is still making, as a player retries.
func (p *person) fetchSegment(ctx context.Context, address string) (int64, bool) {
	for deadline := time.Now().Add(time.Minute); ctx.Err() == nil; time.Sleep(200 * time.Millisecond) {
		req, _ := http.NewRequestWithContext(ctx, "GET", address, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			break
		}
		n, _ := io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			return n, true
		}
		if time.Now().After(deadline) {
			p.w.fail("segment", fmt.Sprint(resp.StatusCode))
			break
		}
	}
	return 0, false
}

func attribute(line, name string) string {
	_, rest, found := strings.Cut(line, name+`="`)
	if !found {
		return ""
	}
	value, _, _ := strings.Cut(rest, `"`)
	return value
}

// readRange reads one mebibyte from offset, as a player does, with no sign-in: the address
// is the grant. It answers the bytes read and the file's size.
func readRange(ctx context.Context, address string, offset int64) (int64, int64, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", address, nil)
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", offset, offset+mib-1))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent {
		return 0, 0, fmt.Errorf("a byte range answered %d", resp.StatusCode)
	}
	n, err := io.Copy(io.Discard, resp.Body)
	_, total, _ := strings.Cut(resp.Header.Get("Content-Range"), "/")
	size, _ := strconv.ParseInt(total, 10, 64)
	return n, size, err
}

// call sends one request, times it under name, and decodes a 2xx answer into out. Anything
// else is counted as a failure of that request.
func (w *wire) call(name, method, path, token string, body, out any) bool {
	started := time.Now()
	status, raw := w.send(method, path, token, body)
	took := time.Since(started)
	if status < 200 || status > 299 {
		w.fail(name, fmt.Sprint(status, " ", string(raw)))
		return false
	}
	if out != nil && json.Unmarshal(raw, out) != nil {
		w.fail(name, "an answer that is not JSON")
		return false
	}
	w.mu.Lock()
	w.times[name] = append(w.times[name], took)
	w.mu.Unlock()
	return true
}

// must is a call the run cannot go on without.
func (w *wire) must(method, path, token string, body, out any) {
	if status, raw := w.send(method, path, token, body); status < 200 || status > 299 || out != nil && json.Unmarshal(raw, out) != nil {
		log.Fatalf("%s %s answered %d: %s", method, path, status, raw)
	}
}

func (w *wire) send(method, path, token string, body any, headers ...string) (int, []byte) {
	var in io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		in = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(method, w.base+path, in)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, []byte(err.Error())
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, raw
}

// fail counts one failed request, and prints the first few of each kind.
func (w *wire) fail(name, why string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.failures[name]++; w.failures[name] <= 3 {
		log.Printf("%s failed: %.300s", name, why)
	}
}

func device(name string) api.DeviceInfo {
	return api.DeviceInfo{InstallationID: fmt.Sprintf("load-%s-%032d", name, 0), Name: name, Platform: "linux", App: "portico-load"}
}

func randomText() string {
	b := make([]byte, 16)
	_, _ = crand.Read(b)
	return hex.EncodeToString(b)
}

// profileDocument is the capability document of one of the server's fixed device profiles.
func profileDocument(file string) json.RawMessage {
	raw, err := os.ReadFile(file)
	if err != nil {
		log.Fatal(err)
	}
	var profile struct {
		Document json.RawMessage `json:"document"`
	}
	if err := json.Unmarshal(raw, &profile); err != nil || len(profile.Document) == 0 {
		log.Fatalf("%s holds no capability document: %v", file, err)
	}
	return profile.Document
}

// cpuSeconds is the processor time the server has used so far.
func cpuSeconds(pid int) float64 {
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		log.Fatal(err)
	}
	// The fields after the command's name, which is in parentheses and may hold spaces.
	fields := strings.Fields(string(stat[bytes.LastIndexByte(stat, ')')+1:]))
	user, _ := strconv.ParseFloat(fields[11], 64)
	system, _ := strconv.ParseFloat(fields[12], 64)
	return (user + system) / 100 // clock ticks; 100 a second on Linux
}

func serverPeakRSS(pid int) int64 {
	status, _ := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	for _, line := range strings.Split(string(status), "\n") {
		if fields := strings.Fields(line); len(fields) >= 2 && fields[0] == "VmHWM:" {
			kb, _ := strconv.ParseInt(fields[1], 10, 64)
			return kb * 1024
		}
	}
	return 0
}

func sum(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}

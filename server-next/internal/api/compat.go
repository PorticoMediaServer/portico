package api

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/PorticoMediaServer/portico/server-next/internal/auth"
	"github.com/PorticoMediaServer/portico/server-next/internal/compat"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/playback"
)

// The Jellyfin-compatible routes, for companion tools. They hold no data and no rights of
// their own: each operation runs the ordinary /v1 route (or the same service a /v1 route
// uses) as the tool's key, so every restriction of the key's person and profile applies.
// They are off until the owner turns them on in the integrations settings. Nothing can be
// played through them: media facts are given, never a media address.
//
// A tool signs in with a username and an app password and gets a token worked out from that
// app password, the server's key and the switch: nothing is stored, revoking the app password
// ends the token, and turning the routes off ends every token made before.

// compatScope is what an API key or personal token needs for each operation. An app password
// has no scopes: its tool may do what its person may.
var compatScope = map[string]Scope{
	compat.MediaUpdated: ScopeLibrary, compat.LibraryRefresh: ScopeLibrary, compat.ItemRefresh: ScopeLibrary,
	compat.NotifyAdmins: ScopeAdmin, compat.SessionCommand: ScopePlayback,
}

// compatToken is the token prefix of a compatible sign-in: "pc." + app password id + "." + proof.
const compatToken = "pc."

type compatState struct {
	// appNames remembers, for each credential, the app names a tool asked keys for. A tool
	// such as Seerr asks for a key by name and then looks for it in the list; a restart
	// forgets the names, and the list then names the key "Portico".
	mu       sync.Mutex
	appNames map[string][]string
}

// serveCompat answers a compatible address. The caller has already matched it.
func (s *Server) serveCompat(w http.ResponseWriter, r *http.Request, op string, values []string) {
	if !s.integrations.get().CompatibleRoutes {
		s.writeError(w, r, ErrNotFound())
		return
	}
	result, err := s.compatOp(r, op, values)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	switch v := result.(type) {
	case nil:
		w.WriteHeader(http.StatusNoContent)
	case compatRedirect:
		http.Redirect(w, r, string(v), http.StatusFound)
	default:
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		json.NewEncoder(w).Encode(result)
	}
}

type compatRedirect string

// compatCaller turns the key a tool sent into its caller, refused wherever the person could
// not sign in. token is what the tool sent, for Auth/Keys to hand back.
func (s *Server) compatCaller(r *http.Request, op string) (Caller, string, error) {
	token, ok := compat.Token(r)
	if !ok || token == "" {
		return Caller{}, "", ErrUnauthorized()
	}
	scope := compatScope[op]
	if scope == "" {
		scope = ScopeRead
	}
	if rest, isCompat := strings.CutPrefix(token, compatToken); isCompat {
		id, proof, _ := strings.Cut(rest, ".")
		key, err := s.db.AppPasswordByID(r.Context(), id, s.now())
		if err == nil && subtle.ConstantTimeCompare([]byte(s.compatProof(key)), []byte(proof)) != 1 {
			err = db.ErrNotFound
		}
		c, err := s.keyCheck(r, key, err, "")
		return c, token, err
	}
	if !strings.HasPrefix(token, keyPrefix) {
		return Caller{}, "", ErrUnauthorized()
	}
	key, err := s.db.KeyCaller(r.Context(), keyHash(token), s.now())
	c, err := s.keyCheck(r, key, err, scope)
	return c, token, err
}

// compatProof ties a compatible sign-in's token to its app password and to the switch.
func (s *Server) compatProof(key db.KeyCaller) string {
	mac := hmac.New(sha256.New, s.compatKey)
	mac.Write([]byte(key.PublicID + "." + key.Hash + "." + strconv.FormatInt(s.integrations.get().CompatibleEpoch, 10)))
	return hex.EncodeToString(mac.Sum(nil))[:40]
}

func (s *Server) compatOp(r *http.Request, op string, values []string) (any, error) {
	q := lowerQuery(r.URL.Query())
	switch op {
	case compat.PublicInfo:
		return s.compatInfo(r, false)
	case compat.Authenticate:
		return s.compatSignIn(r)
	}
	c, token, err := s.compatCaller(r, op)
	if err != nil {
		return nil, err
	}
	r = r.WithContext(context.WithValue(r.Context(), callerKey, c))
	if (op == compat.User || op == compat.UserImage || op == compat.Views || strings.HasPrefix(op, "user_")) && len(values) > 0 {
		if err := s.compatOwnUser(r, c, values[0]); err != nil {
			return nil, err
		}
	}
	if id := q.Get("userid"); id != "" {
		if err := s.compatOwnUser(r, c, id); err != nil {
			return nil, err
		}
	}
	switch op {
	case compat.Info, compat.Configuration:
		return s.compatInfo(r, true)
	case compat.MintKey:
		s.compat.mu.Lock()
		if s.compat.appNames == nil {
			s.compat.appNames = map[string][]string{}
		}
		if name := q.Get("app"); name != "" && len(name) <= 120 && len(s.compat.appNames[token]) < 20 && !contains(s.compat.appNames[token], name) {
			s.compat.appNames[token] = append(s.compat.appNames[token], name)
		}
		s.compat.mu.Unlock()
		return nil, nil
	case compat.Keys:
		// A tool asking for "a key" is given the credential it already holds: the same access,
		// ending when it does.
		s.compat.mu.Lock()
		names := append([]string(nil), s.compat.appNames[token]...)
		s.compat.mu.Unlock()
		if len(names) == 0 {
			names = []string{"Portico"}
		}
		items := []map[string]any{}
		for _, name := range names {
			items = append(items, map[string]any{"AccessToken": token, "AppName": name, "IsActive": true})
		}
		return map[string]any{"Items": items, "TotalRecordCount": len(items), "StartIndex": 0}, nil
	case compat.Users:
		return s.compatUsers(r, c)
	case compat.User:
		me, err := s.compatMe(r)
		return s.compatUser(me.Account, me.Role), err
	case compat.UserImage:
		return nil, ErrNotFound()
	case compat.DisplayPrefs:
		return map[string]any{"Id": "usersettings", "CustomPrefs": map[string]any{}, "Client": q.Get("client"), "SortBy": "SortName"}, nil
	case compat.Views, compat.MediaFolders, compat.VirtualFolders:
		return s.compatLibraries(r, op)
	case compat.UserItems, compat.ItemsList, compat.UserLatest, compat.Latest:
		return s.compatItems(r, q, op == compat.UserLatest || op == compat.Latest, "")
	case compat.UserItem:
		return s.compatItem(r, q, values[1], true)
	case compat.PlaybackInfo:
		item, err := s.compatItem(r, url.Values{"fields": {"mediasources"}}, values[0], true)
		if err != nil {
			return nil, err
		}
		return map[string]any{"MediaSources": item["MediaSources"], "PlaySessionId": ""}, nil
	case compat.Image, compat.IndexedImage:
		return s.compatImage(r, values)
	case compat.Seasons, compat.Episodes:
		return s.compatChildren(r, q, op, values[0])
	case compat.Sessions:
		return s.compatSessions(r, c)
	case compat.SessionCommand:
		return s.compatCommand(r, c, values[0], values[1])
	case compat.Plugins:
		return []any{}, nil
	}
	// Reports of changed files, a library refresh, a title's subtitle refresh and a notice to
	// administrators wait for the scan and notice services they would hand over to.
	return nil, refusal(http.StatusNotImplemented, "not_available", "This server cannot take this request yet.", false)
}

func lowerQuery(q url.Values) url.Values {
	out := url.Values{}
	for k, v := range q {
		out[strings.ToLower(k)] = append(out[strings.ToLower(k)], v...)
	}
	return out
}

// callAs runs a /v1 route in-process as the caller, so it gets exactly the access the caller
// would over the network, and decodes its JSON answer into out.
func (s *Server) callAs(r *http.Request, c Caller, method, path string, query url.Values, out any) error {
	return s.callAsWith(r, c, method, path, query, nil, out)
}

// callAsWith is callAs with a JSON body.
func (s *Server) callAsWith(r *http.Request, c Caller, method, path string, query url.Values, body, out any) error {
	target := path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	var reader io.Reader = http.NoBody
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(context.WithValue(r.Context(), presetCallerKey, c), method, target, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	// Only a 2xx means it was done. The router redirects an address it cleans up (an id of
	// "..", say) without doing anything, so a redirect is not found, never success.
	if rec.Code >= 300 && rec.Code < 400 {
		return ErrNotFound()
	}
	if rec.Code < 200 || rec.Code >= 400 {
		var refused errorBody
		if json.Unmarshal(rec.Body.Bytes(), &refused) == nil && refused.Error.Code != "" {
			refused.Error.Status = rec.Code
			return &refused.Error
		}
		return errors.New("the route answered " + strconv.Itoa(rec.Code))
	}
	if out == nil || rec.Code == http.StatusNoContent {
		return nil
	}
	return json.Unmarshal(rec.Body.Bytes(), out)
}

func (s *Server) compatInfo(r *http.Request, signedIn bool) (any, error) {
	out := map[string]any{"Id": compat.ServerID(s.id), "ServerName": s.serverName(), "Version": compat.Version, "ProductName": "Portico",
		"StartupWizardCompleted": true, "LocalAddress": ""}
	if signedIn {
		out["OperatingSystem"] = ""
		out["HasPendingRestart"] = false
		out["IsShuttingDown"] = false
		out["SupportsLibraryMonitor"] = false
	}
	return out, nil
}

// compatSignIn signs a tool in with a username and an app password, never the account's
// password. It is refused wherever the person could not sign in, and is not a sign-in of
// the person: no device is added.
func (s *Server) compatSignIn(r *http.Request) (any, error) {
	var in struct {
		Username string
		Pw       string
		Password string
	}
	if err := json.NewDecoder(http.MaxBytesReader(nil, r.Body, maxBody)).Decode(&in); err != nil {
		return nil, ErrInvalid("Send a username and password.")
	}
	password := in.Pw
	if password == "" {
		password = in.Password
	}
	if in.Username == "" || password == "" || len(password) > 200 {
		return nil, errBadSignIn()
	}
	key, err := s.db.AppPasswordCaller(r.Context(), in.Username, auth.Hash(auth.CodeKey(password)), s.now())
	c, err := s.keyCheck(r, key, err, "")
	if err != nil {
		if e, ok := asError(err); ok && e.Status == http.StatusUnauthorized {
			return nil, errBadSignIn()
		}
		return nil, err
	}
	token := compatToken + key.PublicID + "." + s.compatProof(key)
	r = r.WithContext(context.WithValue(r.Context(), callerKey, c))
	me, err := s.compatMe(r)
	if err != nil {
		return nil, err
	}
	user := s.compatUser(me.Account, me.Role)
	return map[string]any{"User": user, "AccessToken": token, "ServerId": compat.ServerID(s.id),
		"SessionInfo": map[string]any{"UserId": user["Id"], "UserName": user["Name"], "Client": "Portico", "DeviceName": "Tool"}}, nil
}

func (s *Server) compatMe(r *http.Request) (Me, error) {
	var me Me
	err := s.callAs(r, CallerFrom(r.Context()), "GET", "/v1/me", nil, &me)
	return me, err
}

func (s *Server) compatUser(a Account, role string) map[string]any {
	admin := role == "owner" || role == "admin"
	return map[string]any{"Name": a.Username, "Id": compat.ID(a.ID), "ServerId": compat.ServerID(s.id), "HasPassword": true,
		"HasConfiguredPassword": true, "EnableAutoLogin": false, "Configuration": map[string]any{},
		"Policy": map[string]any{"IsAdministrator": admin, "IsDisabled": false, "EnableAllFolders": true, "EnableMediaPlayback": false}}
}

// compatSeesEveryone: another person's users and streams need an owner or administrator and,
// for an API key or token, the admin scope.
func compatSeesEveryone(c Caller) bool {
	return (c.Role == "owner" || c.Role == "admin") && (c.KeyScopes == nil || contains(c.KeyScopes, string(ScopeAdmin)))
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// compatOwnUser refuses a tool that names another person's user unless it may see everyone.
// The answer is then still what the tool's own profile may see.
func (s *Server) compatOwnUser(r *http.Request, c Caller, id string) error {
	if strings.EqualFold(id, "me") || compatSeesEveryone(c) {
		return nil
	}
	me, err := s.compatMe(r)
	if err != nil {
		return err
	}
	if !strings.EqualFold(strings.ReplaceAll(id, "-", ""), compat.ID(me.Account.ID)) {
		return ErrNotPermitted()
	}
	return nil
}

func (s *Server) compatUsers(r *http.Request, c Caller) (any, error) {
	me, err := s.compatMe(r)
	if err != nil {
		return nil, err
	}
	if !compatSeesEveryone(c) {
		return []any{s.compatUser(me.Account, me.Role)}, nil
	}
	var members Page[Member]
	if err := s.callAs(r, c, "GET", "/v1/admin/members", nil, &members); err != nil {
		return nil, err
	}
	out := []any{}
	for _, m := range members.Items {
		if !m.Disabled {
			out = append(out, s.compatUser(m.Account, m.Role))
		}
	}
	return out, nil
}

func (s *Server) compatLibraries(r *http.Request, op string) (any, error) {
	var libs Libraries
	if err := s.callAs(r, CallerFrom(r.Context()), "GET", "/v1/libraries", nil, &libs); err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for _, l := range libs.Items {
		if op == compat.VirtualFolders {
			out = append(out, map[string]any{"Name": l.Name, "ItemId": compat.ID(l.ID), "CollectionType": compat.CollectionType(l.Kind), "Locations": []string{}})
			continue
		}
		out = append(out, map[string]any{"Name": l.Name, "Id": compat.ID(l.ID), "ServerId": compat.ServerID(s.id), "Type": "CollectionFolder",
			"CollectionType": compat.CollectionType(l.Kind), "IsFolder": true})
	}
	if op == compat.VirtualFolders {
		return out, nil
	}
	return map[string]any{"Items": out, "TotalRecordCount": len(out), "StartIndex": 0}, nil
}

// compatMaxRows bounds one answer: tools page with StartIndex and Limit.
const compatMaxRows = 5000

// compatItems lists titles: by ids, under a library or a title, of some types, newest first
// for the "latest" operations. StartIndex and Limit page over the ordinary list's pages.
func (s *Server) compatItems(r *http.Request, q url.Values, latest bool, parent string) (any, error) {
	start, _ := strconv.Atoi(q.Get("startindex"))
	limit, err := strconv.Atoi(q.Get("limit"))
	if err != nil || limit <= 0 || limit > compatMaxRows {
		limit = compatMaxRows
		if latest {
			limit = 20
		}
	}
	start = max(start, 0)
	if ids := q.Get("ids"); ids != "" {
		out := []map[string]any{}
		for i, id := range strings.Split(ids, ",") {
			if i >= 100 {
				break
			}
			item, err := s.compatItem(r, q, id, false)
			if e, ok := asError(err); ok && e.Status == http.StatusNotFound {
				continue
			}
			if err != nil {
				return nil, err
			}
			out = append(out, item)
		}
		return map[string]any{"Items": out, "TotalRecordCount": len(out), "StartIndex": 0}, nil
	}
	native := url.Values{"limit": {"200"}}
	if p := q.Get("parentid"); p != "" {
		parent = p
	}
	// The ordinary list is of one library or under one title: a tool's list of everything is
	// each library the caller may see, in turn.
	var scopes []url.Values
	switch id := compat.PorticoID(parent); {
	case parent == "":
		var libs Libraries
		if err := s.callAs(r, CallerFrom(r.Context()), "GET", "/v1/libraries", nil, &libs); err != nil {
			return nil, err
		}
		for _, l := range libs.Items {
			scopes = append(scopes, url.Values{"library": {l.ID}})
		}
	case strings.HasPrefix(id, "lib_"):
		scopes = append(scopes, url.Values{"library": {id}})
	case strings.HasPrefix(id, "itm_"):
		scopes = append(scopes, url.Values{"parent": {id}})
	default:
		return nil, ErrNotFound()
	}
	if kinds := compat.PorticoKinds(q.Get("includeitemtypes")); len(kinds) > 0 {
		native.Set("kind", strings.Join(kinds, ","))
	} else if q.Get("includeitemtypes") != "" {
		return map[string]any{"Items": []any{}, "TotalRecordCount": 0, "StartIndex": start}, nil
	}
	if latest || strings.EqualFold(q.Get("sortby"), "DateCreated") {
		native.Set("sort", "added")
		native.Set("order", "desc")
		if !latest && strings.EqualFold(q.Get("sortorder"), "Ascending") {
			native.Set("order", "asc")
		}
	}
	out := []map[string]any{}
	total, skip := 0, start
	for _, scope := range scopes {
		for k, v := range scope {
			native[k] = v
		}
		native.Set("total", "true")
		native.Set("at", strconv.Itoa(skip))
		native.Del("cursor")
		for {
			var page ItemPage
			err := s.callAs(r, CallerFrom(r.Context()), "GET", "/v1/items", native, &page)
			if e, ok := asError(err); ok && e.Status == http.StatusBadRequest && native.Get("kind") != "" {
				break // a library that has no titles of these kinds
			}
			if err != nil {
				return nil, err
			}
			if page.Total != nil {
				total += *page.Total
				skip = max(skip-*page.Total, 0)
			}
			for _, it := range page.Items {
				if len(out) < limit {
					out = append(out, s.compatShape(r, q, it))
				}
			}
			if page.NextCursor == "" || len(out) >= limit || r.Context().Err() != nil {
				break
			}
			native.Set("cursor", page.NextCursor)
			native.Del("total")
			native.Del("at")
		}
		delete(native, "library")
		delete(native, "parent")
	}
	if latest {
		return out, nil
	}
	return map[string]any{"Items": out, "TotalRecordCount": total, "StartIndex": start}, nil
}

func (s *Server) compatChildren(r *http.Request, q url.Values, op, show string) (any, error) {
	parent := show
	if season := q.Get("seasonid"); season != "" && op == compat.Episodes {
		parent = season
	}
	q.Set("includeitemtypes", map[string]string{compat.Seasons: "Season", compat.Episodes: "Episode"}[op])
	if op == compat.Episodes && parent == show {
		// Episodes of a whole show, or of one season by number: read the seasons first.
		seasons, err := s.compatItems(r, url.Values{"includeitemtypes": {"Season"}}, false, show)
		if err != nil {
			return nil, err
		}
		out := []map[string]any{}
		for _, season := range seasons.(map[string]any)["Items"].([]map[string]any) {
			if n := q.Get("season"); n != "" && strconv.Itoa(intOf(season["IndexNumber"])) != n {
				continue
			}
			page, err := s.compatItems(r, q, false, season["Id"].(string))
			if err != nil {
				return nil, err
			}
			out = append(out, page.(map[string]any)["Items"].([]map[string]any)...)
		}
		return map[string]any{"Items": out, "TotalRecordCount": len(out), "StartIndex": 0}, nil
	}
	return s.compatItems(r, q, false, parent)
}

func intOf(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case *int:
		if n != nil {
			return *n
		}
	}
	return -1
}

// compatItem is one title in detail.
func (s *Server) compatItem(r *http.Request, q url.Values, id string, sources bool) (map[string]any, error) {
	native := compat.PorticoID(id)
	if !strings.HasPrefix(native, "itm_") {
		return nil, ErrNotFound()
	}
	var it Item
	if err := s.callAs(r, CallerFrom(r.Context()), "GET", "/v1/items/"+native, nil, &it); err != nil {
		return nil, err
	}
	if sources {
		q = url.Values{"fields": {q.Get("fields") + ",mediasources"}}
	}
	return s.compatShape(r, q, it), nil
}

// compatShape is a title as these tools read it.
func (s *Server) compatShape(r *http.Request, q url.Values, it Item) map[string]any {
	out := map[string]any{"Id": compat.ID(it.ID), "Name": it.Title, "Type": compat.Kind(it.Kind), "ServerId": compat.ServerID(s.id),
		"IsFolder":     it.Kind == "show" || it.Kind == "season" || it.Kind == "artist" || it.Kind == "album" || it.Kind == "collection",
		"LocationType": "FileSystem", "ProviderIds": map[string]string{}, "ImageTags": map[string]string{}, "BackdropImageTags": []string{},
		"UserData": map[string]any{"Played": it.State.Watched, "PlayCount": it.State.PlayCount, "IsFavorite": it.State.Favorite,
			"PlaybackPositionTicks": compat.Ticks(it.State.PositionMs)}}
	switch it.Kind {
	case "movie", "episode":
		out["MediaType"] = "Video"
	case "track", "audiobook":
		out["MediaType"] = "Audio"
	}
	if it.Year != 0 {
		out["ProductionYear"] = it.Year
	}
	if it.ReleaseDate != "" {
		out["PremiereDate"] = it.ReleaseDate + "T00:00:00.0000000Z"
	}
	if it.AddedAt != "" {
		out["DateCreated"] = it.AddedAt
	}
	if it.DurationMs > 0 {
		out["RunTimeTicks"] = compat.Ticks(it.DurationMs)
	}
	if it.Index != nil {
		out["IndexNumber"] = *it.Index
	}
	if it.ParentIndex != nil {
		out["ParentIndexNumber"] = *it.ParentIndex
	}
	if it.ParentID != "" {
		out["ParentId"] = compat.ID(it.ParentID)
	} else if it.LibraryID != "" {
		out["ParentId"] = compat.ID(it.LibraryID)
	}
	switch it.Kind {
	case "season":
		out["SeriesId"] = compat.ID(it.ParentID)
		if it.Parent != nil {
			out["SeriesName"] = it.Parent.Title
		}
		if it.Index != nil {
			out["IndexNumber"] = *it.Index
		}
	case "episode":
		out["SeasonId"] = compat.ID(it.ParentID)
		if it.RootID != "" {
			out["SeriesId"] = compat.ID(it.RootID)
		}
		if it.Root != nil {
			out["SeriesName"] = it.Root.Title
		}
		if it.Parent != nil && it.Parent.Index != nil && it.ParentIndex == nil {
			out["ParentIndexNumber"] = *it.Parent.Index
		}
	}
	for provider, value := range it.ExternalIDs {
		name := map[string]string{"tmdb": "Tmdb", "imdb": "Imdb", "tvdb": "Tvdb", "musicbrainz": "MusicBrainzAlbum", "anilist": "AniList"}[provider]
		if name == "" && provider != "" {
			name = strings.ToUpper(provider[:1]) + provider[1:]
		}
		out["ProviderIds"].(map[string]string)[name] = value
	}
	for role, address := range it.Images {
		tag := imageTag(address)
		switch role {
		case "poster":
			out["ImageTags"].(map[string]string)["Primary"] = tag
		case "backdrop":
			out["BackdropImageTags"] = []string{tag}
		case "thumb", "logo":
			out["ImageTags"].(map[string]string)[strings.ToUpper(role[:1])+role[1:]] = tag
		}
	}
	fields := strings.ToLower(q.Get("fields"))
	if strings.Contains(fields, "mediasources") || strings.Contains(fields, "path") {
		s.compatSources(r, it, out)
	}
	return out
}

func imageTag(address string) string {
	sum := sha256.Sum256([]byte(address))
	return hex.EncodeToString(sum[:8])
}

// compatSources adds a title's versions as media facts, never as something to play, and its
// file paths when the caller may read them (owner and administrators).
func (s *Server) compatSources(r *http.Request, it Item, out map[string]any) {
	versions := it.Versions
	if versions == nil && (it.Kind == "movie" || it.Kind == "episode" || it.Kind == "track") {
		var detail Item
		if s.callAs(r, CallerFrom(r.Context()), "GET", "/v1/items/"+it.ID, nil, &detail) == nil {
			versions = detail.Versions
		}
	}
	paths := map[string]string{}
	var files ItemFiles
	if s.callAs(r, CallerFrom(r.Context()), "GET", "/v1/items/"+it.ID+"/files", nil, &files) == nil {
		for _, v := range files.Versions {
			if len(v.Files) > 0 {
				paths[v.ID] = v.Files[0].Path
			}
		}
	}
	sources := []map[string]any{}
	for _, v := range versions {
		streams := []map[string]any{}
		for _, part := range v.Parts {
			for _, st := range part.Streams {
				stream := map[string]any{"Index": st.Index, "Type": strings.ToUpper(st.Kind[:min(1, len(st.Kind))]) + st.Kind[min(1, len(st.Kind)):],
					"Codec": st.Codec, "Language": st.Language, "IsDefault": st.Default, "IsForced": st.Forced}
				for k, n := range map[string]int{"Width": st.Width, "Height": st.Height, "Channels": st.Channels} {
					if n != 0 {
						stream[k] = n
					}
				}
				streams = append(streams, stream)
			}
			break // the first part describes the version
		}
		src := map[string]any{"Id": compat.ID(v.ID), "Name": v.Name, "Container": v.Container, "Size": v.Size, "Bitrate": v.Bitrate,
			"RunTimeTicks": compat.Ticks(v.DurationMs), "MediaStreams": streams, "Protocol": "File",
			"SupportsDirectPlay": false, "SupportsDirectStream": false, "SupportsTranscoding": false}
		if p := paths[v.ID]; p != "" {
			src["Path"] = p
			if _, ok := out["Path"]; !ok {
				out["Path"] = p
			}
		}
		if v.Width > 0 {
			out["Width"], out["Height"] = v.Width, v.Height
		}
		sources = append(sources, src)
	}
	out["MediaSources"] = sources
}

// compatImage sends the tool to the title's picture, whose address is its own permission.
func (s *Server) compatImage(r *http.Request, values []string) (any, error) {
	if len(values) == 3 && values[2] != "0" {
		return nil, ErrNotFound()
	}
	native := compat.PorticoID(values[0])
	if !strings.HasPrefix(native, "itm_") {
		return nil, ErrNotFound()
	}
	var it Item
	if err := s.callAs(r, CallerFrom(r.Context()), "GET", "/v1/items/"+native, nil, &it); err != nil {
		return nil, err
	}
	role := map[string]string{"primary": "poster", "backdrop": "backdrop", "thumb": "thumb", "logo": "logo"}[strings.ToLower(values[1])]
	if address := it.Images[role]; role != "" && address != "" {
		return compatRedirect(address), nil
	}
	return nil, ErrNotFound()
}

// compatSessions lists what is playing: the caller's own account's streams, or everyone's for
// an owner or administrator allowed to see everyone. Home Assistant reads these; they say they
// cannot be controlled from there, because its controls also browse and start playback.
func (s *Server) compatSessions(r *http.Request, c Caller) (any, error) {
	sessions, err := s.streamsSeenBy(r.Context(), db.Viewer{UserID: c.UserID, ProfileID: c.ProfileID}, compatSeesEveryone(c))
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for _, p := range sessions {
		if p.State == playback.StateEnded {
			continue
		}
		stream := map[string]any{"Id": compat.ID(p.ID), "UserId": compat.ID(p.Device.UserPublicID), "DeviceId": p.Device.PublicID,
			"DeviceName": p.Device.Name, "Client": "Portico", "ApplicationVersion": "", "SupportsMediaControl": false,
			"SupportsRemoteControl": false, "SupportedCommands": []string{}, "PlayableMediaTypes": []string{},
			"Capabilities": map[string]any{"SupportsMediaControl": false, "SupportedCommands": []string{}},
			"PlayState":    map[string]any{"PositionTicks": compat.Ticks(p.PositionMs), "IsPaused": p.State == playback.StatePaused, "CanSeek": false}}
		// A title the caller's profile may not see is left out; the stream itself is listed.
		if p.Visible {
			item := map[string]any{"Id": compat.ID(p.Item.PublicID), "Name": p.Item.Title, "Type": compat.Kind(p.Item.Kind.String()),
				"RunTimeTicks": compat.Ticks(p.Item.DurationMS)}
			if p.Item.Root != nil {
				item["SeriesName"], item["SeriesId"] = p.Item.Root.Title, compat.ID(p.Item.Root.PublicID)
			}
			stream["NowPlayingItem"] = item
		}
		out = append(out, stream)
	}
	return out, nil
}

// compatCommand stops a stream that is playing: the caller's own account's, or anyone's for an
// owner or administrator allowed to see everyone. Pausing, resuming and seeking another
// device's stream wait for the remote control that playing on other devices brings.
func (s *Server) compatCommand(r *http.Request, c Caller, id, command string) (any, error) {
	native := compat.PorticoID(id)
	var target *playback.Session
	for _, p := range s.playback.All() {
		if p.ID == native && p.State != playback.StateEnded && (p.UserID == c.UserID || compatSeesEveryone(c)) {
			target = &p
		}
	}
	if target == nil {
		return nil, ErrNotFound()
	}
	if !strings.EqualFold(command, "stop") {
		return nil, refusal(http.StatusNotImplemented, "not_available", "This server can only stop a stream from here.", false)
	}
	s.playback.Terminate(r.Context(), target.ID, "Stopped by a companion tool.")
	return nil, nil
}

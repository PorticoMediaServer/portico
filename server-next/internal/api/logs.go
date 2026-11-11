package api

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/serverlog"
)

// Messages (the server's log) for the owner and administrators, "Record more detail", and the
// owner's support export. See package serverlog.

// LogLine is one message.
type LogLine struct {
	Time    time.Time         `json:"time"`
	Level   string            `json:"level"`
	Message string            `json:"message"`
	Attrs   map[string]string `json:"attrs,omitempty"`
}

// LogPage is a page of messages, newest first, and whether more detail is being recorded.
type LogPage struct {
	Items      []LogLine `json:"items"`
	NextCursor string    `json:"nextCursor,omitempty"`
	// DetailUntil is when "Record more detail" ends, absent while it is off.
	DetailUntil *time.Time `json:"detailUntil,omitempty"`
}

// LogDetail starts "Record more detail" for Minutes (1 to 240), or ends it with 0.
type LogDetail struct {
	Minutes int `json:"minutes"`
}

// LogDetailState says until when more detail is recorded.
type LogDetailState struct {
	DetailUntil *time.Time `json:"detailUntil,omitempty"`
}

// SupportExportRequest asks for a support export of the last Days days (1 to 30, 7 when
// absent). File paths are replaced unless IncludePaths.
type SupportExportRequest struct {
	Days         int  `json:"days,omitempty"`
	IncludePaths bool `json:"includePaths,omitempty"`
}

const (
	logPage = 200
	// followFor is how long one follow of the log lasts before the app opens it again.
	followFor = 30 * time.Minute
)

// UseLog gives the server its log, for Messages. Without one Messages is empty.
func (s *Server) UseLog(l *serverlog.Log) {
	s.serverLog = l
	l.Keep = func() int { return serverlog.Document.Get(s.settings).KeepDays }
}

func (s *Server) logRoutes() {
	Handle(s, "GET /v1/admin/logs", Admin, func(r *http.Request, _ None) (LogPage, error) {
		out := LogPage{Items: []LogLine{}}
		if s.serverLog == nil {
			return out, nil
		}
		q := r.URL.Query()
		level := q.Get("level")
		if level != "" && level != "debug" && level != "info" && level != "warn" && level != "error" {
			return out, ErrField("level", "Choose debug, info, warn or error.")
		}
		query := serverlog.Query{Level: level, Text: q.Get("text"), Cursor: q.Get("cursor"), Limit: logPage}
		for name, at := range map[string]*time.Time{"from": &query.From, "to": &query.To} {
			if raw := q.Get(name); raw != "" {
				t, err := time.Parse(time.RFC3339, raw)
				if err != nil {
					return out, ErrField(name, "Give a time such as 2026-11-10T18:00:00Z.")
				}
				*at = t
			}
		}
		lines, next, err := s.serverLog.Read(query)
		if errors.Is(err, serverlog.ErrCursor) {
			return out, ErrField("cursor", "This is not a cursor this list gave.")
		}
		for _, e := range lines {
			out.Items = append(out.Items, logLine(e))
		}
		out.NextCursor, out.DetailUntil = next, s.detailUntil()
		return out, err
	})
	Handle(s, "PUT /v1/admin/logs/detail", Admin, func(r *http.Request, req LogDetail) (LogDetailState, error) {
		if req.Minutes < 0 || req.Minutes > 240 {
			return LogDetailState{}, ErrField("minutes", "Record more detail for 1 to 240 minutes, or 0 to stop.")
		}
		if s.serverLog == nil {
			return LogDetailState{}, nil
		}
		until := time.Time{}
		if req.Minutes > 0 {
			until = time.Now().Add(time.Duration(req.Minutes) * time.Minute)
		}
		s.serverLog.Detail(until)
		return LogDetailState{DetailUntil: s.detailUntil()}, nil
	})
	// Following the log is a stream of server-sent events, registered by hand: the last
	// lines in memory, then each new one as it is written.
	follow := &Route{Pattern: "GET /v1/admin/logs/events", Access: Admin, Scope: ScopeAdmin}
	s.routes = append(s.routes, follow)
	s.mux.HandleFunc(follow.Pattern, func(w http.ResponseWriter, r *http.Request) {
		r, err := s.admit(r, follow)
		if err != nil {
			s.writeError(w, r, err)
			return
		}
		s.followLog(w, r)
	})
	export := &Route{Pattern: "POST /v1/admin/support-export", Access: Owner, Scope: ScopeAdmin, Request: reflect.TypeFor[SupportExportRequest]()}
	s.routes = append(s.routes, export)
	s.mux.HandleFunc(export.Pattern, func(w http.ResponseWriter, r *http.Request) {
		r, err := s.admit(r, export)
		if err != nil {
			s.writeError(w, r, err)
			return
		}
		var req SupportExportRequest
		if err := readBody(w, r, &req, false, 0); err != nil {
			s.writeError(w, r, err)
			return
		}
		if req.Days == 0 {
			req.Days = 7
		}
		if req.Days < 1 || req.Days > 30 {
			s.writeError(w, r, ErrField("days", "Choose 1 to 30 days."))
			return
		}
		s.supportExport(w, r, req)
	})
}

func logLine(e serverlog.Entry) LogLine {
	return LogLine{Time: e.Time.UTC(), Level: e.Level, Message: e.Message, Attrs: e.Attrs}
}

func (s *Server) detailUntil() *time.Time {
	if s.serverLog == nil {
		return nil
	}
	if until := s.serverLog.DetailUntil(); !until.IsZero() {
		utc := until.UTC()
		return &utc
	}
	return nil
}

func (s *Server) followLog(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, _ := w.(http.Flusher)
	if s.serverLog == nil {
		return
	}
	heartbeat, end := time.NewTicker(eventsHeartbeat), time.NewTimer(followFor)
	defer heartbeat.Stop()
	defer end.Stop()
	var seq int64
	for {
		lines, more := s.serverLog.Since(seq)
		for _, e := range lines {
			raw, _ := json.Marshal(logLine(e))
			fmt.Fprintf(w, "event: line\ndata: %s\n\n", raw)
			seq = e.Seq
		}
		if flusher != nil {
			flusher.Flush()
		}
		select {
		case <-r.Context().Done():
			return
		case <-end.C:
			return
		case <-heartbeat.C:
			fmt.Fprint(w, ": still here\n\n")
		case <-more:
		}
	}
}

// pathLike finds what looks like a file path: a drive letter or a slash, and at least two
// parts. The API's own addresses (/v1/...) are left as they are.
var pathLike = regexp.MustCompile(`(?:[A-Za-z]:\\|/)(?:[^\s"'<>|:/\\]+[/\\])+[^\s"'<>|:]*`)

// wholePath is a value that is a path from its first character.
var wholePath = regexp.MustCompile(`^(?:[A-Za-z]:\\|/)[^/\\]`)

func hidePaths(text string) string {
	return pathLike.ReplaceAllStringFunc(text, func(p string) string {
		if len(p) > 4 && p[:4] == "/v1/" {
			return p
		}
		return "[path]"
	})
}

// supportExport writes a zip of the messages and activity of the last days, the settings
// without secrets and what this server is. It is made as it is sent and never kept. It holds
// no media, no database and no credentials.
func (s *Server) supportExport(w http.ResponseWriter, r *http.Request, req SupportExportRequest) {
	from := s.now().AddDate(0, 0, -req.Days)
	clean := func(text string) string {
		if req.IncludePaths {
			return text
		}
		return hidePaths(text)
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="portico-support-`+s.now().Format("2006-01-02")+`.zip"`)
	w.Header().Set("Cache-Control", "no-store")
	z := zip.NewWriter(w)
	defer z.Close()
	add := func(name string) *json.Encoder {
		f, _ := z.Create(name)
		return json.NewEncoder(f)
	}
	server := add("server.json")
	server.Encode(map[string]any{"version": s.version, "id": s.id, "os": runtime.GOOS, "arch": runtime.GOARCH, "processors": runtime.NumCPU(),
		"go": runtime.Version(), "madeAt": s.now().UTC(), "days": req.Days, "pathsIncluded": req.IncludePaths})
	settingsOut := map[string]json.RawMessage{}
	for _, name := range s.settings.Names() {
		if doc, err := s.settings.Read(name); err == nil {
			settingsOut[name] = json.RawMessage(clean(string(doc.Values)))
		}
	}
	add("settings.json").Encode(settingsOut)
	if s.serverLog != nil {
		messages := add("messages.jsonl")
		for _, file := range s.serverLog.Files(from) {
			serverlog.Lines(file, func(e serverlog.Entry) {
				if e.Time.Before(from) {
					return
				}
				line := logLine(e)
				line.Message = clean(line.Message)
				for k, v := range line.Attrs {
					// A value that is a path is replaced whole: a path can hold spaces.
					if !req.IncludePaths && wholePath.MatchString(v) && !strings.HasPrefix(v, "/v1/") {
						v = "[path]"
					}
					line.Attrs[k] = clean(v)
				}
				messages.Encode(line)
			})
		}
	}
	activity := add("activity.jsonl")
	var after int64
	for written := 0; written < 100_000; {
		list, err := s.db.ActivityRecord(r.Context(), "", after, 1000)
		if err != nil {
			s.log.Error("the activity record could not be read for a support export", "error", err)
			return
		}
		for _, a := range list {
			if a.At.Before(from) {
				return
			}
			activity.Encode(ActivityEntry{At: a.At.UTC(), Actor: a.Actor, Action: a.Action, Target: clean(a.Target), Detail: clean(a.Detail)})
			written++
		}
		if len(list) < 1000 {
			return
		}
		after = list[len(list)-1].ID
	}
}

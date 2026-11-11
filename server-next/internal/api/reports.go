package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server-next/internal/atomicfile"
	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// "Report a problem", and the report an app offers to send after a crash. Anyone may send one
// with what was playing and, if they agree, their app's log. The owner reads them, sets each
// one's status and replies; the reply reaches the reporter as a notice. Crash reports go
// after 30 days; the daily tidy-up removes them.

// ReportCreate is a report. Context is what was playing and on what device, as the app
// describes it. Log is the app's log, sent only when the person agreed, at most 5 MB.
type ReportCreate struct {
	Kind    string `json:"kind"`
	Message string `json:"message,omitempty"`
	Context JSON   `json:"context,omitempty"`
	Log     string `json:"log,omitempty"`
}

// ProblemReport is a report as its sender and the owner read it.
type ProblemReport struct {
	ID       string `json:"id"`
	Reporter string `json:"reporter"`
	Kind     string `json:"kind"`
	// Status is open, in_progress, resolved or closed.
	Status    string     `json:"status"`
	Message   string     `json:"message,omitempty"`
	Context   JSON       `json:"context,omitempty"`
	HasLog    bool       `json:"hasLog"`
	Reply     string     `json:"reply,omitempty"`
	RepliedAt *time.Time `json:"repliedAt,omitempty"`
	CreatedAt time.Time  `json:"createdAt"`
	UpdatedAt time.Time  `json:"updatedAt"`
}

// ReportEdit sets a report's status, its reply, or both. A new reply is sent to the reporter.
type ReportEdit struct {
	Status *string `json:"status,omitempty"`
	Reply  *string `json:"reply,omitempty"`
}

const (
	maxReportLog = 5 << 20
	reportsPage  = 50
	// crashReportsKept is how long a crash report is kept.
	crashReportsKept = 30 * 24 * time.Hour
)

func (s *Server) reportRoutes() {
	// A report may carry an app's log of up to 5 MB, more than a JSON body may usually be, so
	// this route reads its own body.
	create := &Route{Pattern: "POST /v1/reports", Access: WithProfile, Scope: ScopePlayback, Request: reflect.TypeFor[ReportCreate](),
		Response: reflect.TypeFor[ProblemReport]()}
	s.routes = append(s.routes, create)
	s.mux.HandleFunc(create.Pattern, func(w http.ResponseWriter, r *http.Request) {
		r, err := s.admit(r, create)
		var req ReportCreate
		if err == nil {
			if kind, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); kind != "application/json" {
				err = refusal(http.StatusUnsupportedMediaType, "unsupported_media_type", "Send the request as application/json.", false)
			} else if json.NewDecoder(http.MaxBytesReader(w, r.Body, maxReportLog+64<<10)).Decode(&req) != nil {
				err = ErrInvalid("The request body is not valid JSON for this request, or is larger than 5 MB.")
			}
		}
		var out ProblemReport
		if err == nil {
			out, err = s.createReport(r, req)
		}
		if err != nil {
			s.writeError(w, r, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		json.NewEncoder(w).Encode(out)
	})
	Handle(s, "GET /v1/reports", WithProfile, func(r *http.Request, _ None) (Page[ProblemReport], error) {
		v := viewer(r)
		return s.reportPage(r, db.ReportQuery{Viewer: &v})
	})
	Handle(s, "GET /v1/admin/reports", Owner, func(r *http.Request, _ None) (Page[ProblemReport], error) {
		status := r.URL.Query().Get("status")
		if status != "" && !validReportStatus(status) {
			return Page[ProblemReport]{}, ErrField("status", "Choose open, in_progress, resolved or closed.")
		}
		return s.reportPage(r, db.ReportQuery{Status: status})
	})
	Handle(s, "GET /v1/admin/reports/{id}", Owner, func(r *http.Request, _ None) (ProblemReport, error) {
		report, err := s.db.ReportByID(r.Context(), r.PathValue("id"))
		return problemReport(report), refuse(err)
	})
	Handle(s, "PATCH /v1/admin/reports/{id}", Owner, func(r *http.Request, req ReportEdit) (ProblemReport, error) {
		if req.Status != nil && !validReportStatus(*req.Status) {
			return ProblemReport{}, ErrField("status", "Choose open, in_progress, resolved or closed.")
		}
		if req.Reply != nil {
			*req.Reply = strings.TrimSpace(*req.Reply)
			if utf8.RuneCountInString(*req.Reply) > 2000 {
				return ProblemReport{}, ErrField("reply", "Write a reply of up to 2,000 characters.")
			}
		}
		report, err := s.db.AnswerReport(r.Context(), r.PathValue("id"), req.Status, req.Reply, s.now())
		return problemReport(report), refuse(err)
	})
	Handle(s, "DELETE /v1/admin/reports/{id}", Owner, func(r *http.Request, _ None) (None, error) {
		logs, err := s.db.DeleteReports(r.Context(), r.PathValue("id"), time.Time{})
		s.removeReportLogs(logs)
		return None{}, err
	})
	// The app's log, as the text it was sent as.
	route := &Route{Pattern: "GET /v1/admin/reports/{id}/log", Access: Owner, Scope: ScopeAdmin}
	s.routes = append(s.routes, route)
	s.mux.HandleFunc(route.Pattern, func(w http.ResponseWriter, r *http.Request) {
		r, err := s.admit(r, route)
		if err != nil {
			s.writeError(w, r, err)
			return
		}
		report, err := s.db.ReportByID(r.Context(), r.PathValue("id"))
		if err == nil && report.LogHash == "" {
			err = db.ErrNotFound
		}
		var data []byte
		if err == nil {
			data, err = os.ReadFile(s.reportLogPath(report.LogHash))
		}
		if errors.Is(err, os.ErrNotExist) {
			err = db.ErrNotFound
		}
		if err != nil {
			s.writeError(w, r, refuse(err))
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="report-`+report.PublicID+`.log"`)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(data)
	})
}

func validReportStatus(s string) bool {
	return s == "open" || s == "in_progress" || s == "resolved" || s == "closed"
}

func (s *Server) createReport(r *http.Request, req ReportCreate) (ProblemReport, error) {
	if req.Kind != "problem" && req.Kind != "crash" {
		return ProblemReport{}, ErrField("kind", "Choose problem or crash.")
	}
	req.Message = strings.TrimSpace(req.Message)
	if utf8.RuneCountInString(req.Message) > 5000 {
		return ProblemReport{}, ErrField("message", "Describe the problem in up to 5,000 characters.")
	}
	if req.Kind == "problem" && req.Message == "" {
		return ProblemReport{}, ErrField("message", "Say what went wrong.")
	}
	if len(req.Context) > 16<<10 || len(req.Context) > 0 && !json.Valid(req.Context) {
		return ProblemReport{}, ErrField("context", "The context is at most 16 KB of JSON.")
	}
	if len(req.Log) > maxReportLog {
		return ProblemReport{}, ErrField("log", "The log is at most 5 MB.")
	}
	key, err := idempotencyKey(r)
	if err != nil {
		return ProblemReport{}, err
	}
	// Checked before the log is written: a profile past its day's reports writes nothing.
	repeat, err := s.db.CreatedBefore(r.Context(), viewer(r).UserID, key, requestHash(req))
	if err != nil && !errors.Is(err, db.ErrKeyReused) {
		return ProblemReport{}, err
	}
	if repeat == "" {
		if today, err := s.db.ReportsToday(r.Context(), viewer(r), s.now()); err != nil {
			return ProblemReport{}, err
		} else if today >= db.ReportsPerDay {
			return ProblemReport{}, errTooManyReports()
		}
	}
	// All the logs kept together stay under a size; past it the report is kept without its log.
	if req.Log != "" && s.reportLogsSize()+int64(len(req.Log)) > maxReportLogs {
		req.Log = ""
	}
	hash := ""
	if req.Log != "" {
		// Logs are named by what is in them, so a repeat writes the same file again.
		sum := sha256.Sum256([]byte(req.Log))
		hash = hex.EncodeToString(sum[:])
		if err := os.MkdirAll(filepath.Join(s.dataDir, "reports"), 0o700); err != nil {
			return ProblemReport{}, err
		}
		if err := atomicfile.Write(s.reportLogPath(hash), []byte(req.Log), 0o600); err != nil {
			return ProblemReport{}, err
		}
	}
	report, err := s.db.CreateReport(r.Context(), db.NewReport{Viewer: viewer(r), Kind: req.Kind, Message: req.Message,
		Context: string(req.Context), LogHash: hash, IdempotencyKey: key, RequestHash: requestHash(req), Now: s.now()})
	switch {
	case errors.Is(err, db.ErrKeyReused):
		return ProblemReport{}, errKeyReused()
	case errors.Is(err, db.ErrTooManyReports):
		return ProblemReport{}, errTooManyReports()
	}
	return problemReport(report), err
}

func errTooManyReports() *Error {
	return ErrTooMany("too_many_reports", "You have sent as many reports as you can today. Try again tomorrow.", 24*time.Hour)
}

// maxReportLogs bounds all the app logs kept with reports together.
const maxReportLogs = 512 << 20

// reportLogsSize is how much the kept app logs take.
func (s *Server) reportLogsSize() int64 {
	var size int64
	entries, _ := os.ReadDir(filepath.Join(s.dataDir, "reports"))
	for _, e := range entries {
		if info, err := e.Info(); err == nil {
			size += info.Size()
		}
	}
	return size
}

func (s *Server) reportPage(r *http.Request, q db.ReportQuery) (Page[ProblemReport], error) {
	after, err := cursorID(r)
	if err != nil {
		return Page[ProblemReport]{}, err
	}
	q.After, q.Limit = after, reportsPage+1
	list, err := s.db.Reports(r.Context(), q)
	out := Page[ProblemReport]{Items: []ProblemReport{}}
	if len(list) > reportsPage {
		list = list[:reportsPage]
		out.NextCursor = strconv.FormatInt(list[len(list)-1].ID, 10)
	}
	for _, report := range list {
		out.Items = append(out.Items, problemReport(report))
	}
	return out, err
}

func problemReport(r db.Report) ProblemReport {
	out := ProblemReport{ID: r.PublicID, Reporter: r.ReporterName, Kind: r.Kind, Status: r.Status, Message: r.Message,
		HasLog: r.LogHash != "", Reply: r.Reply, RepliedAt: r.RepliedAt, CreatedAt: r.CreatedAt.UTC(), UpdatedAt: r.UpdatedAt.UTC()}
	if r.Context != "" {
		out.Context = JSON(r.Context)
	}
	if out.RepliedAt != nil {
		utc := out.RepliedAt.UTC()
		out.RepliedAt = &utc
	}
	return out
}

func (s *Server) reportLogPath(hash string) string {
	return filepath.Join(s.dataDir, "reports", hash+".log")
}

func (s *Server) removeReportLogs(hashes []string) {
	for _, hash := range hashes {
		if err := os.Remove(s.reportLogPath(hash)); err != nil && !errors.Is(err, os.ErrNotExist) {
			s.log.Warn("an app log no report needs could not be removed", "error", err)
		}
	}
}

// TidyReports removes crash reports past their 30 days, and every app log no report names: a
// log written for a report that was then refused, or one left by a lost race. The daily
// tidy-up runs it.
func (s *Server) TidyReports(ctx context.Context) error {
	logs, err := s.db.DeleteReports(ctx, "", s.now().Add(-crashReportsKept))
	s.removeReportLogs(logs)
	if err != nil {
		return err
	}
	named, err := s.db.ReportLogs(ctx)
	if err != nil {
		return err
	}
	entries, _ := os.ReadDir(filepath.Join(s.dataDir, "reports"))
	for _, e := range entries {
		hash, ok := strings.CutSuffix(e.Name(), ".log")
		if ok && !named[hash] && s.now().Sub(fileTime(e)) > time.Hour {
			os.Remove(filepath.Join(s.dataDir, "reports", e.Name()))
		}
	}
	return nil
}

// fileTime is when a file was last written; a log younger than an hour may belong to a report
// being made.
func fileTime(e os.DirEntry) time.Time {
	if info, err := e.Info(); err == nil {
		return info.ModTime()
	}
	return time.Now()
}

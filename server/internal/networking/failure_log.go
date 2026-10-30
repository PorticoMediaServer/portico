package networking

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"

	"github.com/PorticoMediaServer/portico/apikit/httpclientclose"
	"github.com/PorticoMediaServer/portico/server/internal/hostlimits"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// A 503 from the identity proof or a claim route used to say nothing about
// why, so a server that stopped proving its identity until restarted left no
// trace of which dependency had failed. These types carry the failing step and
// a coarse class up to the handler, which logs one line per class per minute.
//
// Nothing here may carry key material, nonces, tokens, request bodies or file
// contents: the log records the step, the class, an errno name, and — for
// database errors only — SQLite's own error text, which names tables and codes
// but never values.

// failureStep names the handler step an error came from. It is transparent to
// errors.Is and errors.As, so wrapping never changes how a caller answers.
type failureStep struct {
	step   string
	detail string
	err    error
}

func (e *failureStep) Error() string { return e.err.Error() }
func (e *failureStep) Unwrap() error { return e.err }

func stepFailure(step string, err error) error {
	if err == nil {
		return nil
	}
	return &failureStep{step: step, err: err}
}

// keyFileError is a protected-key read that failed. It still answers
// errors.Is(err, ErrUnavailable) exactly as the bare sentinel did; the cause is
// kept aside for classification only.
type keyFileError struct {
	stage string // lstat, open, stat, read, type, permissions, size, changed, content
	cause error
}

func (e *keyFileError) Error() string { return ErrUnavailable.Error() }
func (e *keyFileError) Unwrap() error { return ErrUnavailable }
func keyFileFailure(stage string, cause error) error {
	return &keyFileError{stage: stage, cause: cause}
}

// authorityError is a lifecycle lease that could not be acquired, was fenced
// between steps, or refused its commit. It unwraps to the error the caller has
// always seen.
type authorityError struct {
	stage string // acquire, missing, check, commit
	err   error
}

func (e *authorityError) Error() string { return e.err.Error() }
func (e *authorityError) Unwrap() error { return e.err }

// failureSummary is what one log line says about one failure.
type failureSummary struct {
	Step   string
	Class  string
	Detail string
	// keyDetail is folded into the rate-limit key. Only bounded values (errno
	// names, fixed reasons) go here, so the limiter's map cannot grow with the
	// variety of database messages.
	keyDetail string
}

func summarizeFailure(request context.Context, err error) failureSummary {
	var s failureSummary
	var step *failureStep
	if errors.As(err, &step) {
		s.Step = step.step
		s.Detail = step.detail
		s.keyDetail = step.detail
	}
	var key *keyFileError
	var authority *authorityError
	var coded interface{ Code() int }
	var errno syscall.Errno
	switch {
	case errors.As(err, &key):
		s.Class = "key file " + key.stage
		if errors.As(key.cause, &errno) {
			s.Detail = errnoName(errno)
			if errno == syscall.EMFILE || errno == syscall.ENFILE {
				if soft, _ := hostlimits.OpenFiles(); soft > 0 {
					s.Detail += ", open file limit " + strconv.FormatUint(soft, 10)
				}
			}
		} else if key.cause != nil {
			s.Detail = "not a system error"
		}
		s.keyDetail = s.Detail
	case errors.As(err, &authority):
		s.Class = "authority " + authority.stage
		if request != nil && request.Err() == nil && errors.Is(err, context.Canceled) {
			s.Detail = "lease canceled while the request was still live"
			s.keyDetail = s.Detail
		}
	case errors.Is(err, context.DeadlineExceeded):
		s.Class = "deadline exceeded"
	case errors.Is(err, context.Canceled):
		s.Class = "canceled"
		switch {
		case request == nil:
		case request.Err() == nil:
			// The request is still live, so the lease context was canceled
			// under it: the authority gate was quiesced mid-operation.
			s.Detail = "lease canceled while the request was still live"
		default:
			// net/http canceled the request's context: the client went away,
			// or the keep-alive connection's context was canceled earlier.
			s.Detail = "request context canceled"
		}
		s.keyDetail = s.Detail
	case errors.As(err, &coded):
		// modernc.org/sqlite errors: the text names the result code and, for
		// schema errors, a table or column — never a bound value.
		s.Class = "database"
		s.Detail = boundedDetail(err.Error())
	case errors.Is(err, sql.ErrConnDone) || errors.Is(err, sql.ErrTxDone):
		s.Class = "database"
		s.Detail = err.Error()
	case errors.As(err, &errno):
		s.Class = "system"
		s.Detail = errnoName(errno)
		s.keyDetail = s.Detail
	case errors.Is(err, ErrStale):
		s.Class = "identity stale"
	case errors.Is(err, ErrInvalid):
		s.Class = "identity invalid"
	case errors.Is(err, ErrUnavailable):
		s.Class = "unavailable"
	case databaseSteps[s.Step]:
		// database/sql's own errors ("sql: database is closed" and the like)
		// are fixed strings with no values in them.
		s.Class = "database"
		s.Detail = boundedDetail(err.Error())
	default:
		// The dynamic type is safe to log and usually enough to find the source.
		s.Class = "other"
		s.Detail = fmt.Sprintf("%T", err)
		s.keyDetail = s.Detail
	}
	return s
}

// databaseSteps are steps whose only failures are database/sql errors.
var databaseSteps = map[string]bool{"snapshot": true, "identity row": true}

func boundedDetail(v string) string {
	v = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, v)
	if len(v) > 160 {
		v = v[:160] + "…"
	}
	return v
}

// errnoName is the conventional name for the errors a file or socket open can
// return. The set is portable across the platforms the server builds for.
func errnoName(e syscall.Errno) string {
	switch e {
	case syscall.EMFILE:
		return "EMFILE"
	case syscall.ENFILE:
		return "ENFILE"
	case syscall.ENOENT:
		return "ENOENT"
	case syscall.EACCES:
		return "EACCES"
	case syscall.EPERM:
		return "EPERM"
	case syscall.ENOTDIR:
		return "ENOTDIR"
	case syscall.ELOOP:
		return "ELOOP"
	case syscall.EIO:
		return "EIO"
	case syscall.EINTR:
		return "EINTR"
	case syscall.EAGAIN:
		return "EAGAIN"
	case syscall.ENOSPC:
		return "ENOSPC"
	case syscall.EBADF:
		return "EBADF"
	case syscall.ENOMEM:
		return "ENOMEM"
	case syscall.ENAMETOOLONG:
		return "ENAMETOOLONG"
	case syscall.EROFS:
		return "EROFS"
	case syscall.ECONNREFUSED:
		return "ECONNREFUSED"
	case syscall.ECONNRESET:
		return "ECONNRESET"
	case syscall.ETIMEDOUT:
		return "ETIMEDOUT"
	case syscall.EHOSTUNREACH:
		return "EHOSTUNREACH"
	case syscall.ENETUNREACH:
		return "ENETUNREACH"
	}
	return "errno " + strconv.Itoa(int(e))
}

// failureLog writes the first failure of each class per window and counts the
// rest, so a client retrying in a loop produces one line a minute, not one line
// a request.
type failureLog struct {
	mu      sync.Mutex
	window  time.Duration
	now     func() time.Time
	logf    func(string, ...any)
	classes map[string]*failureWindow
}
type failureWindow struct {
	started    time.Time
	suppressed int
}

const failureLogClasses = 128

func newFailureLog(logf func(string, ...any)) *failureLog {
	return &failureLog{window: time.Minute, now: time.Now, logf: logf, classes: make(map[string]*failureWindow)}
}

var sharedFailureLog = newFailureLog(log.Printf)

func (l *failureLog) report(route string, status int, request context.Context, err error) {
	if l == nil || err == nil {
		return
	}
	s := summarizeFailure(request, err)
	clientClosed := httpclientclose.Context(request)
	if clientClosed {
		status, s.Class, s.Detail, s.keyDetail = httpclientclose.Status, "client closed", "", ""
	}
	key := route + "\x00" + s.Step + "\x00" + s.Class + "\x00" + s.keyDetail
	l.mu.Lock()
	now := l.now()
	current, ok := l.classes[key]
	if ok && now.Sub(current.started) < l.window {
		current.suppressed++
		l.mu.Unlock()
		return
	}
	suppressed := 0
	if ok {
		suppressed = current.suppressed
		current.started, current.suppressed = now, 0
	} else {
		if len(l.classes) >= failureLogClasses {
			for k, v := range l.classes {
				if now.Sub(v.started) >= l.window {
					delete(l.classes, k)
				}
			}
			if len(l.classes) >= failureLogClasses {
				clear(l.classes)
			}
		}
		l.classes[key] = &failureWindow{started: now}
	}
	logf := l.logf
	l.mu.Unlock()
	var b strings.Builder
	level := "warn"
	if clientClosed {
		level = "info"
	}
	fmt.Fprintf(&b, "[network] %s: %s answered %d: ", level, route, status)
	if s.Step != "" {
		fmt.Fprintf(&b, "step %s, ", s.Step)
	}
	b.WriteString(s.Class)
	if s.Detail != "" {
		fmt.Fprintf(&b, " (%s)", s.Detail)
	}
	if suppressed > 0 {
		fmt.Fprintf(&b, "; %d more like this since the last report", suppressed)
	}
	logf("%s", b.String())
}

// routeLabel names the route by its registered pattern, never by a concrete
// path that could carry an identifier.
func routeLabel(r *http.Request, fallback string) string {
	if r != nil && r.Pattern != "" {
		return r.Pattern
	}
	return fallback
}

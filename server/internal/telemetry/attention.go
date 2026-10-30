package telemetry

import (
	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"sort"
	"strconv"
	"strings"
)

// Attention composes the Server overview's "needs attention" list. Composition
// is a pure function of gathered facts so the list can be tested from fixtures
// and so gathering stays in one bounded place.

// Action tells the client where to send the owner. Kind is a stable key the
// client maps to a route; target names the specific record where one exists.
type Action struct {
	Kind   string `json:"kind"`
	Target string `json:"target"`
}

// MajorIssue is a qualified fact, separate from diagnostic health and ordinary
// attention items. Code selects localized title/details; Subject is a library
// or source label, never a person, file path, diagnostic, or credential.
type MajorIssue struct {
	Kind        string `json:"kind"`
	Code        string `json:"code"`
	Severity    string `json:"severity"`
	Subject     string `json:"subject"`
	Destination Action `json:"destination"`
}

// MajorIssues admits only facts that stop playback or risk data. The callers
// supply measured conditions; counts, scans and watchdog health never qualify.
func MajorIssues(unreachable []NamedRecord, storageNearlyFull, backupFailed, renewalFailed bool) []MajorIssue {
	out := []MajorIssue{}
	for _, v := range unreachable {
		out = append(out, MajorIssue{Kind: "library", Code: "library_unreachable", Severity: "playback_stop", Subject: v.Name, Destination: Action{Kind: "library-source", Target: v.ID}})
		if len(out) == 20 {
			return out
		}
	}
	for _, v := range []struct {
		active             bool
		code, kind, target string
	}{
		{storageNearlyFull, "storage_nearly_full", "storage", "state"},
		{backupFailed, "backup_failed", "backups", ""},
		{renewalFailed, "secure_renewal_failed", "networking", "certificate"},
	} {
		if v.active && len(out) < 20 {
			out = append(out, MajorIssue{Kind: v.kind, Code: v.code, Severity: "data_risk", Subject: "", Destination: Action{Kind: v.kind, Target: v.target}})
		}
	}
	return out
}

type Item struct {
	ID       string `json:"id"`
	Severity string `json:"severity"`
	Title    string `json:"title"`
	Detail   string `json:"detail"`
	Action   Action `json:"action"`
}

// Severities, most urgent first. Ordering the list is the server's job: a
// client must not have to know which of these matters more.
const (
	SeverityCritical = "critical"
	SeverityWarning  = "warning"
	SeverityInfo     = "info"
)

var severityRank = map[string]int{SeverityCritical: 0, SeverityWarning: 1, SeverityInfo: 2}

// OpenAlert is the part of an operations alert this list needs.
type OpenAlert struct {
	ID       string
	Code     string
	Severity string
}

// NamedRecord is any library, source or scan the list names back to the owner.
type NamedRecord struct {
	ID     string
	Name   string
	Detail string
}

// AttentionFacts is everything the list is composed from. Every field is
// gathered under one bounded budget; nothing here triggers further work.
type AttentionFacts struct {
	Alerts              []OpenAlert
	UnavailableSources  []NamedRecord
	FailedScans         []NamedRecord
	PausedScans         []NamedRecord
	CertificateExpiry   *CertificateExpiry
	StorageFreeRatio    *float64
	StorageFreeBytes    *int64
	ConversionsDeclined int
	TranscodingEnabled  bool
	TemporaryDirectory  TemporaryDirectory
	FFmpegOK            bool
}

// CertificateExpiry carries only what the warning needs, so this package never
// holds certificate material.
type CertificateExpiry struct {
	DaysRemaining int
	DNSName       string
}

// CertificateWarningDays is how far ahead a certificate expiry is raised. Two
// weeks leaves room for a renewal to fail and be noticed.
const CertificateWarningDays = 14

// StorageWarningRatio is the free proportion of the state volume below which
// the owner is told. Conversions and metadata both stop working before zero.
const StorageWarningRatio = 0.05

// Attention returns the list, most urgent first and bounded. The cap is high
// enough to name every distinct condition and low enough to stay one screen.
func Attention(facts AttentionFacts) []Item {
	items := []Item{}
	if tool := decoder.CurrentToolchain(); tool != nil {
		if missing := tool.MissingRequired(); len(missing) > 0 {
			items = append(items, Item{ID: "media-toolchain", Severity: SeverityWarning, Title: "Media conversion needs attention", Detail: "Installed FFmpeg is missing: " + strings.Join(missing, ", "), Action: Action{Kind: "settings", Target: "transcoding"}})
		}
	}
	for _, alert := range facts.Alerts {
		severity := SeverityWarning
		if alert.Severity == "critical" {
			severity = SeverityCritical
		} else if alert.Severity == "info" {
			severity = SeverityInfo
		}
		title, detail := "Open alert", "The server raised "+alert.Code+" and it has not been acknowledged."
		if alert.Code == "server_overloaded" {
			title = "Server is overloaded"
			detail = "More than 20 requests timed out in five minutes. The server is still running, but some actions may need to be retried."
		}
		if alert.Code == "state-permissions" {
			title = "State folder is readable by other accounts"
			detail = "Other accounts on this computer can read your Portico data, including sign-in secrets. Fix the permissions from the owner's maintenance page, or dismiss this warning."
		}
		items = append(items, Item{
			ID:       "alert:" + alert.ID,
			Severity: severity,
			Title:    title,
			Detail:   detail,
			Action:   Action{Kind: "alert", Target: alert.ID},
		})
	}
	for _, source := range facts.UnavailableSources {
		items = append(items, Item{
			ID:       "source:" + source.ID,
			Severity: SeverityCritical,
			Title:    "Library source unavailable",
			Detail:   named(source.Name, "A library source") + " is not reachable" + suffix(source.Detail) + " Items from it cannot be played.",
			Action:   Action{Kind: "library-source", Target: source.ID},
		})
	}
	for _, scan := range facts.FailedScans {
		items = append(items, Item{
			ID:       "scan-failed:" + scan.ID,
			Severity: SeverityWarning,
			Title:    "Scan failed",
			Detail:   named(scan.Name, "A library scan") + " stopped before finishing" + suffix(scan.Detail) + " New and changed files are not in the library yet.",
			Action:   Action{Kind: "scan", Target: scan.ID},
		})
	}
	for _, scan := range facts.PausedScans {
		items = append(items, Item{
			ID:       "scan-paused:" + scan.ID,
			Severity: SeverityInfo,
			Title:    "Scan paused",
			Detail:   named(scan.Name, "A library scan") + " is paused and will not resume on its own.",
			Action:   Action{Kind: "scan", Target: scan.ID},
		})
	}
	if c := facts.CertificateExpiry; c != nil && c.DaysRemaining <= CertificateWarningDays {
		severity := SeverityWarning
		if c.DaysRemaining <= 0 {
			severity = SeverityCritical
		}
		items = append(items, Item{
			ID:       "certificate",
			Severity: severity,
			Title:    "Certificate expiring",
			Detail:   "The certificate for " + named(c.DNSName, "this server") + " expires in " + strconv.Itoa(c.DaysRemaining) + " days. Remote access stops working when it does.",
			Action:   Action{Kind: "networking", Target: "certificate"},
		})
	}
	if ratio := facts.StorageFreeRatio; ratio != nil && *ratio < StorageWarningRatio {
		items = append(items, Item{
			ID:       "storage",
			Severity: SeverityCritical,
			Title:    "Storage nearly full",
			Detail:   "The state volume has " + strconv.Itoa(int(*ratio*100)) + "% free. Scans, metadata and conversions all fail when it runs out.",
			Action:   Action{Kind: "storage", Target: "state"},
		})
	}
	if !facts.TranscodingEnabled && facts.ConversionsDeclined > 0 {
		items = append(items, Item{
			ID:       "transcoding-disabled",
			Severity: SeverityWarning,
			Title:    "Transcoding is off and was needed",
			Detail:   "Transcoding is turned off, and " + strconv.Itoa(facts.ConversionsDeclined) + " playback attempts in the last day needed a converted stream.",
			Action:   Action{Kind: "setting", Target: "transcodingEnabled"},
		})
	}
	if !facts.TemporaryDirectory.Ready {
		items = append(items, Item{
			ID:       "temporary-directory",
			Severity: SeverityCritical,
			Title:    "Conversion directory not writable",
			Detail:   "This server cannot write to " + named(facts.TemporaryDirectory.Path, "the conversion working directory") + ". Every conversion will fail.",
			Action:   Action{Kind: "setting", Target: "temporaryDirectory"},
		})
	}
	if !facts.FFmpegOK {
		items = append(items, Item{
			ID:       "ffmpeg",
			Severity: SeverityCritical,
			Title:    "ffmpeg is missing",
			Detail:   "The ffmpeg executable could not be run. Conversions, subtitles and artwork extraction all depend on it.",
			Action:   Action{Kind: "dependency", Target: "ffmpeg"},
		})
	}
	sort.SliceStable(items, func(i, j int) bool {
		return severityRank[items[i].Severity] < severityRank[items[j].Severity]
	})
	if len(items) > 50 {
		items = items[:50]
	}
	return items
}

func named(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
func suffix(detail string) string {
	if detail == "" {
		return "."
	}
	return ": " + detail + "."
}

package scan

import (
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

var issueDates = []*regexp.Regexp{
	regexp.MustCompile(`(?:^|[^0-9])(\d{4}-\d{2}(?:-\d{2})?)(?:[^0-9]|$)`),
	regexp.MustCompile(`(?:^|[^0-9])(\d{8})(?:[^0-9]|$)`),
	regexp.MustCompile(`(?i)(?:^|[^a-z0-9])((?:\d{1,2} +)?[a-z]{3,9} +\d{4})(?:[^a-z0-9]|$)`),
}

// The common unambiguous date shapes in read-web.md. There is no guessed day/month order.
func issueDateName(name string) (date, rest string) {
	for _, pattern := range issueDates {
		m := pattern.FindStringSubmatchIndex(name)
		if m == nil {
			continue
		}
		text := name[m[2]:m[3]]
		for _, layout := range []string{"2006-01-02", "20060102", "2 Jan 2006", "2 January 2006", "January 2006", "Jan 2006", "2006-01"} {
			parsed, err := time.Parse(layout, text)
			if err != nil || parsed.Year() < 1 {
				continue
			}
			format := "2006-01-02"
			if layout == "January 2006" || layout == "Jan 2006" || layout == "2006-01" {
				format = "2006-01"
			}
			return parsed.Format(format), strings.Trim(name[:m[2]]+name[m[3]:], " -_().[]")
		}
	}
	return "", name
}

// A date alone may be a novel's title. Only repeated publication titles with different
// dates establish a run outside the explicit Magazines/Newspapers folders.
func datedIssueRuns(f *folder) map[string]bool {
	first, runs := map[string]string{}, map[string]bool{}
	for _, file := range f.media {
		date, title := issueDateName(cleanName(file.entry.Name))
		if date == "" || title == "" {
			continue
		}
		title = fold(title)
		if earlier, ok := first[title]; ok && earlier != date {
			runs[title] = true
		}
		first[title] = date
	}
	return runs
}

// A publication folder holds dated or numbered issue files. Explicit file fields win;
// Magazines/Newspapers folders also disambiguate weekly magazines named with a full date.
func (r *run) namedPeriodical(d details, rel string, runs map[string]bool) (publication int, date, group string, index *int) {
	publication, date = issue(d)
	_, explicit := db.ParsePublication(d["publication"])
	if explicit && publication == db.PublicationBook {
		return
	}
	folder := path.Dir(rel)
	group = cleanName(path.Base(folder) + ".")
	folderDate, folderTitle := issueDateName(group)
	if folderDate != "" {
		folder = path.Dir(folder)
		group = cleanName(path.Base(folder) + ".")
		if group == "" || group == "." {
			group = folderTitle
		}
	}
	name := cleanName(path.Base(rel))
	fileDate, prefix := issueDateName(name)
	category := db.PublicationBook
	for _, part := range strings.Split(folder, "/") {
		switch strings.ToLower(part) {
		case "magazines", "magazine":
			category = db.PublicationMagazine
		case "newspapers", "newspaper":
			category = db.PublicationNewspaper
		}
	}
	var numberPrefix string
	if fileDate == "" {
		if m := issueNumber.FindStringSubmatchIndex(name); m != nil {
			n, _ := strconv.Atoi(name[m[2]:m[3]])
			if n > 0 {
				index = &n
				numberPrefix = strings.TrimSpace(name[:m[0]])
			}
		}
	}
	switch strings.ToLower(group) {
	case "magazines", "magazine", "newspapers", "newspaper":
		group = ""
	}
	if group == "" || group == "." {
		group = prefix
		if fileDate == "" {
			group = numberPrefix
		}
	}
	if !explicit {
		publication = category
		if publication == db.PublicationBook && r.lib.Kind == db.LibraryBooks && fileDate != "" && runs[fold(prefix)] {
			publication, group = db.PublicationMagazine, prefix
			if len(fileDate) == 10 {
				publication = db.PublicationNewspaper
			}
		}
	}
	if publication == db.PublicationBook {
		return publication, date, "", nil
	}
	if date == "" {
		date = firstOf(folderDate, fileDate)
	}
	if group == "" || group == "." {
		return publication, date, "", index
	}
	return
}

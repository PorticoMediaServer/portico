package remote

import "strings"

// SafeName reports whether a name a remote server listed may be passed on. A listing comes
// from a server Portico does not control, so every kind of source drops, before the scanner
// sees it, a name that is empty or holds "..", a slash, a backslash or a NUL: such a name
// could make a path that points somewhere other than the file listed.
func SafeName(name string) bool {
	return name != "" && name != "." && !strings.Contains(name, "..") && !strings.ContainsAny(name, "/\\\x00")
}

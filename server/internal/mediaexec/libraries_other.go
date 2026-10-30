//go:build !darwin && !linux

package mediaexec

// ResolveLibraries has nothing to resolve where no sandbox backend exists: the
// decoder runs with the platform's own loader and the baseline restrictions.
func ResolveLibraries(string, string, []string) ([]string, error) {
	return nil, nil
}

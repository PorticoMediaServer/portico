//go:build darwin

package mediaexec

import (
	"debug/macho"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// Resolve trusted decoder dylibs without running a shell or granting an entire
// Homebrew tree. The confinement profile receives individual canonical files.
func ResolveLibraries(ffmpeg, ffprobe string, extra []string) ([]string, error) {
	allowed := map[string]bool{}
	seen := map[string]bool{}
	var visit func(string, []string, string) error
	canonical := func(path string) (string, error) {
		if !filepath.IsAbs(path) {
			return "", errors.New("decoder dependency is not absolute")
		}
		path = filepath.Clean(path)
		real, e := filepath.EvalSymlinks(path)
		if e != nil {
			return "", e
		}
		info, e := os.Stat(real)
		if e != nil || !info.Mode().IsRegular() {
			return "", errors.New("decoder dependency unavailable")
		}
		allowed[path] = true
		allowed[real] = true
		if len(allowed) > 256 {
			return "", errors.New("too many decoder dependencies")
		}
		return real, nil
	}
	visit = func(path string, inherited []string, executable string) error {
		if strings.HasPrefix(path, "/usr/lib/") || strings.HasPrefix(path, "/System/") || strings.HasPrefix(path, "/Library/Apple/System/Library/") {
			return nil
		}
		real, e := canonical(path)
		if e != nil {
			return e
		}
		if seen[real] {
			return nil
		}
		seen[real] = true
		file, e := macho.Open(real)
		var fat *macho.FatFile
		if e != nil {
			fat, e = macho.OpenFat(real)
			if e != nil {
				return e
			}
			defer fat.Close()
			wanted := macho.CpuAmd64
			if runtime.GOARCH == "arm64" {
				wanted = macho.CpuArm64
			}
			for _, arch := range fat.Arches {
				if arch.Cpu == wanted {
					file = arch.File
					break
				}
			}
			if file == nil {
				return errors.New("decoder architecture unsupported")
			}
		} else {
			defer file.Close()
		}
		expand := func(value string) string {
			value = strings.ReplaceAll(value, "@loader_path", filepath.Dir(real))
			value = strings.ReplaceAll(value, "@executable_path", filepath.Dir(executable))
			return filepath.Clean(value)
		}
		paths := append([]string{}, inherited...)
		for _, load := range file.Loads {
			if rpath, ok := load.(*macho.Rpath); ok {
				value := expand(rpath.Path)
				if filepath.IsAbs(value) {
					paths = append(paths, value)
				}
			}
		}
		libraries, e := file.ImportedLibraries()
		if e != nil {
			return e
		}
		for _, lib := range libraries {
			target := expand(lib)
			if strings.HasPrefix(lib, "@rpath/") {
				target = ""
				for _, rp := range paths {
					candidate := filepath.Join(rp, strings.TrimPrefix(lib, "@rpath/"))
					if _, e = os.Stat(candidate); e == nil {
						target = candidate
						break
					}
				}
				if target == "" {
					return errors.New("decoder rpath dependency unavailable")
				}
			}
			if e = visit(target, paths, executable); e != nil {
				return e
			}
		}
		return nil
	}
	for _, path := range extra {
		if _, e := canonical(path); e != nil {
			return nil, e
		}
	}
	for _, path := range []string{ffmpeg, ffprobe} {
		if e := visit(path, nil, path); e != nil {
			return nil, e
		}
	}
	out := []string{}
	for path := range allowed {
		if path != ffmpeg && path != ffprobe {
			out = append(out, path)
		}
	}
	sort.Strings(out)
	return out, nil
}

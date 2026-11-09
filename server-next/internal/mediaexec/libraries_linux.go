//go:build linux

package mediaexec

import (
	"crypto/sha256"
	"debug/elf"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ResolveLibraries reads ELF metadata without executing ldd or a shell. Only
// exact owner-trusted executables/dependencies are mounted into the sandbox.
func ResolveLibraries(ffmpeg, ffprobe string, extra []string) ([]string, error) {
	key := strings.Join(append([]string{ffmpeg, ffprobe}, extra...), "\x00")
	if cached := cachedLibraryClosure(key); cached != nil {
		return cached, nil
	}
	watch := libraryWatch{}
	metadataBudget := &libraryMetadataBudget{}
	allowed := map[string]bool{}
	seen := map[string]bool{}
	defaults := []string{"/lib/x86_64-linux-gnu", "/usr/lib/x86_64-linux-gnu", "/lib/aarch64-linux-gnu", "/usr/lib/aarch64-linux-gnu", "/lib64", "/usr/lib64", "/lib", "/usr/lib", "/usr/local/lib"}
	canonical := func(path string) (string, error) {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return "", ErrInvalidJob
		}
		watch.record(path)
		real, e := filepath.EvalSymlinks(path)
		if e != nil {
			return "", e
		}
		info, e := os.Stat(real)
		if e != nil || !info.Mode().IsRegular() {
			return "", ErrInvalidJob
		}
		watch.record(real)
		allowed[path] = true
		allowed[real] = true
		if len(allowed) > maxLibraryFiles {
			return "", ErrInvalidJob
		}
		return real, nil
	}
	supplied := map[string]string{}
	for _, p := range extra {
		if _, e := canonical(p); e != nil {
			return nil, e
		}
		supplied[filepath.Base(p)] = p
	}
	var visit func(string, []string) error
	visit = func(path string, inherited []string) error {
		real, e := canonical(path)
		if e != nil {
			return e
		}
		if seen[real] {
			return nil
		}
		seen[real] = true
		input, e := openLibraryMetadataFile(real)
		if e != nil {
			return e
		}
		defer input.Close()
		info, e := input.Stat()
		if e != nil || !info.Mode().IsRegular() {
			return ErrInvalidJob
		}
		metadata := &libraryMetadataReader{file: input, budget: metadataBudget, sum: sha256.New()}
		f, e := elf.NewFile(metadata)
		if e != nil {
			return e
		}
		defer f.Close()
		for _, p := range f.Progs {
			if p.Type == elf.PT_INTERP {
				b, e := io.ReadAll(io.LimitReader(p.Open(), 4097))
				if e != nil || len(b) > 4096 {
					return ErrInvalidJob
				}
				loader := strings.TrimRight(string(b), "\x00")
				if e = visit(loader, nil); e != nil {
					return e
				}
			}
		}
		paths := append([]string{}, inherited...)
		for _, tag := range []elf.DynTag{elf.DT_RUNPATH, elf.DT_RPATH} {
			values, e := f.DynString(tag)
			if e != nil {
				return e
			}
			for _, v := range values {
				for _, d := range strings.Split(v, ":") {
					d = strings.ReplaceAll(strings.ReplaceAll(d, "${ORIGIN}", filepath.Dir(real)), "$ORIGIN", filepath.Dir(real))
					if filepath.IsAbs(d) {
						paths = append(paths, filepath.Clean(d))
					}
				}
			}
		}
		names, e := f.ImportedLibraries()
		if e != nil {
			return e
		}
		for _, name := range names {
			target := supplied[name]
			if strings.ContainsAny(name, "/\\") {
				return ErrInvalidJob
			}
			if target == "" {
				for _, base := range append(append([]string{}, paths...), defaults...) {
					// A newly installed higher-priority library must invalidate
					// a cached choice, including a previously absent directory.
					watch.record(base)
					p := filepath.Join(base, name)
					watch.record(p)
					if i, e := os.Stat(p); e == nil && i.Mode().IsRegular() {
						target = p
						break
					}
				}
			}
			if target == "" {
				return errors.New("decoder ELF dependency unavailable")
			}
			if e = visit(target, paths); e != nil {
				return e
			}
		}
		node := watch[real]
		node.metadata = metadata.snapshot()
		watch[real] = node
		return nil
	}
	for _, p := range []string{ffmpeg, ffprobe} {
		if e := visit(p, nil); e != nil {
			return nil, e
		}
	}
	out := []string{}
	for p := range allowed {
		if p != ffmpeg && p != ffprobe {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	if watch.unchanged() {
		rememberLibraryClosure(key, out, watch)
	}
	return out, nil
}

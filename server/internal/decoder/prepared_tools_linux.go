//go:build linux

package decoder

import (
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Resolve ELF dependencies without running ldd or a loader on the host. Only
// these literal, descriptor-bound files enter the sandbox, never /usr/lib or a
// caller-supplied directory. Explicit libraries cover optional dlopen modules.
const maxPreparedToolBytes = int64(512 << 20)

type preparedTool struct {
	path   string
	file   *os.File
	info   os.FileInfo
	digest string
}
type preparedTools struct {
	entrypoint string
	files      []*preparedTool
	seen       map[string]*preparedTool
	total      int64
}

func toolFileDigest(f *os.File, size int64) (string, error) {
	if size <= 0 || size > maxPreparedToolBytes {
		return "", ErrInvalidConfiguration
	}
	h := sha256.New()
	n, e := io.Copy(h, io.NewSectionReader(f, 0, size))
	if e != nil {
		return "", e
	}
	if n != size {
		return "", io.ErrUnexpectedEOF
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func (t *preparedTools) close() {
	for _, f := range t.files {
		_ = f.file.Close()
	}
}
func (t *preparedTools) validate() error {
	for _, f := range t.files {
		st, e := f.file.Stat()
		if e != nil {
			return e
		}
		if !os.SameFile(st, f.info) || st.Size() != f.info.Size() || st.Mode() != f.info.Mode() || !st.ModTime().Equal(f.info.ModTime()) {
			return ErrInvalidConfiguration
		}
		digest, e := toolFileDigest(f.file, st.Size())
		if e != nil {
			return e
		}
		if digest != f.digest {
			return ErrInvalidConfiguration
		}
	}
	return nil
}
func (t *preparedTools) digest() string {
	type entry struct{ Path, Digest string }
	entries := make([]entry, 0, len(t.files))
	for _, f := range t.files {
		entries = append(entries, entry{f.path, f.digest})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	raw, _ := json.Marshal(entries)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func (t *preparedTools) add(path string) (*preparedTool, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.ContainsRune(path, 0) {
		return nil, ErrInvalidConfiguration
	}
	// No tool may cover an input, procfs, devices, or the private sandbox helper.
	for _, reserved := range []string{"/input.media", "/proc", "/dev", "/.portico"} {
		if path == reserved || strings.HasPrefix(path, reserved+"/") {
			return nil, ErrInvalidConfiguration
		}
	}
	if p := t.seen[path]; p != nil {
		return p, nil
	}
	if len(t.files) >= 512 {
		return nil, ErrInvalidConfiguration
	}
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() || st.Size() <= 0 || st.Size() > maxPreparedToolBytes || st.Mode().Perm()&0022 != 0 || t.total+st.Size() > 2<<30 {
		f.Close()
		return nil, ErrInvalidConfiguration
	}
	digest, e := toolFileDigest(f, st.Size())
	if e != nil {
		f.Close()
		return nil, e
	}
	p := &preparedTool{path, f, st, digest}
	t.files = append(t.files, p)
	t.seen[path] = p
	t.total += st.Size()
	return p, nil
}
func elfSearchPaths(f *elf.File, origin string) ([]string, error) {
	paths := []string{}
	// RPATH is used only in the absence of RUNPATH, as by the ELF loader. The
	// dependency closure is conservative; unresolved nonstandard loaders fail.
	values, e := f.DynString(elf.DT_RUNPATH)
	if e != nil {
		return nil, e
	}
	if len(values) == 0 {
		values, e = f.DynString(elf.DT_RPATH)
		if e != nil {
			return nil, e
		}
	}
	for _, v := range values {
		for _, p := range strings.Split(v, ":") {
			p = strings.ReplaceAll(strings.ReplaceAll(p, "${ORIGIN}", origin), "$ORIGIN", origin)
			if !filepath.IsAbs(p) || strings.Contains(p, "$") {
				return nil, ErrInvalidConfiguration
			}
			paths = append(paths, filepath.Clean(p))
		}
	}
	triplet := ""
	switch f.Machine {
	case elf.EM_X86_64:
		triplet = "x86_64-linux-gnu"
	case elf.EM_386:
		triplet = "i386-linux-gnu"
	case elf.EM_AARCH64:
		triplet = "aarch64-linux-gnu"
	case elf.EM_ARM:
		triplet = "arm-linux-gnueabihf"
	case elf.EM_RISCV:
		triplet = "riscv64-linux-gnu"
	case elf.EM_PPC64:
		triplet = "powerpc64le-linux-gnu"
	case elf.EM_S390:
		triplet = "s390x-linux-gnu"
	}
	if triplet != "" {
		paths = append(paths, "/lib/"+triplet, "/usr/lib/"+triplet)
	}
	return append(paths, "/lib64", "/usr/lib64", "/lib", "/usr/lib"), nil
}
func pinPreparedTools(executable string, libraries ...string) (*preparedTools, error) {
	t := &preparedTools{seen: map[string]*preparedTool{}}
	ok := false
	defer func() {
		if !ok {
			t.close()
		}
	}()
	if len(libraries) > 256 {
		return nil, ErrInvalidConfiguration
	}
	explicit := map[string]string{}
	for _, p := range libraries {
		if !filepath.IsAbs(p) || filepath.Clean(p) != p {
			return nil, ErrInvalidConfiguration
		}
		name := filepath.Base(p)
		if old := explicit[name]; old != "" && old != p {
			return nil, ErrInvalidConfiguration
		}
		explicit[name] = p
	}
	visited := map[string]bool{}
	var machine elf.Machine
	var class elf.Class
	var walk func(string) error
	walk = func(path string) error {
		p, e := t.add(path)
		if e != nil {
			return e
		}
		resolved, e := filepath.EvalSymlinks(path)
		if e != nil {
			return e
		}
		if path == executable {
			t.entrypoint = resolved
		}
		canonical, e := t.add(resolved)
		if e != nil {
			return e
		}
		if !os.SameFile(p.info, canonical.info) || p.digest != canonical.digest {
			return ErrInvalidConfiguration
		}
		if visited[resolved] {
			return nil
		}
		visited[resolved] = true
		obj, e := elf.NewFile(p.file)
		if e != nil {
			return ErrInvalidConfiguration
		}
		if machine == elf.EM_NONE {
			machine = obj.Machine
			class = obj.Class
		}
		if obj.Machine != machine || obj.Class != class || (obj.Type != elf.ET_EXEC && obj.Type != elf.ET_DYN) {
			return ErrInvalidConfiguration
		}
		for _, prog := range obj.Progs {
			if prog.Type == elf.PT_INTERP {
				if prog.Filesz < 2 || prog.Filesz > 4096 {
					return ErrInvalidConfiguration
				}
				raw, e := io.ReadAll(io.LimitReader(prog.Open(), 4097))
				if e != nil {
					return e
				}
				if len(raw) < 2 || raw[len(raw)-1] != 0 {
					return ErrInvalidConfiguration
				}
				if e = walk(string(raw[:len(raw)-1])); e != nil {
					return e
				}
			}
		}
		dirs, e := elfSearchPaths(obj, filepath.Dir(resolved))
		if e != nil {
			return e
		}
		needed, e := obj.DynString(elf.DT_NEEDED)
		if e != nil {
			return e
		}
		for _, name := range needed {
			if name == "" || strings.ContainsAny(name, "/\x00") {
				return ErrInvalidConfiguration
			}
			candidates := []string{}
			if path := explicit[name]; path != "" {
				candidates = append(candidates, path)
			}
			for _, dir := range dirs {
				candidates = append(candidates, filepath.Join(dir, name))
			}
			found := ""
			for _, path := range candidates {
				st, err := os.Stat(path)
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				if err != nil {
					return err
				}
				if !st.Mode().IsRegular() {
					return ErrInvalidConfiguration
				}
				found = path
				break
			}
			if found == "" {
				return ErrConfinementUnavailable
			}
			// The loader needs the file at its search path, even for an explicitly
			// supplied dlopen/SONAME dependency. Mount an alias at the first default
			// lookup path when that name is supplied outside standard directories.
			if e = walk(found); e != nil {
				return e
			}
			if explicit[name] != "" && len(dirs) > 0 && found == explicit[name] {
				alias := filepath.Join(dirs[0], name)
				if other := t.seen[alias]; other != nil && (!os.SameFile(other.info, t.seen[found].info) || other.digest != t.seen[found].digest) {
					return ErrInvalidConfiguration
				}
				if t.seen[alias] == nil {
					// Open the same inode, not a potentially unrelated file at alias.
					original := t.seen[found]
					f, e := os.Open(found)
					if e != nil {
						return e
					}
					st, e := f.Stat()
					if e != nil || !os.SameFile(st, original.info) {
						f.Close()
						return ErrInvalidConfiguration
					}
					if len(t.files) >= 512 {
						f.Close()
						return ErrInvalidConfiguration
					}
					copy := &preparedTool{alias, f, st, original.digest}
					t.files = append(t.files, copy)
					t.seen[alias] = copy
				}
			}
		}
		return nil
	}
	if e := walk(executable); e != nil {
		return nil, e
	}
	if t.seen[executable].info.Mode().Perm()&0111 == 0 {
		return nil, ErrInvalidConfiguration
	}
	for _, p := range libraries {
		if e := walk(p); e != nil {
			return nil, e
		}
	}
	ok = true
	return t, nil
}

// PreparedToolsDigest includes automatic ELF dependencies, not just configured
// extra files. The run path also validates the exact descriptors after retirement.
func PreparedToolsDigest(executable string, libraries ...string) (string, error) {
	t, e := pinPreparedTools(executable, libraries...)
	if e != nil {
		return "", e
	}
	defer t.close()
	return t.digest(), nil
}

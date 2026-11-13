package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"runtime"

	"github.com/PorticoMediaServer/portico/server-next/internal/storagepaths"
)

// conversionFolder prefers the platform's separate cache, and proves it can write there.
// A missing mount or an unwritable cache falls back inside the data folder, never the
// container layer. The actual chosen path is given in the existing Storage answer.
func conversionFolder(data string, log *slog.Logger) string {
	fallback := filepath.Join(data, "transcodes")
	home, _ := os.UserHomeDir()
	_, docker := os.Stat("/.dockerenv")
	preferred := separateConversions(runtime.GOOS, os.Getenv, home, docker == nil)
	return conversionOutput(preferred, fallback, log)
}

func conversionOutput(preferred, fallback string, log *slog.Logger) string {
	if mounts, err := storagepaths.CurrentMounts(); err == nil {
		if disk, _, err := storagepaths.TranscodeDirectories(preferred, fallback, mounts); err == nil {
			preferred = disk
		} else {
			preferred = fallback
		}
	} else {
		preferred = fallback
	}
	if err := os.MkdirAll(preferred, 0700); err == nil {
		if f, err := os.CreateTemp(preferred, ".portico-write-"); err == nil {
			_, writeErr := f.Write([]byte{0})
			closeErr := f.Close()
			_ = os.Remove(f.Name())
			if writeErr == nil && closeErr == nil {
				return preferred
			}
		}
	}
	log.Warn("the separate conversion folder cannot be written; conversions use the data folder", "folder", fallback)
	return fallback
}

func separateConversions(goos string, env func(string) string, home string, docker bool) string {
	if docker || env("PORTICO_CONTAINER") == "1" {
		return "/transcode/portico-conversions"
	}
	if goos == "windows" {
		base := env("LOCALAPPDATA")
		if env("PORTICO_SERVICE") == "1" {
			base = env("ProgramData")
			if base == "" {
				base = `C:\ProgramData`
			}
		}
		if base == "" {
			return ""
		}
		return base + `\Portico Transcode`
	}
	if goos == "darwin" {
		return filepath.Join(home, "Library", "Caches", "Portico Transcode")
	}
	if env("PORTICO_SERVICE") == "1" {
		return "/var/cache/portico"
	}
	if cache := env("XDG_CACHE_HOME"); filepath.IsAbs(cache) {
		return filepath.Join(cache, "portico")
	}
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".cache", "portico")
}

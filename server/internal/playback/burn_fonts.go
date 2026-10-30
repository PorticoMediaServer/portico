package playback

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/childprocess"
	"github.com/PorticoMediaServer/portico/server/internal/mediaexec"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
	"time"
)

// extractBurnFonts gives libass only bounded, server-named attachment files.
// Container attachment names are never used as filesystem paths.
func (h *HLS) extractBurnFonts(ctx context.Context, input *assets.PlaybackInput, remote subtitles.WindowInput, dir string) (string, error) {
	fonts := filepath.Join(dir, "burn-fonts")
	if ready, err := os.ReadFile(filepath.Join(fonts, "ready")); err == nil && string(ready) == "1" {
		return fonts, nil
	}
	_ = os.RemoveAll(fonts)
	if err := os.MkdirAll(fonts, 0700); err != nil {
		return "", err
	}
	build := func(source, protocols string) []string {
		args := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-y", "-max_alloc", "8388608", "-protocol_whitelist", protocols}
		for i := 0; i < 32; i++ {
			args = append(args, fmt.Sprintf("-dump_attachment:t:%d", i), filepath.Join(fonts, fmt.Sprintf("font-%02d.ttf", i)))
		}
		args = append(args, input.Options...)
		return append(args, "-i", source, "-map", "0:v:0?", "-frames:v", "0", "-an", "-sn", "-dn", "-f", "null", "-")
	}
	bounded, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var runErr error
	if remote != nil {
		runErr = remote.Run(bounded, dir, func(url string) (subtitles.WindowCommand, error) {
			return subtitles.WindowCommand{Args: build(url, "http,tcp")}, nil
		})
	} else {
		if input.File != nil {
			_, _ = input.File.Seek(0, io.SeekStart)
		}
		cmd, err := mediaexec.CommandContext(bounded, mediaexec.Job{Executable: h.binary, Args: build(input.Argument, "file,pipe"), Files: input.ExtraFiles(), InputFD: input.MountFD, InputMount: input.MountPath, ReadPaths: input.ReadPaths(), WriteDirs: []string{dir}, MaxFileBytes: 8 << 20})
		if err != nil {
			return "", err
		}
		cmd.WaitDelay = time.Second
		runErr = childprocess.Run(bounded, cmd)
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	entries, err := os.ReadDir(fonts)
	if err != nil {
		return "", err
	}
	var total int64
	for _, entry := range entries {
		path := filepath.Join(fonts, entry.Name())
		info, err := entry.Info()
		valid := err == nil && info.Mode().IsRegular() && info.Size() >= 4 && info.Size() <= 8<<20 && total+info.Size() <= 64<<20
		if valid {
			file, err := os.Open(path)
			if err != nil {
				valid = false
			} else {
				var header [4]byte
				_, err = io.ReadFull(file, header[:])
				file.Close()
				valid = err == nil && (string(header[:]) == "\x00\x01\x00\x00" || string(header[:]) == "OTTO" || string(header[:]) == "ttcf")
			}
		}
		if !valid {
			_ = os.Remove(path)
			continue
		}
		total += info.Size()
	}
	// A source may have no attachments, or contain an unusable attachment among
	// good ones. Libass can still use the valid fonts and the confined fallback.
	_ = runErr
	pack, err := mediaexec.Fonts()
	if err != nil {
		return "", err
	}
	for _, name := range []string{"DejaVuSans.ttf", "NotoSansCJKjp-Regular.otf", "NotoSansThai.ttf", "NotoSansDevanagari.ttf"} {
		source, target := filepath.Join(pack.Dir, name), filepath.Join(fonts, "zz-fallback-"+name)
		if err := os.Link(source, target); err == nil {
			continue
		}
		data, err := os.ReadFile(source)
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(target, data, 0600); err != nil {
			return "", err
		}
	}
	if err := os.WriteFile(filepath.Join(fonts, "ready"), []byte("1"), 0600); err != nil {
		return "", err
	}
	return fonts, nil
}

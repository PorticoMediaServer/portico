//go:build windows

package storage

import (
	"context"
	"errors"
	"io"
	"os"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/mediaexec"
)

func probeLyricTags(source *os.File, argv []string) ([]map[string]string, error) {
	if len(argv) < 2 || argv[len(argv)-1] != descriptorInput {
		return nil, errors.New("invalid lyric probe")
	}
	if err := ensureWindowsProbeJob(); err != nil {
		return nil, err
	}
	locked, path, closeInput, err := lockWindowsMediaInput(source)
	if err != nil {
		return nil, err
	}
	defer closeInput()
	expected, token, err := windowsFileBinding(locked)
	if err != nil {
		return nil, err
	}
	args, err := replaceScanInput(argv, descriptorInput, path)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cmd, err := mediaexec.CommandArgv(ctx, args)
	if err != nil {
		return nil, err
	}
	cmd.WaitDelay = time.Second
	cmd.Stderr = io.Discard
	buffer := &lyricProbeBuffer{}
	cmd.Stdout = buffer
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	if err := checkWindowsMediaInput(locked, path, expected, token); err != nil {
		return nil, err
	}
	return parseLyricProbe(buffer.data)
}

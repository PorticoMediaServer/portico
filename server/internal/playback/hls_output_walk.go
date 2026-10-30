package playback

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// walkOutput streams small directory batches rather than letting WalkDir
// allocate all filenames of a runaway conversion before applying its bound.
// The pinned root and no-follow opens also keep spill reads inside owned output.
func walkOutput(ctx context.Context, root *os.Root, visit func(string, os.DirEntry) error) error {
	entries := 0
	var walk func(string) error
	walk = func(dir string) error {
		file, err := root.OpenFile(dir, hlsReadFlags, 0)
		if err != nil {
			return err
		}
		defer file.Close()
		for {
			batch, readErr := file.ReadDir(128)
			for _, entry := range batch {
				if err := ctx.Err(); err != nil {
					return err
				}
				rel := filepath.Join(dir, entry.Name())
				entries++
				if entries > HLSMaxSegments*17 || strings.Count(rel, string(filepath.Separator)) > 8 {
					return errors.New("conversion output exceeds traversal bound")
				}
				if err := visit(rel, entry); err != nil {
					return err
				}
				if entry.IsDir() {
					if err := walk(rel); err != nil {
						return err
					}
				}
			}
			if readErr == io.EOF {
				return nil
			}
			if readErr != nil {
				return readErr
			}
		}
	}
	return walk(".")
}

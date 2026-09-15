//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package storage

import (
	"fmt"
	"os"
)

func syncDirectory(root *os.Root, directory string) error {
	file, err := root.Open(directory)
	if err != nil {
		return fmt.Errorf("open directory for sync: %w", err)
	}
	defer func() { _ = file.Close() }()
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync directory: %w", err)
	}
	return nil
}

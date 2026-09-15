//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris

package storage

import "os"

func syncDirectory(_ *os.Root, _ string) error {
	return nil
}

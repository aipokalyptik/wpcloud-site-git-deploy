//go:build linux

package publicfs

import (
	"errors"

	"golang.org/x/sys/unix"
)

var ErrExchangeUnsupported = errors.New("atomic exchange is unsupported")

func Exchange(pathA, pathB string) error {
	if err := unix.Renameat2(unix.AT_FDCWD, pathA, unix.AT_FDCWD, pathB, unix.RENAME_EXCHANGE); err != nil {
		if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EINVAL) || errors.Is(err, unix.ENOTSUP) {
			return ErrExchangeUnsupported
		}
		return err
	}
	return nil
}

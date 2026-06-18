//go:build !linux

package publicfs

import (
	"errors"
	"os"
)

var ErrExchangeUnsupported = errors.New("atomic exchange is unsupported")

func Exchange(pathA, pathB string) error {
	// Non-Linux builds are for local development tests only. Production builds
	// target Linux and use renameat2(RENAME_EXCHANGE) in exchange_linux.go.
	tmp := pathB + ".exchange-nonlinux-tmp"
	os.RemoveAll(tmp)
	if err := os.Rename(pathB, tmp); err != nil {
		return err
	}
	if err := os.Rename(pathA, pathB); err != nil {
		os.Rename(tmp, pathB)
		return err
	}
	return os.Rename(tmp, pathA)
}

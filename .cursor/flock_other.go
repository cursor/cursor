//go:build !windows && (!unix || aix || (solaris && !illumos))

package flock

import (
	"errors"
	"os"
)

func lockFile(_ *os.File) error {
	return errors.ErrUnsupported
}

func unlockFile(_ *os.File) (err error) {
	return errors.ErrUnsupported
}

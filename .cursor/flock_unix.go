//go:build darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd

package flock

import (
	"os"
	"syscall"
)

func lockFile(f *os.File) (err error) {
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
		if err != syscall.EINTR {
			break
		}
	}
	return err
}

func unlockFile(f *os.File) (err error) {
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		if err != syscall.EINTR {
			break
		}
	}
	return err
}

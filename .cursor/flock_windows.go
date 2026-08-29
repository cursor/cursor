//go:build windows

package flock

import (
	"os"
	"syscall"
	"unsafe"
)

var (
	modKernel32      = syscall.NewLazyDLL("kernel32.dll")
	procLockFileEx   = modKernel32.NewProc("LockFileEx")
	procUnlockFileEx = modKernel32.NewProc("UnlockFileEx")
)

const LOCKFILE_EXCLUSIVE_LOCK = 0x00000002

func lockFile(f *os.File) error {
	rc, err := f.SyscallConn()
	if err != nil {
		return err
	}
	var callErr error
	if err := rc.Control(func(fd uintptr) {
		var ol syscall.Overlapped
		r1, _, e1 := procLockFileEx.Call(
			fd,
			uintptr(LOCKFILE_EXCLUSIVE_LOCK),
			0,
			1,
			0,
			uintptr(unsafe.Pointer(&ol)),
		)
		if r1 == 0 {
			callErr = e1
		}
	}); err != nil {
		return err
	}
	return callErr
}

func unlockFile(f *os.File) error {
	rc, err := f.SyscallConn()
	if err != nil {
		return err
	}
	var callErr error
	if err := rc.Control(func(fd uintptr) {
		var ol syscall.Overlapped
		r1, _, e1 := procUnlockFileEx.Call(
			fd,
			0,
			1,
			0,
			uintptr(unsafe.Pointer(&ol)),
		)
		if r1 == 0 {
			callErr = e1
		}
	}); err != nil {
		return err
	}
	return callErr
}

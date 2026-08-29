package flock

import "os"

// Acquire opens (or creates) the lock file at path and blocks until the lock is acquired.
// It returns a release function to unlock and close the file.
func Acquire(path string) (func() error, error) {
	f, err := os.OpenFile(path, os.O_CREATE, 0644)
	if err != nil {
		return nil, err
	}
	if err := lockFile(f); err != nil {
		_ = f.Close()
		return nil, err
	}
	released := false
	release := func() error {
		if released {
			return nil
		}
		released = true
		err := unlockFile(f)
		if err1 := f.Close(); err == nil {
			err = err1
		}
		return err
	}
	return release, nil
}

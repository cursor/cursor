package flock

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAcquireReleaseCreatesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lockfile")

	release, err := Acquire(path)
	if errors.Is(err, errors.ErrUnsupported) {
		t.Skip("file locking unsupported on this platform")
	}
	if err != nil {
		t.Fatalf("Acquire failed: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		release()
		t.Fatalf("lock file not created: %v", err)
	}

	if err := release(); err != nil {
		t.Fatalf("Release failed: %v", err)
	}
	if err := release(); err != nil {
		t.Fatalf("Release should be idempotent: %v", err)
	}
}

func TestLockBlocksUntilRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lockfile")

	first, err := Acquire(path)
	if errors.Is(err, errors.ErrUnsupported) {
		t.Skip("file locking unsupported on this platform")
	}
	if err != nil {
		t.Fatalf("Acquire failed: %v", err)
	}
	defer first()

	result := make(chan error, 1)
	var second func() error
	go func() {
		lock, err := Acquire(path)
		if err == nil {
			second = lock
		}
		result <- err
	}()

	blockCtx, cancelBlock := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancelBlock()
	select {
	case err := <-result:
		if err == nil && second != nil {
			_ = second()
		}
		t.Fatalf("second Acquire should block, returned early: %v", err)
	case <-blockCtx.Done():
	}

	if err := first(); err != nil {
		t.Fatalf("Release failed: %v", err)
	}

	unlockCtx, cancelUnlock := context.WithTimeout(t.Context(), 1*time.Second)
	defer cancelUnlock()
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("second Acquire failed: %v", err)
		}
		if second == nil {
			t.Fatalf("second lock was not set")
		}
		if err := second(); err != nil {
			t.Fatalf("second Release failed: %v", err)
		}
	case <-unlockCtx.Done():
		t.Fatalf("second Acquire did not unblock")
	}
}

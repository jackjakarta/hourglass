// Package lockfile takes advisory flock(2) locks on files. The kernel drops
// a lock when its holder exits, so a crashed process never leaves one
// behind.
package lockfile

import (
	"errors"
	"os"
	"syscall"
	"time"
)

// Lock is a held lock.
type Lock struct{ f *os.File }

// ErrHeld means another process holds the lock.
var ErrHeld = errors.New("lock is held by another process")

// TryLock takes the exclusive lock on path, creating the file if needed.
// It returns ErrHeld without waiting when someone else has it.
func TryLock(path string) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if !errors.Is(err, syscall.EINTR) {
			break
		}
	}
	if err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrHeld
		}
		return nil, err
	}
	return &Lock{f: f}, nil
}

// Acquire takes the lock on path, retrying until it's free or wait passes.
func Acquire(path string, wait time.Duration) (*Lock, error) {
	deadline := time.Now().Add(wait)
	delay := time.Millisecond
	for {
		l, err := TryLock(path)
		if !errors.Is(err, ErrHeld) || time.Now().After(deadline) {
			return l, err
		}
		time.Sleep(delay)
		delay = min(delay*2, 50*time.Millisecond)
	}
}

// Release drops the lock. It is safe on a nil Lock.
func (l *Lock) Release() error {
	if l == nil {
		return nil
	}
	return l.f.Close() // closing the descriptor releases the flock
}

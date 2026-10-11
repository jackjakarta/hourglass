package lockfile

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestTryLock(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.lock")
	a, err := TryLock(p)
	if err != nil {
		t.Fatal(err)
	}
	// flock locks belong to the open file description, so a second open in
	// the same process contends like another process would.
	if _, err := TryLock(p); !errors.Is(err, ErrHeld) {
		t.Fatalf("second TryLock = %v, want ErrHeld", err)
	}
	if _, err := Acquire(p, 20*time.Millisecond); !errors.Is(err, ErrHeld) {
		t.Fatalf("Acquire while held = %v, want ErrHeld", err)
	}
	go func() {
		time.Sleep(20 * time.Millisecond)
		a.Release()
	}()
	b, err := Acquire(p, 2*time.Second)
	if err != nil {
		t.Fatalf("Acquire after release = %v", err)
	}
	b.Release()
	var none *Lock
	if err := none.Release(); err != nil {
		t.Error(err)
	}
}

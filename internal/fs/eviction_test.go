package fs

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestEvictionController_StartStop(t *testing.T) {
	var count atomic.Int64
	ctrl := NewEvictionController(10*time.Millisecond, func() {
		count.Add(1)
	})

	ctrl.Start()
	time.Sleep(25 * time.Millisecond)
	ctrl.Stop()

	// Should have executed at least 2 times in 25ms with 10ms interval
	if count.Load() < 2 {
		t.Errorf("Expected at least 2 executions, got %d", count.Load())
	}
}

func TestEvictionController_StopIdempotent(t *testing.T) {
	ctrl := NewEvictionController(time.Second, func() {})
	ctrl.Start()
	ctrl.Stop()
	ctrl.Stop() // Should not panic
}

func TestEvictionController_StartIdempotent(t *testing.T) {
	ctrl := NewEvictionController(time.Second, func() {})
	ctrl.Start()
	ctrl.Start() // Should not panic or create multiple goroutines
	ctrl.Stop()
}

func TestEvictionController_RunOnce_Dedup(t *testing.T) {
	ctrl := NewEvictionController(0, nil)
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int64

	// First call blocks until released.
	go ctrl.RunOnce(func() {
		calls.Add(1)
		close(started)
		<-release
	})
	<-started // ensure first RunOnce is in flight

	// Second call while first is in flight must be deduped (returns false).
	if launched := ctrl.RunOnce(func() { calls.Add(1) }); launched {
		t.Error("second RunOnce while one is in flight should be deduped")
	}
	close(release)

	// Wait for the first to finish, then a new RunOnce should be accepted.
	time.Sleep(20 * time.Millisecond)
	if !ctrl.RunOnce(func() { calls.Add(1) }) {
		t.Error("RunOnce after previous finished should be accepted")
	}
	// allow the bg goroutine to run
	deadline := time.Now().Add(time.Second)
	for calls.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if calls.Load() != 2 {
		t.Errorf("expected exactly 2 executions, got %d", calls.Load())
	}
}

func TestEvictionController_RunSerialized_LockExclusion(t *testing.T) {
	ctrl := NewEvictionController(0, nil)
	inside := make(chan struct{})
	release := make(chan struct{})

	go func() {
		ctrl.RunSerialized(func() {
			close(inside)
			<-release
		})
	}()
	<-inside // first RunSerialized holds the lock

	var ran bool
	done := make(chan struct{})
	go func() {
		ctrl.RunSerialized(func() { ran = true })
		close(done)
	}()

	// The second RunSerialized must block until the first releases.
	select {
	case <-done:
		t.Fatal("second RunSerialized should block while the lock is held")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	<-done
	if !ran {
		t.Fatal("second RunSerialized did not run after lock release")
	}
}

func TestEvictionController_LockUnlock(t *testing.T) {
	ctrl := NewEvictionController(0, nil)
	ctrl.Lock()
	ctrl.Unlock() // must not deadlock
}

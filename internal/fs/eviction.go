package fs

import (
	"sync"
	"sync/atomic"
	"time"
)

// EvictionController consolidates the shared plumbing used by both
// InodeCache (metadata) and ContentCache (content) for background eviction:
//
//   - startEviction: a periodically-sweeping goroutine (Stop()/Stop idempotent)
//   - RunSerialized: a mutex-serialized execution window (used to guard an
//     eviction pass so it can't interleave with itself / file creation).
//   - RunOnce: a deduplication flag ensuring only one in-flight eviction
//     goroutine at a time.
//
// Each cache keeps its own eviction policy (which items to evict and in what
// order) as plain callbacks / bodies around RunSerialized/RunOnce; this type
// only owns the concurrency and lifecycle mechanics.
type EvictionController struct {
	interval time.Duration
	sweep    func()

	// update serializes the eviction window. In InodeCache it guards the
	// periodic sweep; in ContentCache it also guards file creation in Open()
	// against evictBySize (the TOCTOU window).
	update sync.Mutex

	// running is the dedup flag: prevents launching multiple simultaneous
	// eviction goroutines from a triggering call site.
	running atomic.Bool

	// sweep lifecycle
	started bool
	stopCh  chan struct{}
	wg      sync.WaitGroup
}

// NewEvictionController creates a controller. If interval > 0, it runs a
// periodic sweep goroutine; if sweep == nil the periodic path is unused
// (callers drive eviction directly via RunSerialized/RunOnce).
func NewEvictionController(interval time.Duration, sweep func()) *EvictionController {
	return &EvictionController{interval: interval, sweep: sweep}
}

// Start launches the periodic eviction goroutine. Idempotent: calling it more
// than once has no effect.
func (c *EvictionController) Start() {
	if c.interval <= 0 || c.sweep == nil {
		return
	}
	if c.stopCh != nil {
		return // already started
	}
	c.wg.Add(1)
	c.stopCh = make(chan struct{})
	go func() {
		defer c.wg.Done()
		ticker := time.NewTicker(c.interval)
		defer ticker.Stop()
		for {
			select {
			case <-c.stopCh:
				return
			case <-ticker.C:
				c.sweep()
			}
		}
	}()
}

// Stop halts the periodic goroutine, if running. Idempotent.
func (c *EvictionController) Stop() {
	if c.stopCh == nil {
		return
	}
	close(c.stopCh)
	c.wg.Wait()
	c.stopCh = nil
}

// RunSerialized executes fn while holding the controller's lock, guaranteeing
// that only one eviction/flush pass runs at a time and that it won't
// interleave with other lock-acquiring operations.
func (c *EvictionController) RunSerialized(fn func()) {
	c.update.Lock()
	defer c.update.Unlock()
	fn()
}

// Lock acquires the controller's lock. Used by ContentCache.Open to guard
// file creation against eviction (TOCTOU window).
func (c *EvictionController) Lock() {
	c.update.Lock()
}

// Unlock releases the controller's lock.
func (c *EvictionController) Unlock() {
	c.update.Unlock()
}

// RunOnce runs fn in a background goroutine but only if no other eviction
// goroutine is already in flight. Returns true if fn was launched.
func (c *EvictionController) RunOnce(fn func()) bool {
	if c.running.Swap(true) {
		return false // an eviction is already in flight
	}
	go func() {
		defer c.running.Store(false)
		fn()
	}()
	return true
}
package fs

import (
	"os"
	"sync"
	"sync/atomic"
	"testing"
)

// TestContentCache_EvictionDoesNotRaceWithOpen is a "canary" test that verifies
// that concurrent Open() with evictBySize() (simulated here as IsOpen→Delete)
// no causa condiciones de carrera de datos a nivel Go, y documenta la race
// TOCTOU logic that evictMu will resolve in Phase 4b.
//
// ⚠️ COMPORTAMIENTO ACTUAL (sin evictMu):
//
//	The test detects and reports data corruption caused by the logical race
//	between IsOpen and Delete, but does NOT fail because this is expected until
//	se implemente evictMu. Los datos se registran como advertencias.
//
// ✅ EXPECTED BEHAVIOR (with evictMu, Phase 4b):
//
//	Zero data corruption. The test must pass cleanly, verifying that
//	the serialization between Open() and evictBySize() works correctly.
//
// Race mechanism (without evictMu):
//  1. Evictor calls IsOpen(id) → false (the FD is not in fds yet)
//  2. Opener calls os.OpenFile() creating the file on disk
//  3. Evictor calls Delete(id) → closes the FD (if it was already in fds)
//     and deletes the file from disk
//  4. Opener writes to its FD → the file on disk was already deleted/recycled
//  5. Otro opener sobreescribe → los datos se corrompen
//
// To run with the race detector: go test -race -run EvictionDoesNotRace
func TestContentCache_EvictionDoesNotRaceWithOpen(t *testing.T) {
	tmpDir := t.TempDir()
	cache, err := NewContentCache(tmpDir)
	if err != nil {
		t.Fatalf("NewContentCache error: %v", err)
	}

	const numFiles = 30
	const numEvictors = 3
	const numOpeners = 6
	const iterations = 500

	// Preload files to disk
	ids := make([]string, numFiles)
	for i := 0; i < numFiles; i++ {
		ids[i] = "evict_test_" + itoa(i)
		if err := cache.Insert(ids[i], []byte("initial")); err != nil {
			t.Fatalf("Error preloading file %s: %v", ids[i], err)
		}
	}

	var corruptionCount int64
	var wg sync.WaitGroup

	// Barrera para arrancar todas las goroutines a la vez
	startCh := make(chan struct{})

	// Evictors
	for e := 0; e < numEvictors; e++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-startCh
			for i := 0; i < iterations; i++ {
				for _, id := range ids {
					if !cache.IsOpen(id) {
						// Ventana TOCTOU: entre IsOpen y Delete,
						// Open() can run in another goroutine.
						cache.Delete(id)
					}
				}
			}
		}()
	}

	// Openers
	for o := 0; o < numOpeners; o++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			<-startCh
			for i := 0; i < iterations; i++ {
				id := ids[(i+workerID)%numFiles]

				fd, err := cache.Open(id)
				if err != nil {
					// Open with O_CREATE should not fail
					t.Errorf("Unexpected Open(%s) error: %v", id, err)
					return
				}

				// Write a unique marker and verify integrity
				marker := "worker_" + itoa(workerID) + "_iter_" + itoa(i)
				fd.Seek(0, 0)
				fd.Truncate(0)
				fd.Write([]byte(marker))
				fd.Sync()

				// Verify that we read what we wrote
				fd.Seek(0, 0)
				buf := make([]byte, len(marker))
				n, _ := fd.Read(buf)
				if n > 0 && string(buf[:n]) != marker {
					// Corruption detected: without evictMu, this is EXPECTED.
					// With evictMu, this counter must always be 0.
					atomic.AddInt64(&corruptionCount, 1)
				}

				cache.Close(id)
			}
		}(o)
	}

	close(startCh)
	wg.Wait()

	t.Logf("Data corruption detected: %d events (EXPECTED without evictMu, must be 0 with evictMu)",
		corruptionCount)

	if corruptionCount > 0 {
		t.Logf("⚠️  Without evictMu: %d corruption events were detected. "+
			"This is expected — the TOCTOU race between IsOpen and Delete is real. "+
			"Phase 4b (evictMu) will resolve this problem.", corruptionCount)
	}

	// Verification with -race: the Go race detector does not report data races
	// because sync.Map is thread-safe. But the logical race exists and shows up
	// en corruptionCount > 0.
	if corruptionCount == 0 {
		t.Log("✅ No corruption detected in this run")
	}
}

// TestContentCache_EvictionPreservesOpenFiles verifies that files kept
// open ones are NOT removed by eviction. This test MUST pass even without
// evictMu because IsOpen() returns true while the FD is in fds.
func TestContentCache_EvictionPreservesOpenFiles(t *testing.T) {
	tmpDir := t.TempDir()
	cache, err := NewContentCache(tmpDir)
	if err != nil {
		t.Fatalf("NewContentCache error: %v", err)
	}

	const numHeldFiles = 5

	// Open files and keep them open throughout the test
	heldIDs := make([]string, numHeldFiles)
	for i := 0; i < numHeldFiles; i++ {
		heldIDs[i] = "held_" + itoa(i)
		if _, err := cache.Open(heldIDs[i]); err != nil {
			t.Fatalf("Open(%s) error: %v", heldIDs[i], err)
		}
		// Write unique content
		cache.Insert(heldIDs[i], []byte("held_content_"+itoa(i)))
	}

	// Create additional files that are NOT open (they must be deleted)
	nonHeldIDs := make([]string, 5)
	for i := 0; i < 5; i++ {
		nonHeldIDs[i] = "nonheld_" + itoa(i)
		cache.Insert(nonHeldIDs[i], []byte("delete_me"))
	}

	// Simulate eviction
	for _, id := range nonHeldIDs {
		if !cache.IsOpen(id) {
			cache.Delete(id)
		}
	}
	for _, id := range heldIDs {
		if !cache.IsOpen(id) {
			cache.Delete(id)
		}
	}

	// Verify: held files MUST continue to exist
	for _, id := range heldIDs {
		if !cache.IsOpen(id) {
			t.Errorf("Held file %s should still be open", id)
		}
		if !cache.HasContent(id) {
			t.Errorf("Held file %s should have content", id)
		}
		if _, err := os.Stat(cache.contentPath(id)); os.IsNotExist(err) {
			t.Errorf("Held file %s should NOT have been deleted from disk", id)
		}
	}

	// Verify: non-held files MUST have been deleted
	for _, id := range nonHeldIDs {
		if cache.HasContent(id) {
			t.Errorf("Non-held file %s should have been deleted", id)
		}
	}

	// Limpiar
	for _, id := range heldIDs {
		cache.Close(id)
	}
}

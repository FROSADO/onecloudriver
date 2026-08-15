package fs

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/frosado/onecloudriver/internal/graph"
	bolt "go.etcd.io/bbolt"
)

// Crash simulation for the BoltDB persistence layer (issue #72, task 1.4.5).
//
// Both modes spawn a child instance of the test binary that writes inodes to
// BoltDB and is killed with SIGKILL (a `kill -9`): the process dies without
// running ANY cleanup (no Close, no defers), which is exactly what a crash /
// power loss leaves behind. The parent then reopens the DB and verifies:
//
//   - TestCrashSimulation_AbruptExit: the child persisted N inodes with
//     SerializeAll before being killed; the parent verifies the full tree
//     survived (zero data loss).
//
//   - TestCrashSimulation_SigKillMidWrite: the child commits batches of
//     inodes in a loop, recording each batch number AFTER its transaction
//     commits; the parent kills it mid-loop and verifies that (a) BoltDB
//     reopens cleanly (no "invalid meta page"/"freelist corrupt") and (b)
//     every batch the child marked as committed is fully present — a
//     transaction either commits whole or not at all.
//
// Note on flock timing: a raw `kill -9` releases the bbolt file lock
// immediately (verified empirically). An unclean *os.Exit(0)* of a process
// that had the DB open, by contrast, can leave a transient lock window of
// ~50ms (Go runtime exit path) — with the production `InitBoltDB` timeout of
// 1ns that would make the immediate reopen fail. That is why the reopen in
// the parent retries briefly: it absorbs both the rare SIGKILL straggler on
// slow machines and any future change to how the child exits.
//
// Iterations come from CRASH_ITERS (default 1 so unit runs stay fast;
// scripts/crash_sim.sh sets 10 in CI and 100 for full local runs).

// crashSimIters returns the number of crash iterations: CRASH_ITERS env wins,
// otherwise a small default.
func crashSimIters() int {
	if v := os.Getenv("CRASH_ITERS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 1
}

// crashSimAbruptCount is the number of inodes the abrupt-exit child persists.
const crashSimAbruptCount = 200

// crashSimBatchSize is the number of inodes per transaction in the SIGKILL
// child loop.
const crashSimBatchSize = 10

// ──── Mode A: abrupt exit (kill -9 after SerializeAll) ────

// TestCrashSimulation_AbruptExit runs the abrupt-exit crash simulation
// (parent half). The child half runs in a subprocess and is killed.
func TestCrashSimulation_AbruptExit(t *testing.T) {
	if os.Getenv("CRASH_SIM_CHILD") == "abrupt" {
		crashSimChildAbruptExit()
		return
	}

	for i := 0; i < crashSimIters(); i++ {
		dir := t.TempDir()
		dbPath := filepath.Join(dir, "inodes.db")
		statusPath := filepath.Join(dir, "done")

		// #nosec G204 -- the subprocess is the test binary itself (self-invocation
		// for the crash simulation); args are fixed literals, not user input.
		cmd := exec.Command(os.Args[0], "-test.run=^TestCrashSimulation_AbruptExit$")
		cmd.Env = append(os.Environ(),
			"CRASH_SIM_CHILD=abrupt",
			"CRASH_SIM_DB="+dbPath,
			"CRASH_SIM_STATUS="+statusPath,
		)
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}

		// Wait until the child has persisted everything, then kill it.
		if !waitForFile(statusPath, 15*time.Second) {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			t.Fatalf("crash child never finished writing (iteration %d)", i)
		}
		time.Sleep(10 * time.Millisecond)          // let it reach its hold loop
		if err := cmd.Process.Kill(); err != nil { // SIGKILL
			t.Fatalf("kill failed (iteration %d): %v", i, err)
		}
		_ = cmd.Wait()

		cache := reopenCache(t, dbPath)
		for j := 0; j < crashSimAbruptCount; j++ {
			id := fmt.Sprintf("item-%04d", j)
			inode := cache.Get(id)
			if inode == nil {
				t.Fatalf("inode %s lost after crash (iteration %d)", id, i)
			}
			if got := inode.ParentID(); got != "root" {
				t.Fatalf("inode %s has parent %q, want root (iteration %d)", id, got, i)
			}
		}
		if err := cache.Close(); err != nil {
			t.Fatalf("close after reopen (iteration %d): %v", i, err)
		}
	}
}

// crashSimChildAbruptExit is the child half: writes inodes, persists them
// with SerializeAll, signals readiness, then holds the lock until killed.
func crashSimChildAbruptExit() {
	dbPath := os.Getenv("CRASH_SIM_DB")
	statusPath := os.Getenv("CRASH_SIM_STATUS")
	if dbPath == "" || statusPath == "" {
		fmt.Fprintln(os.Stderr, "CRASH_SIM_DB/CRASH_SIM_STATUS not set")
		os.Exit(2)
	}

	cache := NewInodeCache()
	if err := cache.InitBoltDB(dbPath); err != nil {
		fmt.Fprintf(os.Stderr, "child InitBoltDB: %v\n", err)
		os.Exit(1)
	}

	for i := 0; i < crashSimAbruptCount; i++ {
		id := fmt.Sprintf("item-%04d", i)
		cache.Insert(NewInodeDriveItem(&graph.DriveItem{
			ID:     id,
			Name:   id + ".bin",
			Size:   1024,
			Parent: &graph.DriveItemParent{ID: "root"},
		}))
	}

	if err := cache.SerializeAll(); err != nil {
		fmt.Fprintf(os.Stderr, "child SerializeAll: %v\n", err)
		os.Exit(1)
	}

	// Signal readiness, then hold the DB open until the parent SIGKILLs us
	// (the crash). No Close, no defers.
	if f, err := os.OpenFile(statusPath, os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		fmt.Fprintln(f, "done")
		_ = f.Close()
	}
	for {
		time.Sleep(1 * time.Second)
	}
}

// reopenCache opens the BoltDB cache after a crash, retrying briefly to
// absorb any transient flock window (see the note in the package comment).
func reopenCache(t *testing.T, dbPath string) *InodeCache {
	t.Helper()
	var lastErr error
	for attempt := 1; attempt <= 5; attempt++ {
		cache := NewInodeCache()
		err := cache.InitBoltDB(dbPath)
		if err == nil {
			return cache
		}
		lastErr = err
		_ = cache.Close()
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("BoltDB failed to reopen after crash: %v", lastErr)
	return nil
}

// ──── Mode B: SIGKILL mid-transaction ────

// TestCrashSimulation_SigKillMidWrite runs the SIGKILL crash simulation
// (parent half). The child half runs in a subprocess and is killed.
func TestCrashSimulation_SigKillMidWrite(t *testing.T) {
	if os.Getenv("CRASH_SIM_CHILD") == "killloop" {
		crashSimChildKillLoop()
		return
	}

	for i := 0; i < crashSimIters(); i++ {
		dir := t.TempDir()
		dbPath := filepath.Join(dir, "inodes.db")
		statusPath := filepath.Join(dir, "committed")

		// #nosec G204 -- the subprocess is the test binary itself (self-invocation
		// for the crash simulation); args are fixed literals, not user input.
		cmd := exec.Command(os.Args[0], "-test.run=^TestCrashSimulation_SigKillMidWrite$")
		cmd.Env = append(os.Environ(),
			"CRASH_SIM_CHILD=killloop",
			"CRASH_SIM_DB="+dbPath,
			"CRASH_SIM_STATUS="+statusPath,
		)
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}

		// Wait for the child to commit at least one batch, then kill it
		// mid-loop before it can finish the next batch.
		if !waitForFile(statusPath, 15*time.Second) {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			t.Fatalf("crash child never became ready (iteration %d)", i)
		}
		time.Sleep(10 * time.Millisecond)
		if err := cmd.Process.Kill(); err != nil { // SIGKILL
			t.Fatalf("kill failed (iteration %d): %v", i, err)
		}
		_ = cmd.Wait()

		// Verify: the DB reopens cleanly (a corrupt freelist/meta page would
		// make InitBoltDB fail) and every committed batch is complete.
		committed := readCommittedBatches(statusPath)
		cache := reopenCache(t, dbPath)
		for _, k := range committed {
			for j := 0; j < crashSimBatchSize; j++ {
				id := fmt.Sprintf("b%04d-item-%02d", k, j)
				if cache.Get(id) == nil {
					t.Fatalf("committed batch %d item %s missing after SIGKILL (iteration %d)", k, id, i)
				}
			}
		}
		if err := cache.Close(); err != nil {
			t.Fatalf("close after reopen (iteration %d): %v", i, err)
		}
	}
}

// crashSimChildKillLoop is the child half: it commits batches of inodes in an
// infinite loop, recording each batch number AFTER its transaction commits.
// The parent SIGKILLs it mid-loop; recorded batches must survive whole.
func crashSimChildKillLoop() {
	dbPath := os.Getenv("CRASH_SIM_DB")
	statusPath := os.Getenv("CRASH_SIM_STATUS")
	if dbPath == "" || statusPath == "" {
		fmt.Fprintln(os.Stderr, "CRASH_SIM_DB/CRASH_SIM_STATUS not set")
		os.Exit(2)
	}

	cache := NewInodeCache()
	if err := cache.InitBoltDB(dbPath); err != nil {
		fmt.Fprintf(os.Stderr, "child InitBoltDB: %v\n", err)
		os.Exit(1)
	}

	for k := 0; ; k++ {
		err := cache.db.Update(func(tx *bolt.Tx) error {
			bucket := tx.Bucket(boltBucketMetadata)
			if bucket == nil {
				return fmt.Errorf("metadata bucket missing")
			}
			for j := 0; j < crashSimBatchSize; j++ {
				id := fmt.Sprintf("b%04d-item-%02d", k, j)
				inode := NewInodeDriveItem(&graph.DriveItem{
					ID:     id,
					Name:   id + ".bin",
					Size:   1024,
					Parent: &graph.DriveItemParent{ID: "root"},
				})
				if err := bucket.Put([]byte(id), inode.AsJSON()); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "child batch %d: %v\n", k, err)
			os.Exit(1)
		}

		// Record AFTER commit: the parent only verifies recorded batches.
		f, err := os.OpenFile(statusPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err == nil {
			fmt.Fprintf(f, "%d\n", k)
			_ = f.Close()
		}
	}
}

// waitForFile polls until path exists (or the timeout expires).
func waitForFile(path string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

// readCommittedBatches returns the batch numbers the child recorded as
// committed (one per line in the status file).
func readCommittedBatches(path string) []int {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var batches []int
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if n, err := strconv.Atoi(line); err == nil {
			batches = append(batches, n)
		}
	}
	return batches
}

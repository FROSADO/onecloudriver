package fs

import (
	"bytes"
	"io"
	"sync"
	"testing"
	"time"
)

// blockingReader feeds InsertStream one byte at a time: it writes the first
// byte, signals, then blocks until released before delivering the rest. It lets
// the test pause an InsertStream mid-write while it holds the per-inode lock.
type blockingReader struct {
	data    []byte
	pos     int
	mu      sync.Mutex
	started chan struct{}
	release chan struct{}
}

func (r *blockingReader) Read(p []byte) (int, error) {
	r.mu.Lock()
	if r.pos >= len(r.data) {
		r.mu.Unlock()
		return 0, io.EOF
	}
	if r.pos == 0 {
		n := copy(p, r.data[:1])
		r.pos += n
		close(r.started)
		r.mu.Unlock()
		<-r.release
		return n, nil
	}
	n := copy(p, r.data[r.pos:])
	r.pos += n
	r.mu.Unlock()
	return n, nil
}

// TestContentCache_ReadAll_BlocksUntilConcurrentWriteCompletes verifies the
// per-inode lock from issue #64: a snapshot (ReadAll) taken while a write
// (InsertStream) is in flight must block and return the complete new content,
// never a torn/partial read of the shared FD.
func TestContentCache_ReadAll_BlocksUntilConcurrentWriteCompletes(t *testing.T) {
	cache, err := NewContentCache(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer cache.CloseAll()

	const id = "insert-stream-race"
	if err := cache.Insert(id, []byte("AAAA")); err != nil {
		t.Fatal(err)
	}

	br := &blockingReader{
		data:    []byte("BBBB"),
		started: make(chan struct{}),
		release: make(chan struct{}),
	}

	insertDone := make(chan error, 1)
	go func() {
		_, err := cache.InsertStream(id, br)
		insertDone <- err
	}()

	// InsertStream has truncated the file and is blocked after its first byte,
	// still holding the per-inode lock.
	<-br.started

	resultCh := make(chan []byte, 1)
	go func() {
		resultCh <- cache.ReadAll(id)
	}()

	// With the lock, ReadAll must block (serialized with the in-flight write).
	select {
	case data := <-resultCh:
		close(br.release)
		t.Fatalf("ReadAll returned early with %q; expected it to block until the concurrent write completed", data)
	case <-time.After(250 * time.Millisecond):
		// Expected: still blocked.
	}

	close(br.release)
	if err := <-insertDone; err != nil {
		t.Fatalf("InsertStream: %v", err)
	}

	if data := <-resultCh; string(data) != "BBBB" {
		t.Fatalf("expected complete snapshot %q, got %q", "BBBB", string(data))
	}
}

// TestContentCache_ReadAll_SnapshotIsConsistent_ConcurrentWriteAt runs a
// writer that keeps overwriting the file with monochrome patterns while a
// reader snapshots it concurrently. Every snapshot must be monochrome (a
// complete write), never a mix of two writes. Run under `-race -count=50` per
// the issue #64 acceptance criteria.
func TestContentCache_ReadAll_SnapshotIsConsistent_ConcurrentWriteAt(t *testing.T) {
	cache, err := NewContentCache(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer cache.CloseAll()

	const id = "snapshot-race"
	const size = 64 * 1024

	if err := cache.Insert(id, bytes.Repeat([]byte{0}, size)); err != nil {
		t.Fatal(err)
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for b := byte(1); b != 0; b++ {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := cache.WriteAt(id, bytes.Repeat([]byte{b}, size), 0); err != nil {
				t.Errorf("WriteAt: %v", err)
				return
			}
		}
	}()

	for i := 0; i < 200; i++ {
		data := cache.ReadAll(id)
		if len(data) != size {
			close(stop)
			wg.Wait()
			t.Fatalf("snapshot length %d, want %d", len(data), size)
		}
		for j := 1; j < len(data); j++ {
			if data[j] != data[0] {
				close(stop)
				wg.Wait()
				t.Fatalf("torn snapshot: byte %d = %d, first byte = %d", j, data[j], data[0])
			}
		}
	}

	close(stop)
	wg.Wait()
}

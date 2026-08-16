package fs

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/frosado/onecloudriver/internal/graph"
)

// benchSerializeTreeN is the number of inodes in the benchmark tree.
const benchSerializeTreeN = 1000

// newBenchCache builds a cache with benchSerializeTreeN inodes of the browsed
// tree. If persist is true it runs an initial SerializeDirty so the baseline
// tree is on disk (the benchmark then measures only the delta).
func newBenchCache(b *testing.B, persist bool) *InodeCache {
	b.Helper()
	cache := NewInodeCache()
	if err := cache.InitBoltDB(filepath.Join(b.TempDir(), "inodes.db")); err != nil {
		b.Fatal(err)
	}
	for i := 0; i < benchSerializeTreeN; i++ {
		id := fmt.Sprintf("item-%04d", i)
		cache.Insert(NewInodeDriveItem(&graph.DriveItem{
			ID:     id,
			Name:   id + ".bin",
			Size:   1024,
			Parent: &graph.DriveItemParent{ID: "root"},
		}))
	}
	if persist {
		if err := cache.SerializeDirty(); err != nil {
			b.Fatal(err)
		}
	}
	return cache
}

// BenchmarkSerializeAll_FullTree is the baseline: a full-tree rewrite on every
// delta sync (the pre-#67 behaviour).
func BenchmarkSerializeAll_FullTree(b *testing.B) {
	cache := newBenchCache(b, false)
	defer cache.Close()
	b.ResetTimer()

	var written uint64
	for i := 0; i < b.N; i++ {
		before := cache.serializedBytes.Load()
		if err := cache.SerializeAll(); err != nil {
			b.Fatal(err)
		}
		written += cache.serializedBytes.Load() - before
	}
	b.ReportMetric(float64(written)/float64(b.N), "bytes-written")
}

// BenchmarkSerializeDirty_OneMutation is the post-#67 behaviour: a delta sync
// that changed exactly one inode persists exactly one inode.
func BenchmarkSerializeDirty_OneMutation(b *testing.B) {
	cache := newBenchCache(b, true)
	defer cache.Close()
	b.ResetTimer()

	var written uint64
	for i := 0; i < b.N; i++ {
		cache.markDirty("item-0999")
		before := cache.serializedBytes.Load()
		if err := cache.SerializeDirty(); err != nil {
			b.Fatal(err)
		}
		written += cache.serializedBytes.Load() - before
	}
	b.ReportMetric(float64(written)/float64(b.N), "bytes-written")
}

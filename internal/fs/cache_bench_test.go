package fs

// Performance and persistence benchmarks for the InodeCache.
//
// #97 — serialization costs (BenchmarkSerialize*): how many bytes a delta sync
// writes and whether only dirty inodes are persisted.
//
// #66 — eviction costs (BenchmarkTTLSweep*/BenchmarkSizeEviction*): the cost of
// clearing the metadata cache over 50k folders, comparing the three strategies:
//
//   - BenchmarkTTLSweepBuckets_50k    — bucket ring, steady state (production tick).
//   - BenchmarkTTLSweepBucketsFullWindow_50k — bucket ring, >60s time-jump case.
//   - BenchmarkTTLSweepFullScan_50k   — reference full-map sweep (renamed old impl).
//   - BenchmarkSizeEviction_50k       — persistent size-eviction heap (Phase 8).
//   - BenchmarkFullSweep_50k          — the combined tick (TTL buckets + size heap).
//
// The #66 benchmarks prepare all 50k folders outside the timed section, so
// `ns/op` reflects only the algorithm under measurement.

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/frosado/onecloudriver/internal/graph"
)

// ---- #97: serialization benchmarks ----

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

// ---- #66: eviction benchmarks ----

// benchFolderCount is the fixed measurement target of the #66 eviction
// benchmarks: 50k folders in the cache, as required by the acceptance criteria.
const benchFolderCount = 50000

// benchCacheN builds a cache holding 50k folder inodes, each with a single
// cached child, and returns the cache plus the list of folder inodes. `now`
// pins a stable reference time for registry decisions. The size is fixed at
// 50k (the issue's measurement target) via benchFolderCount.
func benchCacheN(b *testing.B, now time.Time) (*InodeCache, []*Inode) {
	b.Helper()
	quietLogsForBench()

	const n = benchFolderCount
	cache := NewInodeCache()
	folders := make([]*Inode, 0, n)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("folder-%05d", i)
		p := NewInodeDriveItem(&graph.DriveItem{
			ID: id, Name: id, Folder: &graph.Folder{ChildCount: 1},
		})
		cache.inodes.Store(p.ID(), p)
		p.SetChildren([]string{"child-" + id})
		p.Lock()
		p.childrenCachedAt = now
		p.childrenLastAccess = now
		p.Unlock()
		folders = append(folders, p)
	}
	return cache, folders
}

// spreadTTLDistributions stamps each folder with a childrenLastAccess offset so
// that the bundle of effective expiries lands in one distinct ring bucket per
// `step` stride. With a fixed accessCount and baseTTL, expiry = lastAccess +
// effectiveTTL, so stamping lastAccess = base - k*step positions expiry across
// `k` consecutive buckets. This models a steady production distribution where
// every tick owns ~n/k entries.
func spreadTTLDistributions(folders []*Inode, base time.Time, k int, step time.Duration) {
	for i, p := range folders {
		offset := time.Duration(i%k) * step
		p.Lock()
		p.childrenCachedAt = base.Add(-offset)
		p.childrenLastAccess = base.Add(-offset)
		p.Unlock()
	}
}

// BenchmarkTTLSweepBuckets_50k measures the bucket-ring TTL sweep in steady
// state: folders are spread across the 60s window and each tick sweeps a single
// bucket (~50k/60 ≈ 833 entries), re-registering those still fresh. This is the
// production behaviour of the 1s sweep ticker.
func BenchmarkTTLSweepBuckets_50k(b *testing.B) {
	base := time.Now().Truncate(time.Second)
	cache, folders := benchCacheN(b, base)

	spreadTTLDistributions(folders, base, ttlBucketCount, time.Second)
	for _, p := range folders {
		cache.registerTTL(p)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// One tick per iteration: sweep the single bucket at this second.
		cache.sweepExpiredBucket(base.Add(time.Duration(i%ttlBucketCount) * time.Second))
	}
}

// BenchmarkTTLSweepBucketsFullWindow_50k measures the bucket-ring TTL sweep in
// the worst case: a time jump larger than the 60s ring window drains every
// bucket in a single pass (all 50k folders).
func BenchmarkTTLSweepBucketsFullWindow_50k(b *testing.B) {
	base := time.Now().Truncate(time.Second)
	cache, folders := benchCacheN(b, base)

	spreadTTLDistributions(folders, base, ttlBucketCount, time.Second)
	for _, p := range folders {
		cache.registerTTL(p)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		now := base.Add(time.Duration(i%ttlBucketCount+1) * time.Second)
		cache.sweepMu.Lock()
		cache.lastSweep = now.Add(-time.Duration(ttlBucketCount+1) * time.Second)
		cache.sweepMu.Unlock()
		cache.sweepExpiredBuckets(now)
	}
}

// BenchmarkTTLSweepFullScan_50k measures the reference full-map TTL sweep over
// the same 50k folders (the pre-bucket implementation, still kept for parity).
func BenchmarkTTLSweepFullScan_50k(b *testing.B) {
	base := time.Now().Truncate(time.Second)
	cache, _ := benchCacheN(b, base)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cache.evictExpiredChildrenFullScan()
	}
}

// BenchmarkSizeEviction_50k measures the persistent size-eviction heap. 50k
// folders are registered and scored; maxEntries is set just below the count so
// a sweep must pop (50k - maxEntries) candidates, i.e. the worst-case volume
// for the size path. A fraction of folders is re-scored between iterations to
// exercise the stale-generation discard path (task 9.3).
func BenchmarkSizeEviction_50k(b *testing.B) {
	const n = benchFolderCount
	base := time.Now().Truncate(time.Second)
	cache, folders := benchCacheN(b, base)

	cache.SetMaxEntries(n - 1) // force exactly 1 eviction per sweep
	for _, p := range folders {
		cache.updateEvictionEntry(p, base)
	}
	cache.cachedFolders.Store(int64(n))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		// Re-score a folder each iteration to exercise stale-generation pops.
		p := folders[i%n]
		cache.updateEvictionEntry(p, base.Add(time.Second))
		b.StartTimer()

		cache.evictChildrenBySizeLimit()
	}
}

// BenchmarkFullSweep_50k measures the combined production tick: TTL buckets plus
// size-eviction heap through the public sweep entry point.
func BenchmarkFullSweep_50k(b *testing.B) {
	const n = benchFolderCount
	base := time.Now().Truncate(time.Second)
	cache, folders := benchCacheN(b, base)

	spreadTTLDistributions(folders, base, ttlBucketCount, time.Second)
	for _, p := range folders {
		cache.registerTTL(p)
	}
	for _, p := range folders {
		cache.updateEvictionEntry(p, base)
	}
	cache.cachedFolders.Store(int64(n))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cache.ForceSweep()
	}
}

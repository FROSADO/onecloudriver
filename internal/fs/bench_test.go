package fs

import (
	"context"
	"fmt"
	"io"
	"testing"

	"github.com/frosado/onecloudriver/internal/graph"
	"github.com/rs/zerolog"
)

// quietLogsForBench silences debug/trace logging during benchmarks. The code
// under test (e.g. InodeCache.GetChildren) logs at Trace on every cache hit,
// which would interleave with the benchmark output and break the baseline
// parser in scripts/bench_baseline.sh.
func quietLogsForBench() {
	zerolog.SetGlobalLevel(zerolog.ErrorLevel)
}

// Core benchmark suite (issue #72). These measure the hot FUSE-side paths
// that must feel "local"; the perf-focused benchmarks (eviction, sweep,
// delta, upload) land with their own issues (#65/#66/#68/#69).
//
// Targets (see scripts/bench_baseline.sh):
//
//	BenchmarkLookup     p50 < 1ms, p99 < 5ms (cache hit)
//	BenchmarkRead_4KB   throughput > 500 MB/s (local ContentCache)
//	BenchmarkWrite_4KB  latency p99 < 2ms (ContentCache WriteAt)
//	BenchmarkReaddir_1k < 10ms (cache hit on 1000 children)

// BenchmarkLookup measures InodeCache.Get on a cache hit.
func BenchmarkLookup(b *testing.B) {
	quietLogsForBench()
	cache := NewInodeCache()
	cache.Insert(NewInodeDriveItem(&graph.DriveItem{ID: "bench-item", Name: "file.bin", Size: 1024}))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if cache.Get("bench-item") == nil {
			b.Fatal("cache miss")
		}
	}
}

// BenchmarkRead_4KB measures reading 4KB chunks from the local ContentCache
// through the shared FD (the FUSE Read hot path).
func BenchmarkRead_4KB(b *testing.B) {
	quietLogsForBench()
	cache, err := NewContentCache(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	id := "bench-read"
	if err := cache.Insert(id, make([]byte, 64<<20)); err != nil {
		b.Fatal(err)
	}
	fd, err := cache.Open(id)
	if err != nil {
		b.Fatal(err)
	}
	defer cache.Close(id)

	buf := make([]byte, 4096)
	b.SetBytes(4096)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := fd.Seek(0, 0); err != nil {
			b.Fatal(err)
		}
		if _, err := io.ReadFull(fd, buf); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkWrite_4KB measures ContentCache.WriteAt, the FUSE Write path.
func BenchmarkWrite_4KB(b *testing.B) {
	quietLogsForBench()
	cache, err := NewContentCache(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	id := "bench-write"
	if _, err := cache.Open(id); err != nil {
		b.Fatal(err)
	}
	defer cache.Close(id)

	data := make([]byte, 4096)
	b.SetBytes(4096)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := cache.WriteAt(id, data, 0); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkReaddir_1k measures a cache hit of GetChildren on a folder with
// 1000 children (no fetcher call involved).
func BenchmarkReaddir_1k(b *testing.B) {
	quietLogsForBench()
	const n = 1000

	cache := NewInodeCache()
	parent := NewInodeDriveItem(&graph.DriveItem{ID: "parent", Name: "parent", Folder: &graph.Folder{}})
	cache.inodes.Store(parent.ID(), parent)

	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("child-%04d", i)
		ids = append(ids, id)
		cache.inodes.Store(id, NewInodeDriveItem(&graph.DriveItem{
			ID:     id,
			Name:   fmt.Sprintf("file%04d.bin", i),
			Size:   1024,
			Parent: &graph.DriveItemParent{ID: parent.ID()},
		}))
	}
	parent.SetChildren(ids) // childrenCachedAt = now → fresh → cache hit

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		children, err := cache.GetChildren(context.Background(), parent.ID(), nil)
		if err != nil {
			b.Fatal(err)
		}
		if len(children) != n {
			b.Fatalf("expected %d children, got %d", n, len(children))
		}
	}
}

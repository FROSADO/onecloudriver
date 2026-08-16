package fs

import (
	"bytes"
	"os"
	"testing"
)

// benchSnapshotSize is the content size used by the snapshot benchmarks. It is
// large enough to expose full-file allocations while keeping the benchmark
// quick to set up and run.
const benchSnapshotSize = 8 << 20 // 8 MiB

// BenchmarkContentCache_ReadAll measures the legacy snapshot path: it
// materializes the whole file in heap on every call.
func BenchmarkContentCache_ReadAll(b *testing.B) {
	cache, err := NewContentCache(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	defer cache.CloseAll()

	content := bytes.Repeat([]byte("x"), benchSnapshotSize)
	if err := cache.Insert("bench", content); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.SetBytes(benchSnapshotSize)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		data := cache.ReadAll("bench")
		if len(data) != benchSnapshotSize {
			b.Fatalf("ReadAll returned %d bytes, want %d", len(data), benchSnapshotSize)
		}
	}
}

// BenchmarkContentCache_Snapshot measures the streaming snapshot path: it
// copies to a dedicated file with a bounded buffer, avoiding the full-file
// heap allocation.
func BenchmarkContentCache_Snapshot(b *testing.B) {
	cache, err := NewContentCache(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	defer cache.CloseAll()

	content := bytes.Repeat([]byte("x"), benchSnapshotSize)
	if err := cache.Insert("bench", content); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.SetBytes(benchSnapshotSize)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		path, n, err := cache.Snapshot("bench")
		if err != nil {
			b.Fatal(err)
		}
		if n != benchSnapshotSize {
			b.Fatalf("Snapshot returned %d bytes, want %d", n, benchSnapshotSize)
		}
		os.Remove(path)
	}
}

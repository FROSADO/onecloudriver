package fs

import (
	"container/heap"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

type benchmarkEvictionFile struct {
	path    string
	modTime int64
	size    int64
}

func BenchmarkContentCacheEvictionSelection(b *testing.B) {
	for _, fileCount := range []int{1_000, 10_000} {
		b.Run(fmt.Sprintf("heap/%d", fileCount), func(b *testing.B) {
			cache := benchmarkContentCache(b, fileCount)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				candidates := cloneEvictionHeap(cache.evictionHeap)
				for candidates.Len() > 0 {
					_, ok := heap.Pop(&candidates).(*evictEntry)
					if !ok {
						b.Fatal("heap entry has unexpected type")
					}
				}
			}
		})

		b.Run(fmt.Sprintf("directory-scan/%d", fileCount), func(b *testing.B) {
			cache := benchmarkContentCache(b, fileCount)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if _, err := scanEvictionFiles(cache.directory); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func benchmarkContentCache(b *testing.B, fileCount int) *ContentCache {
	b.Helper()
	cache, err := NewContentCache(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < fileCount; i++ {
		path := filepath.Join(cache.directory, fmt.Sprintf("file-%05d", i))
		if err := os.WriteFile(path, []byte("content"), 0600); err != nil {
			b.Fatal(err)
		}
	}
	if err := cache.initializeEvictionIndex(); err != nil {
		b.Fatal(err)
	}
	return cache
}

func cloneEvictionHeap(source evictHeap) evictHeap {
	clone := make(evictHeap, len(source))
	for i, entry := range source {
		copyEntry := *entry
		copyEntry.index = i
		clone[i] = &copyEntry
	}
	heap.Init(&clone)
	return clone
}

func scanEvictionFiles(directory string) ([]benchmarkEvictionFile, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, err
	}
	files := make([]benchmarkEvictionFile, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		files = append(files, benchmarkEvictionFile{
			path:    filepath.Join(directory, entry.Name()),
			modTime: info.ModTime().UnixNano(),
			size:    info.Size(),
		})
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].modTime == files[j].modTime {
			return files[i].path < files[j].path
		}
		return files[i].modTime < files[j].modTime
	})
	return files, nil
}

package fs

import (
	"context"
	"testing"

	"github.com/frosado/onecloudriver/internal/graph"
)

// BenchmarkPreWarm_Depth2_100Items simulates pre-warming a tree with ~100 items
// at depth 2. Measures the time taken to complete the BFS traversal.
func BenchmarkPreWarm_Depth2_100Items(b *testing.B) {
	cache := NewInodeCache()

	// Mock fetcher that simulates a realistic tree
	fetcher := func(ctx context.Context, parentID string) ([]graph.DriveItem, error) {
		switch parentID {
		case "root":
			// Root has 5 folders
			items := make([]graph.DriveItem, 5)
			for i := 0; i < 5; i++ {
				items[i] = graph.DriveItem{
					ID:     "folder_" + string(rune(i)),
					Name:   "Folder " + string(rune(i)),
					Folder: &graph.Folder{ChildCount: 20},
				}
			}
			return items, nil
		default:
			// Each folder has 20 files (not folders, so no further traversal)
			items := make([]graph.DriveItem, 20)
			for i := 0; i < 20; i++ {
				items[i] = graph.DriveItem{
					ID:   "file_" + parentID + "_" + string(rune(i)),
					Name: "file_" + string(rune(i)) + ".txt",
					File: &graph.File{},
				}
			}
			return items, nil
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = preWarm(context.Background(), cache, fetcher, 2)
	}
	b.StopTimer()
}

// BenchmarkPreWarm_Depth3_1000Items simulates pre-warming a larger tree with ~1000 items
// at depth 3. Measures scalability with deeper traversal.
func BenchmarkPreWarm_Depth3_1000Items(b *testing.B) {
	cache := NewInodeCache()

	// Mock fetcher that simulates a larger tree
	callCount := 0
	fetcher := func(ctx context.Context, parentID string) ([]graph.DriveItem, error) {
		callCount++

		switch parentID {
		case "root":
			// Root has 10 folders (level 1)
			items := make([]graph.DriveItem, 10)
			for i := 0; i < 10; i++ {
				items[i] = graph.DriveItem{
					ID:     "l1_folder_" + string(rune(i)),
					Name:   "Level1_Folder_" + string(rune(i)),
					Folder: &graph.Folder{ChildCount: 10},
				}
			}
			return items, nil
		default:
			// Simulate 2 levels of folders, then files
			// This creates a realistic tree structure
			// For simplicity, every folder at level 1/2 has 10 items
			items := make([]graph.DriveItem, 10)
			for i := 0; i < 10; i++ {
				// Determine if this should be a folder (for deeper levels) or file
				if callCount < 50 { // Heuristic: first N calls are folder listings
					items[i] = graph.DriveItem{
						ID:     parentID + "_sub_" + string(rune(i)),
						Name:   "SubFolder_" + string(rune(i)),
						Folder: &graph.Folder{ChildCount: 10},
					}
				} else {
					items[i] = graph.DriveItem{
						ID:   parentID + "_file_" + string(rune(i)),
						Name: "file_" + string(rune(i)) + ".txt",
						File: &graph.File{},
					}
				}
			}
			return items, nil
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		callCount = 0 // Reset for each iteration
		_ = preWarm(context.Background(), cache, fetcher, 3)
	}
	b.StopTimer()
}

// BenchmarkPreWarm_Depth0_NoOp measures the overhead of the no-op case (depth=0).
// Should be negligible (~microseconds).
func BenchmarkPreWarm_Depth0_NoOp(b *testing.B) {
	cache := NewInodeCache()

	fetcher := func(ctx context.Context, _ string) ([]graph.DriveItem, error) {
		// Should not be called
		return []graph.DriveItem{}, nil
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = preWarm(context.Background(), cache, fetcher, 0)
	}
	b.StopTimer()
}

// BenchmarkGetChildren_CacheHit measures GetChildren latency when data is already cached.
// Pre-warm should ensure sub-50ms cache hits.
func BenchmarkGetChildren_CacheHit(b *testing.B) {
	cache := NewInodeCache()

	fetcher := func(ctx context.Context, parentID string) ([]graph.DriveItem, error) {
		if parentID == "root" {
			return []graph.DriveItem{
				{
					ID:     "folder1",
					Name:   "Folder 1",
					Folder: &graph.Folder{ChildCount: 5},
				},
			}, nil
		}
		if parentID == "folder1" {
			return []graph.DriveItem{
				{
					ID:   "file1",
					Name: "file1.txt",
					File: &graph.File{},
				},
			}, nil
		}
		return []graph.DriveItem{}, nil
	}

	// Pre-populate cache via preWarm
	ctx := context.Background()
	_ = preWarm(ctx, cache, fetcher, 2)

	// Now benchmark the cache hit
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = cache.GetChildren(ctx, "folder1", fetcher)
	}
	b.StopTimer()
}

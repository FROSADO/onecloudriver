package fs

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/frosado/onecloudriver/internal/graph"
	"github.com/hanwen/go-fuse/v2/fuse"
)

// netErrorStub simula un error de red transitorio (timeout) para testear el
// modo offline sin depender de DNS ni conexiones reales.
type netErrorStub struct{}

func (netErrorStub) Error() string   { return "simulated network timeout" }
func (netErrorStub) Timeout() bool   { return true }
func (netErrorStub) Temporary() bool { return true }

// transportErr devuelve siempre un error de red simulado en RoundTrip.
type transportErr struct{}

func (transportErr) RoundTrip(req *http.Request) (*http.Response, error) {
	return nil, netErrorStub{}
}

// TestNewOneCloudFS verifica que NewOneCloudFS inicializa correctamente
func TestNewOneCloudFS(t *testing.T) {
	graphClient := graph.NewClient()
	tokenProvider := &mockTokenProvider{token: "test_token"}
	inodeCache := NewInodeCache()
	contentCache := &ContentCache{}

	root := NewOneCloudFS(graphClient, tokenProvider, inodeCache, contentCache, nil)

	if root == nil {
		t.Fatal("NewOneCloudFS returned nil")
	}
}

// TestOneCloudFS_Getattr verifies that the root is a directory
func TestOneCloudFS_Getattr(t *testing.T) {
	graphClient := graph.NewClient()
	tokenProvider := &mockTokenProvider{token: "test_token"}
	inodeCache := NewInodeCache()

	root := NewOneCloudFS(graphClient, tokenProvider, inodeCache, &ContentCache{}, nil)

	var out fuse.AttrOut
	errno := root.Getattr(context.Background(), nil, &out)

	if errno != 0 {
		t.Errorf("Se esperaba errno 0, obtenido %d", errno)
	}
	if out.Mode&syscall.S_IFDIR == 0 {
		t.Error("The root should be a directory (S_IFDIR)")
	}
}

// TestOneCloudFS_Readdir verifies that it lists the root content
func TestOneCloudFS_Readdir(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"value": [
				{"id": "file1", "name": "root-file.txt", "size": 100},
				{"id": "folder1", "name": "MiCarpeta", "folder": {"childCount": 3}}
			]
		}`))
	}))
	defer server.Close()

	graphClient := &graph.Client{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	}
	tokenProvider := &mockTokenProvider{token: "test_token"}
	inodeCache := NewInodeCache()

	root := NewOneCloudFS(graphClient, tokenProvider, inodeCache, &ContentCache{}, nil)

	stream, errno := root.Readdir(context.Background())

	if errno != 0 {
		t.Fatalf("Se esperaba errno 0, obtenido %d", errno)
	}

	var entries []fuse.DirEntry
	for stream.HasNext() {
		entry, errno := stream.Next()
		if errno != 0 {
			t.Fatalf("Error leyendo stream: %d", errno)
		}
		entries = append(entries, entry)
	}

	if len(entries) != 2 {
		t.Errorf("Se esperaban 2 entries, obtenidos %d", len(entries))
	}
}

// TestOneCloudFS_Lookup verifica que Lookup encuentra un archivo
func TestOneCloudFS_Lookup(t *testing.T) {
	t.Skip("Requires mounted FUSE bridge (NewInode) — integration test")
}

// TestOneCloudFS_FetchChildren_OfflineFallback_StaleData verifica el camino
// riskiest of the TTL freshness fix: when children are stale
// (TTL exceeded, e.g. restored from a previous session) and the network fails,
// fetchChildren must serve the local cache instead of propagating the error
// (modo offline de la Fase 3), y debe resetear childrenCachedAt para no
// intentar la red de nuevo en cada Readdir.
func TestOneCloudFS_FetchChildren_OfflineFallback_StaleData(t *testing.T) {
	graphClient := &graph.Client{
		BaseURL:    "http://example.invalid",
		HTTPClient: &http.Client{Transport: transportErr{}},
	}
	tokenProvider := &mockTokenProvider{token: "test_token"}
	inodeCache := NewInodeCache()
	root := NewOneCloudFS(graphClient, tokenProvider, inodeCache, &ContentCache{}, nil)

	// Cache with stale children (simulates a previous session, 2 hours ago)
	parent := NewInodeDriveItem(&graph.DriveItem{
		ID: "root", Name: "root", Folder: &graph.Folder{ChildCount: 1},
	})
	parent.SetChildren([]string{"cached1"})
	parent.Lock()
	parent.childrenCachedAt = time.Now().Add(-2 * time.Hour)
	parent.childrenLastAccess = time.Now().Add(-2 * time.Hour)
	parent.Unlock()
	inodeCache.Insert(parent)
	inodeCache.Insert(NewInodeDriveItem(&graph.DriveItem{ID: "cached1", Name: "cached.txt"}))

	// 1. GetChildren with stale children + network error → fallback to cache
	children, err := inodeCache.GetChildren(context.Background(), "root", root.fetchChildren)
	if err != nil {
		t.Fatalf("GetChildren should serve the cache in offline mode, error: %v", err)
	}
	if len(children) != 1 || children["cached.txt"] == nil {
		t.Errorf("Should serve cached.txt from the cache, got: %v", children)
	}
	if !inodeCache.IsOffline() {
		t.Error("Offline mode should have been activated")
	}

	// 2. Tras el fallback, childrenCachedAt se resetea → el siguiente GetChildren
	//    es un HIT y no vuelve a intentar la red (no martillear el API offline).
	secondCalls := 0
	fetch2 := func(ctx context.Context, parentID string) ([]graph.DriveItem, error) {
		secondCalls++
		return nil, errors.New("should not call the fetcher")
	}
	children2, err := inodeCache.GetChildren(context.Background(), "root", fetch2)
	if err != nil {
		t.Fatalf("Segundo GetChildren error: %v", err)
	}
	if secondCalls != 0 {
		t.Errorf("After the offline fallback, cachedAt should be fresh: there were %d extra fetches", secondCalls)
	}
	if children2["cached.txt"] == nil {
		t.Errorf("cached.txt should keep being served from cache, got: %v", children2)
	}
}

// TestOneCloudFS_FetchChildren_OfflineFallback_EvictedParent reconstruye el
// escenario real detectado en el test offline: la lista de children del padre
// fue evictada por el sweep TTL (children=nil), pero los inodos hijos siguen
// en el sync.Map con su ParentID (persistidos por SerializeAll). Con la red
// down, fetchChildren must rebuild the list from ItemsByParent instead
// de devolver EIO.
func TestOneCloudFS_FetchChildren_OfflineFallback_EvictedParent(t *testing.T) {
	graphClient := &graph.Client{
		BaseURL:    "http://example.invalid",
		HTTPClient: &http.Client{Transport: transportErr{}},
	}
	tokenProvider := &mockTokenProvider{token: "test_token"}
	inodeCache := NewInodeCache()
	root := NewOneCloudFS(graphClient, tokenProvider, inodeCache, &ContentCache{}, nil)

	// Padre con children evictados (sweep TTL → children=nil)
	parent := NewInodeDriveItem(&graph.DriveItem{
		ID: "parent1", Name: "Docs", Folder: &graph.Folder{ChildCount: 2},
	})
	inodeCache.Insert(parent)

	// Inodos hijos con ParentID (siguen en el sync.Map aunque el padre
	// haya sido evictado)
	inodeCache.Insert(NewInodeDriveItem(&graph.DriveItem{
		ID: "child1", Name: "a.txt", Size: 10,
		Parent: &graph.DriveItemParent{ID: "parent1"},
	}))
	inodeCache.Insert(NewInodeDriveItem(&graph.DriveItem{
		ID: "child2", Name: "b.txt", Size: 20,
		Parent: &graph.DriveItemParent{ID: "parent1"},
	}))

	// Error de red → debe reconstruir children desde ItemsByParent
	children, err := inodeCache.GetChildren(context.Background(), "parent1", root.fetchChildren)
	if err != nil {
		t.Fatalf("GetChildren with an evicted parent offline should rebuild, error: %v", err)
	}
	if len(children) != 2 {
		t.Errorf("Se esperaban 2 hijos reconstruidos, obtenidos %d: %v", len(children), children)
	}
	if children["a.txt"] == nil || children["b.txt"] == nil {
		t.Errorf("a.txt and b.txt should be rebuilt, got: %v", children)
	}
	if !inodeCache.IsOffline() {
		t.Error("Offline mode should have been activated")
	}
}

// TestInodeCache_ItemsByParent verifies the reconstruction of the list of
// children por ParentID cuando la lista del padre fue evictada.
func TestInodeCache_ItemsByParent(t *testing.T) {
	cache := NewInodeCache()

	// Padre sin children (evictado)
	parent := NewInodeDriveItem(&graph.DriveItem{
		ID: "parent1", Name: "Docs", Folder: &graph.Folder{},
	})
	cache.Insert(parent)

	cache.Insert(NewInodeDriveItem(&graph.DriveItem{
		ID: "child1", Name: "a.txt", Size: 10,
		Parent: &graph.DriveItemParent{ID: "parent1"},
	}))
	cache.Insert(NewInodeDriveItem(&graph.DriveItem{
		ID: "child2", Name: "b.txt", Size: 20,
		Parent: &graph.DriveItemParent{ID: "parent1"},
	}))
	// Inodo de otro padre (no debe aparecer)
	cache.Insert(NewInodeDriveItem(&graph.DriveItem{
		ID: "other", Name: "c.txt", Size: 30,
		Parent: &graph.DriveItemParent{ID: "parent2"},
	}))

	items := cache.ItemsByParent("parent1")
	if len(items) != 2 {
		t.Errorf("ItemsByParent(parent1) esperaba 2 items, obtenidos %d", len(items))
	}

	names := map[string]bool{}
	for _, it := range items {
		names[it.Name] = true
	}
	if !names["a.txt"] || !names["b.txt"] || names["c.txt"] {
		t.Errorf("ItemsByParent should return only children of parent1: %v", names)
	}
}

// TestInodeCache_SerializeAll_PersistsEvictedSubtree verifies that a subtree
// cuya carpeta fue evictada (children=nil) SÍ sobrevive al round-trip: los
// inodos con ParentID se persisten y ItemsByParent los recupera offline.
func TestInodeCache_SerializeAll_PersistsEvictedSubtree(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Session 1: subtree with an evicted parent
	cache1 := NewInodeCache()
	if err := cache1.InitBoltDB(dbPath); err != nil {
		t.Fatalf("InitBoltDB error: %v", err)
	}

	// root fetched con hijo onedriver_tests
	root := NewInodeDriveItem(&graph.DriveItem{
		ID: "root", Name: "root", Folder: &graph.Folder{ChildCount: 1},
	})
	root.SetChildren([]string{"ontests"})
	cache1.Insert(root)

	// onedriver_tests: lista EVICTADA (children=nil) pero sus hijos persisten
	ontests := NewInodeDriveItem(&graph.DriveItem{
		ID: "ontests", Name: "onedriver_tests", Folder: &graph.Folder{},
		Parent: &graph.DriveItemParent{ID: "root"},
	})
	cache1.Insert(ontests)
	cache1.Insert(NewInodeDriveItem(&graph.DriveItem{
		ID: "paging", Name: "paging", Folder: &graph.Folder{},
		Parent: &graph.DriveItemParent{ID: "ontests"},
	}))
	cache1.Insert(NewInodeDriveItem(&graph.DriveItem{
		ID: "delta", Name: "delta", Folder: &graph.Folder{},
		Parent: &graph.DriveItemParent{ID: "ontests"},
	}))

	if err := cache1.SerializeAll(); err != nil {
		t.Fatalf("SerializeAll error: %v", err)
	}
	cache1.Close()

	// Session 2: restore and rebuild the evicted subtree offline
	cache2 := NewInodeCache()
	if err := cache2.InitBoltDB(dbPath); err != nil {
		t.Fatalf("Segunda InitBoltDB error: %v", err)
	}
	defer cache2.Close()

	// The child inodes of the subtree must exist in memory
	if cache2.Get("paging") == nil || cache2.Get("delta") == nil {
		t.Fatal("paging/delta should be persisted even if onedriver_tests was evicted")
	}

	// ItemsByParent reconstruye la lista de onedriver_tests
	items := cache2.ItemsByParent("ontests")
	if len(items) != 2 {
		t.Errorf("ItemsByParent(ontests) esperaba 2 items, obtenidos %d", len(items))
	}
}

// TestIsNetworkError_RealProxyError reproduce el error EXACTO que produce
// http.Client con un proxy roto (HTTPS_PROXY=http://127.0.0.1:1), que es el
// escenario real del test offline end-to-end. Verifica que isNetworkError lo
// detecta y que el wrapping de la capa Graph no lo oculta.
func TestIsNetworkError_RealProxyError(t *testing.T) {
	// Cliente HTTP con proxy apuntando a un puerto cerrado → connection refused
	transport := &http.Transport{
		Proxy: http.ProxyURL(&url.URL{Scheme: "http", Host: "127.0.0.1:1"}),
	}
	client := &http.Client{Transport: transport}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://graph.microsoft.com/v1.0/me/drive/root/delta", nil)
	if err != nil {
		t.Fatalf("error creando request: %v", err)
	}
	_, err = client.Do(req)
	if err == nil {
		t.Fatal("The broken proxy should produce an error")
	}

	// Mismo wrapping que la capa Graph (drive_item.go:66)
	wrapped := fmt.Errorf("error de red al consultar Graph: %w", err)
	if !isNetworkError(wrapped) {
		t.Errorf("isNetworkError should detect the REAL error from the broken proxy. Error: %v", err)
	}
	if !isNetworkError(err) {
		t.Errorf("Direct isNetworkError should detect the REAL error from the broken proxy. Error: %v", err)
	}
}
func TestOneCloudFS_Lookup_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"value": []}`))
	}))
	defer server.Close()

	graphClient := &graph.Client{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	}
	tokenProvider := &mockTokenProvider{token: "test_token"}
	inodeCache := NewInodeCache()

	root := NewOneCloudFS(graphClient, tokenProvider, inodeCache, &ContentCache{}, nil)

	var out fuse.EntryOut
	_, errno := root.Lookup(context.Background(), "no_existe.txt", &out)

	if errno != syscall.ENOENT {
		t.Errorf("Se esperaba errno ENOENT, obtenido %d", errno)
	}
}

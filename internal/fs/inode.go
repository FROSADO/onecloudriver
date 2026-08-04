package fs

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/frosado/onecloudriver/internal/graph"
	"github.com/hanwen/go-fuse/v2/fuse"
)

// Inode represents a OneDrive file or folder as a node of the metadata tree.
// Wraps a graph.DriveItem with thread-safe protection (RWMutex)
// and adds hierarchical child tracking.
//
// Fiel al Inode de onedriver (docs/onedriverCode/fs/inode.go), con estas
// diferencias deliberadas:
//   - No nodeID (the go-fuse/v2 framework manages IDs automatically)
//   - hasChanges: flag de dirty tracking para write-back a OneDrive
//   - localID/isLocalID: items created locally that don't exist in OneDrive yet
//   - Mode() se computa del DriveItem, sin campo mode propio (solo items de API)
type Inode struct {
	sync.RWMutex
	graph.DriveItem

	// children is nil when the children have NOT been fetched yet
	// (uninitialized). Once fetched, it is non-nil (can be an empty slice
	// if the folder is empty). This is the "nil = uninitialized" pattern
	// del onedriver original.
	children []string

	// subdir counts subdirectories for the NLink = 2 + subdir calculation (POSIX).
	subdir uint32

	// mode is the custom POSIX mode (0 = use the default for the type).
	// Updated via Setattr(chmod). If 0, Mode() returns the default
	// (S_IFDIR|0755 para carpetas, S_IFREG|0644 para archivos).
	mode uint32

	// hasChanges es true cuando el contenido local difiere del servidor.
	// Se usa para write-back: al hacer Flush/Fsync, se sube a OneDrive.
	hasChanges bool

	// ──── Phase 4: TTL+LFU eviction ────
	// Estos campos solo aplican a carpetas con children cacheados (directorios).
	// Used to calculate the eviction score and decide which children to free
	// cuando se excede maxEntries o expira el TTL.
	childrenAccessCount uint64    // contador de hits, con decay en cada sweep
	childrenLastAccess  time.Time // last time the children were accessed
	childrenCachedAt    time.Time // momento en que se poblaron los children
}

// localID prefix is used to identify items created locally that haven't
// no han sido subidos a OneDrive.
const localIDPrefix = "local-"

// isLocalID returns true if the ID was generated locally (item not yet uploaded).
func isLocalID(id string) bool {
	return len(id) > len(localIDPrefix) && id[:len(localIDPrefix)] == localIDPrefix
}

// newLocalID generates a unique ID for a locally created item.
func newLocalID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// Fallback determinista si rand falla (extremadamente improbable)
		return localIDPrefix + "fallback"
	}
	return localIDPrefix + hex.EncodeToString(b)
}

// SerializeableInode is a DTO (Data Transfer Object) for JSON/BoltDB serialization.
// Evita serializar sync.RWMutex y time.Time (que tiene campos no exportados).
type SerializeableInode struct {
	graph.DriveItem
	Children             []string `json:"children"`
	Subdir               uint32   `json:"subdir"`
	Mode                 uint32   `json:"mode"`
	HasChanges           bool     `json:"hasChanges"`
	ChildrenAccessCount  uint64   `json:"childrenAccessCount"`
	ChildrenLastAccessMs int64    `json:"childrenLastAccessMs"` // UnixMilli
	ChildrenCachedAtMs   int64    `json:"childrenCachedAtMs"`   // UnixMilli
}

// NewInodeDriveItem crea un Inode a partir de un DriveItem obtenido de la API.
func NewInodeDriveItem(item *graph.DriveItem) *Inode {
	if item == nil {
		return nil
	}
	return &Inode{
		DriveItem: *item,
	}
}

// NewInodeLocal crea un Inode para un archivo/carpeta creado localmente que
// doesn't have a representation in OneDrive yet. Uses a UUID-generated local ID.
// Faithful to onedriver's NewInode pattern.
func NewInodeLocal(name string, mode uint32, parent *Inode) *Inode {
	item := &graph.DriveItem{
		ID:   newLocalID(),
		Name: name,
	}
	if mode&syscall.S_IFDIR != 0 {
		item.Folder = &graph.Folder{}
	}
	if parent != nil {
		item.Parent = &graph.DriveItemParent{ID: parent.ID()}
	}
	return &Inode{
		DriveItem:  *item,
		hasChanges: !item.IsFolder(), // archivos nuevos: dirty hasta subir
	}
}

// HasChanges devuelve true si el contenido local difiere del servidor.
func (i *Inode) HasChanges() bool {
	i.RLock()
	defer i.RUnlock()
	return i.hasChanges
}

// SetHasChanges marca/desmarca el flag de dirty tracking.
func (i *Inode) SetHasChanges(v bool) {
	i.Lock()
	defer i.Unlock()
	i.hasChanges = v
}

// ID devuelve el ID interno del item (thread-safe).
func (i *Inode) ID() string {
	i.RLock()
	defer i.RUnlock()
	return i.DriveItem.ID
}

// Name devuelve el nombre del item (thread-safe).
func (i *Inode) Name() string {
	i.RLock()
	defer i.RUnlock()
	return i.DriveItem.Name
}

// IsDir devuelve true si el item es una carpeta.
func (i *Inode) IsDir() bool {
	i.RLock()
	defer i.RUnlock()
	return i.DriveItem.IsFolder()
}

// Mode devuelve los permisos/modo POSIX del item.
// If a custom mode has been set via chmod (Setattr),
// se devuelve ese; si no, se computa del tipo (folder vs archivo).
func (i *Inode) Mode() uint32 {
	i.RLock()
	customMode := i.mode
	i.RUnlock()
	if customMode != 0 {
		return customMode
	}
	if i.IsDir() {
		return syscall.S_IFDIR | 0755
	}
	return syscall.S_IFREG | 0644
}

// SetMode establece un modo POSIX personalizado. Usar 0 para volver al default.
func (i *Inode) SetMode(m uint32) {
	i.Lock()
	defer i.Unlock()
	i.mode = m
}

// ModTime returns the modification date as a Unix timestamp.
func (i *Inode) ModTime() uint64 {
	i.RLock()
	defer i.RUnlock()
	return i.DriveItem.ModTimeUnix()
}

// Size returns the size in bytes. For folders, returns 4096 (standard).
func (i *Inode) Size() uint64 {
	if i.IsDir() {
		return 4096
	}
	i.RLock()
	defer i.RUnlock()
	return i.DriveItem.Size
}

// NLink returns the number of hard links. For files it's 1.
// For folders: 2 + subdir (POSIX standard, faithful to the original onedriver).
func (i *Inode) NLink() uint32 {
	if i.IsDir() {
		i.RLock()
		defer i.RUnlock()
		return 2 + i.subdir
	}
	return 1
}

// ParentID devuelve el ID del padre, o "" si no tiene.
func (i *Inode) ParentID() string {
	i.RLock()
	defer i.RUnlock()
	if i.DriveItem.Parent == nil {
		return ""
	}
	return i.DriveItem.Parent.ID
}

// Path devuelve la ruta completa del item.
func (i *Inode) Path() string {
	name := i.Name()
	if i.ParentID() == "" && name == "root" {
		return "/"
	}
	return name
}

// Children devuelve los IDs de los hijos. nil = no inicializado.
func (i *Inode) Children() []string {
	i.RLock()
	defer i.RUnlock()
	return i.children
}

// HasChildren returns true if the item has real children (according to the
// Graph API or the already-fetched children). Faithful to the original onedriver
// que usa Folder.ChildCount.
//
// For local items (created with Mkdir but without fetching children yet),
// devuelve false porque ChildCount es 0.
func (i *Inode) HasChildren() bool {
	i.RLock()
	defer i.RUnlock()
	// Si el API reporta ChildCount > 0, hay hijos (aunque no se hayan consultado)
	if i.DriveItem.Folder != nil && i.DriveItem.Folder.ChildCount > 0 {
		return true
	}
	// Para items sin ChildCount (locales o API con ChildCount=0),
	// consultar la lista local si ya fue inicializada
	return len(i.children) > 0
}

// IsChildrenFetched devuelve true si los hijos ya fueron consultados de la
// API (children != nil), independientemente de si hay 0 o N hijos.
// Se usa en InodeCache para decidir cache hit vs miss.
func (i *Inode) IsChildrenFetched() bool {
	i.RLock()
	defer i.RUnlock()
	return i.children != nil
}

// SetChildren sets the children IDs. Calling with an empty slice
// indicates the folder was fetched and is empty (different from nil).
// Also initializes the eviction tracking fields (Phase 4).
func (i *Inode) SetChildren(ids []string) {
	i.Lock()
	defer i.Unlock()
	now := time.Now()
	i.children = ids
	i.childrenCachedAt = now
	i.childrenLastAccess = now
	// No reseteamos accessCount de forma intencional: la ventana de frescura
	// de GetChildren (time.Since(childrenCachedAt) < effectiveTTL) crece con
	// la frecuencia, y refetchear no debe castigarla. El decay del sweep
	// (accessCount >>= 1 cada 30s) + el cap 20× de effectiveTTL la mantienen
	// acotada a lo largo de la vida del proceso. Si es la primera vez, empieza
	// en 0.
}

// ──── Phase 4: Eviction getters/setters ────

// ChildrenAccessCount devuelve el contador de hits de los children.
func (i *Inode) ChildrenAccessCount() uint64 {
	i.RLock()
	defer i.RUnlock()
	return i.childrenAccessCount
}

// ChildrenLastAccess returns the last time the children were accessed.
func (i *Inode) ChildrenLastAccess() time.Time {
	i.RLock()
	defer i.RUnlock()
	return i.childrenLastAccess
}

// ChildrenCachedAt devuelve el momento en que se poblaron los children.
func (i *Inode) ChildrenCachedAt() time.Time {
	i.RLock()
	defer i.RUnlock()
	return i.childrenCachedAt
}

// BumpChildrenAccess incrementa el contador de hits y actualiza lastAccess.
// Thread-safe: usa el lock del Inode.
func (i *Inode) BumpChildrenAccess() {
	i.Lock()
	i.childrenAccessCount++
	i.childrenLastAccess = time.Now()
	i.Unlock()
}

// DecayChildrenAccess aplica decay al contador: accessCount >>= 1.
// Called from the eviction sweep (Phase 4).
func (i *Inode) DecayChildrenAccess() {
	i.Lock()
	i.childrenAccessCount >>= 1
	i.Unlock()
}

// EvictChildren libera los children cacheados (los pone a nil).
// The Inode still exists in the tree; only memory is freed.
func (i *Inode) EvictChildren() {
	i.Lock()
	i.children = nil
	i.Unlock()
}

// Subdir returns the number of subdirectories.
func (i *Inode) Subdir() uint32 {
	i.RLock()
	defer i.RUnlock()
	return i.subdir
}

// SetSubdir sets the number of subdirectories.
func (i *Inode) SetSubdir(n uint32) {
	i.Lock()
	defer i.Unlock()
	i.subdir = n
}

// DriveItemPtr devuelve un puntero al DriveItem interno para compatibilidad
// with code that expects *graph.DriveItem.
func (i *Inode) DriveItemPtr() *graph.DriveItem {
	return &i.DriveItem
}

// makeAttr construye un fuse.Attr completo desde el Inode para uso con FUSE.
// Fiel al original de onedriver, incluyendo Owner (UID/GID del proceso).
func (i *Inode) makeAttr() fuse.Attr {
	mtime := i.ModTime()
	return fuse.Attr{
		Ino:   0, // go-fuse assigns the Ino automatically
		Size:  i.Size(),
		Nlink: i.NLink(),
		Ctime: mtime,
		Mtime: mtime,
		Atime: mtime,
		Mode:  i.Mode(),
		Owner: fuse.Owner{
			Uid: uint32(os.Getuid()), //#nosec G115 -- UID/GID range is within uint32 on Linux systems
			Gid: uint32(os.Getgid()), //#nosec G115 -- UID/GID range is within uint32 on Linux systems
		},
	}
}

// AsJSON serializa el Inode a JSON para persistencia (BoltDB).
// ⚠️ No es MarshalJSON() — el original de onedriver documenta que implementar
// the standard interface breaks delta sync for business accounts.
func (i *Inode) AsJSON() []byte {
	i.RLock()
	defer i.RUnlock()
	data, _ := json.Marshal(SerializeableInode{
		DriveItem:            i.DriveItem,
		Children:             i.children,
		Subdir:               i.subdir,
		Mode:                 i.mode,
		HasChanges:           i.hasChanges,
		ChildrenAccessCount:  i.childrenAccessCount,
		ChildrenLastAccessMs: i.childrenLastAccess.UnixMilli(),
		ChildrenCachedAtMs:   i.childrenCachedAt.UnixMilli(),
	})
	return data
}

// fillEntryOut rellena un fuse.EntryOut con los metadatos de un DriveItem
// para que el kernel de Linux pueda cachear el lookup.
func fillEntryOut(out *fuse.EntryOut, item *graph.DriveItem) {
	if item.IsFolder() {
		out.Mode = syscall.S_IFDIR | 0755
		out.Nlink = 2
		out.Size = 4096
	} else {
		out.Mode = syscall.S_IFREG | 0644
		out.Nlink = 1
		out.Size = item.Size
	}
	out.Owner.Uid = uint32(os.Getuid()) //#nosec G115 -- UID/GID range is within uint32 on Linux systems
	out.Owner.Gid = uint32(os.Getgid()) //#nosec G115 -- UID/GID range is within uint32 on Linux systems

	if item.ModTime != nil {
		out.Mtime = item.ModTimeUnix()
		out.Atime = out.Mtime
		out.Ctime = out.Mtime
	} else {
		unix := time.Now().Unix()
		if unix < 0 {
			unix = 0
		}
		now := uint64(unix)
		out.Mtime = now
		out.Atime = now
		out.Ctime = now
	}
	out.Blksize = 4096
	out.Blocks = (out.Size + 511) / 512
	out.SetEntryTimeout(1 * time.Second)
	out.SetAttrTimeout(1 * time.Second)
}

// NewInodeJSON reconstruye un Inode desde JSON almacenado en BoltDB.
func NewInodeJSON(data []byte) (*Inode, error) {
	var raw SerializeableInode
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	return &Inode{
		DriveItem:           raw.DriveItem,
		children:            raw.Children,
		subdir:              raw.Subdir,
		mode:                raw.Mode,
		hasChanges:          raw.HasChanges,
		childrenAccessCount: raw.ChildrenAccessCount,
		childrenLastAccess:  time.UnixMilli(raw.ChildrenLastAccessMs),
		childrenCachedAt:    time.UnixMilli(raw.ChildrenCachedAtMs),
	}, nil
}

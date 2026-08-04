package fs

import (
	"context"
	"errors"
	"os"
	"syscall"
	"time"

	"github.com/frosado/onecloudriver/internal/graph"
	"github.com/frosado/onecloudriver/internal/types"
	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

// OneCloudFS represents the root folder of our filesystem.
type OneCloudFS struct {
	fs.Inode

	nodeDeps
}

// NewOneCloudFS creates a new root filesystem connected to OneDrive.
func NewOneCloudFS(
	graphClient *graph.Client,
	tokenProvider types.TokenProvider,
	inodeCache *InodeCache,
	contentCache *ContentCache,
	uploadManager *UploadManager,
) *OneCloudFS {
	return &OneCloudFS{
		nodeDeps: nodeDeps{
			graphClient:   graphClient,
			tokenProvider: tokenProvider,
			inodeCache:    inodeCache,
			contentCache:  contentCache,
			uploadManager: uploadManager,
		},
	}
}

var _ = (fs.NodeGetattrer)((*OneCloudFS)(nil))
var _ = (fs.NodeReaddirer)((*OneCloudFS)(nil))
var _ = (fs.NodeLookuper)((*OneCloudFS)(nil))
var _ = (fs.NodeMkdirer)((*OneCloudFS)(nil))
var _ = (fs.NodeRmdirer)((*OneCloudFS)(nil))
var _ = (fs.NodeUnlinker)((*OneCloudFS)(nil))
var _ = (fs.NodeRenamer)((*OneCloudFS)(nil))
var _ = (fs.NodeCreater)((*OneCloudFS)(nil))

// Getattr responde a stat sobre la carpeta RAÍZ.
func (r *OneCloudFS) Getattr(ctx context.Context, fh fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = syscall.S_IFDIR | 0755
	out.Owner.Uid = uint32(os.Getuid()) //#nosec G115 -- UID/GID range is within uint32 on Linux systems
	out.Owner.Gid = uint32(os.Getgid()) //#nosec G115 -- UID/GID range is within uint32 on Linux systems

	out.Nlink = 2
	out.Size = 4096
	out.Blksize = 4096
	unix := max(time.Now().Unix(), 0)
	now := uint64(unix)
	out.Mtime = now
	out.Atime = now
	out.Ctime = now
	return 0
}

var _ = (fs.NodeStatfser)((*OneCloudFS)(nil))

func (r *OneCloudFS) Statfs(ctx context.Context, out *fuse.StatfsOut) syscall.Errno {
	const blockSize = 4096

	out.Bsize = blockSize
	out.Frsize = blockSize

	// Valores dummy razonables.
	// Later you can compute these from the real OneDrive quota.
	out.Blocks = 1 << 30
	out.Bfree = out.Blocks
	out.Bavail = out.Blocks

	out.Files = 1 << 20
	out.Ffree = out.Files

	out.NameLen = 255

	return 0
}

// Readdir lists the contents of the OneDrive root.
func (r *OneCloudFS) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	return readdirChildren(ctx, "root", r.inodeCache, r.fetchChildren)
}

// Lookup searches for a specific file/folder in the root.
func (r *OneCloudFS) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	return r.nodeDeps.fuseLookup(ctx, r, "root", name, out)
}

// fetchChildren conecta InodeCache con Graph API, con soporte para modo offline.
func (r *OneCloudFS) fetchChildren(ctx context.Context, parentID string) ([]graph.DriveItem, error) {
	return r.nodeDeps.fetchChildrenWithOffline(ctx, parentID)
}

// isNetworkError detects transient network errors (DNS, timeout, connection
// refused/reset). Uses errors.As to traverse the %w wrapping applied by the
// Graph layer (e.g. "error de red al consultar Graph: %w" in drive_item.go):
// without this, a wrapped error would never be recognized and offline mode (Phase 3)
// would fail in production.
//
// 🔧 In addition to Timeout()/Temporary(), it explicitly checks the typical
// connection errnos (ECONNREFUSED, ECONNRESET, EHOSTUNREACH, ENETUNREACH,
// ETIMEDOUT) with errors.Is. Reason: net.OpError.Temporary() returns false for
// connection refused in Go, so a dead proxy or a cut network — which
// produce exactly ECONNREFUSED — did not activate the offline fallback
// (bug detected in the real offline test: stale subfolder → "Error en Lookup").
func isNetworkError(err error) bool {
	if err == nil {
		return false
	}

	// Tier 1: Timeout()/Temporary() (DNSError with timeout, context deadline, etc.)
	var netErr interface {
		Timeout() bool
		Temporary() bool
	}
	if errors.As(err, &netErr) && (netErr.Timeout() || netErr.Temporary()) {
		return true
	}

	// Tier 2: connection errnos. errors.Is traverses the entire wrapping chain
	// (url.Error → net.OpError → os.SyscallError → syscall.Errno).
	for _, target := range []error{
		syscall.ECONNREFUSED,
		syscall.ECONNRESET,
		syscall.EHOSTUNREACH,
		syscall.ENETUNREACH,
		syscall.ETIMEDOUT,
	} {
		if errors.Is(err, target) {
			return true
		}
	}
	return false
}

// Mkdir creates a new folder in the root.
func (r *OneCloudFS) Mkdir(ctx context.Context, name string, mode uint32, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	return r.nodeDeps.fuseMkdir(ctx, r, "root", name, mode, out)
}

// Rmdir removes an empty folder from the root.
func (r *OneCloudFS) Rmdir(ctx context.Context, name string) syscall.Errno {
	return r.nodeDeps.fuseRmdir(ctx, "root", name)
}

// Unlink removes a file from the root.
func (r *OneCloudFS) Unlink(ctx context.Context, name string) syscall.Errno {
	return r.nodeDeps.fuseUnlink(ctx, "root", name)
}

// Rename renames/moves from the root.
func (r *OneCloudFS) Rename(ctx context.Context, name string, newParent fs.InodeEmbedder, newName string, flags uint32) syscall.Errno {
	return r.nodeDeps.fuseRename(ctx, "root", name, newParent, newName, flags)
}

// Create creates a new file in the root.
func (r *OneCloudFS) Create(ctx context.Context, name string, flags uint32, mode uint32, out *fuse.EntryOut) (*fs.Inode, fs.FileHandle, uint32, syscall.Errno) {
	return r.nodeDeps.fuseCreate(ctx, r, "root", name, flags, mode, out)
}

// GetFolderID devuelve "root" (para compatibilidad con rename).
func (r *OneCloudFS) GetFolderID() string {
	return "root"
}

// resolveNewParentID extrae el ID de la carpeta destino durante un rename.
// Supports both *DriveItemNode and *OneCloudFS (root).
func resolveNewParentID(p fs.InodeEmbedder) string {
	switch n := p.(type) {
	case *DriveItemNode:
		return n.GetFolderID()
	case *OneCloudFS:
		return "root"
	default:
		return ""
	}
}

// ──── Funciones compartidas (exportadas para tests y fs_ops.go) ────

// fetchChildrenFromGraph is the shared implementation of the ChildrenFetcher.
func fetchChildrenFromGraph(ctx context.Context, client *graph.Client, tp types.TokenProvider, parentID string) ([]graph.DriveItem, error) {
	if parentID == "root" || parentID == "" {
		return client.ListDriveRoot(ctx, tp)
	}
	return client.ListChildren(ctx, tp, graph.ItemID(parentID))
}

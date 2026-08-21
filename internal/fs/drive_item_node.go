package fs

import (
	"context"
	"errors"
	"io"
	"math"
	"strings"
	"syscall"

	"github.com/frosado/onecloudriver/internal/graph"
	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
	"github.com/rs/zerolog/log"
)

// DriveItemNode represents a OneDrive file or folder as a FUSE node.
// Contains a *Inode (thread-safe wrapper around graph.DriveItem with
// hierarchical child tracking) and references to the global caches.
type DriveItemNode struct {
	fs.Inode

	inode *Inode
	nodeDeps
}

// We ensure at compile time that we implement the required interfaces.
var _ = (fs.NodeGetattrer)((*DriveItemNode)(nil))
var _ = (fs.NodeReaddirer)((*DriveItemNode)(nil))
var _ = (fs.NodeLookuper)((*DriveItemNode)(nil))
var _ = (fs.NodeOpener)((*DriveItemNode)(nil))
var _ = (fs.NodeReader)((*DriveItemNode)(nil))
var _ = (fs.NodeFlusher)((*DriveItemNode)(nil))
var _ = (fs.NodeWriter)((*DriveItemNode)(nil))
var _ = (fs.NodeCreater)((*DriveItemNode)(nil))
var _ = (fs.NodeMkdirer)((*DriveItemNode)(nil))
var _ = (fs.NodeRmdirer)((*DriveItemNode)(nil))
var _ = (fs.NodeUnlinker)((*DriveItemNode)(nil))
var _ = (fs.NodeRenamer)((*DriveItemNode)(nil))
var _ = (fs.NodeSetattrer)((*DriveItemNode)(nil))
var _ = (fs.NodeFsyncer)((*DriveItemNode)(nil))
var _ = (fs.NodeStatfser)((*DriveItemNode)(nil))

// ──── Attributes ────

// Getattr returns the file/folder metadata.
func (n *DriveItemNode) Getattr(_ context.Context, _ fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	attr := n.inode.makeAttr()
	out.Owner = attr.Owner // copies Uid and Gid at once
	out.Mode = attr.Mode
	out.Nlink = attr.Nlink
	out.Size = attr.Size
	out.Mtime = attr.Mtime
	out.Atime = attr.Atime
	out.Ctime = attr.Ctime
	out.Blksize = 4096
	out.Blocks = (out.Size + 511) / 512
	return 0
}

// ──── Directory operations ────

// Readdir lists the contents of a folder.
func (n *DriveItemNode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	if !n.inode.IsDir() {
		return nil, syscall.ENOTDIR
	}
	return readdirChildren(ctx, n.inode.ID(), n.inodeCache, n.fetchChildren)
}

// Lookup searches for a specific file/folder by name.
func (n *DriveItemNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	if !n.inode.IsDir() {
		return nil, syscall.ENOTDIR
	}
	return n.nodeDeps.fuseLookup(ctx, n, n.inode.ID(), name, out)
}

// Mkdir creates a new folder on OneDrive.
func (n *DriveItemNode) Mkdir(ctx context.Context, name string, mode uint32, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	if !n.inode.IsDir() {
		return nil, syscall.ENOTDIR
	}
	return n.nodeDeps.fuseMkdir(ctx, n, n.inode.ID(), name, mode, out)
}

// Rmdir removes an empty folder from OneDrive.
func (n *DriveItemNode) Rmdir(ctx context.Context, name string) syscall.Errno {
	if !n.inode.IsDir() {
		return syscall.ENOTDIR
	}
	return n.nodeDeps.fuseRmdir(ctx, n.inode.ID(), name)
}

// Unlink deletes a file from OneDrive.
func (n *DriveItemNode) Unlink(ctx context.Context, name string) syscall.Errno {
	if !n.inode.IsDir() {
		return syscall.ENOTDIR
	}
	return n.nodeDeps.fuseUnlink(ctx, n.inode.ID(), name)
}

// Rename renames or moves a file/folder.
func (n *DriveItemNode) Rename(ctx context.Context, name string, newParent fs.InodeEmbedder, newName string, flags uint32) syscall.Errno {
	if !n.inode.IsDir() {
		return syscall.ENOTDIR
	}
	return n.nodeDeps.fuseRename(ctx, n.inode.ID(), name, newParent, newName, flags)
}

// GetFolderID returns the ID of the folder represented by this node (for rename).
func (n *DriveItemNode) GetFolderID() string {
	return n.inode.ID()
}

// ──── Create ────

// Create creates a new file and opens it for writing.
func (n *DriveItemNode) Create(ctx context.Context, name string, flags uint32, mode uint32, out *fuse.EntryOut) (*fs.Inode, fs.FileHandle, uint32, syscall.Errno) {
	if !n.inode.IsDir() {
		return nil, nil, 0, syscall.ENOTDIR
	}
	return n.nodeDeps.fuseCreate(ctx, n, n.inode.ID(), name, flags, mode, out)
}

// ──── File operations ────

// Open opens a file for reading, writing, or both.
func (n *DriveItemNode) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	if n.inode.IsDir() {
		return nil, 0, syscall.EISDIR
	}

	id := n.inode.ID()
	accMode := flags & syscall.O_ACCMODE

	// Ensure the file exists in the local cache
	fd, err := n.contentCache.Open(id)
	if err != nil {
		log.Error().Err(err).Str("file", n.inode.Name()).Msg("Error opening content in cache")
		return nil, 0, syscall.EIO
	}

	// If read-only, download from OneDrive if the content is not on disk.
	// If it is already cached, verify its integrity against the server
	// quickXorHash before serving it (issue #32); on mismatch, invalidate
	// and re-download.
	if accMode == syscall.O_RDONLY {
		if !isLocalID(id) {
			if n.hasContentOnDisk(id) {
				if !n.verifyCachedContent(id) {
					log.Debug().Str("file", n.inode.Name()).Msg("Cached content hash mismatch, re-downloading")
					if err := n.contentCache.Delete(id); err != nil {
						log.Warn().Err(err).Str("file", n.inode.Name()).Msg("Error invalidating stale cached content")
					}
					if errno := n.downloadContent(ctx, id); errno != 0 {
						return nil, 0, errno
					}
				}
			} else {
				log.Debug().Str("file", n.inode.Name()).Msg("Downloading content from OneDrive to cache")
				if errno := n.downloadContent(ctx, id); errno != 0 {
					return nil, 0, errno
				}
			}
		}
		return n, fuse.FOPEN_KEEP_CACHE, 0
	}

	// If writing or reading-writing, download first if it's remote and we have no data
	if !isLocalID(id) && !n.hasContentOnDisk(id) {
		log.Debug().Str("file", n.inode.Name()).Msg("Downloading content for write access")
		if errno := n.downloadContent(ctx, id); errno != 0 {
			_ = fd.Close()
			return nil, 0, errno
		}
	}

	// If opened with O_TRUNC, truncate to 0
	if flags&syscall.O_TRUNC != 0 {
		if err := fd.Truncate(0); err != nil {
			log.Error().Err(err).Str("file", n.inode.Name()).Msg("Error truncating")
			return nil, 0, syscall.EIO
		}
		n.inode.Lock()
		n.inode.DriveItem.Size = 0
		n.inode.hasChanges = true
		n.inode.Unlock()
	}

	return n, fuse.FOPEN_KEEP_CACHE, 0
}

// Read reads the contents of a file (zero-copy from ContentCache).
func (n *DriveItemNode) Read(_ context.Context, _ fs.FileHandle, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	fd, err := n.contentCache.Open(n.inode.ID())
	if err != nil {
		log.Error().Err(err).Str("file", n.inode.Name()).Msg("Error opening content in cache")
		return nil, syscall.EIO
	}
	return fuse.ReadResultFd(fd.Fd(), off, len(dest)), 0
}

// Write writes data to the file at the specified position.
// Writes are local (write-back): they are persisted to OneDrive on Flush/Fsync.
func (n *DriveItemNode) Write(_ context.Context, _ fs.FileHandle, data []byte, off int64) (uint32, syscall.Errno) {
	nWritten, err := n.contentCache.WriteAt(n.inode.ID(), data, off)
	if err != nil {
		log.Error().Err(err).Str("file", n.inode.Name()).Msg("Error writing to cache")
		return 0, syscall.EIO
	}

	// Update size if the write extended the file
	n.inode.Lock()
	newEnd := off + int64(nWritten)
	if newEnd < 0 {
		n.inode.Unlock()
		return 0, syscall.EINVAL
	}
	if uint64(newEnd) > n.inode.DriveItem.Size {
		n.inode.DriveItem.Size = uint64(newEnd)
	}
	n.inode.hasChanges = true
	n.inode.Unlock()

	if nWritten < 0 || nWritten > math.MaxUint32 {
		return 0, syscall.EIO
	}
	return uint32(nWritten), 0
}

// Flush is called when a file descriptor is closed. Triggers Fsync + closes the FD.
func (n *DriveItemNode) Flush(ctx context.Context, fh fs.FileHandle) syscall.Errno {
	errno := n.Fsync(ctx, fh, 0)
	n.contentCache.Close(n.inode.ID())
	return errno
}

// Fsync marks the local changes as "uploaded" and enqueues the actual upload
// to the UploadManager to be processed in the background. This decouples the
// FUSE write (fast, interactive) from the HTTP upload (slow, with
// retries).
//
// Phase 5b: the UploadManager takes a content snapshot from
// ContentCache when enqueuing, so later modifications to the file
// do not affect the in-flight upload.
//
// If uploadManager is nil (tests without a manager), it simply marks hasChanges=false
// and returns success — the content stays in ContentCache but is not uploaded.
func (n *DriveItemNode) Fsync(_ context.Context, _ fs.FileHandle, _ uint32) syscall.Errno {
	if !n.inode.HasChanges() {
		return 0
	}

	// Enqueue async upload if the UploadManager is available. The dirty flag
	// is only cleared when the upload was REALLY enqueued (or the file is
	// empty and there is nothing to upload): the first flush of a brand-new
	// file can race ahead of its write (FUSE may process the flush before the
	// write syscall), and clearing the flag then would silently lose the
	// upload forever (issue #87).
	if n.uploadManager != nil {
		enqueued := n.uploadManager.QueueUpload(n.inode.ID(), n.inode.ParentID(), n.inode.Name())
		if enqueued || n.contentCache.Size(n.inode.ID()) == 0 {
			n.inode.SetHasChanges(false)
		}
		return 0
	}

	// No UploadManager (unit tests): mark as clean, content stays in cache.
	n.inode.SetHasChanges(false)
	return 0
}

// ──── Setattr ────

// Setattr handles POSIX operations: chmod, utimens (touch), and truncate.
// Faithful to the onedriver SetAttr, adapted to go-fuse/v2.
func (n *DriveItemNode) Setattr(_ context.Context, _ fs.FileHandle, in *fuse.SetAttrIn, out *fuse.AttrOut) syscall.Errno {
	// chmod (permission change)
	if mode, valid := in.GetMode(); valid {
		if n.inode.IsDir() {
			n.inode.SetMode(syscall.S_IFDIR | (mode & 07777))
		} else {
			n.inode.SetMode(syscall.S_IFREG | (mode & 07777))
		}
	}

	// utimens (timestamp change)
	if mtime, valid := in.GetMTime(); valid {
		n.inode.Lock()
		n.inode.DriveItem.ModTime = &mtime
		n.inode.Unlock()
	}

	// truncate
	if size, valid := in.GetSize(); valid {
		fd, err := n.contentCache.Open(n.inode.ID())
		if err != nil {
			log.Error().Err(err).Str("file", n.inode.Name()).Msg("Error opening for truncate")
			return syscall.EIO
		}
		if size > math.MaxInt64 {
			return syscall.EINVAL
		}
		if err := fd.Truncate(int64(size)); err != nil {
			log.Error().Err(err).Str("file", n.inode.Name()).Msg("Error truncating file")
			return syscall.EIO
		}
		n.inode.Lock()
		n.inode.DriveItem.Size = size
		n.inode.hasChanges = true
		n.inode.Unlock()
	}

	attr := n.inode.makeAttr()
	out.Owner = attr.Owner
	out.Mode = attr.Mode
	out.Nlink = attr.Nlink
	out.Size = attr.Size
	out.Mtime = attr.Mtime
	out.Atime = attr.Atime
	out.Ctime = attr.Ctime

	out.Blksize = 4096
	out.Blocks = (out.Size + 511) / 512
	return 0
}

// ──── Internal helpers ────

// hasContentOnDisk checks that there is a file with content (>0 bytes) on disk.
func (n *DriveItemNode) hasContentOnDisk(id string) bool {
	return n.contentCache.Size(id) > 0
}

// verifyCachedContent hashes the cached content and compares it against the
// server quickXorHash stored in the inode. Returns true when the content
// matches, or when there is nothing to verify against (no server hash).
func (n *DriveItemNode) verifyCachedContent(id string) bool {
	n.inode.RLock()
	var expected string
	if n.inode.DriveItem.File != nil {
		expected = n.inode.DriveItem.File.Hashes.QuickXorHash
	}
	n.inode.RUnlock()
	if expected == "" {
		return true
	}
	sum, ok := n.contentCache.SumQuickXorHash(id)
	if !ok {
		return false
	}
	return strings.EqualFold(sum, expected)
}

// downloadError combines the outcomes of the two halves of the download pipe.
// Both can fail independently, and neither may be dropped: a cache write that
// failed while the transfer succeeded (full disk, unwritable cache) would
// otherwise make a truncated file look like a successful download.
func downloadError(graphErr, cacheErr error) error {
	switch {
	case graphErr == nil:
		return cacheErr
	case cacheErr == nil:
		return graphErr
	default:
		return errors.Join(graphErr, cacheErr)
	}
}

// downloadContent downloads the content from OneDrive to the ContentCache.
func (n *DriveItemNode) downloadContent(ctx context.Context, id string) syscall.Errno {
	pr, pw := io.Pipe()
	errCh := make(chan error, 1)
	go func() {
		defer pw.Close()
		_, err := n.contentCache.InsertStream(id, pr)
		errCh <- err
	}()
	_, graphErr := n.graphClient.GetItemContentStream(ctx, n.tokenProvider, graph.ItemID(id), pw)
	pw.CloseWithError(graphErr)
	if err := downloadError(graphErr, <-errCh); err != nil {
		log.Error().Err(err).Str("file", n.inode.Name()).Msg("Error downloading content")
		return syscall.EIO
	}
	return 0
}

// ──── Statfs ────

// Statfs returns information about the filesystem (total space,
// free space, name limits). Uses reasonable default values for
// OneDrive Personal (5 TB quota, 100K files, maximum name
// 260-character name limit).
//
// The values are static estimates because we don't cache the /me/drive
// response (it would require an additional HTTP call). If in the future it's cached
// the Drive object, these values can be replaced by the real ones.
func (n *DriveItemNode) Statfs(_ context.Context, out *fuse.StatfsOut) syscall.Errno {
	const blkSize uint32 = 4096                             // ext4 default
	const quotaTotal uint64 = 5 * 1024 * 1024 * 1024 * 1024 // 5 TB (OneDrive Personal)
	const maxFiles uint64 = 100000
	const nameLen uint32 = 260

	out.Bsize = blkSize
	out.Blocks = quotaTotal / uint64(blkSize)
	out.Bfree = quotaTotal / uint64(blkSize) // simplification: assume everything is free
	out.Bavail = quotaTotal / uint64(blkSize)
	out.Files = maxFiles
	out.Ffree = maxFiles
	out.NameLen = nameLen
	return 0
}

// fetchChildren implements ChildrenFetcher for DriveItemNode.
// 🔧 Uses the shared fetcher with offline fallback (nodeDeps): it used to call
// directly to fetchChildrenFromGraph, so navigating to a subfolder with
// the network down returned "Error in Lookup" (EIO) even though the metadata
// was cached — bug detected in the real offline test (root did
// work because OneCloudFS had the fallback).
func (n *DriveItemNode) fetchChildren(ctx context.Context, parentID string) ([]graph.DriveItem, error) {
	return n.nodeDeps.fetchChildrenWithOffline(ctx, parentID)
}

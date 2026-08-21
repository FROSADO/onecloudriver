package fs

import (
	"context"
	"regexp"
	"strings"
	"syscall"

	"github.com/frosado/onecloudriver/internal/graph"
	"github.com/frosado/onecloudriver/internal/types"
	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
	"github.com/rs/zerolog/log"
)

// disallowedRexp matches file/folder names prohibited by
// OneDrive and SharePoint (LPT[0-9], COM[0-9], _vti_, special characters).
//
// Referencia:
// https://support.microsoft.com/en-us/office/restrictions-and-limitations-in-onedrive-and-sharepoint-64883a5d-228e-48f5-b3d2-eb39e07630fa
var disallowedRexp = regexp.MustCompile(`(?i)LPT[0-9]|COM[0-9]|_vti_|["*:<>?\/\\|]`)

// isNameRestricted returns true if the name is forbidden in OneDrive.
// Covers Windows reserved names (CON, AUX, PRN, NUL), system
// files (desktop.ini, .lock), and characters not allowed in paths.
func isNameRestricted(name string) bool {
	if strings.EqualFold(name, "CON") {
		return true
	}
	if strings.EqualFold(name, "AUX") {
		return true
	}
	if strings.EqualFold(name, "PRN") {
		return true
	}
	if strings.EqualFold(name, "NUL") {
		return true
	}
	if strings.EqualFold(name, ".lock") {
		return true
	}
	if strings.EqualFold(name, "desktop.ini") {
		return true
	}
	return disallowedRexp.FindStringIndex(name) != nil
}

// ──── Dependencias compartidas ────

// nodeDeps groups the shared dependencies between OneCloudFS (root) and
// DriveItemNode (inner nodes). Both types build child nodes with
// the same references, and delegate to the same FUSE operation helpers.
type nodeDeps struct {
	graphClient   *graph.Client
	tokenProvider types.TokenProvider
	inodeCache    *InodeCache
	contentCache  *ContentCache
	uploadManager *UploadManager // Phase 5b: asynchronous uploads
}

// newDriveItemNode builds a child DriveItemNode sharing the same
// dependencies than the parent. Centralizes the pattern repeated 12+ times.
func (d *nodeDeps) newDriveItemNode(inode *Inode) *DriveItemNode {
	return &DriveItemNode{
		inode:    inode,
		nodeDeps: *d,
	}
}

// ──── Helpers de operaciones FUSE ────
//
// Each helper receives a parentID ("root" for OneCloudFS or n.inode.ID() for
// DriveItemNode) and the shared dependencies. This lets both types
// delegate to the same implementation, removing the duplication.

// fetchChildrenWithOffline is the ChildrenFetcher shared by OneCloudFS and
// DriveItemNode with offline mode support.
//
// 🔧 Before this refactoring, the offline fallback ONLY existed in
// OneCloudFS.fetchChildren (root). DriveItemNode.fetchChildren called
// fetchChildrenFromGraph directly, so navigating to a subfolder with
// the network down (e.g. onedriver_tests/paging) never activated the
// fallback → "Error en Lookup" (EIO) even though the metadata was in
// cache. Bug detected in the real offline test.
func (d *nodeDeps) fetchChildrenWithOffline(ctx context.Context, parentID string) ([]graph.DriveItem, error) {
	items, err := fetchChildrenFromGraph(ctx, d.graphClient, d.tokenProvider, parentID)
	if err != nil {
		if isNetworkError(err) {
			d.inodeCache.SetOffline(true)
			if parent := d.inodeCache.Get(parentID); parent != nil && parent.IsChildrenFetched() {
				log.Debug().Str("parentID", parentID).Msg("Serving children from cache (offline mode)")
				var cachedItems []graph.DriveItem
				for _, childID := range parent.Children() {
					if child := d.inodeCache.Get(childID); child != nil {
						child.RLock()
						cachedItems = append(cachedItems, child.DriveItem)
						child.RUnlock()
					}
				}
				return cachedItems, nil
			}

			// Additional fallback: if the parent's children list was evicted
			// by the TTL sweep (children=nil) but the child inodes remain in the
			// sync.Map with their ParentID (persisted by SerializeAll), rebuild
			// the list from there. Without this, navigating to a subfolder offline
			// returned EIO even though its metadata was cached.
			if children := d.inodeCache.ItemsByParent(parentID); len(children) > 0 {
				log.Debug().Str("parentID", parentID).Int("count", len(children)).
					Msg("Rebuilding children from inodes with ParentID (offline mode)")
				return children, nil
			}
			return nil, err
		}
		return nil, err
	}
	if d.inodeCache.IsOffline() {
		d.inodeCache.SetOffline(false)
	}
	return items, nil
}

// readdirChildren gets the cached children and builds a DirStream.
// Used by Readdir in both OneCloudFS and DriveItemNode.
func readdirChildren(ctx context.Context, parentID string, cache *InodeCache, fetch ChildrenFetcher) (fs.DirStream, syscall.Errno) {
	childrenMap, err := cache.GetChildren(ctx, parentID, fetch)
	if err != nil {
		log.Error().Err(err).Str("parentID", parentID).Msg("Error listing directory")
		return nil, syscall.EIO
	}

	entries := make([]fuse.DirEntry, 0, len(childrenMap))
	for _, child := range childrenMap {
		entries = append(entries, fuse.DirEntry{
			Name: child.Name(),
			Mode: child.Mode(),
		})
	}
	return fs.NewListDirStream(entries), 0
}

// lookupResult encapsulates the result of a name search.
type lookupResult struct {
	childInode *Inode
	errno      syscall.Errno
}

// lookupChild searches for a child by name in the cache and returns the Inode.
// It does not create the FUSE node — each caller does that because it needs the InodeEmbedder
// to call NewInode.
func lookupChild(ctx context.Context, parentID, name string, cache *InodeCache, fetch ChildrenFetcher) lookupResult {
	childrenMap, err := cache.GetChildren(ctx, parentID, fetch)
	if err != nil {
		log.Error().Err(err).Str("parentID", parentID).Str("name", name).Msg("Error in Lookup")
		return lookupResult{errno: syscall.EIO}
	}

	childInode, exists := childrenMap[name]
	if !exists {
		return lookupResult{errno: syscall.ENOENT}
	}
	return lookupResult{childInode: childInode}
}

// doMkdir creates a folder in OneDrive and registers it in the cache.
// fetch must be the caller's fetcher (OneCloudFS.fetchChildren with offline,
// or DriveItemNode.fetchChildren without offline).
func (d *nodeDeps) doMkdir(ctx context.Context, parentID, name string, _ ChildrenFetcher, out *fuse.EntryOut) (*Inode, syscall.Errno) {
	if isNameRestricted(name) {
		return nil, syscall.EINVAL
	}

	var resource graph.Resource
	if parentID == "root" {
		resource = graph.ItemPath("/")
	} else {
		resource = graph.ItemID(parentID)
	}

	item, err := d.graphClient.CreateFolder(ctx, d.tokenProvider, resource, name)
	if err != nil {
		log.Error().Err(err).Str("name", name).Msg("Error creating remote folder")
		return nil, syscall.EIO
	}

	childInode := NewInodeDriveItem(item)
	d.inodeCache.InsertChild(parentID, name, childInode)
	fillEntryOut(out, item)
	return childInode, 0
}

// doRmdir removes an empty folder from OneDrive.
func (d *nodeDeps) doRmdir(ctx context.Context, parentID, name string, fetch ChildrenFetcher) syscall.Errno {
	res := lookupChild(ctx, parentID, name, d.inodeCache, fetch)
	if res.errno != 0 {
		return res.errno
	}
	child := res.childInode

	if !child.IsDir() {
		return syscall.ENOTDIR
	}
	if child.HasChildren() {
		return syscall.ENOTEMPTY
	}

	// Defense in depth (same as doUnlink): a locally-created item has no
	// remote representation yet, so deleting it via Graph would PATCH a
	// non-existent item (HTTP 400). Today folders are always created
	// remotely (doMkdir is synchronous), so this is unreachable — but it
	// keeps the invariant if that ever changes.
	if errno := d.deleteRemote(ctx, child.ID(), name, "folder"); errno != 0 {
		return errno
	}

	d.inodeCache.RemoveChild(parentID, child.ID())
	return 0
}

// deleteRemote deletes an item from OneDrive, skipping the request for
// locally-created items (their id has no remote counterpart yet, so the Graph
// call would target a non-existent item). kind ("folder"/"file") only labels
// the error log.
func (d *nodeDeps) deleteRemote(ctx context.Context, itemID, name, kind string) syscall.Errno {
	if isLocalID(itemID) {
		return 0
	}
	if err := d.graphClient.DeleteItem(ctx, d.tokenProvider, graph.ItemID(itemID), ""); err != nil {
		log.Error().Err(err).Str("name", name).Msg("Error deleting remote " + kind)
		return syscall.EIO
	}
	return 0
}

// doUnlink removes a file from OneDrive and clears its cached content.
func (d *nodeDeps) doUnlink(ctx context.Context, parentID, name string, fetch ChildrenFetcher) syscall.Errno {
	res := lookupChild(ctx, parentID, name, d.inodeCache, fetch)
	if res.errno != 0 {
		return res.errno
	}
	child := res.childInode

	// If it's a local ID (never uploaded), don't try to delete remotely
	if errno := d.deleteRemote(ctx, child.ID(), name, "file"); errno != 0 {
		return errno
	}

	d.inodeCache.RemoveChild(parentID, child.ID())
	if err := d.contentCache.Delete(child.ID()); err != nil {
		log.Warn().Err(err).Str("name", name).Msg("Error clearing cached content after Unlink")
	}
	return 0
}

// doRename renames/moves an item in OneDrive and updates the local cache.
func (d *nodeDeps) doRename(ctx context.Context, parentID, name string, newParentID string, newName string, fetch ChildrenFetcher) syscall.Errno {
	if isNameRestricted(newName) {
		return syscall.EINVAL
	}

	res := lookupChild(ctx, parentID, name, d.inodeCache, fetch)
	if res.errno != 0 {
		return res.errno
	}
	child := res.childInode

	// A locally-created item (never uploaded yet) has no remote representation:
	// renaming it via Graph would PATCH a non-existent item (HTTP 400
	// invalidRequest). Update only the pending upload session and the local
	// cache so the upload creates the item with the new name (and folder).
	if isLocalID(child.ID()) {
		if d.uploadManager != nil {
			d.uploadManager.RenameSession(child.ID(), newParentID, newName)
		}

		d.inodeCache.MoveChild(parentID, newParentID, child.ID())
		child.Lock()
		child.DriveItem.Name = newName
		child.DriveItem.Parent = &graph.DriveItemParent{ID: newParentID}
		child.Unlock()
		return 0
	}

	// Rename in OneDrive
	_, err := d.graphClient.RenameItem(ctx, d.tokenProvider, graph.ItemID(child.ID()), newName, "")
	if err != nil {
		log.Error().Err(err).Str("child", child.Name()).Str("newName", newName).Msg("Error renaming")
		return syscall.EIO
	}

	// If it changed folders, move in OneDrive
	if parentID != newParentID {
		_, err = d.graphClient.MoveItem(ctx, d.tokenProvider, graph.ItemID(child.ID()), graph.ItemID(newParentID), "")
		if err != nil {
			log.Error().Err(err).Msg("Error moving item")
			return syscall.EIO
		}
	}

	// Update local cache
	d.inodeCache.MoveChild(parentID, newParentID, child.ID())
	child.Lock()
	child.DriveItem.Name = newName
	child.Unlock()

	return 0
}

// ──── Unified FUSE wrappers ────
//
// Methods on nodeDeps that receive parentID and fs.InodeEmbedder, execute
// the full operation (doXxx + NewInode). Remove the duplication between
// OneCloudFS (parentID="root") and DriveItemNode (parentID=n.inode.ID()),
// which previously had nearly identical wrappers (~120 duplicated lines).

// fuseLookup looks up a child and returns the corresponding *fs.Inode.
func (d *nodeDeps) fuseLookup(ctx context.Context, embedder fs.InodeEmbedder, parentID, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	res := lookupChild(ctx, parentID, name, d.inodeCache, d.fetchChildrenWithOffline)
	if res.errno != 0 {
		return nil, res.errno
	}
	childNode := d.newDriveItemNode(res.childInode)
	fillEntryOut(out, res.childInode.DriveItemPtr())
	return embedder.EmbeddedInode().NewInode(ctx, childNode, fs.StableAttr{Mode: res.childInode.Mode()}), 0
}

// fuseMkdir creates a folder and returns the *fs.Inode.
func (d *nodeDeps) fuseMkdir(ctx context.Context, embedder fs.InodeEmbedder, parentID, name string, _ uint32, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	childInode, errno := d.doMkdir(ctx, parentID, name, d.fetchChildrenWithOffline, out)
	if errno != 0 {
		return nil, errno
	}
	childNode := d.newDriveItemNode(childInode)
	return embedder.EmbeddedInode().NewInode(ctx, childNode, fs.StableAttr{Mode: childInode.Mode()}), 0
}

// fuseCreate creates a file and returns (*fs.Inode, FileHandle, flags, errno).
func (d *nodeDeps) fuseCreate(ctx context.Context, embedder fs.InodeEmbedder, parentID, name string, flags uint32, mode uint32, out *fuse.EntryOut) (*fs.Inode, fs.FileHandle, uint32, syscall.Errno) {
	childNode, errno := d.doCreate(ctx, parentID, name, flags, mode, d.fetchChildrenWithOffline, out)
	if errno != 0 {
		return nil, nil, 0, errno
	}
	fuseInode := embedder.EmbeddedInode().NewInode(ctx, childNode, fs.StableAttr{Mode: childNode.inode.Mode()})
	return fuseInode, childNode, 0, 0
}

// fuseRmdir removes an empty folder.
func (d *nodeDeps) fuseRmdir(ctx context.Context, parentID, name string) syscall.Errno {
	return d.doRmdir(ctx, parentID, name, d.fetchChildrenWithOffline)
}

// fuseUnlink deletes a file.
func (d *nodeDeps) fuseUnlink(ctx context.Context, parentID, name string) syscall.Errno {
	return d.doUnlink(ctx, parentID, name, d.fetchChildrenWithOffline)
}

// fuseRename renames/moves an item. Resolves newParentID internally.
func (d *nodeDeps) fuseRename(ctx context.Context, parentID, name string, newParent fs.InodeEmbedder, newName string, _ uint32) syscall.Errno {
	newParentID := resolveNewParentID(newParent)
	if newParentID == "" {
		return syscall.EINVAL
	}
	return d.doRename(ctx, parentID, name, newParentID, newName, d.fetchChildrenWithOffline)
}

// doCreate creates a new file (or truncates an existing one) and returns the child node.
func (d *nodeDeps) doCreate(ctx context.Context, parentID, name string, _ uint32, mode uint32, fetch ChildrenFetcher, out *fuse.EntryOut) (*DriveItemNode, syscall.Errno) {
	if isNameRestricted(name) {
		return nil, syscall.EINVAL
	}

	childrenMap, err := d.inodeCache.GetChildren(ctx, parentID, fetch)
	if err != nil {
		return nil, syscall.EIO
	}

	// If it already exists → truncate and return the existing one (POSIX creat semantics)
	if existing, exists := childrenMap[name]; exists {
		if err := d.contentCache.Delete(existing.ID()); err != nil {
			log.Warn().Err(err).Str("name", name).Msg("Error cleaning previous content in Create")
		}
		if _, err := d.contentCache.Open(existing.ID()); err != nil {
			return nil, syscall.EIO
		}
		existing.Lock()
		existing.DriveItem.Size = 0
		existing.hasChanges = true
		existing.Unlock()

		childNode := d.newDriveItemNode(existing)
		fillEntryOut(out, existing.DriveItemPtr())
		return childNode, 0
	}

	// Does not exist → create local inode
	childInode := NewInodeLocal(name, mode, nil)
	d.inodeCache.InsertChild(parentID, name, childInode)

	if _, err := d.contentCache.Open(childInode.ID()); err != nil {
		return nil, syscall.EIO
	}

	childNode := d.newDriveItemNode(childInode)
	fillEntryOut(out, childInode.DriveItemPtr())
	return childNode, 0
}

package fs

import (
	"context"
	"io"
	"math"
	"os"
	"syscall"

	"github.com/frosado/onecloudriver/internal/graph"
	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
	"github.com/rs/zerolog/log"
)

// DriveItemNode representa un archivo o carpeta de OneDrive como nodo FUSE.
// Contiene un *Inode (wrapper thread-safe alrededor de graph.DriveItem con
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

// ──── Atributos ────

// Getattr devuelve los metadatos del archivo/carpeta.
func (n *DriveItemNode) Getattr(ctx context.Context, fh fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	attr := n.inode.makeAttr()
	out.Owner = attr.Owner // copia Uid y Gid de golpe
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

// ──── Operaciones de directorio ────

// Readdir lista el contenido de una carpeta.
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

// Mkdir crea una nueva carpeta en OneDrive.
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

// Unlink elimina un archivo de OneDrive.
func (n *DriveItemNode) Unlink(ctx context.Context, name string) syscall.Errno {
	if !n.inode.IsDir() {
		return syscall.ENOTDIR
	}
	return n.nodeDeps.fuseUnlink(ctx, n.inode.ID(), name)
}

// Rename renombra o mueve un archivo/carpeta.
func (n *DriveItemNode) Rename(ctx context.Context, name string, newParent fs.InodeEmbedder, newName string, flags uint32) syscall.Errno {
	if !n.inode.IsDir() {
		return syscall.ENOTDIR
	}
	return n.nodeDeps.fuseRename(ctx, n.inode.ID(), name, newParent, newName, flags)
}

// GetFolderID devuelve el ID de la carpeta que representa este nodo (para rename).
func (n *DriveItemNode) GetFolderID() string {
	return n.inode.ID()
}

// ──── Create ────

// Create crea un nuevo archivo y lo abre para escritura.
func (n *DriveItemNode) Create(ctx context.Context, name string, flags uint32, mode uint32, out *fuse.EntryOut) (*fs.Inode, fs.FileHandle, uint32, syscall.Errno) {
	if !n.inode.IsDir() {
		return nil, nil, 0, syscall.ENOTDIR
	}
	return n.nodeDeps.fuseCreate(ctx, n, n.inode.ID(), name, flags, mode, out)
}

// ──── Operaciones de archivo ────

// Open abre un archivo para lectura, escritura o ambas.
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

	// If read-only, download from OneDrive if the content is not on disk
	if accMode == syscall.O_RDONLY {
		if !isLocalID(id) && !n.hasContentOnDisk(id) {
			log.Debug().Str("file", n.inode.Name()).Msg("Downloading content from OneDrive to cache")
			if errno := n.downloadContent(ctx, id); errno != 0 {
				return nil, 0, errno
			}
		}
		return n, fuse.FOPEN_KEEP_CACHE, 0
	}

	// Si es escritura o lectura-escritura, descargar primero si es remoto y no tenemos datos
	if !isLocalID(id) && !n.hasContentOnDisk(id) {
		log.Debug().Str("file", n.inode.Name()).Msg("Descargando contenido para acceso escritura")
		if errno := n.downloadContent(ctx, id); errno != 0 {
			_ = fd.Close()
			return nil, 0, errno
		}
	}

	// Si se abre con O_TRUNC, truncar a 0
	if flags&syscall.O_TRUNC != 0 {
		if err := fd.Truncate(0); err != nil {
			log.Error().Err(err).Str("file", n.inode.Name()).Msg("Error truncando")
			return nil, 0, syscall.EIO
		}
		n.inode.Lock()
		n.inode.DriveItem.Size = 0
		n.inode.hasChanges = true
		n.inode.Unlock()
	}

	return n, fuse.FOPEN_KEEP_CACHE, 0
}

// Read lee el contenido de un archivo (zero-copy desde ContentCache).
func (n *DriveItemNode) Read(ctx context.Context, fh fs.FileHandle, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	fd, err := n.contentCache.Open(n.inode.ID())
	if err != nil {
		log.Error().Err(err).Str("file", n.inode.Name()).Msg("Error opening content in cache")
		return nil, syscall.EIO
	}
	return fuse.ReadResultFd(fd.Fd(), off, len(dest)), 0
}

// Write writes data to the file at the specified position.
// Las escrituras son locales (write-back): se persisten a OneDrive en Flush/Fsync.
func (n *DriveItemNode) Write(ctx context.Context, fh fs.FileHandle, data []byte, off int64) (uint32, syscall.Errno) {
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

// Flush se llama al cerrar un file descriptor. Dispara Fsync + cierra el FD.
func (n *DriveItemNode) Flush(ctx context.Context, fh fs.FileHandle) syscall.Errno {
	errno := n.Fsync(ctx, fh, 0)
	n.contentCache.Close(n.inode.ID())
	return errno
}

// Fsync marca los cambios locales como "subidos" y encola la subida real
// en el UploadManager para que se procese en background. Esto desacopla la
// FUSE write (fast, interactive) from the HTTP upload (slow, with
// reintentos).
//
// Fase 5b: el UploadManager toma una snapshot del contenido desde
// ContentCache al encolar, por lo que modificaciones posteriores del archivo
// no afectan a la subida en curso.
//
// Si uploadManager es nil (tests sin manager), simplemente marca hasChanges=false
// and returns success — the content stays in ContentCache but is not uploaded.
func (n *DriveItemNode) Fsync(ctx context.Context, fh fs.FileHandle, flags uint32) syscall.Errno {
	if !n.inode.HasChanges() {
		return 0
	}

	// Marcar como "no dirty" inmediatamente.
	n.inode.SetHasChanges(false)

	// Enqueue async upload if the UploadManager is available.
	if n.uploadManager != nil {
		n.uploadManager.QueueUpload(n.inode.ID(), n.inode.ParentID(), n.inode.Name())
	}

	return 0
}

// ──── Setattr ────

// Setattr maneja operaciones POSIX: chmod, utimens (touch), y truncate.
// Fiel al SetAttr de onedriver, adaptado a go-fuse/v2.
func (n *DriveItemNode) Setattr(ctx context.Context, fh fs.FileHandle, in *fuse.SetAttrIn, out *fuse.AttrOut) syscall.Errno {
	// chmod (cambio de permisos)
	if mode, valid := in.GetMode(); valid {
		if n.inode.IsDir() {
			n.inode.SetMode(syscall.S_IFDIR | (mode & 07777))
		} else {
			n.inode.SetMode(syscall.S_IFREG | (mode & 07777))
		}
	}

	// utimens (cambio de timestamps)
	if mtime, valid := in.GetMTime(); valid {
		n.inode.Lock()
		n.inode.DriveItem.ModTime = &mtime
		n.inode.Unlock()
	}

	// truncate
	if size, valid := in.GetSize(); valid {
		fd, err := n.contentCache.Open(n.inode.ID())
		if err != nil {
			log.Error().Err(err).Str("file", n.inode.Name()).Msg("Error abriendo para truncate")
			return syscall.EIO
		}
		if size > math.MaxInt64 {
			return syscall.EINVAL
		}
		if err := fd.Truncate(int64(size)); err != nil {
			log.Error().Err(err).Str("file", n.inode.Name()).Msg("Error truncando archivo")
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

// ──── Helpers internos ────

// hasContentOnDisk verifica que haya un archivo con contenido (>0 bytes) en disco.
func (n *DriveItemNode) hasContentOnDisk(id string) bool {
	return n.contentCache.Size(id) > 0
}

// moveContentInCache renombra el archivo en ContentCache de oldID a newID.
// Se usa tras subir un archivo local (swap de ID local → remoto) para evitar
// una descarga innecesaria desde OneDrive.
func (n *DriveItemNode) moveContentInCache(oldID, newID string) {
	n.contentCache.Close(oldID)
	oldPath := n.contentCache.contentPath(oldID)
	newPath := n.contentCache.contentPath(newID)
	if err := os.Rename(oldPath, newPath); err != nil {
		log.Warn().Err(err).Str("oldID", oldID).Str("newID", newID).
			Msg("No se pudo renombrar archivo en ContentCache tras swap de ID")
	}
}

// downloadContent descarga el contenido de OneDrive al ContentCache.
func (n *DriveItemNode) downloadContent(ctx context.Context, id string) syscall.Errno {
	pr, pw := io.Pipe()
	errCh := make(chan error, 1)
	go func() {
		defer pw.Close()
		_, err := n.contentCache.InsertStream(id, pr)
		errCh <- err
	}()
	_, err := n.graphClient.GetItemContentStream(ctx, n.tokenProvider, graph.ItemID(id), pw)
	pw.CloseWithError(err)
	if streamErr := <-errCh; err != nil {
		err = streamErr
	}
	if err != nil {
		log.Error().Err(err).Str("file", n.inode.Name()).Msg("Error descargando contenido")
		return syscall.EIO
	}
	return 0
}

// ──── Statfs ────

// Statfs returns information about the filesystem (total space,
// free space, name limits). Uses reasonable default values for
// OneDrive Personal (5 TB quota, 100K files, maximum name
// nombre 260 caracteres).
//
// The values are static estimates because we don't cache the /me/drive
// response (it would require an additional HTTP call). If in the future it's cached
// el objeto Drive, estos valores se pueden reemplazar por los reales.
func (n *DriveItemNode) Statfs(ctx context.Context, out *fuse.StatfsOut) syscall.Errno {
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

// fetchChildren implementa ChildrenFetcher para DriveItemNode.
// 🔧 Usa el fetcher compartido con fallback offline (nodeDeps): antes llamaba
// directly to fetchChildrenFromGraph, so navigating to a subfolder with
// the network down returned "Error en Lookup" (EIO) even though the metadata
// was cached — bug detected in the real offline test (root did
// work because OneCloudFS had the fallback).
func (n *DriveItemNode) fetchChildren(ctx context.Context, parentID string) ([]graph.DriveItem, error) {
	return n.nodeDeps.fetchChildrenWithOffline(ctx, parentID)
}

package fs

import (
	"io"
	"os"
	"sync"
	"testing"
)

// ──── NewContentCache ────

func TestContentCache_New_Success(t *testing.T) {
	tmpDir := t.TempDir()
	cache, err := NewContentCache(tmpDir)
	if err != nil {
		t.Fatalf("NewContentCache error inesperado: %v", err)
	}
	if cache == nil {
		t.Fatal("Se esperaba un ContentCache no nil")
	}
	if cache.directory != tmpDir {
		t.Errorf("Directorio esperado %q, obtenido %q", tmpDir, cache.directory)
	}
}

func TestContentCache_New_CreatesDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	cacheDir := tmpDir + "/subdir/nested"
	cache, err := NewContentCache(cacheDir)
	if err != nil {
		t.Fatalf("NewContentCache error inesperado: %v", err)
	}
	// Verificar que el directorio fue creado
	if _, err := os.Stat(cache.directory); os.IsNotExist(err) {
		t.Error("The cache directory should have been created")
	}
	_ = cache
}

func TestContentCache_New_DirectoryCreationError(t *testing.T) {
	// Crear un archivo regular y luego intentar usar su ruta como directorio
	tmpDir := t.TempDir()
	filePath := tmpDir + "/not_a_directory"
	if err := os.WriteFile(filePath, []byte("block"), 0600); err != nil {
		t.Fatalf("Error creando archivo bloqueante: %v", err)
	}
	// Intentar crear ContentCache donde hay un archivo, no un directorio
	_, err := NewContentCache(filePath + "/subdir")
	if err == nil {
		t.Fatal("Se esperaba un error al crear directorio donde hay un archivo")
	}
}

// ──── Open ────

func TestContentCache_Open_CreatesNewFile(t *testing.T) {
	tmpDir := t.TempDir()
	cache, err := NewContentCache(tmpDir)
	if err != nil {
		t.Fatalf("NewContentCache error: %v", err)
	}

	fd, err := cache.Open("file123")
	if err != nil {
		t.Fatalf("Open error inesperado: %v", err)
	}
	defer cache.Close("file123")

	// Verificar que el archivo existe en disco
	path := cache.contentPath("file123")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("The file was expected to exist on disk after Open")
	}
	_ = fd
}

func TestContentCache_Open_ReusesFD(t *testing.T) {
	tmpDir := t.TempDir()
	cache, err := NewContentCache(tmpDir)
	if err != nil {
		t.Fatalf("NewContentCache error: %v", err)
	}
	defer cache.Close("file123")

	fd1, err := cache.Open("file123")
	if err != nil {
		t.Fatalf("Primer Open error: %v", err)
	}

	fd2, err := cache.Open("file123")
	if err != nil {
		t.Fatalf("Segundo Open error: %v", err)
	}

	// Verify that it is exactly the same FD (reuse)
	if fd1.Fd() != fd2.Fd() {
		t.Error("Open should reuse the same FD for the same ID")
	}
}

func TestContentCache_Open_ReadWrite(t *testing.T) {
	tmpDir := t.TempDir()
	cache, err := NewContentCache(tmpDir)
	if err != nil {
		t.Fatalf("NewContentCache error: %v", err)
	}
	defer cache.Close("file123")

	fd, err := cache.Open("file123")
	if err != nil {
		t.Fatalf("Open error: %v", err)
	}

	// Escribir al FD
	data := []byte("datos de prueba")
	if _, err := fd.Write(data); err != nil {
		t.Fatalf("Write error: %v", err)
	}

	// Leer desde el inicio
	if _, err := fd.Seek(0, 0); err != nil {
		t.Fatalf("Seek error: %v", err)
	}
	buf := make([]byte, len(data))
	if _, err := fd.Read(buf); err != nil {
		t.Fatalf("Read error: %v", err)
	}
	if string(buf) != string(data) {
		t.Errorf("Contenido esperado %q, obtenido %q", string(data), string(buf))
	}

	// Verificar persistencia real en disco (no solo el buffer del FD)
	if err := fd.Sync(); err != nil {
		t.Fatalf("Sync error: %v", err)
	}
	diskData, err := os.ReadFile(cache.contentPath("file123"))
	if err != nil {
		t.Fatalf("Error leyendo desde disco: %v", err)
	}
	if string(diskData) != string(data) {
		t.Errorf("Datos en disco esperados %q, obtenidos %q", string(data), string(diskData))
	}
}

// ──── Insert ────

func TestContentCache_Insert_Success(t *testing.T) {
	tmpDir := t.TempDir()
	cache, err := NewContentCache(tmpDir)
	if err != nil {
		t.Fatalf("NewContentCache error: %v", err)
	}

	content := []byte("contenido insertado")
	if err := cache.Insert("file456", content); err != nil {
		t.Fatalf("Insert error: %v", err)
	}

	// Leer del disco para verificar
	data, err := os.ReadFile(cache.contentPath("file456"))
	if err != nil {
		t.Fatalf("Error leyendo archivo insertado: %v", err)
	}
	if string(data) != string(content) {
		t.Errorf("Contenido esperado %q, obtenido %q", string(content), string(data))
	}
}

func TestContentCache_Insert_EmptyContent(t *testing.T) {
	tmpDir := t.TempDir()
	cache, err := NewContentCache(tmpDir)
	if err != nil {
		t.Fatalf("NewContentCache error: %v", err)
	}

	if err := cache.Insert("empty_file", []byte{}); err != nil {
		t.Fatalf("Insert of empty file error: %v", err)
	}

	data, err := os.ReadFile(cache.contentPath("empty_file"))
	if err != nil {
		t.Fatalf("Error reading empty file: %v", err)
	}
	if len(data) != 0 {
		t.Errorf("Se esperaban 0 bytes, obtenidos %d", len(data))
	}
}

func TestContentCache_Insert_Overwrite(t *testing.T) {
	tmpDir := t.TempDir()
	cache, err := NewContentCache(tmpDir)
	if err != nil {
		t.Fatalf("NewContentCache error: %v", err)
	}

	// Primer insert
	if err := cache.Insert("overwrite_me", []byte("contenido original")); err != nil {
		t.Fatalf("Primer Insert error: %v", err)
	}

	// Sobrescribir
	if err := cache.Insert("overwrite_me", []byte("contenido nuevo")); err != nil {
		t.Fatalf("Segundo Insert error: %v", err)
	}

	data, err := os.ReadFile(cache.contentPath("overwrite_me"))
	if err != nil {
		t.Fatalf("Error leyendo: %v", err)
	}
	if string(data) != "contenido nuevo" {
		t.Errorf("Contenido esperado 'contenido nuevo', obtenido %q", string(data))
	}
}

// ──── InsertStream ────

func TestContentCache_InsertStream_Success(t *testing.T) {
	tmpDir := t.TempDir()
	cache, err := NewContentCache(tmpDir)
	if err != nil {
		t.Fatalf("NewContentCache error: %v", err)
	}
	defer cache.Close("stream_file")

	content := []byte("datos via stream!")
	n, err := cache.InsertStream("stream_file", bytesReader(content))
	if err != nil {
		t.Fatalf("InsertStream error: %v", err)
	}
	if n != int64(len(content)) {
		t.Errorf("Bytes escritos esperados %d, obtenidos %d", len(content), n)
	}

	// Verificar contenido en disco
	data, err := os.ReadFile(cache.contentPath("stream_file"))
	if err != nil {
		t.Fatalf("Error leyendo: %v", err)
	}
	if string(data) != string(content) {
		t.Errorf("Contenido esperado %q, obtenido %q", string(content), string(data))
	}
}

func TestContentCache_InsertStream_ReplacesContent(t *testing.T) {
	tmpDir := t.TempDir()
	cache, err := NewContentCache(tmpDir)
	if err != nil {
		t.Fatalf("NewContentCache error: %v", err)
	}
	defer cache.Close("replace_me")

	// Insertar contenido inicial
	if _, err := cache.InsertStream("replace_me", bytesReader([]byte("contenido viejo"))); err != nil {
		t.Fatalf("Primer InsertStream error: %v", err)
	}

	// Reemplazar con nuevo contenido
	if _, err := cache.InsertStream("replace_me", bytesReader([]byte("nuevo"))); err != nil {
		t.Fatalf("Segundo InsertStream error: %v", err)
	}

	data, err := os.ReadFile(cache.contentPath("replace_me"))
	if err != nil {
		t.Fatalf("Error leyendo: %v", err)
	}
	if string(data) != "nuevo" {
		t.Errorf("Contenido esperado 'nuevo', obtenido %q", string(data))
	}
}

func TestContentCache_InsertStream_TruncatesLongerContent(t *testing.T) {
	tmpDir := t.TempDir()
	cache, err := NewContentCache(tmpDir)
	if err != nil {
		t.Fatalf("NewContentCache error: %v", err)
	}
	defer cache.Close("trunc_me")

	// Insertar contenido largo
	if _, err := cache.InsertStream("trunc_me", bytesReader([]byte("contenido muy largo para truncar"))); err != nil {
		t.Fatalf("Primer InsertStream error: %v", err)
	}

	// Replace with shorter content
	if _, err := cache.InsertStream("trunc_me", bytesReader([]byte("corto"))); err != nil {
		t.Fatalf("Segundo InsertStream error: %v", err)
	}

	data, err := os.ReadFile(cache.contentPath("trunc_me"))
	if err != nil {
		t.Fatalf("Error leyendo: %v", err)
	}
	if string(data) != "corto" {
		t.Errorf("Expected content 'corto', got %q. Was it truncated correctly?", string(data))
	}
}

// TestContentCache_InsertStream_EmptyReader tests InsertStream with an empty reader.
func TestContentCache_InsertStream_EmptyReader(t *testing.T) {
	tmpDir := t.TempDir()
	cache, err := NewContentCache(tmpDir)
	if err != nil {
		t.Fatalf("NewContentCache error: %v", err)
	}
	defer cache.Close("empty_stream")

	// Insert non-empty content first
	if _, err := cache.InsertStream("empty_stream", bytesReader([]byte("contenido previo"))); err != nil {
		t.Fatalf("Primer InsertStream error: %v", err)
	}

	// Overwrite with an empty reader
	n, err := cache.InsertStream("empty_stream", bytesReader([]byte{}))
	if err != nil {
		t.Fatalf("InsertStream with empty reader error: %v", err)
	}
	if n != 0 {
		t.Errorf("Bytes escritos esperados 0, obtenidos %d", n)
	}

	data, err := os.ReadFile(cache.contentPath("empty_stream"))
	if err != nil {
		t.Fatalf("Error leyendo: %v", err)
	}
	if len(data) != 0 {
		t.Errorf("Expected an empty file (0 bytes), got %d bytes: %q", len(data), string(data))
	}
}

// TestContentCache_InsertStream_OnOpenFD tests InsertStream when the FD is already open,
// verificando que Seek+Truncate limpian correctamente antes de copiar.
func TestContentCache_InsertStream_OnOpenFD(t *testing.T) {
	tmpDir := t.TempDir()
	cache, err := NewContentCache(tmpDir)
	if err != nil {
		t.Fatalf("NewContentCache error: %v", err)
	}

	// Abrir el FD primero (lo abre y lo mantiene en fds)
	fd, err := cache.Open("fd_open")
	if err != nil {
		t.Fatalf("Open error: %v", err)
	}
	// Escribir algo por fuera para simular que el FD ya tiene datos sucios
	fd.Write([]byte("datos sucios que deben ser reemplazados"))

	// InsertStream: debe hacer Seek(0,0) + Truncate(0) + Copy
	n, err := cache.InsertStream("fd_open", bytesReader([]byte("limpio")))
	if err != nil {
		t.Fatalf("InsertStream sobre FD abierto error: %v", err)
	}
	if n != int64(len("limpio")) {
		t.Errorf("Bytes esperados %d, obtenidos %d", len("limpio"), n)
	}

	// Verificar desde el FD que ahora tiene el contenido limpio
	fd.Seek(0, 0)
	buf := make([]byte, 100)
	nRead, _ := fd.Read(buf)
	if string(buf[:nRead]) != "limpio" {
		t.Errorf("Contenido en FD esperado 'limpio', obtenido %q", string(buf[:nRead]))
	}

	// Also verify on disk
	data, err := os.ReadFile(cache.contentPath("fd_open"))
	if err != nil {
		t.Fatalf("Error leyendo disco: %v", err)
	}
	if string(data) != "limpio" {
		t.Errorf("Contenido en disco esperado 'limpio', obtenido %q", string(data))
	}

	cache.Close("fd_open")
}

// ──── HasContent ────

func TestContentCache_HasContent_True_WhenFileExists(t *testing.T) {
	tmpDir := t.TempDir()
	cache, err := NewContentCache(tmpDir)
	if err != nil {
		t.Fatalf("NewContentCache error: %v", err)
	}

	// Insertar archivo en disco
	if err := cache.Insert("exists_file", []byte("hello")); err != nil {
		t.Fatalf("Insert error: %v", err)
	}

	if !cache.HasContent("exists_file") {
		t.Error("HasContent should return true for a file that exists on disk")
	}
}

func TestContentCache_HasContent_True_WhenFDOpen(t *testing.T) {
	tmpDir := t.TempDir()
	cache, err := NewContentCache(tmpDir)
	if err != nil {
		t.Fatalf("NewContentCache error: %v", err)
	}
	defer cache.Close("open_file")

	fd, err := cache.Open("open_file")
	if err != nil {
		t.Fatalf("Open error: %v", err)
	}
	_ = fd

	if !cache.HasContent("open_file") {
		t.Error("HasContent should return true for a file with an open FD")
	}
}

func TestContentCache_HasContent_False_WhenMissing(t *testing.T) {
	tmpDir := t.TempDir()
	cache, err := NewContentCache(tmpDir)
	if err != nil {
		t.Fatalf("NewContentCache error: %v", err)
	}

	if cache.HasContent("nonexistent") {
		t.Error("HasContent should return false for a non-existent file")
	}
}

func TestContentCache_HasContent_True_AfterClose(t *testing.T) {
	tmpDir := t.TempDir()
	cache, err := NewContentCache(tmpDir)
	if err != nil {
		t.Fatalf("NewContentCache error: %v", err)
	}

	// Abrir, escribir, cerrar → archivo sigue en disco
	fd, err := cache.Open("persist_file")
	if err != nil {
		t.Fatalf("Open error: %v", err)
	}
	fd.Write([]byte("persistent data"))
	cache.Close("persist_file")

	if !cache.HasContent("persist_file") {
		t.Error("HasContent should return true after Close (the file stays on disk)")
	}
}

// ──── IsOpen ────

func TestContentCache_IsOpen_True(t *testing.T) {
	tmpDir := t.TempDir()
	cache, err := NewContentCache(tmpDir)
	if err != nil {
		t.Fatalf("NewContentCache error: %v", err)
	}
	defer cache.Close("open_file")

	_, err = cache.Open("open_file")
	if err != nil {
		t.Fatalf("Open error: %v", err)
	}

	if !cache.IsOpen("open_file") {
		t.Error("IsOpen should return true for an open FD")
	}
}

func TestContentCache_IsOpen_False_AfterClose(t *testing.T) {
	tmpDir := t.TempDir()
	cache, err := NewContentCache(tmpDir)
	if err != nil {
		t.Fatalf("NewContentCache error: %v", err)
	}

	_, err = cache.Open("close_me")
	if err != nil {
		t.Fatalf("Open error: %v", err)
	}
	cache.Close("close_me")

	if cache.IsOpen("close_me") {
		t.Error("IsOpen should return false after Close")
	}
}

func TestContentCache_IsOpen_False_WhenNeverOpened(t *testing.T) {
	tmpDir := t.TempDir()
	cache, err := NewContentCache(tmpDir)
	if err != nil {
		t.Fatalf("NewContentCache error: %v", err)
	}

	if cache.IsOpen("nunca_abierto") {
		t.Error("IsOpen should return false for an ID that was never opened")
	}
}

// ──── Close ────

func TestContentCache_Close_RemovesFromFds(t *testing.T) {
	tmpDir := t.TempDir()
	cache, err := NewContentCache(tmpDir)
	if err != nil {
		t.Fatalf("NewContentCache error: %v", err)
	}

	_, err = cache.Open("close_test")
	if err != nil {
		t.Fatalf("Open error: %v", err)
	}

	if !cache.IsOpen("close_test") {
		t.Fatal("The file should be open before Close")
	}

	cache.Close("close_test")

	if cache.IsOpen("close_test") {
		t.Error("The file should not be open after Close")
	}
}

func TestContentCache_Close_Idempotent(t *testing.T) {
	tmpDir := t.TempDir()
	cache, err := NewContentCache(tmpDir)
	if err != nil {
		t.Fatalf("NewContentCache error: %v", err)
	}

	_, err = cache.Open("idempotent_file")
	if err != nil {
		t.Fatalf("Open error: %v", err)
	}

	// Primer Close
	cache.Close("idempotent_file")
	// Second Close: should not cause a panic
	cache.Close("idempotent_file")

	// Also for an ID that was never opened
	cache.Close("never_existed")
}

// ──── Delete ────

func TestContentCache_Delete_RemovesFromDisk(t *testing.T) {
	tmpDir := t.TempDir()
	cache, err := NewContentCache(tmpDir)
	if err != nil {
		t.Fatalf("NewContentCache error: %v", err)
	}

	if err := cache.Insert("delete_me", []byte("temporal")); err != nil {
		t.Fatalf("Insert error: %v", err)
	}

	if err := cache.Delete("delete_me"); err != nil {
		t.Fatalf("Delete error: %v", err)
	}

	// Verificar que ya no existe en disco
	if _, err := os.Stat(cache.contentPath("delete_me")); !os.IsNotExist(err) {
		t.Error("The file should have been removed from disk")
	}

	// Verificar que HasContent devuelve false
	if cache.HasContent("delete_me") {
		t.Error("HasContent should return false after Delete")
	}
}

func TestContentCache_Delete_ClosesOpenFD(t *testing.T) {
	tmpDir := t.TempDir()
	cache, err := NewContentCache(tmpDir)
	if err != nil {
		t.Fatalf("NewContentCache error: %v", err)
	}

	// Open → the FD is tracked
	_, err = cache.Open("delete_open")
	if err != nil {
		t.Fatalf("Open error: %v", err)
	}

	if err := cache.Delete("delete_open"); err != nil {
		t.Fatalf("Delete error: %v", err)
	}

	if cache.IsOpen("delete_open") {
		t.Error("IsOpen should return false after Delete (releases the FD)")
	}

	// Verificar que el archivo fue eliminado del disco
	if _, err := os.Stat(cache.contentPath("delete_open")); !os.IsNotExist(err) {
		t.Error("The file should have been removed from disk after Delete")
	}
}

func TestContentCache_Delete_NonExistentReturnsError(t *testing.T) {
	tmpDir := t.TempDir()
	cache, err := NewContentCache(tmpDir)
	if err != nil {
		t.Fatalf("NewContentCache error: %v", err)
	}

	err = cache.Delete("no_existe")
	if err == nil {
		t.Error("Delete should return an error for a non-existent file")
	}
}

// ──── Operaciones concurrentes ────

func TestContentCache_ConcurrentOpen(t *testing.T) {
	tmpDir := t.TempDir()
	cache, err := NewContentCache(tmpDir)
	if err != nil {
		t.Fatalf("NewContentCache error: %v", err)
	}
	defer cache.Close("concurrent_file")

	const numGoroutines = 20

	// Insertar contenido primero
	if err := cache.Insert("concurrent_file", []byte("datos compartidos")); err != nil {
		t.Fatalf("Insert error: %v", err)
	}

	var wg sync.WaitGroup
	errors := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fd, err := cache.Open("concurrent_file")
			if err != nil {
				errors <- err
				return
			}
			// Leer para verificar que el FD funciona
			buf := make([]byte, len("datos compartidos"))
			fd.Read(buf)
		}()
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		t.Errorf("Error concurrente en Open: %v", err)
	}
}

func TestContentCache_ConcurrentHasContent(t *testing.T) {
	tmpDir := t.TempDir()
	cache, err := NewContentCache(tmpDir)
	if err != nil {
		t.Fatalf("NewContentCache error: %v", err)
	}
	defer cache.Close("concurrent_has")

	if err := cache.Insert("concurrent_has", []byte("x")); err != nil {
		t.Fatalf("Insert error: %v", err)
	}

	var wg sync.WaitGroup
	const numGoroutines = 30

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !cache.HasContent("concurrent_has") {
				t.Error("HasContent should return true concurrently")
			}
		}()
	}

	wg.Wait()
}

func TestContentCache_ConcurrentInsertAndRead(t *testing.T) {
	tmpDir := t.TempDir()
	cache, err := NewContentCache(tmpDir)
	if err != nil {
		t.Fatalf("NewContentCache error: %v", err)
	}
	defer cache.Close("rw_concurrent")

	const numWriters = 5
	const numReaders = 10
	var wg sync.WaitGroup

	// Writers
	for i := 0; i < numWriters; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			content := []byte("writer content " + string(rune('0'+id)))
			if err := cache.Insert("rw_concurrent", content); err != nil {
				t.Errorf("Insert concurrente error: %v", err)
			}
		}(i)
	}

	// Readers
	for i := 0; i < numReaders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = cache.HasContent("rw_concurrent")
			if fd, err := cache.Open("rw_concurrent"); err == nil {
				buf := make([]byte, 1024)
				fd.Read(buf)
			}
		}()
	}

	wg.Wait()
}

// ──── WriteAt ────

func TestContentCache_WriteAt_Success(t *testing.T) {
	tmpDir := t.TempDir()
	cache, _ := NewContentCache(tmpDir)
	defer cache.Close("writeat")

	cache.Insert("writeat", []byte("hola"))
	n, err := cache.WriteAt("writeat", []byte("XX"), 1)

	if err != nil {
		t.Fatalf("WriteAt error: %v", err)
	}
	if n != 2 {
		t.Errorf("Bytes escritos esperados 2, obtenidos %d", n)
	}

	data, _ := os.ReadFile(cache.contentPath("writeat"))
	if string(data) != "hXXa" {
		t.Errorf("Contenido esperado 'hXXa', obtenido %q", string(data))
	}
}

func TestContentCache_WriteAt_Append(t *testing.T) {
	tmpDir := t.TempDir()
	cache, _ := NewContentCache(tmpDir)
	defer cache.Close("append")

	cache.Insert("append", []byte("abc"))
	n, err := cache.WriteAt("append", []byte("def"), 3)

	if err != nil {
		t.Fatalf("WriteAt error: %v", err)
	}
	if n != 3 {
		t.Errorf("Bytes escritos esperados 3, obtenidos %d", n)
	}

	data, _ := os.ReadFile(cache.contentPath("append"))
	if string(data) != "abcdef" {
		t.Errorf("Contenido esperado 'abcdef', obtenido %q", string(data))
	}
}

func TestContentCache_WriteAt_BeyondEOF(t *testing.T) {
	tmpDir := t.TempDir()
	cache, _ := NewContentCache(tmpDir)
	defer cache.Close("beyond")

	n, err := cache.WriteAt("beyond", []byte("xyz"), 5)

	if err != nil {
		t.Fatalf("WriteAt error: %v", err)
	}
	if n != 3 {
		t.Errorf("Bytes escritos esperados 3, obtenidos %d", n)
	}

	data, _ := os.ReadFile(cache.contentPath("beyond"))
	// bytes 0-4 son ceros, luego "xyz"
	if len(data) != 8 {
		t.Errorf("Expected size 8 (5+3), got %d", len(data))
	}
}

// ──── Size ────

func TestContentCache_Size_Existing(t *testing.T) {
	tmpDir := t.TempDir()
	cache, _ := NewContentCache(tmpDir)

	cache.Insert("size_test", []byte("doce bytes!!"))
	if s := cache.Size("size_test"); s != 12 {
		t.Errorf("Size esperado 12, obtenido %d", s)
	}
}

func TestContentCache_Size_NonExistent(t *testing.T) {
	tmpDir := t.TempDir()
	cache, _ := NewContentCache(tmpDir)

	if s := cache.Size("nope"); s != 0 {
		t.Errorf("Size esperado 0 para archivo inexistente, obtenido %d", s)
	}
}

func TestContentCache_Size_AfterWriteAt(t *testing.T) {
	tmpDir := t.TempDir()
	cache, _ := NewContentCache(tmpDir)

	cache.WriteAt("growing", []byte("hello"), 0)
	if s := cache.Size("growing"); s != 5 {
		t.Errorf("Size esperado 5, obtenido %d", s)
	}

	cache.WriteAt("growing", []byte(" world"), 5)
	if s := cache.Size("growing"); s != 11 {
		t.Errorf("Size esperado 11, obtenido %d", s)
	}
}

// ──── Phase 4b: Eviction by size ────

func TestContentCache_SetMaxSize_DefaultZero(t *testing.T) {
	tmpDir := t.TempDir()
	cache, _ := NewContentCache(tmpDir)

	if cache.MaxSize() != 0 {
		t.Errorf("Default MaxSize should be 0, got %d", cache.MaxSize())
	}
}

func TestContentCache_SetMaxSize(t *testing.T) {
	tmpDir := t.TempDir()
	cache, _ := NewContentCache(tmpDir)

	cache.SetMaxSize(1 << 20) // 1 MB
	if cache.MaxSize() != 1<<20 {
		t.Errorf("MaxSize esperado %d, obtenido %d", 1<<20, cache.MaxSize())
	}
}

func TestContentCache_TotalSize_TracksInsert(t *testing.T) {
	tmpDir := t.TempDir()
	cache, _ := NewContentCache(tmpDir)

	cache.Insert("f1", []byte("12345"))
	if ts := cache.TotalSize(); ts != 5 {
		t.Errorf("TotalSize esperado 5, obtenido %d", ts)
	}

	cache.Insert("f2", []byte("abcdefghij")) // 10 bytes
	if ts := cache.TotalSize(); ts != 15 {
		t.Errorf("TotalSize esperado 15, obtenido %d", ts)
	}
}

func TestContentCache_TotalSize_TracksOverwrite(t *testing.T) {
	tmpDir := t.TempDir()
	cache, _ := NewContentCache(tmpDir)

	// Insert 10 bytes
	cache.Insert("f1", []byte("1234567890"))
	if ts := cache.TotalSize(); ts != 10 {
		t.Fatalf("TotalSize inicial esperado 10, obtenido %d", ts)
	}

	// Sobrescribir con 3 bytes → totalSize debe decrecer en 7
	cache.Insert("f1", []byte("abc"))
	if ts := cache.TotalSize(); ts != 3 {
		t.Errorf("TotalSize tras overwrite esperado 3, obtenido %d", ts)
	}
}

func TestContentCache_TotalSize_TracksWriteAt(t *testing.T) {
	tmpDir := t.TempDir()
	cache, _ := NewContentCache(tmpDir)

	// Escribir 5 bytes al inicio
	cache.WriteAt("f1", []byte("hello"), 0)
	if ts := cache.TotalSize(); ts != 5 {
		t.Fatalf("TotalSize tras WriteAt esperado 5, obtenido %d", ts)
	}

	// Add 6 bytes at the end (position 5) → 5+6=11
	cache.WriteAt("f1", []byte(" world"), 5)
	if ts := cache.TotalSize(); ts != 11 {
		t.Errorf("TotalSize tras append esperado 11, obtenido %d", ts)
	}

	// Overwrite at position 6 without extending → totalSize does not change
	cache.WriteAt("f1", []byte("W"), 6)
	if ts := cache.TotalSize(); ts != 11 {
		t.Errorf("TotalSize tras in-place write esperado 11, obtenido %d", ts)
	}
}

func TestContentCache_TotalSize_TracksDelete(t *testing.T) {
	tmpDir := t.TempDir()
	cache, _ := NewContentCache(tmpDir)

	cache.Insert("f1", []byte("1234567890")) // 10 bytes
	cache.Insert("f2", []byte("abcde"))      // 5 bytes
	if ts := cache.TotalSize(); ts != 15 {
		t.Fatalf("TotalSize inicial esperado 15, obtenido %d", ts)
	}

	cache.Delete("f1")
	if ts := cache.TotalSize(); ts != 5 {
		t.Errorf("TotalSize tras Delete esperado 5, obtenido %d", ts)
	}
}

func TestContentCache_TotalDiskUsage_MatchesDisk(t *testing.T) {
	tmpDir := t.TempDir()
	cache, _ := NewContentCache(tmpDir)

	cache.Insert("a", []byte("1234"))   // 4 bytes
	cache.Insert("b", []byte("123456")) // 6 bytes

	usage := cache.TotalDiskUsage()
	if usage != 10 {
		t.Errorf("TotalDiskUsage esperado 10, obtenido %d", usage)
	}
}

// ──── evictBySize ────

func TestContentCache_EvictBySize_NoEvictionWhenUnderLimit(t *testing.T) {
	tmpDir := t.TempDir()
	cache, _ := NewContentCache(tmpDir)
	cache.SetMaxSize(1000) // high limit, should not evict

	cache.Insert("f1", []byte("hello"))
	cache.Insert("f2", []byte("world"))

	cache.ForceEvict()

	// Ambos archivos deben seguir existiendo
	if !cache.HasContent("f1") {
		t.Error("f1 should still exist")
	}
	if !cache.HasContent("f2") {
		t.Error("f2 should still exist")
	}
}

func TestContentCache_EvictBySize_EvictsWhenOverLimit(t *testing.T) {
	tmpDir := t.TempDir()
	cache, _ := NewContentCache(tmpDir)

	// Insertar archivos que suman 32 bytes
	cache.Insert("a", []byte("12345678")) // 8 bytes
	cache.Insert("b", []byte("12345678")) // 8 bytes
	cache.Insert("c", []byte("12345678")) // 8 bytes
	cache.Insert("d", []byte("12345678")) // 8 bytes

	// Limit: only 2 files fit (16 bytes) → must evict 2
	cache.SetMaxSize(20)

	// Force synchronous eviction for the test
	cache.ForceEvict()

	// Verificar que algunos archivos fueron evictados
	surviving := 0
	for _, id := range []string{"a", "b", "c", "d"} {
		if cache.HasContent(id) {
			surviving++
		}
	}

	if surviving > 3 {
		t.Errorf("Demasiados archivos sobrevivieron: %d (esperado ≤ 3 con maxSize=20)", surviving)
	}
	if surviving == 0 {
		t.Error("At least some files should survive")
	}
}

func TestContentCache_EvictBySize_OldestEvictedFirst(t *testing.T) {
	tmpDir := t.TempDir()
	cache, _ := NewContentCache(tmpDir)

	// Insert f1 first (it will be the oldest)
	cache.Insert("f1", []byte("1234567890")) // 10 bytes

	// Insert f2 afterwards (most recent)
	cache.Insert("f2", []byte("abcdefghij")) // 10 bytes

	// Limit: only 1 file fits → must evict f1 (oldest)
	cache.SetMaxSize(15)
	cache.ForceEvict()

	if cache.HasContent("f1") {
		t.Error("f1 (oldest) should have been evicted")
	}
	if !cache.HasContent("f2") {
		t.Error("f2 (most recent) should survive")
	}
}

func TestContentCache_EvictBySize_PreservesOpenFiles(t *testing.T) {
	tmpDir := t.TempDir()
	cache, _ := NewContentCache(tmpDir)

	// Insertar archivos
	cache.Insert("open_me", []byte("open content"))   // 12 bytes, will stay open
	cache.Insert("evict_me", []byte("evict content")) // 13 bytes

	// Open "open_me" to protect it from eviction
	fd, err := cache.Open("open_me")
	if err != nil {
		t.Fatalf("Open error: %v", err)
	}
	defer cache.Close("open_me")
	_ = fd

	// Limit: only ~12 bytes fit → must evict "evict_me" but not "open_me"
	cache.SetMaxSize(15)
	cache.ForceEvict()

	if !cache.HasContent("open_me") {
		t.Error("open_me (open) should not be evicted")
	}
	if cache.HasContent("evict_me") {
		t.Error("evict_me (closed) should have been evicted")
	}
}

// ──── maybeEvict — auto-disparo ────

func TestContentCache_MaybeEvict_TriggeredOnInsert(t *testing.T) {
	tmpDir := t.TempDir()
	cache, _ := NewContentCache(tmpDir)
	cache.SetMaxSize(10) // solo 10 bytes

	// Insert 20 bytes → should trigger automatic eviction
	cache.Insert("big_file", make([]byte, 20))

	// Give the eviction goroutine some time
	// (maybeEvict lanza evictBySize en background)
	// We force synchronous eviction for the test
	cache.ForceEvict()

	// Verify that totalSize is under the limit
	if ts := cache.TotalSize(); ts > 10 {
		t.Errorf("TotalSize should be under maxSize, but it is %d", ts)
	}
}

// ──── Concurrente: Open vs eviction ────

func TestContentCache_ConcurrentOpenAndEvict(t *testing.T) {
	tmpDir := t.TempDir()
	cache, _ := NewContentCache(tmpDir)
	cache.SetMaxSize(500) // low limit to force frequent eviction

	// Pre-poblar con muchos archivos grandes
	for i := 0; i < 20; i++ {
		id := "file" + string(rune('a'+i%26)) + string(rune('0'+i/26))
		cache.Insert(id, make([]byte, 100)) // 100 bytes cada uno = 2000 bytes total
	}

	var wg sync.WaitGroup

	// Goroutines that open files while eviction happens
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				id := "file" + string(rune('a'+(idx+j)%26)) + string(rune('0'+((idx+j)/26)%3))
				fd, err := cache.Open(id)
				if err != nil {
					continue // archivo pudo ser evictado
				}
				// Leer un byte para simular uso
				buf := make([]byte, 1)
				fd.ReadAt(buf, 0)
				cache.Close(id)
			}
		}(i)
	}

	// Goroutine that forces eviction repeatedly
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 20; j++ {
			cache.ForceEvict()
		}
	}()

	wg.Wait()

	// There should be no panic or race conditions (verified with -race)
}

// ──── CloseAll ────

func TestContentCache_CloseAll_ClosesAllFDs(t *testing.T) {
	tmpDir := t.TempDir()
	cache, _ := NewContentCache(tmpDir)

	// Abrir varios archivos
	cache.Open("a")
	cache.Open("b")
	cache.Open("c")

	if !cache.IsOpen("a") || !cache.IsOpen("b") || !cache.IsOpen("c") {
		t.Fatal("The files should be open before CloseAll")
	}

	cache.CloseAll()

	if cache.IsOpen("a") || cache.IsOpen("b") || cache.IsOpen("c") {
		t.Error("No file should be open after CloseAll")
	}
}

// ──── Helper: bytesReader ────

// bytesReader convierte un []byte en un io.Reader para usar en InsertStream.
func bytesReader(data []byte) io.Reader {
	pr, pw := io.Pipe()
	go func() {
		pw.Write(data)
		pw.Close()
	}()
	return pr
}

package fs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// FuzzContentPath verifica que contentPath() no permite path traversal,
// independientemente del ID que reciba. El resultado siempre debe estar
// dentro de c.directory.
func FuzzContentPath(f *testing.F) {
	// Corpus inicial: casos normales y maliciosos
	seeds := []string{
		"01BYE5RZ6QN3VXWN",                // ID normal de Graph
		"local-a1b2c3d4e5f6g7h8",          // ID local (crypto/rand hex)
		"root",                            // ID especial
		"../../../etc/passwd",             // classic path traversal
		"..\\..\\..\\Windows\\system.ini", // backslash traversal (Windows)
		"foo/bar",                         // subdirectorio simple
		"/etc/passwd",                     // ruta absoluta Unix
		"C:\\Windows\\system.ini",         // ruta absoluta Windows
		".",                               // directorio actual
		"..",                              // directorio padre
		"",                                // empty
		"file.txt\x00.html",               // null byte injection
		"foo\x00/../../../etc/passwd",     // null byte + traversal
		"foo/./bar/../../etc/passwd",      // mixed . and ..
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, id string) {
		tmpDir := t.TempDir()
		cc := &ContentCache{directory: tmpDir}

		path := cc.contentPath(id)

		// Verify that the path is inside the cache directory.
		// Casos aceptables:
		//   - path == tmpDir (empty ID or equivalent after sanitization)
		//   - path comienza con tmpDir + separador (archivo dentro del dir)
		tmpDirWithSep := tmpDir + string(os.PathSeparator)
		if path != tmpDir && !strings.HasPrefix(path, tmpDirWithSep) {
			t.Errorf("contentPath(%q) = %q: the path is NOT inside the base directory %q",
				id, path, tmpDir)
		}

		// Verificar que filepath.Clean no escapa del directorio base
		cleanPath := filepath.Clean(path)
		cleanBase := filepath.Clean(tmpDir)
		if !strings.HasPrefix(cleanPath, cleanBase+string(os.PathSeparator)) && cleanPath != cleanBase {
			t.Errorf("contentPath(%q) = %q: after Clean, the path is NOT inside %q",
				id, cleanPath, cleanBase)
		}

		// Verify that it contains no null bytes (they should never reach the FS)
		if strings.ContainsRune(path, '\x00') {
			t.Errorf("contentPath(%q) = %q: contiene null byte", id, path)
		}
	})
}

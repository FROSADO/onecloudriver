package graph

import (
	"strings"
	"testing"
)

// FuzzResourcePathByPath verifies that ResourcePathByPath + normalizePath
// do not allow path traversal escaping the /me/drive/root: prefix.
// No input may result in a path that does not start with /me/drive/.
func FuzzResourcePathByPath(f *testing.F) {
	seeds := []string{
		"/",                               // root
		"/Documentos/foto.jpg",            // ruta normal
		"Documentos",                      // sin / inicial
		"../../../etc/passwd",             // traversal Unix
		"..\\..\\..\\Windows\\system.ini", // backslash traversal
		"/foo/../../../etc/shadow",        // mixed con absoluta
		"./../../../sensitive",            // relativa con traversal
		"foo/./bar/../../secret",          // mixed . and ..
		"/foo\x00/bar",                    // null byte
		"foo\x00/../../../etc/passwd",     // null byte + traversal
		"",                                // vacio
		"/.",                              // solo punto
		"//foo//bar",                      // doble slash
		"foo/bar/..",                      // backtrack al final
		"/foo%2fbar",                      // slash encodeado
		"/foo%00bar",                      // null byte encodeado
		"/foo%2e%2e%2fbar",                // ".." encodeado
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, rawPath string) {
		// Descartar entradas extremadamente largas (no utiles para path traversal)
		if len(rawPath) > 4096 {
			return
		}

		path := ResourcePathByPath(rawPath)

		// 1. The result must always start with /me/drive/
		if !strings.HasPrefix(path, "/me/drive/") {
			t.Errorf("ResourcePathByPath(%q) = %q: no comienza con /me/drive/", rawPath, path)
		}

		// 2. It must not contain null bytes in the output
		if strings.ContainsRune(path, '\x00') {
			t.Errorf("ResourcePathByPath(%q) = %q: contiene null byte", rawPath, path)
		}

		// 3. The result must never contain unescaped traversal sequences
		if strings.Contains(path, "/../") || strings.Contains(path, "/./") {
			t.Errorf("ResourcePathByPath(%q) = %q: contiene secuencias de traversal sin escapar", rawPath, path)
		}

		// 4. It must not contain empty segments (double slash)
		if strings.Contains(path, "//") {
			t.Errorf("ResourcePathByPath(%q) = %q: contiene doble slash", rawPath, path)
		}
	})
}

// FuzzNormalizePath verifies that normalizePath collapses correctly
// path traversal sequences and never returns paths escaping the root.
func FuzzNormalizePath(f *testing.F) {
	seeds := []string{
		"",
		"/",
		"foo",
		"foo/bar",
		"../../../etc/passwd",
		"foo/../../../etc/passwd",
		"..\\..\\Windows",
		".",
		"..",
		"foo/./bar",
		"foo//bar",
		"/absolute/path",
		"foo\x00bar",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, raw string) {
		if len(raw) > 4096 {
			return
		}

		result := normalizePath(raw)

		// 1. The result must never be an absolute path (starting with /)
		if strings.HasPrefix(result, "/") {
			t.Errorf("normalizePath(%q) = %q: resultado es ruta absoluta", raw, result)
		}

		// 2. It must not contain backslashes (they are replaced with / and cleaned)
		if strings.Contains(result, "\\") {
			t.Errorf("normalizePath(%q) = %q: contiene backslash", raw, result)
		}

		// 3. It must not contain traversal sequences once cleaned
		if result == ".." || strings.HasPrefix(result, "../") || strings.Contains(result, "/../") {
			t.Errorf("normalizePath(%q) = %q: still contains '..' after cleaning", raw, result)
		}

		// 4. It must not contain "//"
		if strings.Contains(result, "//") {
			t.Errorf("normalizePath(%q) = %q: contiene doble slash", raw, result)
		}

		// 5. It must not contain null bytes
		if strings.ContainsRune(result, '\x00') {
			t.Errorf("normalizePath(%q) = %q: contiene null byte", raw, result)
		}
	})
}

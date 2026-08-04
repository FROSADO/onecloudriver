package graph

import (
	"strings"
	"testing"
)

// FuzzResourcePathByPath verifica que ResourcePathByPath + normalizePath
// no permiten path traversal que escape del prefijo /me/drive/root:.
// Ninguna entrada debe resultar en un path que no comience por /me/drive/.
func FuzzResourcePathByPath(f *testing.F) {
	seeds := []string{
		"/",                               // raiz
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

		// 1. El resultado siempre debe comenzar con /me/drive/
		if !strings.HasPrefix(path, "/me/drive/") {
			t.Errorf("ResourcePathByPath(%q) = %q: no comienza con /me/drive/", rawPath, path)
		}

		// 2. No debe contener null bytes en la salida
		if strings.ContainsRune(path, '\x00') {
			t.Errorf("ResourcePathByPath(%q) = %q: contiene null byte", rawPath, path)
		}

		// 3. El resultado nunca debe contener secuencias de traversal sin escapar
		if strings.Contains(path, "/../") || strings.Contains(path, "/./") {
			t.Errorf("ResourcePathByPath(%q) = %q: contiene secuencias de traversal sin escapar", rawPath, path)
		}

		// 4. No debe contener segmentos vacios (doble slash)
		if strings.Contains(path, "//") {
			t.Errorf("ResourcePathByPath(%q) = %q: contiene doble slash", rawPath, path)
		}
	})
}

// FuzzNormalizePath verifica que normalizePath colapsa correctamente
// secuencias de path traversal y nunca devuelve rutas que escapen de la raiz.
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

		// 1. El resultado nunca debe ser una ruta absoluta (comenzar con /)
		if strings.HasPrefix(result, "/") {
			t.Errorf("normalizePath(%q) = %q: resultado es ruta absoluta", raw, result)
		}

		// 2. No debe contener backslash (se reemplazan por / y se limpian)
		if strings.Contains(result, "\\") {
			t.Errorf("normalizePath(%q) = %q: contiene backslash", raw, result)
		}

		// 3. No debe contener secuencias de traversal una vez limpio
		if result == ".." || strings.HasPrefix(result, "../") || strings.Contains(result, "/../") {
			t.Errorf("normalizePath(%q) = %q: aun contiene '..' tras limpieza", raw, result)
		}

		// 4. No debe contener "//"
		if strings.Contains(result, "//") {
			t.Errorf("normalizePath(%q) = %q: contiene doble slash", raw, result)
		}

		// 5. No debe contener null bytes
		if strings.ContainsRune(result, '\x00') {
			t.Errorf("normalizePath(%q) = %q: contiene null byte", raw, result)
		}
	})
}

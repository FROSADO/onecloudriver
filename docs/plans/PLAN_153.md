# PLAN_153 — Issue #153: singleflight per-folder children fetch to avoid Lookup/Readdir stampede on cold folders

> Plan de implementación y especificación técnica (SDD) para la issue #153 (`perf(fs): singleflight per-folder children fetch to avoid Lookup/Readdir stampede on cold folders`).
> Objetivo: eliminar la estampida (*thundering herd / stampede*) en carpetas frías o expiradas, donde múltiples llamadas concurrentes de `Lookup` o `Readdir` (por ejemplo, al listar un directorio grande con `ls -la` o `readdirplus`) disparan múltiples peticiones idénticas y simultáneas de paginación hacia el API de Graph, provocando throttling (HTTP 429) y thrashing.

---

## 1. Contexto y Análisis de la Situación

### 1.1 El Problema
Cuando los hijos de una carpeta no están en caché (carpeta fría) o su caché ha expirado (TTL vencido), cualquier llamada a `Lookup` de un nombre o a `Readdir` sobre dicha carpeta invoca `InodeCache.getChildren(...)`. 
Actualmente, `InodeCache` no realiza ninguna deduplicación *in-flight*. Si un explorador de archivos o `ls -la` solicita metadatos para N entradas simultáneamente (ej. a través de `readdirplus` o ráfagas de `Lookup`), el sistema lanza **N peticiones HTTP concurrentes e idénticas** al API de Microsoft Graph para listar el mismo `parentID`.

Esto provoca:
1. **Throttling (HTTP 429)** en el API de Graph por exceso de peticiones repetidas.
2. **Latencia extrema y thrashing**, agravando el problema descrito en el issue #152.
3. Consumo innecesario de ancho de banda y recursos de CPU.

### 1.2 La Solución con SDD y `singleflight`
Utilizaremos el paquete estándar de Go `golang.org/x/sync/singleflight` (`singleflight.Group`) integrado dentro de `InodeCache`.
- Cada `InodeCache` poseerá un campo `sf singleflight.Group`.
- Dentro de `getChildren`, agruparemos la ejecución de la carga de hijos por `parentID`.
- Si múltiples goroutines solicitan `getChildren` para el mismo `parentID` mientras una petición de red ya está en vuelo, todas esperarán a la única ejecución real y compartirán el resultado (tanto los elementos devueltos como el error, si lo hubiera).

---

## 2. Especificación Técnica

### 2.1 Cambios en `internal/fs/cache.go`
1. Añadir el campo `sf singleflight.Group` a la estructura `InodeCache`:
   ```go
   type InodeCache struct {
       // ... campos existentes ...
       sf singleflight.Group
   }
   ```
2. Modificar el método `getChildren` para que la llamada al fetcher (y la posterior creación de inodos/actualización de padres) se ejecute a través de `c.sf.Do`:
   - *Nota importante*: La clave de `singleflight` será `parentID`.
   - Dado que `c.sf.Do` devuelve `(interface{}, error, bool)`, realizaremos un cast seguro del resultado a `map[string]*Inode`.

### 2.2 Garantías de Concurrencia y Correctitud
- **Aislamiento por ParentID**: Peticiones a carpetas distintas (`parentA` vs `parentB`) corren en paralelo sin bloquearse mutuamente.
- **Transparencia en Caché**: Si hay un *cache hit* (datos frescos en memoria), `getChildren` retorna inmediatamente *sin* invocar a `singleflight`, manteniendo la ruta rápida ultra-optimizada.
- **Invalidez y Errores**: `singleflight` maneja de forma natural la propagación de errores a todas las goroutines concurrentes. Si el fetcher falla, todas reciben el error (ninguna almacena un estado corrupto o vacío incorrecto).

---

## 3. Plan de Trabajo Detallado (Fases Atómicas)

Las fases están diseñadas de forma didáctica para un programador en Go con experiencia intermedia, guiadas por pruebas (TDD).

### Fase 1 — Especificación y Preparación de Tests Unitarios (`cache_test.go`)
- **Objetivo**: Escribir una prueba unitaria de estrés concurrente que **falle** actualmente y verifique el comportamiento esperado de `singleflight`.
- **Detalle de la prueba (`TestInodeCache_GetChildren_SingleflightDedup`)**:
  - Configurar un fetcher con un retraso artificial (ej. `time.Sleep(50 * time.Millisecond)`).
  - Lanzar N (ej. 20) goroutines concurrentes llamando a `cache.GetChildren` para el mismo `parentID`.
  - Contar exactamente cuántas veces se invocó al fetcher real.
  - **Aserción**: `callCount == 1`, y todas las goroutines reciben los hijos correctamente sin errores.
- **Fichero**: `internal/fs/cache_test.go`.

### Fase 2 — Implementación de `singleflight` en `InodeCache` (`cache.go`)
- **Objetivo**: Integrar `golang.org/x/sync/singleflight` en `InodeCache.getChildren`.
- **Detalle**:
  - Importar `"golang.org/x/sync/singleflight"`.
  - Añadir `sf singleflight.Group` en la estructura `InodeCache`.
  - En `getChildren`, envolver la lógica de fetch y población de inodos dentro de `c.sf.Do(parentID, func() (any, error) { ... })`.
- **Verificación**: Ejecutar `go test ./internal/fs/... -run TestInodeCache_GetChildren_SingleflightDedup -race`.

### Fase 3 — Validación Integral de Regresiones y Concurrencia
- **Objetivo**: Comprobar que todos los tests existentes de `InodeCache` y FUSE sigan pasando sin regresiones, y verificar el comportamiento bajo `-race`.
- **Comandos**:
  ```bash
  go test ./internal/fs/... -count=1 -race -short
  go test ./internal/control/... ./cmd/onecloudriver/... -count=1 -race -short
  ```

### Fase 4 — Verificación de Estándares, Linting y Auditoría
- **Objetivo**: Asegurar calidad de código, cumplimiento de linters y ausencia de vulnerabilidades.
- **Comandos**:
  ```bash
  golangci-lint run --timeout=5m ./...
  gosec -quiet -severity=high ./... && govulncheck ./...
  ```

---

## 4. Ficheros Involucrados

| Fichero | Cambio |
|---|---|
| `internal/fs/cache.go` | Añadir `sf singleflight.Group` y envolver la sección de fetch en `c.sf.Do` dentro de `getChildren` |
| `internal/fs/cache_test.go` | Añadir `TestInodeCache_GetChildren_SingleflightDedup` para probar concurrencia masiva y conteo de llamadas al fetcher |
| `docs/plans/PLAN_153.md` | Este documento de especificación y plan detallado (SDD) |

---

## 5. Criterios de Aceptación

1. **Deduplicación comprobada**: Ráfagas concurrentes de `GetChildren` para un `parentID` frío invocan el fetcher exactamente 1 vez.
2. **Cero regresiones**: Toda la suite de tests unitarios y de integración pasa satisfactoriamente.
3. **Cero carreras de datos**: Las ejecuciones bajo `go.test -race` reportan 0 advertencias.
4. **Clean Code**: Cumplimiento estricto de los linters del proyecto (`golangci-lint`).

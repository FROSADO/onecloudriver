# PLAN_5 — Issue #39: `list` con `--path`/`--id`

> Plan elaborado para alguien que está aprendiendo Go: cada tarea es atómica,
> explica los conceptos del lenguaje que se tocan y lista los ficheros exactos
> que hay que modificar. El código del proyecto es la referencia: sigue los
> patrones existentes (helpers de `common.go`, tests con `setupGraphCommand`).

## 1. Evaluación: ¿sigue aplicando la issue tras los últimos cambios?

La issue **#39 sigue aplicando** en el estado actual de `main` (commit
`4e9d192`, tras la release v0.1.5). Verificación punto por punto sobre el
código real:

| Afirmación del plan de la issue | Verificado en el código actual |
|---|---|
| `list` solo lista la raíz con `ListDriveRoot` | ✅ `cmd/onecloudriver/list.go` solo llama a `graphClient.ListDriveRoot(...)`; es el único de los 8 comandos de ítems sin `--id`/`--path` |
| `GetItem` acepta `ItemID` y `ItemPath` | ✅ `internal/graph/items.go` → `func (cli *Client) GetItem(ctx, tokenProvider, res Resource)` con `validateResource` |
| `ListChildren` acepta ambos y pagina | ✅ `internal/graph/children.go` → `func (cli *Client) ListChildren(ctx, tokenProvider, r Resource)` |
| `DriveItem.IsFolder()` existe | ✅ `internal/graph/models.go` → `func (drive *DriveItem) IsFolder() bool { return drive.Folder != nil }` |
| `Parent` con `.ID` poblado existe | ✅ `internal/graph/models.go` → `type DriveItemParent struct { Path, ID, DriveID, DriveType }` |
| La capa HTTP es inyectable a nivel de CLI | ✅ `setupGraphCommand` en `item_commands_coverage_test.go` (inyecta `graph.WithBaseURL(server.URL)`) |
| Helper recomendado `buildOptionalResource` | ➖ No existe todavía: hay que crearlo imitando `buildOptionalDestResource` (mismo patrón) |

### 1.1 Hallazgo adicional: la documentación ya promete la funcionalidad

Los ficheros de documentación **ya muestran ejemplos de `list` con una ruta**
aunque el comando todavía no lo soporta (documentación "aspiracional"):

- `docs/MANUAL.md:560` → `onecloudriver list "/Documents" -a user@outlook.com`
- `docs/MANUAL.es.md:562` → `onecloudriver list "/Documentos" -a usuario@outlook.com`
- `docs/onecloudriver.1:38` → `.B list \fI[path]\fR`
- `README.md:192` → `onecloudriver list -a user@outlook.com /Documents`

**Ojo (discrepancia de diseño)**: la doc usa un **argumento posicional**
(`list "/Documents"`), pero el plan aprobado de la issue usa **flags**
(`list --path "/Documents"`), igual que los otros 7 comandos. Decisión: se
implementa con flags y **se corrige la doc** para que muestre la sintaxis
real. (El man page ya usa `list \fI[path]\fR` — también hay que corregirlo.)

### 1.2 Conclusión

No hay ningún cambio reciente que invalide el plan. La implementación es
posible sin tocar `internal/graph` (solo `cmd/onecloudriver` y docs).

---

## 2. Diseño de la solución

**Comportamiento final del comando `list`:**

| Flags | Comportamiento |
|---|---|
| (ninguno) | Lista la raíz del drive (comportamiento actual) |
| `--id <id>` | Lista el contenido de la carpeta con ese ID |
| `--path <ruta>` | Lista el contenido de la carpeta en esa ruta |
| `--id` + `--path` juntos | Error: `you must specify exactly one of --id or --path` |
| `--id`/`--path` apuntando a un **fichero** | Lista la carpeta que lo contiene (vía `Parent.ID`) |

**Algoritmo (resolución GetItem-first):**

1. Si no hay `--id` ni `--path` → `ListDriveRoot` (como hoy).
2. Si hay exactamente uno → `GetItem(resource)`:
   - Si `item.IsFolder()` → `ListChildren(ItemID(item.ID))`.
   - Si es un fichero → `ListChildren(ItemID(item.Parent.ID))` (lista el
     contenedor).
3. Si la lista está vacía:
   - Sin flags → `The root folder is empty.`
   - Con flags → `The folder is empty.`

**Decisiones de estilo (consistencia con el repo):**

- Flags `--id`/`--path` **sin shorthand** (`addIDPathFlags`, no
  `addIDPathFlagsWithShorthand`): `list` ya usa `-a`/`-o` y los otros 6
  comandos de ítems no tienen shorthand. Solo `info` los tiene.
- Mensaje de error de validación idéntico al de `buildResource`:
  `you must specify exactly one of --id or --path` (los tests lo comprueban).
- El helper nuevo se llama `buildOptionalResource` y **copia el patrón** de
  `buildOptionalDestResource` (permite que ambos estén vacíos; rechaza ambos
  a la vez).

---

## 3. Tareas atómicas

### Tarea 1 — Crear la rama de trabajo

- **Objetivo**: trabajar aislado de `main` siguiendo la convención del repo
  (`issue-<N>-<slug>`).
- **Comandos**:
  ```bash
  git checkout main
  git pull --ff-only origin main
  git checkout -b issue-39-list-target-folder
  ```
- **Verificación**: `git branch --show-current` → `issue-39-list-target-folder`.

### Tarea 2 — Helper `buildOptionalResource` en `cmd/onecloudriver/common.go`

- **Ficheros**: `cmd/onecloudriver/common.go`, `cmd/onecloudriver/common_test.go`.
- **Conceptos Go**:
  - *Interfaces*: `graph.Resource` es una interfaz con `ResourcePath()`,
    `IsEmpty()`, `ParentReference()`. `ItemID` e `ItemPath` la implementan.
  - *Zero value / nil*: devolver `nil` como `Resource` cuando no hay flags es
    la forma de decir "no hay objetivo" (el caller decide el comportamiento).
- **Qué hacer**: añadir la función (al lado de `buildOptionalDestResource`):

  ```go
  // buildOptionalResource builds a graph.Resource from at most one of itemID
  // or itemPath. It returns (nil, nil) when neither is provided, so callers
  // can fall back to a default target (list uses the root folder). Both
  // flags together are an error: they are mutually exclusive.
  func buildOptionalResource(itemID, itemPath string) (graph.Resource, error) {
      if itemID != "" && itemPath != "" {
          return nil, fmt.Errorf("you must specify exactly one of --id or --path")
      }
      if itemID == "" && itemPath == "" {
          // ⚠️ Gotcha de Go: devolver graph.ItemPath("") aquí NO es nil.
          // Una interfaz que contiene un valor (aunque sea el zero value del
          // tipo) NO es nil. El caller comprueba r == nil para decidir si
          // lista la raíz, así que hay que devolver la interfaz nil explícita.
          return nil, nil
      }
      if itemID != "" {
          return graph.ItemID(itemID), nil
      }
      return graph.ItemPath(itemPath), nil
  }
  ```

- **Tests** (tabla, patrón del repo): `common_test.go` → tabla con 4 casos:
  ambos vacíos → `(nil, nil)`; solo `--id` → `ItemID`; solo `--path` →
  `ItemPath`; ambos → error con `exactly one of --id or --path`.
- **Verificación**: `go test ./cmd/onecloudriver/ -run TestBuildOptionalResource -count=1`.

### Tarea 3 — Modificar `cmd/onecloudriver/list.go`

- **Fichero**: `cmd/onecloudriver/list.go`.
- **Conceptos Go**:
  - *Métodos de `*cobra.Command`*: `cmd.Flags().GetString("id")` lee el valor
    del flag; `cmd.ErrOrStderr()` da el writer de errores.
  - *Composición de errores*: `fmt.Errorf("error listing files: %w", err)`
    envuelve el error de Graph con contexto (`%w` + `errors.Is/As`).
  - *Early return*: cada rama de error sale pronto, el flujo feliz queda
    lineal (patrón de todo el repo).
- **Qué hacer**:
  1. Cambiar `Short` para reflejar el nuevo comportamiento, p. ej.:
     `List the contents of a OneDrive folder (root by default)`.
  2. En `registerListCmd`, registrar los flags con ayuda propia de `list`:
     ```go
     addIDPathFlags(listCmd,
         "ID of the folder to list (default: root)",
         "Path of the folder to list (default: root, e.g.: /Documents)")
     ```
  3. En `RunE`, tras `resolveFormatter`, leer los flags y resolver el objetivo:
     ```go
     itemID, _ := cmd.Flags().GetString("id")
     itemPath, _ := cmd.Flags().GetString("path")
     r, err := buildOptionalResource(itemID, itemPath)
     if err != nil {
         return err
     }

     graphClient := getClient(cmd)
     var items []graph.DriveItem
     if r == nil {
         items, err = graphClient.ListDriveRoot(cmd.Context(), acc)
     } else {
         items, err = listFolderContents(cmd.Context(), graphClient, acc, r)
     }
     if err != nil {
         return fmt.Errorf("error listing files: %w", err)
     }
     ```
  4. Mensaje de vacío contextual:
     ```go
     if len(items) == 0 {
         if r == nil {
             fmt.Println("The root folder is empty.")
         } else {
             fmt.Println("The folder is empty.")
         }
         return nil
     }
     ```
  5. Añadir la función auxiliar `listFolderContents` (mismo paquete):

     ```go
     // listFolderContents lists the children of the folder addressed by r. If
     // r points to a file, it lists the folder that contains it instead.
     func listFolderContents(ctx context.Context, graphClient *graph.Client, acc *auth.Account, r graph.Resource) ([]graph.DriveItem, error) {
         item, err := graphClient.GetItem(ctx, acc, r)
         if err != nil {
             return nil, err
         }

         if item.IsFolder() {
             return graphClient.ListChildren(ctx, acc, graph.ItemID(item.ID))
         }

         if item.Parent == nil || item.Parent.ID == "" {
             return nil, fmt.Errorf("cannot determine the parent folder of %q", item.Name)
         }
         return graphClient.ListChildren(ctx, acc, graph.ItemID(item.Parent.ID))
     }
     ```

     (Necesita importar `context` y `auth` — el resto de imports ya existen.)
- **Verificación**: `go build ./cmd/onecloudriver/...` compila.

### Tarea 4 — Tests unitarios de `list`

- **Ficheros nuevos/alterados**:
  - Nuevo `cmd/onecloudriver/list_test.go`.
  - `cmd/onecloudriver/main_test.go` → ampliar `TestListCmd_HasFlags`.
- **Conceptos Go**:
  - *Test table-driven*: tabla de casos con `t.Run` (patrón del repo).
  - *Handler HTTP*: el test usa `httptest` y devuelve JSON distinto según la
    ruta (`/me/drive/items/<id>` vs `/me/drive/items/<id>/children`).
  - *Captura de stdout*: `captureStdoutForTest` (ya existe) para leer la salida.
- **Rutas Graph que devuelve cada recurso** (clave para los asserts):
  - `ItemID("root")` → `/me/drive/root`
  - `ItemID("folder-id")` → `/me/drive/items/folder-id`
  - `ItemPath("/Documents")` → `/me/drive/root:/Documents`
  - `ListChildren(x)` → `<ruta-de-x>/children`
- **Casos a cubrir**:
  1. Sin flags → `GET /me/drive/root/children` (regresión).
  2. `--id folder-id` (carpeta) → `GET /me/drive/items/folder-id` y luego
     `GET /me/drive/items/folder-id/children`.
  3. `--path /Documents` (carpeta) → `GET /me/drive/root:/Documents` y luego
     `.../children`.
  4. `--id` a un **fichero** → `GET /me/drive/items/file-id` devuelve un
     DriveItem **sin** `Folder` y con `Parent.ID=parent-id` → se llama
     `GET /me/drive/items/parent-id/children`.
  5. `--path /` (raíz explícita) → `GET /me/drive/root` + `children`.
  6. Carpeta vacía con `--path` → stdout contiene `The folder is empty.`
  7. Sin flags y vacío → stdout contiene `The root folder is empty.`
  8. `--id` + `--path` juntos → error `exactly one of --id or --path`
     (sin llamadas a Graph; se puede usar `execCmd`).
  9. Error de Graph (500) → error con contexto `error listing files:`
     (ya existe `TestListCmd_GraphError`, mantener).
- **Ampliar `TestListCmd_HasFlags`**: comprobar que `--id` y `--path` existen
  y que **no** tienen shorthand.
- **Verificación**: `go test ./cmd/onecloudriver/ -run 'TestListCmd|TestBuildOptionalResource' -count=1`.

### Tarea 5 — Actualizar la documentación

- **Ficheros** (todos los que ya prometían la funcionalidad):
  - `docs/MANUAL.md:559-560` y `docs/MANUAL.es.md:561-562`: cambiar el ejemplo
    posicional por el de flags:
    ```
    onecloudriver list -a user@outlook.com
    onecloudriver list --path "/Documents" -a user@outlook.com
    ```
  - `docs/onecloudriver.1:38` y `docs/onecloudriver.1.es:39`: cambiar
    `.B list \fI[path]\fR` por algo como:
    `.B list [\fB\-\-id\fR \fIid\fR | \fB\-\-path\fR \fIpath\fR]`
    y actualizar el ejemplo de las líneas ~207/206.
  - `README.md:192` y `README.es.md:195`: cambiar
    `onecloudriver list -a user@outlook.com /Documents` por
    `onecloudriver list --path /Documents -a user@outlook.com`.
- **Verificación**: `grep -rn 'list "/' docs README.md README.es.md` no debe
  devolver ejemplos posicionales de `list`.

### Tarea 6 — Verificación final (antes de commitear)

- **Concepto Go**: el *vet* estático (`go vet`) y el *race detector*
  (`-race`) son comprobaciones obligatorias del repo.
- **Comandos** (en orden, sin lanzar unit e integration en paralelo):
  ```bash
  go build ./...
  go vet ./cmd/onecloudriver/...
  go test ./cmd/onecloudriver/... -count=1
  make test-unit
  ```
- **Además**: `make lint-all` si golangci-lint está disponible (si no, la CI
  lo hará).
- **Nota**: la batería de integración FUSE (`make test-integration`) **no** se
  toca: este cambio es 100% CLI, sin tocar FUSE.

---

## 4. Commit final (convenciones del repo)

```bash
git add cmd/onecloudriver/list.go cmd/onecloudriver/common.go \
        cmd/onecloudriver/list_test.go cmd/onecloudriver/common_test.go \
        cmd/onecloudriver/main_test.go \
        docs/plans/PLAN_5.md docs/MANUAL.md docs/MANUAL.es.md \
        docs/onecloudriver.1 docs/onecloudriver.1.es README.md README.es.md
git commit -m "feat(cli): allow list to target a folder by --id/--path (Closes #39)"
```

Después: push de la rama, PR con `Closes #39` y esperar los checks de CI
(reglas de `CONTRIBUTING.md`).

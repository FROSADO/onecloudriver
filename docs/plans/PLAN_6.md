# PLAN_6 — Issue #30: internacionalizar la salida de la CLI (i18n)

> Plan elaborado para alguien que está aprendiendo Go: tareas atómicas,
> conceptos del lenguaje explicados y ficheros exactos a tocar. Se asume
> conocimiento previo de `PLAN_5.md` (mismo estilo de trabajo y verificación).

## 1. Evaluación: ¿sigue aplicando la issue?

El análisis de la issue #30 es correcto y **sigue aplicando** en `main`
(verificado):

| Hecho | Evidencia en el código actual |
|---|---|
| Sin infraestructura i18n | `internal/i18n/` **no existe**; `grep -rn 'i18n\|gettext\|locale'` no devuelve nada fuera de comentarios |
| Sin dependencias de i18n | `go.mod` no tiene `golang.org/x/text` ni `go-i18n/v2` (proyecto ligero: cobra, zerolog, keyring, bbolt, yaml, humanize, go-fuse) |
| Strings hardcoded | ~**131** call sites `fmt.*` + **39** `Short:`/`Long:` en `cmd/onecloudriver/`; las mayores superficies: `service.go` (29), `output.go` (22), `mount.go` (13), `account.go` (12) |
| Formatter con labels EN | `output.go:94-130`: headers `TYPE/NAME/SIZE/MODIFIED`, labels `File`/`Folder`, `directory`/`file`, `(N elements)` |
| Tests que asertan inglés | `item_commands_coverage_test.go`, `list_test.go`, `common_test.go`... aserciones literales como `"Folder created: Photos (ID: folder-id)"` o `"The root folder is empty."` |
| Precedente de init por entorno | `internal/printer` selecciona símbolos emoji/ASCII en `init()` según TTY → mismo patrón que seguirá i18n |

**Conclusión**: el diseño de la issue se puede implementar sin tocar la
lógica de Graph/FUSE; es un cambio de **capa de presentación** en
`cmd/onecloudriver` + `internal/i18n` (nuevo) + unas pocas salidas de
`internal/auth` y `internal/fs`.

---

## 2. Decisiones de diseño (resuelven las "Open questions" de la issue)

| Decisión | Valor | Justificación |
|---|---|---|
| **Backend** | `github.com/nicksnyder/go-i18n/v2` + catálogos **JSON** | Sin codegen (a diferencia de `x/text/message`+gotext/PO), catálogos legibles, embebibles con `//go:embed`, ecosistema activo. Añade `golang.org/x/text` como dependencia |
| **Claves** | IDs estables con namespace: `cmd.list.header.type`, `err.account.notfound` | `en.json` mapea id→frase EN y es el fallback. Cambiar la redacción EN no rompe traducciones; `grep -rn 'L("'` extrae el catálogo completo |
| **Detección** | `--lang` flag → `LC_ALL` → `LC_MESSAGES` → `LANG` → `en` | Orden estándar POSIX. Sin fichero de config global (solo hay JSON por cuenta) |
| **Tests deterministas** | `i18n.Init("en")` explícito en `TestMain` de cada paquete afectado | Las aserciones en inglés existentes siguen válidas; el parsing se testea como función pura |
| **Salida máquina** | JSON/YAML **no se traducen**; solo el formatter `text` | Contrato machine-readable estable |
| **Logs** | Se mantienen en inglés | Convención de log-processing (journald) |
| **Prompts interactivos** | Se incluyen (sub-paso aparte) | Son superficie de usuario (`account remove`, OAuth) |

---

## 3. Arquitectura

```
internal/i18n/
  i18n.go        // bundle + Init + L()/Ld() (localizador global)
  detect.go      // ParseLocale (función PURA) + DetectLanguage (lee env)
  locales/
    en.json      // catálogo por defecto (fallback)
    es.json
  i18n_test.go   // matriz de detección + completitud de catálogos
```

**API pública** (todo el paquete usa estas dos funciones):

```go
// Init fija el idioma de la ejecución. Se llama UNA vez por proceso.
// lang es un código BCP-47 ("en", "es", "es-ES"...). Si el idioma no está
// en el bundle, go-i18n cae al idioma por defecto (en).
func Init(lang string)

// L devuelve el mensaje traducido para el id. Ej.: L("cmd.list.header.type").
func L(id string) string

// Ld es L con datos de plantilla. Ej.: Ld("err.account.notfound", map[string]any{"Name": n})
func Ld(id string, data map[string]any) string
```

**Producción** (en `PersistentPreRun` de `main.go`):

```go
lang := i18n.DetectLanguage()          // lee --lang / LC_ALL / LC_MESSAGES / LANG
if f := cmd.Flags().Lookup("lang"); f != nil && f.Value.String() != "" {
    lang = f.Value.String()            // --lang gana siempre
}
i18n.Init(lang)
```

**Tests** (en `TestMain` de cada paquete que aserte mensajes):

```go
func TestMain(m *testing.M) {
    i18n.Init("en") // fija inglés: las aserciones existentes siguen pasando
    os.Exit(m.Run())
}
```

---

## 4. Tareas atómicas

### FASE 1 — Infraestructura

#### Tarea 1.1 — Añadir la dependencia

- **Objetivo**: `go-i18n/v2` disponible en el módulo.
- **Comandos**:
  ```bash
  go get github.com/nicksnyder/go-i18n/v2@latest
  go mod tidy
  ```
- **Conceptos Go**: `go get` añade la dependencia a `go.mod`; `go mod tidy`
  limpia las que no se usan. Verás que aparece `golang.org/x/text` en
  `go.mod` (requisito de go-i18n).
- **Verificación**: `go build ./...` sigue compilando.

#### Tarea 1.2 — `internal/i18n/detect.go`: parsing puro + detección

- **Ficheros**: `internal/i18n/detect.go` (nuevo).
- **Conceptos Go**:
  - *Función pura*: `ParseLocale` no lee entorno ni estado global →
    testable con tabla de casos sin tocar variables de entorno.
  - *Strings*: `strings.Split`, `strings.ReplaceAll`, `strings.TrimPrefix`
    para normalizar el locale POSIX.
  - *`golang.org/x/text/language`*: `language.Parse("es-ES")` devuelve un
    `language.Tag`; `tag.String()` lo serializa a BCP-47.
- **Qué hacer**:
  ```go
  // ParseLocale normaliza un locale POSIX ("es_ES.UTF-8") a BCP-47 ("es-ES")
  // y devuelve el idioma base ("es"). Devuelve "en" si no se puede parsear
  // o si el locale es "C"/"POSIX" (indefinido → inglés por defecto).
  func ParseLocale(locale string) string {
      locale = strings.TrimSpace(locale)
      if locale == "" || locale == "C" || locale == "POSIX" {
          return "en"
      }
      // "es_ES.UTF-8@euro" -> "es-ES" (quitar modificadores y codificación)
      locale = strings.SplitN(locale, "@", 2)[0]
      locale = strings.SplitN(locale, ".", 2)[0]
      locale = strings.ReplaceAll(locale, "_", "-")
      tag, err := language.Parse(locale)
      if err != nil {
          return "en"
      }
      base, _ := tag.Base() // solo el idioma base: "es-ES" -> "es"
      return base.String()
  }

  // DetectLanguage devuelve el idioma del entorno en orden de prioridad:
  // --lang (vía flag) > LC_ALL > LC_MESSAGES > LANG. El flag se aplica en
  // main.go; aquí solo se leen las variables POSIX.
  func DetectLanguage() string {
      for _, env := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
          if v := os.Getenv(env); v != "" {
              if lang := ParseLocale(v); lang != "en" {
                  return lang
              }
          }
      }
      return "en"
  }
  ```
  > ⚠️ Nota: si `LC_ALL` existe pero parsea a "en" (ej. `C.UTF-8`), se sigue
  > mirando `LANG`; por eso se retorna solo cuando `lang != "en"`. Si al
  > final no hay nada, `en`.
- **Verificación**: `go vet ./internal/i18n/...`.

#### Tarea 1.3 — `internal/i18n/i18n.go`: bundle + Init + L()

- **Ficheros**: `internal/i18n/i18n.go` (nuevo), `internal/i18n/locales/en.json`, `internal/i18n/locales/es.json`.
- **Conceptos Go**:
  - *`//go:embed`*: directiva de compilación que incrusta ficheros en el
    binario (funciona en builds empaquetados deb/rpm sin ficheros en runtime).
  - *`sync.Once` / init*: el bundle se construye una sola vez (package-level).
  - *Maps*: los catálogos JSON se cargan con `bundle.LoadMessageFile`.
- **Qué hacer**:
  ```go
  //go:embed locales/*.json
  var localeFS embed.FS

  var (
      bundle *i18n.Bundle
      l      *i18n.Localizer
  )

  func init() {
      bundle = i18n.NewBundle(language.English) // default = en (fallback)
      bundle.RegisterUnmarshalFunc("json", json.Unmarshal)
      // Cargar todos los catálogos embebidos
      entries, _ := localeFS.ReadDir("locales")
      for _, e := range entries {
          f, _ := localeFS.Open("locales/" + e.Name())
          bundle.ParseMessageFile(f, e.Name())
      }
  }

  // Init fija el idioma de la ejecución.
  func Init(lang string) {
      l = i18n.NewLocalizer(bundle, lang)
  }

  // L devuelve la traducción del id (fallback: el idioma por defecto del bundle).
  func L(id string) string {
      if l == nil { return id } // Init no llamado: devolver el id (debug-friendly)
      return l.MustLocalize(&i18n.LocalizeConfig{MessageID: id})
  }

  // Ld devuelve la traducción aplicando datos de plantilla ({{.Name}}).
  func Ld(id string, data map[string]any) string {
      if l == nil { return id }
      return l.MustLocalize(&i18n.LocalizeConfig{MessageID: id, TemplateData: data})
  }
  ```
  > ⚠️ `MustLocalize` panica si el id no existe → el test de completitud
  > (Tarea 1.5) garantiza que nunca ocurra. Alternativa sin panic:
  > `Localize` + devolver el id si hay error (elegir UNA y documentarlo).
- **Catálogos** (formato JSON plano; los parámetros usan `{{.Nombre}}`):
  ```json
  // locales/en.json
  {
    "cmd.list.header.type": "Type",
    "cmd.list.empty.root": "The root folder is empty.",
    "err.account.notfound": "account '{{.Name}}' does not exist"
  }
  ```
  ```json
  // locales/es.json  (mismos ids)
  {
    "cmd.list.header.type": "Tipo",
    "cmd.list.empty.root": "La carpeta raíz está vacía.",
    "err.account.notfound": "la cuenta '{{.Name}}' no existe"
  }
  ```
- **Verificación**: `go build ./internal/i18n/...`.

#### Tarea 1.4 — Flag global `--lang` + init en `main.go`

- **Ficheros**: `cmd/onecloudriver/main.go`.
- **Conceptos Go**: *flags persistentes* (`PersistentFlags`) → disponibles en
  todos los subcomandos; `PersistentPreRun` se ejecuta antes de cada RunE.
- **Qué hacer**:
  1. En `init()`: `rootCmd.PersistentFlags().String("lang", "", "Language override (e.g. es, en). Default: system locale")`.
  2. Al principio de `PersistentPreRun` (antes de logging/manager):
     ```go
     lang, _ := cmd.Flags().GetString("lang")
     if lang == "" {
         lang = i18n.DetectLanguage()
     }
     i18n.Init(lang)
     ```
  3. Importar `internal/i18n` en `main.go`.
- **Verificación**: `go build ./cmd/onecloudriver/...`.

#### Tarea 1.5 — Tests de infraestructura (`internal/i18n/i18n_test.go`)

- **Ficheros**: `internal/i18n/i18n_test.go` (nuevo).
- **Conceptos Go**: *table-driven tests*; `t.Setenv` (solo sin `t.Parallel`);
  función pura → casos sin entorno.
- **Qué hacer**:
  1. `TestParseLocale` (tabla): `""`→en, `"C"`→en, `"POSIX"`→en,
     `"es_ES.UTF-8"`→es, `"en_US"`→en, `"es"`→es, `"fr_FR"`→fr,
     `"es_ES@euro"`→es, basura `"xyz123"`→en.
  2. `TestDetectLanguage` (tabla con `t.Setenv`): sin env→en; solo `LANG=es_ES.UTF-8`→es; `LC_ALL=fr_FR` + `LANG=es`→fr (LC_ALL gana); `LC_ALL=C.UTF-8` + `LANG=es_ES`→es.
  3. `TestLocalize_EnglishAndSpanish`: `i18n.Init("es")` → `L("cmd.list.empty.root")` == `"La carpeta raíz está vacía."`; `i18n.Init("en")` → cadena EN; `Ld("err.account.notfound", ...)` con datos.
  4. `TestCatalogCompleteness`: leer los `.go` de `cmd/` e `internal/` con
     regexp `L(?:d)?\("([a-z0-9.]+)"` → todos los ids existen en `en.json`;
     y todo id de `en.json` existe en `es.json` (y viceversa).
- **Verificación**: `go test ./internal/i18n/... -count=1`.

---

### FASE 2 — Hacer localizables los strings (roll-out)

> Estrategia: **trocear por comando** para que cada PR sea revisable. Cada
> tarea: sustituir el literal por `L("id")`/`Ld("id", data)` y añadir el id a
> `en.json` + `es.json` (traducción).

#### Tarea 2.0 — `TestMain` con `i18n.Init("en")` en `cmd/onecloudriver`

- **Ficheros**: `cmd/onecloudriver/smoke_test.go` (extender el `TestMain` ya existente).
  ⚠️ No `main_test.go`: solo puede haber un `TestMain` por paquete, y
  `smoke_test.go` ya lo define para construir el binario de smoke tests.
  Añadir `i18n.Init("en")` al inicio de ese `TestMain`.
- **Por qué**: en cuanto la Fase 2 envuelva strings, los tests que asertan
  inglés dependerían del locale de la máquina de desarrollo (ej.
  `es_ES.UTF-8`). Fijar `en` en `TestMain` los hace deterministas.
- **Conceptos Go**: `TestMain(m *testing.M)` es el punto de entrada de los
  tests del paquete; `m.Run()` ejecuta la suite.
- **Verificación**: `go test ./cmd/onecloudriver/... -count=1` sigue verde.

#### Tarea 2.1 — Formatter `text` (`output.go`)

- **Ficheros**: `cmd/onecloudriver/output.go`, catálogos.
- **Qué hacer**: envolver headers (`TYPE`, `NAME`, `SIZE`, `MODIFIED`) y
  labels (`File`, `Folder`, `directory`, `file`, `(N elements)`). Los ids:
  `fmt.list.header.type`, `fmt.item.file`, `fmt.item.folder`,
  `fmt.item.directory`, `fmt.item.elements` (`{{.Count}}`).
- **Verificación**: `go test ./cmd/onecloudriver/... -run 'Test.*Output|TestListCmd' -count=1`.

#### Tarea 2.2 — Comandos de ítems (list, info, download, mkdir, rm, rename, mv, copy, upload)

- **Ficheros**: `cmd/onecloudriver/{list,info,download,mkdir,rm,rename,mv,copy,upload}.go`, catálogos.
- **Qué hacer**: envolver los `fmt.*` de cada `RunE` (mensajes de progreso y
  confirmación) con ids `cmd.<comando>.*`.
- **Errores**: se mantienen en inglés (los `fmt.Errorf("error ...: %w", err)`
  NO se localizan). Decisión de diseño: son contexto de depuración y
  `item_commands_coverage_test.go` aserta sus frases literales. Ver §8.1.
- **copy.go**: al envolverlo, corregir el `\n` literal de
  `fmt.Printf("Copy started. Monitor progress at:\\n%s\\n", ...)` (bug
  preexistente: imprime backslash-n en vez de salto de línea).
- **Verificación**: `go test ./cmd/onecloudriver/... -count=1`.

#### Tarea 2.3 — account, service, mount, sync (mayor superficie)

- **Ficheros**: `cmd/onecloudriver/{account,service,mount,sync,common}.go`, catálogos.
- **Qué hacer**: envolver todo el `fmt.*` de consola (no logs). `service.go`
  (29 call sites) se puede trocear en dos commits si es necesario.
- **Verificación**: `go test ./cmd/onecloudriver/... -count=1`.

#### Tarea 2.4 — Salidas de consola de `internal/auth` e `internal/fs`

- **Ficheros**: `internal/auth/flow.go` (mensajes OAuth), `internal/fs/mount.go` (montaje/offline), catálogos, `TestMain` en esos paquetes si asertan strings.
- **Qué hacer**: envolver solo lo que imprime a consola del usuario (los logs
  vía zerolog se quedan EN).
- **Verificación**: `make test-unit`.

---

### FASE 3 — Help de cobra (la parte delicada)

#### Tarea 3.1 — Pase de localización del árbol de comandos

- **Ficheros**: `cmd/onecloudriver/main.go`, catálogos.
- **Problema**: `Short`/`Long`/flag-usage se asignan en `init()` (estáticos) y
  cobra **no** ejecuta `PersistentPreRun` para `--help`.
- **Solución propuesta**:
  1. Un helper `localizeCommandTree(root *cobra.Command)` que recorre
     `root.Commands()` y asigna `cmd.Short = L("cmd.<name>.short")`,
     `cmd.Long = L("cmd.<name>.long")` y los `flag.Usage` localizados.
  2. Invocarlo desde `PersistentPreRun` (para la ejecución) **y** desde un
     `rootCmd.SetHelpFunc`/`SetUsageFunc` propio que primero localiza el
     árbol y luego delega en el render por defecto.
- **Riesgo y salida de emergencia**: si el render de ayuda se resiste, se
  documenta que el help queda EN y solo los mensajes runtime se traducen
  (cumple los objetivos 1-2 de la issue). Decidir antes de empezar.
- **Verificación**: `onecloudriver --help`, `onecloudriver list --help` y
  `onecloudriver list -h` en consola ES muestran textos traducidos; `LANG=C`
  muestra EN.

---

### FASE 4 — Catálogo español completo

#### Tarea 4.1 — Traducir `es.json`

- **Ficheros**: `internal/i18n/locales/es.json`.
- **Qué hacer**: traducir cada id usando la terminología de `docs/MANUAL.es.md`
  (consistencia EN/ES). Mantener `{{.Placeholders}}` intactos.
- **Verificación**: el test `TestCatalogCompleteness` (Tarea 1.5) pasa;
  `LANG=es_ES.UTF-8 onecloudriver list` muestra salida en español.

---

## 5. Verificación final

```bash
go build ./...
go vet ./internal/i18n/... ./cmd/onecloudriver/...
go test ./internal/i18n/... -count=1
make test-unit          # con TestMain fijando "en" en los paquetes afectados
make lint-all           # si golangci-lint está disponible (si no, la CI)
```

**Prueba manual** (binario local):

```bash
LANG=es_ES.UTF-8 ./onecloudriver list -a tu@cuenta.com   # salida en español
./onecloudriver list --lang es -a tu@cuenta.com          # override por flag
LANG=C ./onecloudriver list -a tu@cuenta.com             # fallback inglés
./onecloudriver list -o json -a tu@cuenta.com | head     # JSON sin traducir (contrato)
```

---

## 6. Commits (convenciones del repo)

Trocear por fase, Conventional Commits:

```bash
feat(i18n): add locale detection and embedded catalogs (#30)      # Fase 1
refactor(cli): localize item command output (#30)                 # Tarea 2.2
refactor(cli): localize account/service/mount/sync output (#30)   # Tarea 2.3
...
feat(i18n): localize cobra help texts (#30)                       # Fase 3
feat(i18n): add full Spanish catalog (#30)                        # Fase 4
```

Rama: `issue-30-i18n-cli`. PR con `Closes #30`.

## 7. Riesgos

1. **Help de cobra** (Fase 3): la única parte con riesgo real; mitigación en
   Tarea 3.1 (salida de emergencia documentada).
2. **Volumen**: 131 call sites → PRs troceados por comando, no uno gigante.
3. **Cobertura**: envolver strings no toca lógica; el `TestMain` con `en`
   fijo hace de red de seguridad para las aserciones existentes.
4. **`MustLocalize` panic** si falta un id: lo previene `TestCatalogCompleteness` (corre en CI).

---

## 8. Ampliaciones y correcciones (revisión 2026-08-31)

Resultado de la revisión del plan contra la implementación. Matiza o amplía
lo anterior donde se indica.

### 8.1 Errores de contexto se mantienen en inglés (decisión)

Los `fmt.Errorf("error <contexto>: %w", err)` de los comandos **no se
localizan**. Solo se traducen los mensajes de éxito/progreso que van a
stdout/stderr como texto de usuario. Razón: son contexto de depuración, y
`item_commands_coverage_test.go` aserta sus frases literales
(`"error creating folder:"`, `"error listing files:"`, …). Mantenerlos en
inglés evita churn de tests y conserva los errores greppables.

Esto **modifica** el texto de la Tarea 2.2 (que antes proponía envolverlos
con `L("err.<comando>.context")`).

### 8.2 `MustLocalize` (resuelve la "open question" de la Tarea 1.3)

Se mantiene `MustLocalize` (ya implementado). El panic solo ocurre si falta
un id, y `TestCatalogCompleteness` lo impide en CI. La alternativa
(`Localize` + devolver el id ante error) se descarta por ahora para no
ocultar ids rotos.

### 8.3 `--lang` debe normalizarse (ampliación de la Tarea 1.4)

`i18n.Init` espera BCP-47 (`es-ES`), pero `--lang` admite `es_ES` o
`es_ES.UTF-8`. Normalizar antes de `Init`:

```go
lang, _ := cmd.Flags().GetString("lang")
if lang == "" {
    lang = i18n.DetectLanguage()
}
i18n.Init(i18n.ParseLocale(lang)) // normaliza --lang (es_ES → es)
```

### 8.4 Subtítulo y unidades del formatter (ampliación de la Tarea 2.1)

- Eliminar el `strings.ToLower(i18n.L("fmt.item.file"))` del subtítulo
  "file info"/"directory info": no escala a idiomas sin mayúsculas triviales
  y en español produce "archivo info" (pobre). Usar ids dedicados:
  `fmt.info.file` = "file info" / `fmt.info.folder` = "directory info"
  (ES: "información del archivo" / "información de la carpeta").
- Localizar las unidades hardcoded de `output.go`: `"%d B"` y
  `"%d bytes (%.2f KB)"`.
- Eliminar el id redundante `fmt.item.directory` (solo existía para el
  `ToLower`); `fmt.item.file`/`fmt.item.folder` cubren el campo `Type`.

### 8.5 Pluralización (ampliación de la Tarea 2.1)

Los mensajes con conteo (`fmt.item.elements`, unidades de tamaño) deben usar
la pluralización CLDR de go-i18n con claves `.one`/`.other`, no un
`{{.Count}}` fijo:

```json
"fmt.item.elements": {
  "one":   "- ({{.Count}} element)",
  "other": "- ({{.Count}} elements)"
}
```

`Ld` lo resuelve automáticamente según `Count`.

### 8.6 Fase 3 (help de cobra) — desglose real

Localizar el help no es solo mutar `Short`/`Long`/`flag.Usage`. Cobra genera
texto propio hardcodeado (`Usage:`, `Available Commands:`, `Flags:`,
`Global Flags:`, `Use "…" for more information`, el comando `help`
auto-generado, `completion`, `--version`). Desglosar en sub-tareas:

1. `localizeCommandTree(root)`: asigna `Short`/`Long`/`flag.Usage` con ids
   **estáticos** (`cmd.<name>.short`, `cmd.<name>.long`, `flag.<name>.usage`).
   Evitar ids dinámicos (`L("cmd."+name+".short")`): `TestCatalogCompleteness`
   no los detectaría (la regex solo captura literales) y el panic de
   `MustLocalize` quedaría sin red de seguridad.
2. `SetHelpFunc`/`SetUsageFunc`/`SetFlagErrorFunc` con plantillas localizadas
   para el marco del help (títulos de sección, línea "Use … for more
   information", etc.).
3. Comando `help` y `completion`: decidir si se localizan o se dejan EN
   (salida de emergencia documentada).

### 8.7 Completitud de catálogos con ids estáticos

`TestCatalogCompleteness` captura `L("...")`/`Ld("...")` con regex. Cualquier
id construido dinámicamente queda fuera de su cobertura. Regla del plan:
**todos los ids deben ser literales**. Si en Fase 3 se necesita un id
dinámico, añadir a `collectMessageIDs` una enumeración explícita de esos ids.

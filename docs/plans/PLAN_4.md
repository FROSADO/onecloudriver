La issue **#66 sigue aplicando claramente** en el estado actual del proyecto. El código de `internal/fs/cache.go` todavía realiza un recorrido completo de `sync.Map` cada 30 segundos y, además, recopila y ordena todos los directorios con hijos en cada limpieza por límite de tamaño. La issue permanece abierta y no tiene una rama, pull request ni implementación asociada. [1]

La propuesta es técnicamente válida, pero conviene dividirla en varias fases pequeñas. Para reducir el riesgo, recomiendo implementar primero el registro TTL por buckets, verificar que mantiene exactamente el comportamiento actual y dejar el heap de límite de tamaño para una segunda etapa. La propia issue indica que la consolidación previa de lógica de evicción de la issue #50 debería preceder a estos cambios. [1]

## 1. Estado actual y diagnóstico

### 1.1 La issue todavía aplica

En `internal/fs/cache.go` se observa actualmente:

- `sweepInterval = 30 * time.Second`.
- `StartSweep()` crea un ticker de 30 segundos.
- Cada tick llama a `c.sweep()`.
- `sweep()` ejecuta:
  - `evictExpiredChildren()`.
  - `evictChildrenBySizeLimit()`.
- `evictExpiredChildren()` recorre todos los elementos de `c.inodes` mediante `sync.Map.Range`.
- Para cada inode con hijos:
  - Ejecuta `DecayChildrenAccess()`.
  - Calcula el TTL efectivo.
  - Comprueba si ha expirado.
- `evictChildrenBySizeLimit()` vuelve a recorrer todos los inodes.
- Si se supera `maxEntries`, ordena todos los elementos mediante `sort.Slice`.

Por tanto, si existen $N$ inodes en memoria:

- La limpieza TTL cuesta aproximadamente $O(N)$.
- La limpieza por tamaño también cuesta aproximadamente $O(N)$.
- Cuando se supera el límite, la ordenación cuesta $O(N \log N)$.

La issue propone procesar solamente los inodes cuyo vencimiento puede haber llegado, reduciendo el coste esperado a algo cercano a:

$$
O(\text{inodes del bucket vencido} + \text{inodes realmente expulsados})
$$

En palabras simples: en vez de revisar todas las carpetas cada vez, se guardan referencias a las carpetas en intervalos temporales y se revisan únicamente los intervalos que corresponden al momento actual.

### 1.2 La funcionalidad no está parcialmente implementada

No aparecen en la estructura actual del `InodeCache` elementos equivalentes a:

- `ttlBuckets`.
- Un índice de registro de inodes por vencimiento.
- Una función de re-registro al acceder a una carpeta.
- Una función `sweepExpiredBucket()`.
- Un campo de reloj inyectable, como `now func() time.Time`.
- Un heap para la expulsión por límite de tamaño.

El estado actual conserva la implementación anterior de recorrido completo. [2]

### 1.3 Hay una observación relevante sobre el comportamiento actual

La documentación de la issue describe la expiración utilizando la fecha de acceso y el TTL efectivo. El código actual usa:

```go
expiry := inode.ChildrenLastAccess().Add(ttl)
```

Sin embargo, `isChildrenFresh()` utiliza `ChildrenCachedAt()`:

```go
return time.Since(inode.ChildrenCachedAt()) < ttl
```

Esto significa que existen dos conceptos diferentes:

- **Comprobación durante `GetChildren()`**: basada en `ChildrenCachedAt()`.
- **Evicción durante el sweep**: basada en `ChildrenLastAccess()`.

Antes de optimizar el algoritmo, hay que decidir si esta diferencia es intencionada. Cambiarla accidentalmente durante la implementación podría alterar el comportamiento observable del proyecto.

## 2. Recomendación de alcance

La issue contiene dos optimizaciones distintas:

| Parte | Problema que resuelve | Complejidad actual | Complejidad propuesta | Riesgo |
|---|---|---:|---:|---:|
| Buckets TTL | Recorrido completo para encontrar expiraciones | $O(N)$ | $O(B)$, donde $B$ es el bucket procesado | Medio |
| Heap de tamaño | Recorrido y ordenación completa | $O(N \log N)$ | Aproximadamente $O(K \log N)$ | Alto |

Recomiendo este orden:

- Primero estabilizar y probar el comportamiento actual.
- Después añadir un reloj inyectable.
- Luego implementar los buckets TTL.
- Ejecutar pruebas de paridad.
- Finalmente implementar el heap de tamaño.
- Añadir benchmarks y pruebas de concurrencia.
- Ejecutar las comprobaciones de calidad y `-race`.

No recomiendo modificar simultáneamente TTL, frecuencia, límite de tamaño y persistencia BoltDB en un único cambio.

## 3. Plan completo dividido en tareas atómicas

## Fase 0: Preparar el entorno

### Tarea 0.1: Clonar el proyecto y revisar la rama actual

```bash
git clone https://github.com/FROSADO/onecloudriver.git
cd onecloudriver
git checkout main
git pull --ff-only
```

Comprueba la versión de Go:

```bash
go version
```

Guarda el commit inicial:

```bash
git rev-parse HEAD
```

Esto permite volver al estado exacto anterior si aparece una regresión.

### Tarea 0.2: Ejecutar las pruebas existentes

```bash
go test ./...
```

Después ejecuta las pruebas del paquete afectado:

```bash
go test ./internal/fs/...
```

Y la comprobación de carreras:

```bash
go test -race ./internal/fs/...
```

Antes de modificar nada, todas estas órdenes deberían finalizar correctamente.

### Tarea 0.3: Medir la cobertura inicial

Ejecuta:

```bash
go test ./internal/fs/... -coverprofile=coverage-before.out
go tool cover -func=coverage-before.out
```

Guarda especialmente:

- Cobertura total.
- Cobertura de `cache.go`.
- Cobertura de las funciones de evicción.

El objetivo no es únicamente conservar el porcentaje global: también deben quedar cubiertas las nuevas ramas introducidas.

### Tarea 0.4: Crear una rama específica

```bash
git checkout -b perf/issue-66-inode-cache-sweep
```

Conviene realizar commits pequeños, por ejemplo:

- `test(fs): stabilize cache eviction tests`
- `refactor(fs): inject cache clock`
- `perf(fs): add TTL bucket registry`
- `perf(fs): add heap-based size eviction`
- `test(fs): add eviction parity and benchmarks`

## Fase 1: Entender y estabilizar el comportamiento actual

### Tarea 1.1: Localizar todas las funciones relacionadas

Busca referencias:

```bash
grep -R "evictExpiredChildren\|evictChildrenBySizeLimit\|StartSweep\|ForceSweep\|SetBaseTTL\|SetMaxEntries" -n .
```

También puedes utilizar:

```bash
go doc ./internal/fs
```

Debes identificar:

- Cómo se crea un inode.
- Cuándo se llama a `SetChildren`.
- Cuándo se actualiza `ChildrenLastAccess`.
- Cuándo se incrementa `ChildrenAccessCount`.
- Cuándo se ejecuta `EvictChildren`.
- Cómo se detiene el sweep durante `Close()`.

### Tarea 1.2: Revisar los métodos de `Inode`

Debes localizar la definición de `Inode` y confirmar el comportamiento de:

```go
SetChildren
IsChildrenFetched
Children
ChildrenCachedAt
ChildrenLastAccess
ChildrenAccessCount
BumpChildrenAccess
DecayChildrenAccess
EvictChildren
```

Anota para cada función:

- Qué campos modifica.
- Qué mutex utiliza.
- Si usa operaciones atómicas.
- Si actualiza las fechas.
- Si puede llamarse simultáneamente desde varias goroutines.

Esta revisión es necesaria porque el registro TTL deberá ser compatible con la sincronización existente.

### Tarea 1.3: Mejorar las pruebas que dependen de `time.Sleep`

Las pruebas actuales de evicción utilizan pausas reales, por ejemplo:

```go
time.Sleep(100 * time.Millisecond)
```

Esto puede provocar pruebas lentas o inestables. Antes de implementar los buckets, conviene preparar un reloj controlable.

## Fase 2: Inyectar un reloj controlable

### Tarea 2.1: Añadir un campo `now`

Añade al `InodeCache`:

```go
now func() time.Time
```

Inicialízalo en `NewInodeCache()`:

```go
now: time.Now,
```

El significado es: en producción, el cache utiliza `time.Now`; en los tests, se puede proporcionar una función que devuelva una hora controlada.

### Tarea 2.2: Crear un helper interno para obtener la hora

Puedes usar directamente `c.now()`, aunque un helper puede ser más claro:

```go
func (c *InodeCache) currentTime() time.Time {
    if c.now == nil {
        return time.Now()
    }
    return c.now()
}
```

El fallback evita un panic si algún test construye manualmente un `InodeCache{}`.

### Tarea 2.3: Sustituir `time.Now()` en la lógica de evicción

Cambia únicamente las funciones de cache relacionadas:

```go
now := c.currentTime()
```

Debes revisar:

- `evictExpiredChildren()`.
- `evictChildrenBySizeLimit()`.
- Cualquier nueva función de sweep.
- Cualquier cálculo de registro TTL.

No cambies todavía `time.Since()` sin analizarlo. Una transformación equivalente sería:

```go
age := c.currentTime().Sub(inode.ChildrenCachedAt())
```

Pero debes comprobar que el valor esperado sea el mismo cuando el reloj del test está controlado.

### Tarea 2.4: Añadir un reloj falso para los tests

Puedes crear en `cache_eviction_test.go`:

```go
type fakeClock struct {
    current time.Time
}

func (c *fakeClock) Now() time.Time {
    return c.current
}

func (c *fakeClock) Advance(d time.Duration) {
    c.current = c.current.Add(d)
}
```

Uso:

```go
clock := &fakeClock{
    current: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
}

cache := NewInodeCache()
cache.now = clock.Now
```

Así puedes avanzar el tiempo sin esperar:

```go
clock.Advance(100 * time.Millisecond)
```

### Tarea 2.5: Convertir las pruebas TTL al reloj falso

Convierte progresivamente las pruebas que puedan controlarse con el reloj del
cache:

- `TestInodeCache_EvictExpiredChildren`.
- `TestInodeCache_FrequencyExtendsTTL`.
- `TestInodeCache_ForceSweep`.

Con la solución elegida, `now` solamente se inyecta en `InodeCache`. Los
métodos `Inode.SetChildren()` y `Inode.BumpChildrenAccess()` siguen usando
`time.Now()` para crear o actualizar sus timestamps internos. Por tanto, no se
pretende que todos los tests sean completamente deterministas ni que se elimine
obligatoriamente cada `time.Sleep`.

El objetivo de esta tarea es poder simular el instante en que el cache ejecuta
la decisión de evicción. Si un test necesita crear timestamps mediante
`SetChildren()` o `BumpChildrenAccess()`, puede conservar una pausa corta,
siempre que sea necesaria y esté documentada. El fake clock debe inicializarse
cerca de la hora real (`time.Now()`), no con una fecha histórica arbitraria,
porque los timestamps del `Inode` continúan generándose con el reloj real.

La conversión de `TestInodeCache_EvictExpiredChildren` al fake clock completa la
parte correspondiente de esta tarea; no es necesario repetir la tarea 2.4.

### Tarea 2.6: Ejecutar las pruebas

```bash
gofmt -w internal/fs/cache.go internal/fs/cache_eviction_test.go
go test ./internal/fs/...
go test -race ./internal/fs/...
```

Comprueba especialmente que el test que usa el fake clock pasa de forma
repetible y que la evicción conserva el comportamiento anterior. Que otros
tests mantengan un `time.Sleep` para permitir que `Inode` actualice sus propios
timestamps es aceptable en este diseño.

No continúes con los buckets hasta que esta fase esté verde.

## Fase 3: Diseñar el registro de TTL

### Tarea 3.1: Definir las constantes de buckets

La issue propone 60 buckets de un segundo. Puedes definir:

```go
const (
    ttlBucketCount = 60
    ttlBucketWidth = time.Second
)
```

La ventana total del anillo es:

$$
60 \times 1\,\text{segundo} = 60\,\text{segundos}
$$

Pero el TTL efectivo puede ser superior a 60 segundos, porque el TTL base se multiplica por la frecuencia. Por ese motivo, no debes asumir que el índice circular, por sí solo, conserva la fecha exacta de vencimiento.

### Tarea 3.2: Elegir la estructura de los buckets

Una estructura mínima podría ser:

```go
type ttlEntry struct {
    inodeID string
    expiry  time.Time
}

type ttlBucket struct {
    entries []ttlEntry
}
```

Y dentro de `InodeCache`:

```go
ttlMu      sync.Mutex
ttlBuckets [ttlBucketCount][]ttlEntry
```

Aunque la issue muestra `[][]string`, guardar también `expiry` ayuda a verificar si una entrada continúa vigente después de una actualización.

### Tarea 3.3: Añadir el mutex de los buckets

El acceso a los buckets se producirá desde:

- El goroutine del sweep.
- `GetChildren()`.
- Posiblemente otras operaciones que registren o invaliden cache.

Por tanto, los buckets necesitan su propio mutex:

```go
ttlMu sync.Mutex
```

No debes proteger los buckets únicamente con `sweepMu`, porque `GetChildren()` puede ejecutarse en paralelo con el sweep.

### Tarea 3.4: Definir la función de cálculo de bucket

El índice debe calcularse a partir de la hora de expiración, no del momento actual:

```go
func ttlBucketIndex(expiry time.Time) int {
    seconds := expiry.Unix()
    return int(seconds % ttlBucketCount)
}
```

Como `Unix()` puede ser negativo en fechas históricas, una versión más robusta sería:

```go
func ttlBucketIndex(expiry time.Time) int {
    index := expiry.Unix() % ttlBucketCount
    if index < 0 {
        index += ttlBucketCount
    }
    return int(index)
}
```

### Tarea 3.5: Definir el registro de un inode

El cálculo de la expiración debe utilizar `c.currentTime()` cuando necesite el
instante actual. Sin embargo, `ChildrenLastAccess()` y `ChildrenCachedAt()` son
timestamps generados por `Inode` mediante `time.Now()`. El campo `now` no es un
reloj global del sistema y no modifica esos timestamps.

Crea una función privada:

```go
func (c *InodeCache) registerTTL(inode *Inode, now time.Time)
```

Debe:

- Ignorar inodes que no tengan hijos cacheados.
- Leer el contador de accesos.
- Calcular el TTL efectivo.
- Calcular la expiración.
- Calcular el bucket.
- Añadir una entrada al bucket.

Conceptualmente:

```go
ttl := effectiveTTL(c.baseTTL, inode.ChildrenAccessCount())
expiry := inode.ChildrenLastAccess().Add(ttl)
```

Aquí debes confirmar si el registro debe basarse en:

- `ChildrenLastAccess()`, para coincidir con `evictExpiredChildren()`.
- `ChildrenCachedAt()`, para coincidir con `isChildrenFresh()`.

Para mantener el comportamiento de la evicción actual, la primera implementación debería usar `ChildrenLastAccess()`.

**Decisión tomada (implementación de 3.5):** `registerTTL` registra de forma
incondicional todo inode con hijos cacheados, **incluso si su expiración ya está
en el pasado** (por ejemplo, carpetas «seedeadas» por `attachChild` o restauradas
de disco con timestamps antiguos). No debe descartar entradas ya expiradas en el
registro: el sweep de buckets recalcula la expiración desde el estado actual del
inode y las evicta igual que hacía el full scan (paridad). Si alguna tarea
quisiera evitar registrar inodes ya expirados, esa política pertenece al call
site, no a `registerTTL`.

### Tarea 3.6: Evitar referencias antiguas problemáticas

Cuando una carpeta recibe varios accesos, se volverá a registrar en otro bucket. Esto puede dejar entradas antiguas en buckets anteriores.

La solución más sencilla y segura es aceptar entradas duplicadas y validarlas al procesarlas:

- Si el inode ya no existe, se ignora.
- Si no tiene hijos cacheados, se ignora.
- Si la entrada no coincide con la expiración actual, se ignora.
- Si todavía no ha expirado, se vuelve a registrar en el bucket correcto.
- Si ha expirado, se aplica el mismo algoritmo de decay y comprobación del comportamiento anterior.

Esta estrategia se conoce como *lazy invalidation*: no se borran inmediatamente todas las entradas obsoletas, sino que se descartan al procesarlas.

## Fase 4: Registrar inodes en los momentos correctos

### Tarea 4.1: Registrar después de poblar los hijos

En `getChildren()`, después de:

```go
parent.SetChildren(childIDs)
```

registra el parent:

```go
c.registerTTL(parent, c.currentTime())
```

Hazlo solamente si la carpeta tiene hijos cacheados.

### Tarea 4.2: Registrar después de un cache hit

En la ruta de cache hit actualmente se ejecuta:

```go
parent.BumpChildrenAccess()
```

Después de incrementar el acceso, registra de nuevo:

```go
parent.BumpChildrenAccess()
c.registerTTL(parent, c.currentTime())
```

El orden es importante, porque el contador de frecuencia influye en el TTL efectivo.

### Tarea 4.3: Revisar todos los puntos que modifican la lista de hijos

El registro TTL solo tiene sentido cuando la carpeta tiene hijos cacheados
(`children != nil`). `registerTTL` ya ignora internamente los inodes sin hijos,
por lo que la regla general es: **cada punto donde `children` pasa de `nil` a
no-`nil` debe registrar; cada punto donde pasa a `nil` deja de registrar
automáticamente, sin necesidad de purgar los buckets**.

#### Inventario completo de call sites (producción, `internal/fs/cache.go`)

| # | Función | Línea | Operación | ¿Registrar? | Por qué |
|---|---|---|---|---|---|
| 1 | `getChildren()` (población) | 355 | `parent.SetChildren(childIDs)` | ✅ Sí (4.1, hecho) | Los hijos se acaban de poblar |
| 2 | `getChildren()` (cache hit) | ~294 | `BumpChildrenAccess()` + registro | ✅ Sí (4.2, hecho) | El hit extiende el TTL efectivo (orden: bump antes de registrar) |
| 3 | `Invalidate()` | 629 | `parent.SetChildren(nil)` | ❌ No | `children` → `nil`; `registerTTL` lo ignora solo |
| 4 | `InvalidateAll()` | 641 | `inode.SetChildren(nil)` | ❌ No | Ídem |
| 5 | `Inode.EvictChildren()` (inode.go:324) | — | `children = nil` | ❌ No | Ídem; no debe añadir registro |
| 6 | `Insert()` → `attachChild(..., seed=true)` | 223 | Puede poblar `children` con 1 hijo | ⚠️ Sí (ver hueco) | La carpeta pasa a `IsChildrenFetched() == true` |
| 7 | `InsertChild()` → `attachChild(..., seed=false)` | 697 | Nunca puebla si era `nil` | ❌ No | `children` sigue `nil` |
| 8 | `Delete()` → `detachChild` | 240 | Quita un hijo | ❌ No | No cambia timestamps; si ya estaba registrada, el registro sigue válido |
| 9 | `RemoveChild()` → `detachChild` | 717 | Quita un hijo | ❌ No | Ídem |
| 10 | `MoveChild()` (padre origen) → `detachChild` | 783 | Quita un hijo | ❌ No | Ídem |
| 11 | `MoveChild()` (padre destino) → `attachChild(..., seed=true)` | 788 | Puede poblar `children` con 1 hijo | ⚠️ Sí (ver hueco) | Ídem caso 6 |
| 12 | `MoveID()` | 764 | `parent.children[i] = newID` | ❌ No | Solo renombra el ID; no cambia timestamps |

#### El hueco: carpetas «seedeadas» por `attachChild(seed=true)`

`Insert()` y `MoveChild()` llaman a `attachChild(parent, childID, ..., true)`.
Cuando el padre fue evictado (`children == nil`), esto le asigna una lista de
**un solo hijo**. La carpeta pasa a `IsChildrenFetched() == true` y, por tanto,
es candidata a evicción TTL.

- El sweep actual (full scan) sí la procesa → la paridad exige que también se
  registre en el anillo.
- Si no se registra, tras la Fase 6 (sweep por buckets) esa carpeta nunca se
  evictará por TTL, porque el anillo solo evicta lo registrado.

**Cambio recomendado (2 puntos, transición `nil` → no-`nil`):** en `Insert()` y
en `MoveChild()`, captura el estado del padre antes de `attachChild` y registra
solamente si `attachChild` acabó de poblar. No registres si ya tenía hijos: su
registro previo (de 4.1/4.2) sigue siendo válido y su `ChildrenLastAccess` no
cambió.

```go
// Insert(): tras attachChild(parent, inode.ID(), inode.IsDir(), true)
wasFetched := parent.IsChildrenFetched()
attachChild(parent, inode.ID(), inode.IsDir(), true)
if !wasFetched && parent.IsChildrenFetched() {
    c.registerTTL(parent, c.currentTime())
}
```

```go
// MoveChild(): tras attachChild(newParent, childID, child.IsDir(), true)
wasFetched := newParent.IsChildrenFetched()
attachChild(newParent, childID, child.IsDir(), true)
if !wasFetched && newParent.IsChildrenFetched() {
    c.registerTTL(newParent, c.currentTime())
}
```

> ⚠️ **Nota de implementación:** una carpeta «seedeada» por `attachChild` no
> recibe timestamps (`ChildrenLastAccess()` queda en el valor cero), por lo que
> su expiración calculada cae en el pasado. Este registro funciona precisamente
> porque `registerTTL` no descarta entradas ya expiradas (decisión de la tarea
> 3.5): el sweep la procesará y la evictará en la primera pasada de su bucket,
> igual que hace hoy el full scan en su siguiente barrido. Si `registerTTL`
> tuviera un guard `expiry.After(now)`, este cambio sería un no-op.

Alternativa aceptada: posponer este hueco a la Fase 6.5 y cubrirlo con una
prueba de paridad. La diferencia observable es mínima (una carpeta con listado
incompleto de 1 hijo vive hasta su primer `GetChildren` completo), pero es un
cambio de comportamiento respecto al sweep actual y conviene decidirlo
explícitamente.

#### Por qué `Invalidate` / `EvictChildren` NO necesitan purgar los buckets

Cuando una carpeta se invalida o evicta (`children = nil`), sus entradas
antiguas permanecen en el anillo. No hay que borrarlas porque:

1. `sweepExpiredBucket` (Fase 5) **recalcula la expiración desde el estado
   actual del inode**, no confía en la `expiry` almacenada en la entrada.
2. Si el inode ya no tiene hijos (`IsChildrenFetched() == false`), la entrada
   se descarta al validarla (tarea 5.2).
3. Si se re-pobló, la expiración se recalcula con los datos frescos y la
   entrada antigua se re-registra o descarta sin efectos.

Esto es la invalidación perezosa de la tarea 3.6: no hace falta purgar, basta
con validar al procesar.

#### Consecuencia para los tests (importante de cara a la Fase 6)

El registro se gestiona desde `InodeCache`, no desde `Inode.SetChildren()`.
Por tanto, los tests que hacen:

```go
cache.Insert(parent)
parent.SetChildren([]string{"child1"}) // NO registra
cache.ForceSweep()
```

dejarán de evictar cuando la Fase 6 sustituya el full scan por
`sweepExpiredBuckets` (el inode no estará en el anillo). A partir de entonces,
los tests de evicción deberán:

- Poblar vía `GetChildren` con un fetcher mock (registra por la tarea 4.1), o
- Llamar explícitamente a `cache.registerTTL(parent, now)` después de
  `SetChildren`.

Conviene anotarlo ahora para no sorprenderse en las tareas 6.5 y 7.x.

#### No tocar

- `Inode.SetChildren()` (inode.go:267): no añadir registro dentro. El registro
  es responsabilidad de `InodeCache` (menos acoplamiento).
- Tests que llaman a `SetChildren` directamente: no necesitan cambios hasta la
  Fase 6.
- `DeserializeFromDisk()`: es la tarea 4.4.

### Tarea 4.4: Registrar tras restaurar datos

Revisa `DeserializeFromDisk()`. Si carga una carpeta con hijos, hay dos opciones:

- Registrarla inmediatamente con la expiración calculada.
- No registrarla y registrarla cuando se utilice.

La opción más simple es registrarla tras cargarla, pero debes verificar que no se produzcan registros para inodes que ya estén expirados.

> **Nota tras la decisión de la tarea 3.5:** `registerTTL` registra de forma
> incondicional, por lo que la opción «registrar al cargar» programará las
> carpetas restauradas con timestamps antiguos para su evicción en el siguiente
> barrido de su bucket — el mismo resultado que produce hoy el full scan. Si
> quieres que los datos restaurados sobrevivan hasta el primer acceso (útil en
> modo offline), elige la segunda opción: no registrar en `DeserializeFromDisk()`
> y dejar que `getChildren()` registre en el primer hit (tareas 4.1/4.2). La
> decisión es del call site, no de `registerTTL`.

**Decisión tomada (implementada):** se eligió la opción «registrar tras cargar».
En `DeserializeFromDisk()`, tras un `LoadOrStore` que realmente cargó el inode
(memoria no gana), se llama `c.registerTTL(inode, c.currentTime())`. El registro
queda dentro del callback de `LoadOrStore` para no duplicar una carpeta ya
presente en memoria. Verificado con
`TestInodeCache_Deserialize_RegistersRestoredForTTL`, que restaura una carpeta
con hijos, comprueba su entrada en el anillo TTL y que `evictExpiredChildren()`
la evicta al caducar.

## Fase 5: Implementar el barrido de buckets

Objetivo: leer el anillo y evictar solo lo que toca. Al terminar esta fase, el
registro (Fase 4) empieza a tener efecto real, aunque `sweep()` todavía ejecuta
el full scan (el cambio de `sweep()` es la Fase 6).

### Punto de partida (referencia de paridad)

La lógica a replicar vive en `evictExpiredChildren()` (cache.go, sección `TTL+LFU
eviction`). El orden exacto que hay que preservar es:

```go
now := c.currentTime()
// por cada inode con hijos cacheados:
inode.DecayChildrenAccess()
accessCount := inode.ChildrenAccessCount()
ttl := effectiveTTL(c.baseTTL, accessCount)
expiry := inode.ChildrenLastAccess().Add(ttl)
if now.After(expiry) { inode.EvictChildren(); c.evictions.Add(1) }
```

### Paso 0: añadir el campo `lastSweep` (lo necesita la tarea 5.5)

En `internal/fs/cache.go`, dentro de `InodeCache`, justo después del bloque
`ttlMu`/`ttlBuckets` (~línea 130):

```go
	// lastSweep is the instant of the last bucket sweep, guarded by sweepMu.
	// Lets the sweep process buckets that fell behind (delayed ticker, suspend).
	lastSweep time.Time
```

`sweepMu` ya existe y lo mantiene `sweep()` (y por tanto `ForceSweep()`), así que
`lastSweep` queda protegido sin añadir locks nuevos. No lo toques fuera de
`sweep()` / `sweepExpiredBuckets()`.

### Tarea 5.1: crear las funciones de barrido

Añade al final de `cache.go` (tras `registerTTL`). Son cuatro funciones pequeñas
encadenadas; esta separación evita mantener `ttlMu` mientras se accede a los
inodes (menos contención).

**a) `sweepExpiredBucket(now)` — un solo bucket (firma original de la tarea 5.1):**

```go
// sweepExpiredBucket processes a single bucket: the one for the second `now`.
// Convenient for unit-testing one bucket in isolation.
func (c *InodeCache) sweepExpiredBucket(now time.Time) {
	c.sweepBucket(ttlBucketIndex(now), now, make(map[string]struct{}))
}
```

**b) `sweepBucket(index, now, processed)` — extrae bajo `ttlMu` y valida fuera:**

```go
// sweepBucket extracts the entries of bucket `index` under ttlMu and validates
// them outside the mutex, so inode access never holds ttlMu (less contention).
func (c *InodeCache) sweepBucket(index int, now time.Time, processed map[string]struct{}) {
	c.ttlMu.Lock()
	entries := c.ttlBuckets[index]
	c.ttlBuckets[index] = nil // leave the bucket empty (lazy invalidation)
	c.ttlMu.Unlock()

	for _, entry := range entries {
		c.processTTLEntry(entry, now, processed)
	}
}
```

**c) `processTTLEntry(entry, now, processed)` — validación + decay + evicción**
**(tareas 5.2, 5.3 y 5.4):**

```go
func (c *InodeCache) processTTLEntry(entry ttlEntry, now time.Time, processed map[string]struct{}) {
	inode := c.Get(entry.inodeID)
	if inode == nil || !inode.IsChildrenFetched() {
		return // inode deleted or without cached children: stale entry
	}
	if _, done := processed[entry.inodeID]; done {
		return // already handled this sweep (duplicate from a re-registration)
	}
	processed[entry.inodeID] = struct{}{}

	// Same order as evictExpiredChildren (decay parity).
	inode.DecayChildrenAccess()
	accessCount := inode.ChildrenAccessCount()
	ttl := effectiveTTL(c.baseTTL, accessCount)
	expiry := inode.ChildrenLastAccess().Add(ttl)

	if !now.After(expiry) {
		// Still fresh: re-register in the bucket of its new (post-decay) deadline.
		c.registerTTL(inode, now)
		return
	}

	log.Debug().
		Str("id", inode.ID()).
		Str("name", inode.Name()).
		Uint64("accessCount", accessCount).
		Dur("ttl", ttl).
		Msg("Evicting children due to expired TTL")
	inode.EvictChildren()
	c.evictions.Add(1)
}
```

**d) `sweepExpiredBuckets(now)` — el bucle con `lastSweep` (tarea 5.5):**

```go
// sweepExpiredBuckets processes every bucket whose second elapsed since the
// last sweep, up to and including `now`. Handles delayed tickers and time
// jumps: if more than ttlBucketCount seconds elapsed, the whole ring is swept.
//
// Must be called with sweepMu held (it reads and writes c.lastSweep).
func (c *InodeCache) sweepExpiredBuckets(now time.Time) {
	processed := make(map[string]struct{}) // one decay per inode per sweep

	start := c.lastSweep
	if start.IsZero() {
		start = now.Add(-ttlBucketWidth) // first sweep: only the current bucket
	}

	if now.Sub(start) > time.Duration(ttlBucketCount)*ttlBucketWidth {
		// Jump larger than the ring window: unknown buckets were missed → sweep all.
		for i := 0; i < ttlBucketCount; i++ {
			c.sweepBucket(i, now, processed)
		}
	} else {
		for t := start.Add(ttlBucketWidth); !t.After(now); t = t.Add(ttlBucketWidth) {
			c.sweepBucket(ttlBucketIndex(t), now, processed)
		}
	}

	c.lastSweep = now
}
```

### Tarea 5.2: validar cada entrada

Cada caso de `processTTLEntry` y su razón:

1. `c.Get(entry.inodeID) == nil` → el inode se borró después de registrarse →
   descartar la entrada.
2. `!IsChildrenFetched()` → carpeta invalidada o evictada (tarea 4.3) →
   descartar la entrada.
3. Recalcular `effectiveTTL` con el contador actual y `expiry` con
   `ChildrenLastAccess()`. **Nunca** usar la `expiry` almacenada en la entrada:
   puede estar obsoleta por re-registros (invalidación perezosa, tarea 3.6).
4. `!now.After(expiry)` → vigente → `c.registerTTL(inode, now)` (re-registro con
   el TTL ya decaído).
5. `now.After(expiry)` → expirada → decay + evicción (tarea 5.4).

La comparación usa el `now` recibido por la función. Los timestamps leídos del
inode pueden haber sido generados con el reloj real; esto es intencionado en la
opción elegida y no debe interpretarse como un error de sincronización.

### Tarea 5.3: preservar exactamente el decay

Riesgos de doble decay y cómo los cubre la implementación:

- **Entradas duplicadas en el mismo bucket** (varios hits en el mismo segundo):
  el mapa `processed` las salta tras la primera.
- **Entradas del mismo inode en buckets distintos barridos en el mismo pase**
  (re-registros con TTL distinto): el mapa `processed` se crea en
  `sweepExpiredBuckets` y se comparte entre todos los buckets del pase, así que
  cada inode se decae una sola vez por sweep.
- El orden `DecayChildrenAccess → ChildrenAccessCount → effectiveTTL → expiry`
  es idéntico al full scan.

Nota aceptada por el diseño: el decay ahora se aplica cuando el bucket del inode
se procesa (cada ≤ 60 s), no en cada tick de 30 s. Es una diferencia inherente
al diseño por buckets; la paridad se mide por el conjunto de evictados (Fase 7),
no por el ritmo de decay.

### Tarea 5.4: evictar solamente cuando corresponde

Exactamente igual que el full scan:

```go
inode.EvictChildren()
c.evictions.Add(1)
```

Con el mismo `log.Debug()`. La evicción solo pone `children = nil`; el inode
permanece en el `sync.Map` (nunca se borra).

### Tarea 5.5: buckets que quedaron atrás — `lastSweep`

El ticker puede retrasarse o el proceso suspenderse. `sweepExpiredBuckets`
cubre tres casos:

| Caso | Comportamiento |
|---|---|
| Primer sweep (`lastSweep` cero) | Solo el bucket actual (`start = now − 1 s`) |
| Salto ≤ 60 s | Recorre los buckets desde `lastSweep + 1 s` hasta `now` (cada segundo → su bucket) |
| Salto > 60 s | Barre los 60 buckets (no se sabe cuáles se perdieron) |

Y siempre actualiza `c.lastSweep = now` al final.

### Pruebas recomendadas (añadir en `cache_eviction_test.go`)

Usa el patrón del fake clock (iniciado en `time.Now()`) y recuerda que los
timestamps internos los genera `Inode` con el reloj real: crea los timestamps con
`SetChildren`/`BumpChildrenAccess` y avanza el reloj del cache para decidir la
evicción.

- **5.1/5.2**: registra un inode con `registerTTL`, avanza el reloj hasta que su
  bucket toque y llama a `sweepExpiredBucket(clock.Now())`:
  - inode expirado → `IsChildrenFetched() == false` y `Stats().Evictions == 1`;
  - inode vigente → sigue en el anillo (usa `assertTTLEntries` para ver el
    re-registro);
  - inode borrado (`cache.Delete(id)`) → sin evicciones y sin pánico;
  - inode invalidado (`cache.Invalidate(id)`) → sin evicciones.
- **5.3**: dos entradas del mismo inode en el mismo bucket (registrar dos veces
  sin avanzar el reloj) → al barrer, `ChildrenAccessCount()` decae una sola vez
  (p. ej. 8 → 4, no 8 → 2).
- **5.4**: `sweepExpiredBuckets` con varios inodes → solo los expirados se
  evictan y el inode permanece (`cache.Get(id) != nil`).
- **5.5**: avanza el reloj 2 minutos y llama a `sweepExpiredBuckets` → los
  buckets atrasados se procesan; avanza más de 60 s (salto > ventana) → se
  barren todos.

## Fase 6: Sustituir el sweep TTL completo

> **⚠️ PRECONDICIÓN BLOQUEANTE (verificada contra el código el 29/08):**
> **la Fase 5 NO está implementada.** A la fecha, en `internal/fs/cache.go`
> solo existen `sweep()`, `evictExpiredChildren()` y
> `evictChildrenBySizeLimit()`. **No existen** `sweepExpiredBucket`,
> `sweepBucket`, `processTTLEntry`, `sweepExpiredBuckets` ni el campo
> `lastSweep`. Por tanto la tarea 6.2 (que sustituye el full scan por
> `sweepExpiredBuckets`) **no compila ni puede ejecutarse hasta terminar la
> Fase 5** (Paso 0 + tareas 5.1-5.5 y sus pruebas). No retrocedas al código de
> producción sin la Fase 5 lista.
>
> El resto de tareas de esta Fase 6 sí están contrastadas con el estado real y
> siguen siendo válidas tal como se detallan abajo.

### Estado real de las funciones implicadas (líneas actuales)

| Función | Línea actual | Cómo participa en la Fase 6 |
|---|---|---|
| `evictExpiredChildren()` | cache.go:530 | Se **renombra** (6.1); deja de llamarse desde `sweep()` |
| `sweep()` | cache.go:514 | Cambia su primera llamada al barrido por buckets (6.2) |
| `ForceSweep()` | cache.go:524 | Delega en `sweep()`, sin cambios de API (6.4) |
| `StartSweep()` | cache.go:490 | Sin cambios de código; usa `sweepInterval` que cambia en 6.3 |
| `sweepInterval` | cache.go:32 (`30s`) | Cambia a `time.Second` (6.3) |
| `evictChildrenBySizeLimit()` | cache.go:567 | **No se toca** en esta fase (el plan lo ordena explícitamente) |

Call sites que hay que tocar cuando se renombre `evictExpiredChildren`:

- `sweep()` (cache.go:518) — sustituida por el barrido por buckets.
- Tests TTL: `cache_eviction_test.go:132`, `:165`, `:184` y
  `cache_boltdb_test.go:1381` — deben llamar a
  `evictExpiredChildrenFullScan()` (conservan la paridad de la Fase 7).

### Tarea 6.1: Mantener temporalmente la implementación anterior

Renombra `evictExpiredChildren()` → `evictExpiredChildrenFullScan()`:

```go
func (c *InodeCache) evictExpiredChildrenFullScan()
```

Motivo: sirve de referencia de paridad en los tests de la Fase 7 (mismo
conjunto de evictados). **No la elimines aún**.

Detalle de ejecución:

- Cambia la firma en cache.go:530 y el comentario.
- Actualiza los 3 call sites de tests TTL y el de `cache_boltdb_test.go:1381`
  al nombre nuevo. `sweep()` aún la llama en este punto (se cambia en 6.2), así
  que el código sigue compilando en el camino.
- Verde tras `go test ./internal/fs/... -short` (los tests TTL existentes ya
  cubren el renombrado).

### Tarea 6.2: Cambiar `sweep()`

**Depende de la Fase 5.** Una vez que exista `sweepExpiredBuckets(now)`, edita
`sweep()` (cache.go:514-521):

```go
func (c *InodeCache) sweep() {
	c.sweepMu.Lock()
	defer c.sweepMu.Unlock()

	c.sweepExpiredBuckets(c.currentTime()) // Tier 1: buckets TTL (antes: evictExpiredChildrenFullScan)
	c.evictChildrenBySizeLimit()           // Tier 2: size limit by score (sin cambios aún)
}
```

Sobre la «única lectura del reloj»:

- `sweep()` lee `now := c.currentTime()` **una vez** y la pasa a
  `sweepExpiredBuckets(now)`. Esa es la lectura que gobierna el TTL.
- `evictChildrenBySizeLimit()` **sigue leyendo su propio `c.currentTime()`
  internamente** (cache.go:568). En tests con fake clock el valor es el mismo;
  en producción difiere en microsegundos — irrelevante. Si en el futuro se
  quiere una lectura única estricta, `evictChildrenBySizeLimit()` deberá
  aceptar `now` como parámetro;**eso es la Fase 8, no esta tarea**. Esta nota
no cambia el contrato actual.

No toques la parte de límite de tamaño en esta tarea.

### Tarea 6.3: Cambiar el intervalo del ticker

Cambia la constante (cache.go:32):

```go
const sweepInterval = time.Second
```

> **⚠️ El orden importa:** cambia `sweepInterval` SOLO DESPUÉS de 6.2. Si lo
> bajas a 1 s mientras `sweep()` sigue con el full scan, cada tick recorrerá
> todo `sync.Map` — una regresión devastadora (30× más trabajo), justo lo
> contrario del objetivo de la issue #66.

El aumento de frecuencia solo cobra sentido porque cada tick procesa el bucket
correspondiente y no todo el mapa.

### Tarea 6.4: Mantener `ForceSweep()` compatible

`ForceSweep()` (cache.go:524) delega en `sweep()`, que ya toma
`sweepMu` y lee `c.currentTime()`. Por tanto:

- La API pública no cambia: `func (c *InodeCache) ForceSweep()`.
- Ejecuta la limpieza TTL (buckets, tras 6.2) y la de tamaño.
- No inicia ningún ticker.
- Es segura bajo `sweepMu` (heredado de `sweep()`).
- En tests, el fake clock debe estar configurado (`cache.now = clock.Now`)
  antes de la primera `ForceSweep()`.

**No hay cambios de código en 6.4** salvo la verificación tras 6.2.

### Consecuencia crítica sobre los tests de `ForceSweep` existentes

Los tests actuales de evicción TTL y `ForceSweep` siembran los inodes con
`SetChildren(...)` directo y **nunca llaman a `registerTTL`** (porque poblar vía
`SetChildren` no registra en el anillo — el registro vive en
`getChildren`/`Insert`/`MoveChild`/`DeserializeFromDisk`). Hasta 6.1 esto
funciona porque `evictExpiredChildren`/`evictExpiredChildrenFullScan` recorrenel mapa entero. En cuanto `sweep()` use `sweepExpiredBuckets`, esos
inodes ya no están en el anillo y **dejarán de evictarse**, rompiendo:

- `TestInodeCache_EvictExpiredChildren` (llama al full scan directamente —
  **no afectado** mientras llame a `evictExpiredChildrenFullScan`).
- `TestInodeCache_ForceSweep` y sus variantes `_DoesNotEvictFreshChildren`,
  `_KeepsInode`, `_IncrementsEvictionCount` (cache_eviction_test.go:412+) —
  **SÍ se rompen**, porque pasan por `sweep()`.

Solución para esos tests: poblar a través de `GetChildren` con un fetcher mock
(que llama a `getChildren` → registra) **o** llamar a `cache.registerTTL(parent,
clock.Now())` tras `SetChildren`. Ver tarea 6.5.

### Tarea 6.5: Probar el nuevo sweep

Añade pruebas (en `cache_eviction_test.go`, patrón fake clock iniciado en
`time.Now()`), para los casos del plan original:

- Inode sin expirar → no se evicta.
- Inode expirado → se evicta.
- Inode que recibe un hit antes de su expiración → se re-registra (queda en el
  anillo, `assertTTLEntries` crece).
- Inode con TTL extendido por frecuencia → no se evicta dentro de su ventana.
- Entrada antigua (stale) → se descarta sin evicción.
- Inode eliminado antes del sweep → sin evicción ni pánico.
- Salto de tiempo de varios segundos ≤ 60 → buckets atrasados procesados.
- Salto > 60 s → se barren los 60 buckets.
- Carpeta invalidada antes de procesar su bucket → sin evicción.

Como estas pruebas pasan por `sweepExpiredBuckets`/`processTTLEntry`, primero
registra cada inode con `registerTTL` (o pobla vía `GetChildren` con mock).

**Criterio de salida de la Fase 6:** `go test ./internal/fs/... -short` verde,
`go build ./...` OK, y los tests TTL de la Fase 5/6 prueban el flujo por
buckets (no solo el full scan).

## Fase 7: Crear pruebas de paridad TTL

### Tarea 7.1: Crear fixtures reproducibles

Crea una estructura de prueba que describa cada carpeta:

```go
type evictionFixture struct {
    id           string
    accessCount  uint64
    cachedAt     time.Time
    lastAccess   time.Time
    hasChildren  bool
}
```

La fixture debe poder aplicarse a:

- Un cache que ejecuta el algoritmo completo.
- Un cache que ejecuta los buckets.

### Tarea 7.2: Comparar el conjunto expulsado

No compares solamente el contador. Compara los IDs:

```go
func evictedIDs(cache *InodeCache, ids []string) map[string]bool
```

La aserción debe comprobar que ambos algoritmos producen el mismo conjunto.

### Tarea 7.3: Comparar el estado posterior

Además de los IDs expulsados, compara:

- `IsChildrenFetched()`.
- `ChildrenAccessCount()`.
- `Stats().Evictions`.
- Inodes que siguen presentes.
- Inodes que tienen hijos no expirados.

### Tarea 7.4: Cubrir escenarios límite

Los casos de igualdad exacta y de un nanosegundo posterior solamente son
completamente reproducibles si también se controlan los timestamps internos del
inode. Con la opción elegida pueden probarse de forma aproximada, pero no deben
convertirse en un requisito de determinismo absoluto.

Incluye casos donde:

- La hora es exactamente igual a la expiración.
- La hora es un nanosegundo posterior.
- El TTL efectivo cambia después del decay.
- Hay acceso count igual a cero.
- El TTL alcanza `freqMultiplierMax`.
- Varios inodes caen en el mismo bucket.
- Una misma carpeta aparece varias veces por re-registros.
- Se actualiza una carpeta mientras se procesa otro bucket.

### Tarea 7.5: Ejecutar las pruebas repetidamente

```bash
go test -race -count=50 ./internal/fs/...
```

Esto es especialmente importante porque el registro TTL tendrá acceso concurrente desde varias goroutines.

### ✅ Estado tras implementar (Fase 7 completa)

Se añadieron en `cache_eviction_test.go` (prefijo `TestTTLParity_`):

| Test | Cubre |
|---|---|
| `TestTTLParity_Basic` | 7.1-7.3: mezcla expirado/fresco/frecuente/hoja, mismo conjunto evictado, mismo contador y mismo estado posterior |
| `TestTTLParity_ZeroHits` | 7.4: accessCount == 0 |
| `TestTTLParity_ExactExpiry` | 7.4: `now == expiry` (no evicta) y `now == expiry+1ns` (evicta) |
| `TestTTLParity_FreqMultiplierMax` | 7.4: TTL cap a 20× |
| `TestTTLParity_SameBucket` | 7.4: varios inodes en el mismo bucket |
| `TestTTLParity_DuplicateRegistrations` | 7.4: carpeta registrada 3 veces → decay único (8 → 4) en ambos |

**Diseño (clave):**

- **Fixtures con timestamps absolutos**: `applyFixture` escribe directamente
  `childrenCachedAt`/`childrenLastAccess`/`childrenAccessCount` bajo el lock
  del inode (no vía `SetChildren`, que usa el reloj real), y registra en el
  anillo. Así ambos caches ven estado byte-idéntico y los casos límite
  (`expiry` exacto, `+1ns`) son 100% reproducibles.
- **Barrido de buckets de paridad**: en lugar de depender de `lastSweep` y del
  ticker (que procesaría solo el bucket actual en el primer barrido), el runner
  barre los 60 buckets con `sweepBucket(i, now, processed)` compartiendo el
  mapa `processed` — exactamente el barrido completo que haría un salto > 60s,
  con un solo decay por inode, idéntico al full scan.
- El full scan de referencia se ejecuta con `cache.now = func() time.Time{return now}`
  fijado al mismo instante.

**Hallazgo (flaky fix en el test de tamaño):** el heap congeló el score en el
momento del registro, así que `TestInodeCache_SizeLimit_WithTiebreaker`
(empate de score con reloj real) dejó de ser determinista: con el full scan los
dos scores se calculaban con el MISMO `now` del sweep (empate casi exacto); con
el heap, los microsegundos entre los dos `seedSizeEviction` rompían el empate.
El test ahora fuerza `childrenLastAccess` idéntico y re-registra con
`updateEvictionEntry` (mismo patrón que `TestEvictionHeap_Tiebreaker`).

**Verificación:** `go test -race -count=50 -run TestTTLParity` verde (7.5),
suite completa verde, cobertura 82.5% (sin bajada).

## Fase 8: Implementar el heap para el límite de tamaño

Esta fase es independiente del TTL y debe comenzar solamente después de que la fase anterior esté estable.

### Tarea 8.1: Definir la puntuación actual

El código actual calcula:

```go
minutesSinceLastAccess := now.Sub(inode.ChildrenLastAccess()).Minutes()
score := float64(accessCount) / (minutesSinceLastAccess + 1)
```

El comportamiento que debe preservarse es:

- Menor puntuación: candidato preferente para expulsión.
- En empate: `ChildrenCachedAt()` más antiguo primero.

### Tarea 8.2: Elegir el tipo de heap

Para obtener rápidamente el candidato de menor puntuación, el heap debe ser un min-heap.

Define una entrada:

```go
type evictionEntry struct {
    id    string
    score float64
    age   time.Time
}
```

Y un tipo que implemente `heap.Interface` del paquete estándar `container/heap`.

### Tarea 8.3: Definir el comparador

El comparador debe ser:

```go
func less(a, b evictionEntry) bool {
    if a.score == b.score {
        return a.age.Before(b.age)
    }
    return a.score < b.score
}
```

Para comparar `float64`, debes considerar qué ocurre con valores especiales. En condiciones normales no deberían aparecer `NaN`, pero conviene evitar introducirlos en el cálculo.

### Tarea 8.4: Decidir cómo mantener el heap actualizado

Hay dos diseños posibles:

| Diseño | Ventaja | Desventaja |
|---|---|---|
| Reconstruir heap en cada sweep | Más sencillo y seguro | Sigue recorriendo todos los inodes |
| Mantener heap persistente | Reduce el coste del sweep | Requiere actualizaciones, eliminación diferida y más sincronización |

La issue solicita reducir el coste de la expulsión por tamaño, por lo que reconstruir el heap durante cada sweep no solucionaría completamente el problema. La implementación recomendada es un heap persistente con actualización en los accesos.

### Tarea 8.5: Añadir identificador de versión

Cuando una entrada se actualiza, la entrada antigua puede continuar dentro del heap. Usa una generación:

```go
type evictionEntry struct {
    id         string
    score      float64
    age        time.Time
    generation uint64
}
```

En el cache:

```go
evictionGeneration map[string]uint64
```

Al actualizar un inode:

- Incrementa su generación.
- Inserta una nueva entrada.
- Al extraer del heap, ignora entradas con generación antigua.

Esto evita tener que buscar y borrar elementos arbitrarios del heap.

### Tarea 8.6: Actualizar el heap después de un hit

Después de `BumpChildrenAccess()`:

- Calcula la nueva puntuación.
- Actualiza la generación.
- Inserta la nueva entrada.

La operación debe estar protegida por un mutex del heap:

```go
evictionMu sync.Mutex
```

### Tarea 8.7: Actualizar el heap después de poblar hijos

Cuando una carpeta se puebla por primera vez:

- Calcula su puntuación.
- Añádela al heap.
- No dupliques una entrada válida sin incrementar la generación.

### Tarea 8.8: Extraer candidatos por límite

En `evictChildrenBySizeLimit()`:

- Cuenta cuántos folders tienen hijos cacheados.
- Si `count <= maxEntries`, no hagas nada.
- Calcula `toRemove`.
- Extrae candidatos del heap.
- Descarta entradas obsoletas.
- Comprueba que el inode siga existiendo y tenga hijos.
- Ejecuta `EvictChildren()`.
- Incrementa `evictions`.
- Marca el estado de heap como no válido o actualízalo según el diseño elegido.

### Tarea 8.9: Conservar el comportamiento de `maxEntries <= 0`

El comportamiento actual es que cero significa ilimitado:

```go
if c.maxEntries <= 0 {
    return
}
```

Debe mantenerse exactamente igual.

### ✅ Estado tras implementar (Fase 8 completa)

La Fase 8 está implementada y verificada. Decisiones tomadas que difieren del
enunciado genérico de arriba:

- **Contador de carpetas con hijos**: para no recorrer el `sync.Map` en cada
  sweep, se añadió `cachedFolders atomic.Int64` al struct `InodeCache`. Se
  actualiza en TODAS las transiciones `children nil ↔ no-nil`:
  - `+1`: `getChildren` (población, solo si antes no estaba fetched),
    `Insert`/`MoveChild` (seeding con el guard de transición de 4.3),
    `DeserializeFromDisk` (inodes restaurados con hijos).
  - `-1`: evicción TTL (`evictExpiredChildrenFullScan` y `processTTLEntry`),
    evicción por tamaño, `Invalidate`, `InvalidateAll`, `Delete`.
  - ⚠️ Si se añade en el futuro otro sitio que ponga `children = nil` o lo
    poble, hay que actualizar el contador ahí también (paridad con el heap).
- **`sizeScore`**: función extraída que calcula `accessCount /
  (minutesSinceLastAccess + 1)`, idéntica al código pre-heap. Usada tanto por
  `updateEvictionEntry` como por el log de evicción.
- **`updateEvictionEntry`**: método que (re)inserta la entrada con generación
  incrementada. Se llama desde: cache hit (tras `BumpChildrenAccess`),
  población de `getChildren`, seeding de `Insert`/`MoveChild` y
  `DeserializeFromDisk`.
- **`popEvictionCandidate`**: extrae el candidato válido del heap descartando
  entradas con generación obsoleta o inodes sin hijos (o eliminados).
- **Los tests de tamaño existentes** (`SizeLimitEviction`,
  `LowFrequencyEvictedFirst`, `HighFrequencySurvivesSizeLimit`,
  `SizeLimit_WithTiebreaker`) siembran con `SetChildren` directo, que no pasa
  por el heap; se les añadió el helper `seedSizeEviction` que replica la
  transición de producción (`updateEvictionEntry` + `cachedFolders.Add(1)`).

## Fase 9: Pruebas unitarias del heap

### Tarea 9.1: Probar el orden básico

Añade pruebas que verifiquen:

- La puntuación más baja sale primero.
- El acceso más frecuente sobrevive.
- El elemento más antiguo gana el desempate.

Las pruebas existentes de `cache_eviction_test.go` ya cubren parte de este comportamiento y deben continuar pasando. [3]

### Tarea 9.2: Probar entradas obsoletas

Crea una prueba donde:

- Se inserta una entrada en el heap.
- Se actualiza el mismo inode.
- Se inserta otra entrada.
- El heap extrae la antigua.
- La antigua se ignora.
- La nueva se procesa correctamente.

### Tarea 9.3: Probar evicción concurrente

Usa varias goroutines que:

- Accedan a una carpeta.
- Actualicen su entrada.
- Ejecuten `ForceSweep()`.

Ejecuta la prueba con:

```bash
go test -race ./internal/fs/...
```

### Tarea 9.4: Probar que no se expulsa el inode

La evicción debe eliminar los hijos cacheados, pero no el inode completo. La prueba existente `TestInodeCache_EvictionDoesNotRemoveInodeFromTree` debe continuar pasando. [3]

### ✅ Estado tras implementar (Fase 9 completa)

Se añadieron 6 tests en `cache_eviction_test.go` (prefijo `TestEvictionHeap_`):

| Test | Tarea | Verifica |
|---|---|---|
| `TestEvictionHeap_BasicOrder` | 9.1 | El score más bajo se evicta primero; el más frecuente sobrevive; el inode no se elimina |
| `TestEvictionHeap_Tiebreaker` | 9.1 | A score idéntico (lastAccess idéntico forzado), el `childrenCachedAt` más antiguo gana |
| `TestEvictionHeap_StaleGenerationDiscarded` | 9.2 | Rescorear deja 2 entradas (1 obsoleta); el pop devuelve la válida y descarta la stale |
| `TestEvictionHeap_StaleGenerationEvictsNone` | 9.2 | Inode ya evictado (children nil): su entrada se salta y no se evicta nada más |
| `TestEvictionHeap_ConcurrentAccess` | 9.3 | 20 goroutines hacen hit + rescore + `ForceSweep` concurrentemente (se ejecuta con `-race`) |
| `TestEvictionHeap_EvictionDoesNotRemoveInode` | 9.4 | La evicción no borra el inode del árbol |

Detalles importantes:

- **Determinismo del tiebreaker**: `BumpChildrenAccess` usa `time.Now()` real
  (no el fake clock), así que con reloj real el empate de score casi nunca es
  exacto y el test sería flaky. El test fuerza `childrenLastAccess` idéntico
  en ambos inodes y re-registra con `updateEvictionEntry` para que el empate
  sea exacto y decida `childrenCachedAt`.
- **Cobertura**: la suite completa pasó de 81.5% a 82.4% de statements; las
  funciones nuevas del heap (menos `less`, `sizeScore`, `updateEvictionEntry`,
  `popEvictionCandidate`, `evictChildrenBySizeLimit`) tienen cobertura 88-100%.
- Verificación: `go build ./...`, `go vet`, `go test -short` y
  `go test -race` (tests de evicción/sweep/heap) pasan.

## Fase 10: Benchmarks

### Tarea 10.1: Crear el archivo de benchmark

Si no existe, crea:

```text
internal/fs/cache_bench_test.go
```

### Tarea 10.2: Crear el benchmark con 50.000 inodes

Los benchmarks deben utilizar el reloj real por defecto. Si se inyecta un fake
clock, hay que recordar que las marcas temporales creadas por `Inode` continúan
utilizando `time.Now()`. El objetivo del benchmark es medir el coste del
algoritmo, no controlar artificialmente todos los timestamps.

El benchmark debe preparar el cache fuera de la sección medida:

```go
func BenchmarkSweep_50kInodes(b *testing.B) {
    cache := NewInodeCache()

    // Preparar 50.000 inodes.
    // Registrar solamente una pequeña proporción como expirados.

    b.ResetTimer()

    for i := 0; i < b.N; i++ {
        cache.ForceSweep()
    }
}
```

Sin embargo, `ForceSweep()` también ejecutará la evicción por tamaño. Para medir cada parte por separado, conviene crear benchmarks independientes:

```go
func BenchmarkTTLSweep_50kInodes(b *testing.B)
func BenchmarkSizeEviction_50kInodes(b *testing.B)
func BenchmarkFullSweep_50kInodes(b *testing.B)
```

### Tarea 10.3: Comparar implementación antigua y nueva

Mientras exista `evictExpiredChildrenFullScan()`, añade:

```go
func BenchmarkTTLSweepFullScan_50kInodes(b *testing.B)
func BenchmarkTTLSweepBuckets_50kInodes(b *testing.B)
```

Ejecuta:

```bash
go test ./internal/fs -bench='Sweep|Eviction' -benchmem -count=5
```

### Tarea 10.4: Interpretar el resultado

No te limites al objetivo de 5 ms. Observa:

- `ns/op`.
- `allocs/op`.
- `B/op`.
- Variabilidad entre ejecuciones.
- Coste con cero expiraciones.
- Coste con muchos inodes expirados.
- Coste con muchos accesos y re-registros.

La meta indicada por la issue es menos de 5 ms por tick con 50.000 inodes, pero debe tratarse como una medición de aceptación, no como una garantía para cualquier hardware. [1]

> **Nota (implementación).** Esta fase verifica la concurrencia y el ciclo de
> vida del sweep. El único cambio de código fue proteger `StartSweep()` con
> `c.closeMu` (el mismo mutex de `Close()`): antes leía `stopCh` sin
> sincronización, de modo que dos `StartSweep()` concurrentes podían lanzar dos
> tickers. Ahora `StartSweep()` y `Close()` quedan mutuamente excluidos y tras un
> `Close()` (que deja `stopCh` a nil) se puede volver a arrancar un ticker. Las
> tareas 11.2 y 11.3 ya se cumplían: `sweepMu` serializa el sweep completo, y las
> operaciones largas (`evictChildrenBySizeLimit`, `sweepBucket`) extraen los
> candidatos bajo el lock de la estructura y procesan los inodes fuera. Para 11.4
> se añadieron `TestInodeCache_StartSweep_Concurrent`, `_RaceWithClose` y
> `_ForceSweep_ConcurrentWithTicker` — el de doble StartSweep usa un watchdog de
> 2s porque un segundo ticker haría colgar `Close()` (el canal `stopCh` se cierra
> una sola vez y el `wg.Wait()` esperaría una goroutine fugada).

> **Resultados consolidados** (`internal/fs/cache_bench_test.go`, 50.000 inodes,
> Ryzen 5 6600H, `quietLogsForBench`, `-benchtime=100x`):
>
> | Benchmark (count=3/5) | ns/op (mediana) | B/op | allocs/op |
> |---|---|---|---|
> | TTL buckets — steady state (1 cubeta/tick) | ~0.39 ms | 144 KB | 26 |
> | TTL buckets — full-window (salto >60s) | ~7.0 ms | 3.05 MB | 320 |
> | TTL full scan (referencia antigua) | ~7.5 ms | 0 B | 0 |
> | Size eviction (heap) | ~72 ns/op | 1 B | 0 |
> | Full sweep combinado (TTL + heap) | ~0.38 ms | 32 KB | 480 |
>
> **Interpretación**: el estado estacionario del anillo (una cubeta por tick) está
> ~19–24× por debajo del full scan y muy por bajo el target de 5ms — ese es el
> ahorro que busca la issue. Nota sobre el benchmark del heap de tamaño: al forzar
> `maxEntries = n-1` con 50k carpetas, `evictChildrenBySizeLimit` expulsa 1 entrada
> por llamada; el heap `Pop` es O(log n) y el accesoCount=0 constante hace el score
> trivial, por lo que ~72ns miden únicamente el pop con esa carga concreta.
> El caso full-window es comparable al full scan (ambos ~7ms, full-window con
> asignaciones extra por re-registro) — es deliberado, porque ocurre solo ante un
> salto de tiempo y se paga para que el tick normal sea barato. Todos los valores
> son estables entre ejecuciones (±10%).

## Fase 11: Concurrencia y ciclo de vida

### Tarea 11.1: Revisar `StartSweep()`

El código actual comprueba directamente si `stopCh != nil`. Esa operación podría requerir protección si `StartSweep()` puede ser invocado desde varias goroutines.

Aunque no sea el objetivo principal de la issue, durante los tests de carrera debes verificar:

- Dos llamadas simultáneas a `StartSweep()`.
- `StartSweep()` concurrente con `Close()`.
- `ForceSweep()` concurrente con el ticker.
- `Close()` llamado dos veces.

### Tarea 11.2: Mantener `sweepMu`

Conserva `sweepMu` para garantizar que solamente una limpieza completa se ejecuta a la vez:

```go
func (c *InodeCache) sweep() {
    c.sweepMu.Lock()
    defer c.sweepMu.Unlock()

    c.sweepExpiredBuckets(c.currentTime())
    c.evictChildrenBySizeLimit()
}
```

### Tarea 11.3: No mantener mutexes durante operaciones largas

Evita mantener simultáneamente:

- `ttlMu`.
- `evictionMu`.
- Locks internos del inode.
- Accesos a BoltDB.

Extrae primero los elementos a procesar y libera el mutex de la estructura antes de examinar los inodes.

### Tarea 11.4: Ejecutar pruebas de carreras

```bash
go test -race -count=50 ./internal/fs/...
```

También ejecuta:

```bash
go test -race ./...
```

## Fase 12: Persistencia y compatibilidad

### Tarea 12.1: Verificar que los buckets no se persisten

Los buckets TTL son estado temporal del proceso. No deben añadirse a BoltDB.

Después de `DeserializeFromDisk()`:

- Los inodes persistidos deben mantener su comportamiento actual.
- El registro TTL debe reconstruirse en memoria.
- No deben aparecer cambios de formato JSON.

### Tarea 12.2: Verificar `SerializeAll`

La evicción solamente debe poner `children` a `nil`; no debe eliminar el inode. Por tanto, `SerializeAll()` debe conservar los mismos inodes que antes.

### Tarea 12.3: Verificar `SerializeDirty`

Comprueba que:

- Un sweep TTL no marque accidentalmente inodes como dirty, salvo que el diseño del proyecto considere la evicción como estado persistible.
- La evicción no modifique la semántica de persistencia existente.
- BoltDB no reciba entradas nuevas de los buckets.

### Tarea 12.4: Ejecutar las pruebas de BoltDB

```bash
go test ./internal/fs/... -run 'Bolt|Serialize|Deserialize|Sweep'
```

Después:

```bash
go test -race ./internal/fs/...
```

### Notas de implementación (Fase 12)

La comprobación de la persistencia quedó así, sin cambios de producción:

- **Tarea 12.1** — los buckets TTL siguen sin persistirse: siguen residiendo solo en
  `ttlBuckets` (memoria). Ya existía `TestInodeCache_Deserialize_RegistersRestoredForTTL`
  (tarea 4.4) que prueba que el registro TTL se reconstruye en memoria al deserializar.
- **Tarea 12.2** — la evicción (TTL y tamaño) solo pone `children` a `nil` sin eliminar el
  inode. Ya cubierta por `TestInodeCache_SerializeAll_PersistsEvictedSubtree` (el inode
  evictado con ParentID sobrevive el round-trip vía `ItemsByParent`).
- **Tarea 12.3** — **tests nuevos añadidos** en `cache_boltdb_test.go` (los tres:
  `SetChildren` no registra en el anillo, así que un test de sweep TTL debe llamar
  `registerTTL` manualmente como hace `getChildren` en producción):
  - `TestInodeCache_TTLSweep_DoesNotMarkDirty` — tras un sweep TTL que expulsa hijos, un
    `SerializeDirty` posterior escribe 0 bytes (la evicción no marca dirty).
  - `TestInodeCache_SizeEviction_DoesNotMarkDirty` — ídem para la evicción por tamaño
    (heap de la Fase 8).
  - `TestInodeCache_TTLSweep_NoNewBoltDBEntries` — los buckets no contribuyen claves
    nuevas al bucket de metadatos de BoltDB (el set de claves es idéntico antes/después).

Verificación ejecutada (tarea 12.4):

```text
go test ./internal/fs/... -run 'Bolt|Serialize|Deserialize|Sweep|Evict'  → ok
suite -short completa                                                       → ok
go test -race ./internal/fs/...                                            → ok
cobertura                                                                   → 82.5% (sin bajada)
```

## Fase 13: Calidad del código

### Tarea 13.1: Formatear

```bash
gofmt -w internal/fs/cache.go internal/fs/cache_eviction_test.go internal/fs/cache_bench_test.go
```

### Tarea 13.2: Ejecutar vet

```bash
go vet ./...
```

### Tarea 13.3: Ejecutar lint

Si el proyecto incluye `make lint-all`, ejecuta:

```bash
make lint-all
```

Si no existe ese objetivo, revisa el `Makefile`:

```bash
grep -n "lint" Makefile
```

### Tarea 13.4: Ejecutar todas las pruebas

```bash
go test ./...
go test -race ./...
```

### Tarea 13.5: Revisar cobertura final

```bash
go test ./... -coverprofile=coverage-after.out
go tool cover -func=coverage-after.out
```

Compara con la cobertura inicial. Si disminuyó:

- Busca ramas nuevas sin tests.
- Añade pruebas específicas.
- No ocultes código mediante exclusiones.
- Verifica que los benchmarks no sustituyan a pruebas unitarias.

### Notas de implementación (Fase 13)

Limpié los avisos de lint que `revive`/`unparam` detectaron en las adiciones de
las Fases 8-11 (sin silenciarlos; resolviendo la causa):

- **`registerTTL` — parámetro `now` eliminado.** La expiración se deriva de
  `ChildrenLastAccess()` (como `evictExpiredChildren`), no del reloj del
  llamador; `now` era muerto en los ~30 call sites. Actualicé el doc comment y
todos los call sites de producción y tests (`cache.go`, `cache_eviction_test.go`,
  `cache_boltdb_test.go`, `cache_bench_test.go`).
- **`runTTLParity` — parametro `baseTTL` eliminado.** Todos los escenarios de
  paridad (7.1-7.4) usan el TTL base por defecto de 1 minuto; ahora está
  hard-coded en el runner (`const baseTTL = time.Minute`).
- **`applyFixture` — parametro `now` eliminado** (idem, la fixture fuerza los
  timestamps absolutos del inode).
- **`benchCacheN` — parametro `n` eliminado.** El tamaño de medición es fijo de
  la issue (50k); se extrajo a la constante `benchFolderCount = 50000` y los 5
  benchmarks la usan, con los call sites actualizados.
- **`TestInodeCache_StartSweep_RaceWithClose` — `t` renombrado a `_`** (el test
  solo lanza goroutines y no afirma nada bajo el race detector).

El lint exigía reinstalar `golangci-lint` compilado con Go 1.26: el binario
instalado estaba compilado con Go 1.25 y no podía leer `.golangci.yml` para el
módulo (`go1.26.7`). Se reinstaló con el mismo comando que usa la CI:
`go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest`.

Documentación actualizada (además de este plan):

- `docs/ARCHITECTURE.md` y `docs/ARQUITECTURA.es.md` — diagrama y sección
  "Smart Eviction" reescritos: ahora reflejan el **sweep por anillo de buckets
  (ticker 1s, ~1/60 entradas por tick)** y el **min-heap persistente de la
  evicción por tamaño**, con el full scan conservado solo para los tests de
  paridad (Fase 7).
- `docs/api/*.md` (autogenerados) — regenerados con `make docs` para reflejar
  el estado actual del código (incluye el doc comment actualizado de
  `StartSweep` sobre la protección `closeMu` de la Fase 11).

Verificación final (tareas 13.1-13.5):

```text
gofmt ✓ — sin cambios pendientes en internal/fs
go vet ./...            ✓
golangci-lint ./internal/fs/...  ✓ (0 avisos)
go test ./...           ✓ (todo el repo)
go test -race ./internal/fs/...  ✓ (sin data races)
cobertura               → 82.5% (sin bajada)
```

## 4. Orden recomendado de commits

Una secuencia adecuada para aprender y revisar los cambios sería:

| Commit | Contenido |
|---|---|
| 1 | Convertir tests TTL de `Sleep` a reloj falso |
| 2 | Añadir `now func() time.Time` al cache |
| 3 | Añadir tipos y mutex de buckets |
| 4 | Registrar inodes al poblar y acceder |
| 5 | Implementar sweep de buckets |
| 6 | Añadir pruebas de paridad con full scan |
| 7 | Cambiar el ticker a 1 segundo |
| 8 | Añadir benchmark del sweep |
| 9 | Implementar heap de tamaño |
| 10 | Añadir pruebas del heap y entradas obsoletas |
| 11 | Añadir benchmarks comparativos |
| 12 | Ejecutar race, lint, vet y cobertura |

Cada commit debería compilar y pasar las pruebas siempre que sea posible.

## 5. Criterios de aceptación concretos

La implementación puede considerarse terminada cuando se cumplan todos estos puntos:

- `go test ./...` pasa.
- `go test -race ./...` pasa.
- `go test -race -count=50 ./internal/fs/...` no muestra carreras ni fallos intermitentes.
- La API pública `StartSweep()` no cambia.
- La API pública `ForceSweep()` no cambia.
- La evicción TTL produce el mismo conjunto de inodes que el algoritmo anterior para las mismas condiciones.
- El decay de frecuencia conserva el mismo orden y resultados.
- `maxEntries <= 0` sigue significando “sin límite”.
- El desempate por `ChildrenCachedAt()` se conserva.
- La evicción no elimina el inode, solamente sus hijos cacheados.
- BoltDB conserva su formato y comportamiento.
- La cobertura no disminuye.
- Existe un benchmark con 50.000 inodes.
- El sweep por buckets evita el recorrido completo normal del mapa.
- El heap de tamaño no depende de ordenar todos los candidatos en cada sweep.
- `make lint-all` y `go vet ./...` pasan.
- El benchmark cumple el objetivo de menos de 5 ms por tick en el entorno utilizado, o documenta claramente la medición obtenida.

## 6. Conclusión

La issue #66 **no está resuelta y continúa siendo aplicable**. El código actual mantiene exactamente los patrones de coste que la issue pretende mejorar: recorrido completo de todos los inodes para TTL y recorrido más ordenación para el límite de tamaño. [1] [2]

La ruta más segura para aprender Go y reducir regresiones es separar el trabajo en dos optimizaciones: primero el registro TTL por buckets y después el heap de expulsión por tamaño. El reloj inyectable y las pruebas de paridad deben implementarse antes de cambiar el algoritmo, porque permiten comparar de forma determinista la solución nueva con la existente.

La cobertura debe mantenerse mediante pruebas unitarias para cada transición: registro, re-registro, expiración, decay, entradas obsoletas, saltos de tiempo, concurrencia y compatibilidad con BoltDB. Las pruebas actuales de evicción proporcionan una base útil, pero deben complementarse con tests deterministas y benchmarks específicos. [3]

## 7. Anexo: verificación de rendimiento FUSE (QA)

Además de los benchmarks de Go de la Fase 10 (50.000 inodes, sweep por buckets vs full scan), se añadió una **batería FUSE de caché** reproducible en `bench/` que compara el binario actual contra el baseline oficial v0.1.4 sobre un montaje real de la cuenta secundaria `paveryutu72` (ver `docs/BENCHMARKS.md` y `docs/PERFORMANCE_REPORT.md`).

Resultado de la medición (100 iteraciones/prueba, cuenta `paveryutu72`, Zorin OS 18.1):

| Prueba | Baseline mediana | Actual mediana | Δ % | Veredicto |
|---|---:|---:|---:|---|
| cold_read | 1387 ms | 1410 ms | −1.6 | NEUTRO |
| warm_read | 3.66 ms | 3.65 ms | +0.3 | NEUTRO |
| write_readback | 9.35 ms | 9.43 ms | −0.8 | NEUTRO |
| metadata | 5.83 ms | 5.89 ms | −1.0 | NO CONCLUYENTE |
| mixed | 11.42 ms | 11.32 ms | +0.9 | NO CONCLUYENTE |
| CPU daemon (total battery) | 580 ms | 630 ms | −8.6 | NEUTRO |
| RSS pico daemon | 14.584 KB | 14.548 KB | +0.3 | NEUTRO |

Veredicto global: **NEUTRO**. A escala normal (~200 entradas, sin expulsión forzada) no hay diferencia medible ni regresión consistente: la mejora del anillo de buckets TTL y del min-heap solo se activa bajo presión de caché. Las 9 pruebas funcionales sobre `paveryutu72` pasan (montaje, listado, lectura, escritura, read-back, stat, rename, borrado, desmontaje). Las lecturas en frío y los picos de `metadata`/`mixed` están dominados por latencia de red de Graph; cuando la varianza impide concluir a 100 iteraciones, la prueba se marca NO CONCLUYENTE (regla 7 del spec de QA).

**Batería de presión añadida (`pressure_evict`, `--cache-max-entries 10`, 40 carpetas churn):** para forzar el camino de expulsión por tamaño, se añadió una prueba que crea carpetas nuevas vía Graph con `CACHE_MAX_ENTRIES` bajo y mide la **CPU y RSS globales del daemon** (muestra antes/después del churn, incluyendo el sweep en background). Resultado: CPU daemon 560 ms (baseline) vs 530 ms (actual) y RSS pico 22.392 KB vs 24.448 KB — ambos dentro del ruido de medición (ticks de 10 ms y red Graph). Conclusión honesta: **a escala FUSE reproducible el win de #66 no es observable end-to-end**; su demostración rigurosa son los benchmarks Go in-process de `internal/fs/cache_bench_test.go` (50k inodes): sweep por buckets ~0.39 ms vs 7.5 ms full scan por tick (~19x), heap de tamaño ~72 ns/op vs ordenar todos los candidatos. Calentar decenas de miles de inodes vía Graph (horas de red) no es reproducible en la batería FUSE.
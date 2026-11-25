# Plan de Trabajo: Singleflight y Protección de Stampede en onecloudriver

## Resumen Ejecutivo

La issue #153 (estampida de `Lookup`/`Readdir` sobre carpetas frías) está
**resuelta y mergeada**: el PR #156 integró el patrón `singleflight` en
`InodeCache.GetChildren` y cerró las issues #153 y #157.

Este documento, originalmente un plan de evaluación y aplicación de
`singleflight`, se reformula tras el merge para reflejar la realidad:

1. **Documentar** la estrategia de protección en la arquitectura
   (completada, issue #158).
2. **Observabilidad ligera** de la caché, coherente con una aplicación de
   escritorio doméstica (opcional y diferida).

No hay planes de aplicar `singleflight` a nuevos componentes: la evaluación
concluyó que el resto del pipeline no lo necesita.

## Estado Actual de la Feature

### ✅ Implementación (completada)

| Elemento | Detalle |
|---|---|
| PR | [#156](https://github.com/frosado/onecloudriver/pull/156), mergeado el 2026-11-24 |
| Issues cerradas | #153 (stampede) y #157 (bump de Go 1.26.11) |
| Mecanismo | `singleflight.Group` por carpeta dentro de `InodeCache.GetChildren` |
| Detalles del fix | Fetch compartido con `DoChan` (desacoplado del contexto de la request que lo inicia, para que una cancelación no envenene a los demás esperantes), re-verificación de caché dentro del grupo, seguimiento de carpetas in-flight para que los barridos TTL/tamaño no las evict-en, y persistencia (`markDirty`) por llamante aunque se una a un fetch ajeno |
| Tests | Regresión unitaria (dedup, cancelación, persistencia, eviction skipping) + integración FUSE (stampede de `readdirplus` sobre carpeta fría paginada: todo el burst comparte un único listado de 2 requests HTTP) |
| Documentación | Apartado "Concurrent Access: Stampede Protection (singleflight)" en `docs/ARCHITECTURE.md` + espejo en español en `docs/ARQUITECTURA.es.md` ✅ (#158) |

### Mecanismos de protección por componente

| Componente | Ubicación | Mecanismo | Riesgo de Stampede |
|-----------|----------|-----------|-------------------|
| **InodeCache.GetChildren** | `internal/fs/cache.go` | Singleflight por carpeta | ✅ Protegido (#156) |
| **ContentCache** | `internal/fs/content_cache.go` | Bloques por-id | ✅ Sin stampede |
| Graph API Calls | `internal/graph/` | Parcial | Bajo |
| Upload Pipeline | `internal/fs/upload_manager.go` | Secuencial | Nulo |
| Download Operations | `internal/graph/download.go` | Secuencial | Nulo |
| Delta Sync | `internal/fs/delta.go` | Secuencial | Nulo |
| Service Commands | `cmd/onecloudriver/service.go` | Secuencial | Nulo |

### Historial: de la evaluación a la implementación

- **Fase 0 — Evaluación** (completada, doc de 2026-09-20): el análisis inicial
  concluyó que el patrón solo era necesario para el escenario de carpeta y
  recomendó no aplicarlo en otros componentes. Detalle en
  [PLAN_SINGLEFLIGHT_EVALUATION.md](PLAN_SINGLEFLIGHT_EVALUATION.md) (EN) y
  [PLAN_SINGLEFLIGHT_EVALUATION_ES.md](PLAN_SINGLEFLIGHT_EVALUATION_ES.md) (ES).
- **Fase 1 — Implementación** (completada, 2026-11-24): el escenario de
  stampede de #153 se confirmó como problema real de usuario, por lo que la
  protección se implementó en `InodeCache.GetChildren` y se mergeó vía #156.

> Nota: la versión anterior de este plan mezclaba ambos momentos (la conclusión
> "no tocar nada" de la evaluación pre-fix con el estado post-fix), lo que
> daba lugar a un "Estado: Completado" contradictorio con fases sin ejecutar.
> Esta versión separa ambas cosas.

## Tareas del Plan

### Tarea 1 — Documentar la estrategia en la arquitectura (✅ completada)

**Qué**: añadir un apartado de protección de concurrencia en
`docs/ARCHITECTURE.md` y `docs/ARQUITECTURA.es.md` (sección
`Data Layer: InodeCache`), explicando:

- Por qué `singleflight` se aplica solo a la búsqueda de hijos por carpeta
  (único punto con fetches concurrentes a Graph).
- Los detalles del fix #156: fetch compartido con `DoChan`, re-check de caché
  dentro del grupo, carpetas in-flight protegidas de los barridos de evicción.
- Por qué el resto de componentes no lo necesita (accesos por-id en
  ContentCache, pipelines secuenciales de subida/descarga/delta).

**Estado**: ✅ completada por el PR de la issue #158 (este cambio): apartado
"Concurrent Access: Stampede Protection (singleflight)" en `ARCHITECTURE.md` y
su espejo "Acceso concurrente: protección anti-estampida (singleflight)" en
`ARQUITECTURA.es.md`.

### Tarea 2 — Observabilidad ligera de la caché (opcional, diferida)

**Contexto**: onecloudriver es una herramienta de escritorio de uso doméstico.
El proyecto ya decidió evitar un stack completo de telemetría:
[`internal/obs/obs.go`](../../internal/obs/obs.go) expone contadores expvar y
pprof en un servidor de debug **opt-in** (`mount --debug`), sin
Prometheus/OpenTelemetry, por diseño (PLAN_1 §8.4).

**Qué es aceptable**:

- Contadores expvar adicionales en `/debug/vars` vía `obs.Register` si el
  diagnóstico lo pide (p. ej. dedups de `singleflight`, misses de caché).
- A futuro, si se quiere UI: una vista simple sobre la infraestructura
  existente que muestre el uso de la caché o el número de ficheros
  sincronizados. Esto sería una feature aparte con su propia issue.

**Qué se descarta explícitamente**: dashboards de rendimiento, alertas y
tracking de patrones de llamadas a Graph — sobredimensionados para una app
doméstica y contrarios a la decisión de diseño de `internal/obs`.

**Prioridad**: Baja (solo si se detecta necesidad real de diagnóstico)
**Estado**: diferida, sin issue abierta.

### Gobernanza — Revisión trimestral

Reevaluar trimestralmente si nuevas características (concurrencia masiva,
prefetch) requieren protección adicional. Es un recordatorio de proceso para
el project board, no una tarea de código de este plan.

## Qué se eliminó del plan original y por qué

| Tarea del plan original | Decisión | Motivo |
|---|---|---|
| Métricas de concurrencia para subidas | Reducida | Ya existen contadores `uploads_completed`/`uploads_failed` en expvar |
| Tracking de patrones de llamadas Graph | Eliminada | Sin evidencia de necesidad; los logs existentes cubren el diagnóstico |
| Dashboard de rendimiento de caché | Eliminada | Contradice la decisión de `internal/obs` (sin stack de telemetría) |
| Alertas para patrones sospechosos | Eliminada | Inapropiado para una app de escritorio monousuario |
| Tareas trimestrales como "actividades" | Movida | Es gobernanza de proyecto (project board), no trabajo de implementación |

## Roadmap

```
Fase 0: Evaluación ✅
Fase 1: Implementación + tests ✅ (#156)
Tarea 1: Documentación de arquitectura ✅ (#158)
    ↓
Tarea 2: Observabilidad ligera (opcional/diferida)
    ↓
Gobernanza: revisión trimestral (project board)
```

## Métricas de Éxito

1. **Documentación completa**: apartado de protección de concurrencia mergeado
   en `ARCHITECTURE.md` y `ARQUITECTURA.es.md`.
2. **Sin stampede**: sin reportes nuevos de problemas de concurrencia (el fix
   de #153 tiene tests de regresión unitarios y FUSE).
3. **(Opcional)** Contadores expvar si se activa la Tarea 2.

## Riesgos y Mitigación

| Riesgo | Probabilidad | Impacto | Mitigación |
|--------|-------------|---------|------------|
| Nuevo escenario de stampede | Baja | Alto | Revisión trimestral (gobernanza) |
| Cambio en patrones de concurrencia | Media | Medio | La documentación de la Tarea 1 fija la estrategia como referencia |
| La documentación queda desactualizada | Media | Bajo | Actualizar al tocar los componentes listados |

## Próximos Pasos

1. Mergear el PR de la issue #158 (documentación de arquitectura + este plan).
2. Anotar la revisión trimestral en el project board.

## Referencias

- [PLAN_153.md](PLAN_153.md) — Plan de implementación de la issue #153
- [PLAN_SINGLEFLIGHT_EVALUATION.md](PLAN_SINGLEFLIGHT_EVALUATION.md) — Análisis detallado (EN)
- [PLAN_SINGLEFLIGHT_EVALUATION_ES.md](PLAN_SINGLEFLIGHT_EVALUATION_ES.md) — Análisis detallado (ES)
- [Issue #153](https://github.com/frosado/onecloudriver/issues/153) — Stampede de Lookup/Readdir en carpetas frías
- [PR #156](https://github.com/frosado/onecloudriver/pull/156) — Implementación y documentación inicial

---

**Fecha**: 2026-09-20 (original) · 2026-11-25 (reformulado tras el merge de #156; Tarea 1 completada en la issue #158)
**Estado**: Implementación y documentación de arquitectura completadas; observabilidad ligera opcional/diferida (Tarea 2)

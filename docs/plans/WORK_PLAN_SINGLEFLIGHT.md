# Plan de Trabajo: Evaluación y Aplicación de Singleflight en onecloudriver

## Resumen Ejecutivo

Este documento presenta un plan de trabajo para evaluar y aplicar el patrón `singleflight` en onecloudriver. Tras un análisis exhaustivo del código, se identifica que el patrón ya está optimamente implementado para el escenario crítico de stampede (issue #153), con oportunidades limitadas adicionales.

## Estado Actual

### ✅ Protecciones Implementadas

| Componente | Ubicación | Mecanismo | Estado |
|-----------|----------|-----------|--------|
| **InodeCache.GetChildren** | `internal/fs/cache.go` | Singleflight por carpeta | ✅ Implementado (#153) |
| **ContentCache** | `internal/fs/content_cache.go` | Bloques por-id | ✅ Sin stampede |

### ⚠️ Componentes Analizados

| Componente | Ubicación | Protegido | Riesgo de Stampede |
|-----------|----------|-----------|-------------------|
| Graph API Calls | `internal/graph/` | Parcial | Bajo |
| Upload Pipeline | `internal/fs/upload_manager.go` | Secuencial | Nulo |
| Download Operations | `internal/graph/download.go` | Secuencial | Nulo |
| Delta Sync | `internal/fs/delta.go` | Secuencial | Nulo |
| Service Commands | `cmd/onecloudriver/service.go` | Secuencial | Nulo |

## Plan de Trabajo

### Fase 1: Evaluación Completa (Completada)

**Objetivo**: Identificar todas las oportunidades de aplicar singleflight

**Actividades Realizadas**:
- [x] Analizar InodeCache (ya protegido)
- [x] Analizar ContentCache (ya protegido)
- [x] Analizar Graph API calls
- [x] Analizar operaciones de subida
- [x] Analizar operaciones de descarga
- [x] Analizar delta sync
- [x] Analizar comandos de servicio
- [x] Verificar existencia de fuse_fs.go (no encontrado)

**Resultado**: El patrón ya está optimamente aplicado al escenario crítico.

### Fase 2: Documentación de Protección Actual

**Objetivo**: Clarificar la estrategia de protección actual

**Tareas**:
- [ ] Actualizar documentación para explicar por qué singleflight solo es necesario para el escenario de carpeta
- [ ] Documentar los diferentes mecanismos de protección por componente
- [ ] Crear diagrama de protección concurrente

**Prioridad**: Media  
**Esfuerzo**: 2-3 horas

### Fase 3: Monitoreo de Rendimiento

**Objetivo**: Detectar cualquier nuevo escenario de stampede

**Tareas**:
- [ ] Agregar métricas de concurrencia para operaciones de subida
- [ ] Implementar tracking de patrones de llamadas a la API Graph
- [ ] Crear dashboard de rendimiento de caché
- [ ] Configurar alertas para patrones sospechosos

**Prioridad**: Baja  
**Esfuerzo**: 4-6 horas

### Fase 4: Evaluación Continua

**Objetivo**: Mantener la protección actual y detectar cambios

**Actividades**:
- [ ] Revisar quarterly si hay nuevas características que requieran singleflight
- [ ] Monitorear el rendimiento actual
- [ ] Evaluar si hay mejoras de rendimiento necesarias

**Prioridad**: Baja  
**Frecuencia**: Trimestral

## Recomendaciones de Implementación

### Inmediatas (Prioridad Baja)

1. **Documentación**: Actualizar la documentación para clarificar la estrategia actual
2. **Monitoreo**: Agregar métricas básicas de rendimiento

### Futuras (Prioridad Muy Baja)

1. **Si se implementa concurrencia masiva**: Re-evaluar singleflight
2. **Si se implementan pre-búsquedas**: Evaluar new opportunities

## Roadmap de Implementación

```
Fase 1: Evaluación Completa ✅
    ↓
Fase 2: Documentación (2-3 horas)
    ↓
Fase 3: Monitoreo (4-6 horas)
    ↓
Fase 4: Evaluación Continua (trimestral)
```

## Recursos Necesarios

- **Tiempo**: 6-9 horas para documentación y monitoreo
- **Personas**: 1 desarrollador
- **Herramientas**: Métricas de rendimiento, logging

## Métricas de Éxito

1. **Documentación Completa**: Todos los componentes documentados
2. **Monitoreo Activo**: Métricas de rendimiento funcionando
3. **Sin Stampede**: Sin reportes de problemas de concurrencia

## Riesgos y Mitigación

| Riesgo | Probabilidad | Impacto | Mitigación |
|--------|-------------|---------|------------|
| Nuevo escenario de stampede | Baja | Alto | Monitoreo continuo |
| Cambio en patrones de concurrencia | Media | Medio | Revisión trimestral |
| Desempeño degradado | Muy Baja | Alto | Métricas proactivas |

## Conclusiones

### Ventajas de la Actual Estrategia

1. **Optimización**: Singleflight solo donde es necesario
2. **Simplicidad**: Mecanismos de protección claros
3. **Rendimiento**: Sin overhead innecesario

### Limitaciones

1. **Complejidad**: Diferentes mecanismos para diferentes componentes
2. **Mantenimiento**: Documentación detallada requerida

## Próximos Pasos

1. **Documentar**: Actualizar la documentación actual
2. **Monitorear**: Agregar métricas de rendimiento
3. **Evaluar**: Revisar trimestralmente

## Referencias

- [PLAN_SINGLEFLIGHT_EVALUATION.md](PLAN_SINGLEFLIGHT_EVALUATION.md) - Análisis detallado
- [PLAN_SINGLEFLIGHT_EVALUATION_ES.md](PLAN_SINGLEFLIGHT_EVALUATION_ES.md) - Versión en español
- [Issue #153](https://github.com/frosado/onecloudriver/issues/153) - Implementación original

---

**Fecha**: 2026-09-20  
**Autor**: Análisis Automatizado  
**Estado**: Completado

## Notas Finales

El plan de trabajo identifica que el patrón `singleflight` ya está optimamente implementado en onecloudriver. La estrategia actual de protección concurrente es apropiada y no requiere cambios significativos.

### Recomendación Principal

**No implementar singleflight en nuevos componentes** a menos que:
1. Se implemente concurrencia masiva
2. Se detecten nuevos escenarios de stampede
3. Se requiera optimización significativa de rendimiento

### Justificación

El overhead de implementar singleflight en componentes que ya están protegidos:
- Añade complejidad innecesaria
- Potencialmente degrada el rendimiento
- No proporciona beneficios significativos

### Acción Recomendada

Enfocarse en:
1. Documentación clara de la estrategia actual
2. Monitoreo proactivo de rendimiento
3. Evaluación continua de nuevos escenarios

---

**Fin del Plan de Trabajo**

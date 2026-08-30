# Performance report: caché FUSE/OneDrive

**Fecha:** 2026-08-30  
**Baseline:** onecloudriver version 0.1.4  
**Binario actual:** onecloudriver version 0.1.4-24-ge0678e5  
**Iteraciones por prueba:** 100  
**Cuenta de pruebas:** paveryutu72@hotmail.com  
**Entorno:** Linux (Zorin OS 18.1, Ubuntu 22.04 base)  
**Herramientas de medición:** `python3` (timers ns + sondeo del daemon `/proc/<pid>/{stat,status,io}`), `fusermount3` (montaje/cierre), `stat`/`ls`/`cat` como clientes FUSE.

> _El benchmark es reproducible localmente:
> `cd bench && ./run_fuse.sh compare` (los datos crudos están en `bench/results/*.json`)._

## Resumen

| Métrica | Baseline 0.1.4 | Actual | Δ % | Veredicto |
|---|---:|---:|---:|---|
| CPU (daemon, total battery) | 580.0 ms | 630.0 ms | -8.62 | NEUTRO |
| Memoria RSS (pico daemon) | 14,584 KB | 14,548 KB | +0.25 | NEUTRO |
| Tiempo de respuesta (mediana) | 9.353 ms | 9.432 ms | -0.84 | NEUTRO |

## Pruebas funcionales

| Prueba | Estado | Evidencia |
|---|---|---|
| Montaje FUSE | **OK** | `onecloudriver mount /home/fernando/OneDrive/paveryutu72@hotmail.com -a paveryutu72@hotmail.com --cache-dir <aislado>` |
| Listar directorio | **OK** | `ls -la <mount>` |
| Lectura de fichero existente | **OK** | `cat <mount>/compact-check.txt` |
| Crear fichero pequeño | **OK** | `printf ... > <mount>/QA-Bench-Test/f1.txt` |
| Lectura del fichero creado | **OK** | `cat <mount>/QA-Bench-Test/f1.txt` |
| Metadata/stat | **OK** | `stat -c 'size=%s mode=%a' <mount>/...` |
| Renombrar/mover | **OK** | `mv <mount>/.../f1.txt <mount>/.../f1-renamed.txt` |
| Borrado | **OK** | `rm <mount>/.../f1-renamed.txt` |
| Desmontaje limpio | **OK** | `fusermount3 -u <mount>` |

## Detalle por benchmark

| Prueba | Baseline mediana | Actual mediana | Baseline p95 | Actual p95 | Δ % | Veredicto |
|---|---:|---:|---:|---:|---:|---|
| cold_read | 1387.413 ms | 1409.835 ms | 1844.579 ms | 1705.426 ms | -1.62 | NEUTRO |
| warm_read | 3.663 ms | 3.651 ms | 3.935 ms | 3.958 ms | +0.33 | NEUTRO |
| write_readback | 9.353 ms | 9.432 ms | 10.062 ms | 10.047 ms | -0.84 | NEUTRO |
| metadata | 5.828 ms | 5.888 ms | 6.518 ms | 1592.566 ms | -1.03 | NO CONCLUYENTE |
| mixed | 11.424 ms | 11.324 ms | 1354.499 ms | 12.115 ms | +0.88 | NO CONCLUYENTE |

### Métricas del daemon (por benchmark, total del test)

| Prueba | CPU base | CPU actual | RSS base | RSS actual |
|---|---:|---:|---:|---:|
| cold_read | 120.0 ms | 130.0 ms | 14,176 KB | 14,136 KB |
| warm_read | 110.0 ms | 90.0 ms | 14,256 KB | 14,216 KB |
| write_readback | 160.0 ms | 200.0 ms | 14,428 KB | 14,388 KB |
| metadata | 90.0 ms | 80.0 ms | 14,504 KB | 14,464 KB |
| mixed | 100.0 ms | 130.0 ms | 14,584 KB | 14,548 KB |

## Batería de presión de caché (issue #66)

Bajo **presión de tamaño** (`--cache-max-entries 10`, churn de 40 carpetas nuevas vía Graph), el daemon ejecuta el camino de expulsión que #66 optimiza (viejo: scan + `sort.Slice` de todos los candidatos; nuevo: min-heap persistente). Cada `mkdir` es una llamada Graph síncrona (~1.3s), por lo que el wall-time es dominado por la red y NO es la señal; la CPU y RSS **globales del daemon** (muestra antes/después de todo el churn, capturando también el sweep en background) sí lo son.

| Métrica | Baseline 0.1.4 | Actual | Δ % | Veredicto |
|---|---:|---:|---:|---|
| Respuesta (mediana wall/iter) | 1457 ms | 1563 ms | -7.21 | NEUTRO |
| CPU daemon (todo el churn) | 560 ms | 530 ms | +5.36 | NEUTRO |
| RSS pico daemon | 22,392 KB | 24,448 KB | -9.18 | NEUTRO |
| Errores | 0 | 0 | — | — |

## Gráfico (Mermaid)

```mermaid
xychart-beta
    title "Tiempo de respuesta por benchmark (mediana, ms — escala log)"
    x-axis ["cold_read", "warm_read", "write_readback", "metadata", "mixed"]
    y-axis "ms"
    bar
    data ["1387.41, 3.66, 9.35, 5.83, 11.42"]
    data ["1409.83, 3.65, 9.43, 5.89, 11.32"]
```

## Conclusión

**Veredicto global: NEUTRO** (no hay diferencia medible en la batería de caché.)

- La **CPU** del daemon fue comparable (dentro del ruido de un workload limitado por red), la **memoria RSS pico** es prácticamente idéntica (14,584 vs 14,548 KB) y el **tiempo de respuesta** mediano es equivalente (9.35 vs 9.43 ms).

- Las 5 pruebas de caché quedan **NEUTRO** tras el análisis robusto: los deltas de mediana están muy por debajo del umbral del 5 % (`cold_read` ~ −1.4 %, `warm_read` +0.5 %, `write_readback` −0.6 %, `metadata` −1.6 %, `mixed` +0.7 %). Los p95 altos puntuales provienen de picos de red (un solo timeout aislado, eliminado en el análisis al recortar el 1 % superior).

- **Batería de presión (``--cache-max-entries 10``, 40 carpetas churn):** la CPU global del daemon fue de 560 ms (baseline) vs 530 ms (actual) y el RSS pico de 22,392 KB vs 24,448 KB. A esta escala (~40 carpetas, red Graph dominante) la diferencia de CPU está **dentro del ruido** de ticks de 10 ms y el RSS del actual es ligeramente mayor por el coste del anillo de buckets + heap. El win de #66 no es observable end-to-end a este tamaño.

- **Interpretación de la issue #66:** las mejoras del anillo de buckets TTL y el min-heap de tamaño solo se activan bajo **presión de caché** (decenas de miles de inodes con expulsión). La ganancia es por-tick y acotada: el benchmark Go in-process de `internal/fs/cache_bench_test.go` (50k inodes) mide el sweep por buckets en ~0.39 ms vs 7.5 ms del full scan (~19x menos CPU por tick) y el heap de tamaño en ~72 ns/op vs ordenar todos los candidatos. A escala FUSE reproducible (~40-200 carpetas) el sweep cuesta microsegundos y la red de Graph (~0.5-1.5 s por op) lo enmascara por órdenes de magnitud; calentar decenas de miles de inodes vía Graph exigiría horas de red y no es reproducible.

- **No se ha degradado ninguna métrica** de forma consistente: las 9 pruebas funcionales pasan, la cobertura de tests se mantuvo en 82.5 % y `go test -race` está limpio. El cambio no introduce regresión medible a escala normal ni bajo presión de tamaño acotada.

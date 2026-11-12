# ADR 004 — Alcance ejecutable de v1

Estado: aceptado para esta implementación. Fecha: 2026-11-11.

Completar primero una instancia Go, cuatro algoritmos en memoria, proxy, upstream de
prueba, pruebas automatizadas y escenarios k6. Sin dependencias Go externas.
Los ADR 002 y 003 describen futuras fases Redis/Lua y resiliencia; no están implementados
en v1. No se simulan pruebas de Redis donde no existe Redis.
La interfaz real es Allow(context.Context, string) (RateLimitInfo, error).

La comparación de ADR 001 es conceptual: no existe una precisión universal del 90-95%
para Sliding Counter ni garantía general de error <10%. El resultado depende del tráfico.
La idoneidad de cada algoritmo se evaluará mediante mediciones. Las afirmaciones de
latencias Redis de ADR 002 no son resultados del proyecto. INCR seguido de EXPIRE requiere
atomicidad conjunta para evitar estados parciales; se revisará en la fase distribuida.
ADR 003 contiene dos políticas contradictorias de apertura del circuito; resolver esa
decisión será requisito previo a implementar Redis, sin atribuir garantías absolutas.

En v1 una falla de capacidad del limiter devuelve 503, nunca omite silenciosamente la cuota.

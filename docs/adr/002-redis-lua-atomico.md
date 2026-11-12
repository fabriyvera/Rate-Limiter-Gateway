# ADR 002 — Redis como storage y Lua scripts para atomicidad

## Metadatos

| Campo       | Valor                          |
|-------------|-------------------------------|
| Estado      | Aceptado                      |
| Fecha       | 2025-06                       |
| Autor       | Fabricio Vera                 |
| Proyecto    | Rate Limiter / API Gateway    |

---

## Contexto

Cada request que llega al gateway necesita hacer al menos dos operaciones
sobre el estado del rate limiter:

1. **Leer** el contador actual del cliente.
2. **Incrementar** ese contador (o decrementar tokens, según el algoritmo).

El problema central es que estas dos operaciones deben comportarse como
una sola operación indivisible. Si dos requests del mismo cliente llegan
al mismo milisegundo y ambas leen el contador antes de que cualquiera
lo incremente, ambas verán el mismo valor y ambas pasarán — incluso si
una de ellas debería haber sido bloqueada. Esto es una race condition.

```
Request A: lee contador = 99  (límite: 100)  → decide: permitir
Request B: lee contador = 99  (límite: 100)  → decide: permitir
Request A: incrementa → contador = 100
Request B: incrementa → contador = 101  ← BUG: pasaron 101 requests
```

Este problema se vuelve crítico en un sistema distribuido donde múltiples
instancias del gateway comparten el mismo estado. La solución requiere
un storage que soporte operaciones atómicas y baja latencia.

---

## Decisión

Se usa **Redis** como storage compartido con **scripts Lua** para ejecutar
la lógica de chequeo e incremento como una operación atómica e indivisible.

---

## Por qué Redis

### 1. Operaciones atómicas nativas

Redis garantiza que cada comando individual es atómico. `INCR`, `GET`,
`ZADD` nunca se ejecutan parcialmente. Esto resuelve el problema para
algoritmos simples como Fixed Window, donde un solo `INCR` con `EXPIRE`
es suficiente.

### 2. Estructuras de datos adecuadas para cada algoritmo

Cada algoritmo del proyecto necesita una estructura diferente, y Redis
las tiene todas:

| Algoritmo            | Estructura Redis | Operación clave         |
|----------------------|-----------------|-------------------------|
| Fixed Window         | String + TTL    | INCR / EXPIRE           |
| Token Bucket         | Hash            | HGET + HSET (via Lua)   |
| Sliding Window Log   | Sorted Set      | ZADD + ZCOUNT + ZREMRANGEBYSCORE |
| Sliding Window Counter| String x2      | GET + INCR + EXPIRE     |

### 3. Latencia sub-milisegundo

Redis opera completamente en memoria. La latencia típica de un GET/SET
es de 0.1–0.5ms en condiciones normales. Para un gateway que necesita
decidir en microsegundos si permite o bloquea, esto es aceptable sin
impacto perceptible en la latencia total de la request.

### 4. TTL nativo

Redis expira keys automáticamente. Esto es fundamental para el rate
limiter: los contadores de clientes inactivos se limpian solos sin
necesidad de un proceso de mantenimiento externo.

### 5. Comportamiento conocido en producción

Redis es la solución estándar de la industria para rate limiting
distribuido. Stripe, GitHub, Twitter y Cloudflare lo usan para este
problema específico. Su modelo single-threaded para comandos garantiza
consistencia sin locks.

---

## El problema de atomicidad para algoritmos complejos

Para Fixed Window, un `INCR` atómico es suficiente. Pero para Token Bucket
o Sliding Window Counter, necesitamos múltiples operaciones que dependan
unas de otras:

**Token Bucket (pseudocódigo del problema):**

```
tokens_actuales = GET tokens:{key}
ultimo_refill   = GET last_refill:{key}
ahora           = time.now()
tokens_nuevos   = min(capacidad, tokens_actuales + tasa × (ahora - ultimo_refill))

// ← Entre esta línea y la siguiente, otra instancia puede leer el mismo valor

if tokens_nuevos >= 1:
    SET tokens:{key} = tokens_nuevos - 1
    SET last_refill:{key} = ahora
    return ALLOW
else:
    return DENY
```

Si dos instancias del gateway ejecutan este bloque simultáneamente sobre
el mismo cliente, ambas leerán los mismos `tokens_actuales` y `ultimo_refill`,
calculan los mismos `tokens_nuevos`, y ambas decrementan — consumiendo
2 tokens cuando deberían haber consumido 1, o peor: ambas pasan cuando
solo debería pasar una.

---

## Por qué Lua scripts en Redis

Redis ejecuta scripts Lua de forma **atómica y bloqueante**: mientras
el script corre, ningún otro comando puede modificar las keys involucradas.
Esto convierte todo el bloque de lógica en una operación indivisible.

```lua
-- increment.lua (Token Bucket simplificado)
local key_tokens     = KEYS[1]
local key_refill     = KEYS[2]
local capacidad      = tonumber(ARGV[1])
local tasa           = tonumber(ARGV[2])  -- tokens por segundo
local ahora          = tonumber(ARGV[3])  -- unix timestamp en ms

local tokens         = tonumber(redis.call('GET', key_tokens) or capacidad)
local ultimo_refill  = tonumber(redis.call('GET', key_refill) or ahora)

-- Calcular tokens ganados desde el último refill
local elapsed        = (ahora - ultimo_refill) / 1000  -- en segundos
local tokens_nuevos  = math.min(capacidad, tokens + tasa * elapsed)

if tokens_nuevos >= 1 then
    redis.call('SET', key_tokens, tokens_nuevos - 1)
    redis.call('SET', key_refill, ahora)
    redis.call('EXPIRE', key_tokens, 3600)
    redis.call('EXPIRE', key_refill, 3600)
    return {1, math.floor(tokens_nuevos - 1)}  -- {allowed, tokens_restantes}
else
    return {0, 0}  -- {denied, tokens_restantes}
end
```

Este script completo se ejecuta sin interrupciones. No hay ventana donde
otra instancia pueda leer un estado intermedio.

### Alternativas consideradas a Lua y por qué se descartaron

**MULTI/EXEC (transacciones Redis):**
Redis soporta transacciones con `MULTI/EXEC`, pero no son verdaderamente
atómicas en el sentido que necesitamos: si usamos `WATCH` para detectar
cambios entre el `GET` y el `SET`, la transacción puede fallar y
necesitamos reintentar en bucle. Bajo alta concurrencia esto genera
contención y latencia impredecible. Lua es más simple y más eficiente.

**Locks con SETNX:**
Implementar un distributed lock (tipo Redlock) para proteger la sección
crítica añade latencia, complejidad y nuevos puntos de falla. Para
operaciones tan pequeñas como un chequeo de rate limit, el overhead del
lock supera el beneficio.

**Operaciones individuales atómicas de Redis:**
Para Fixed Window alcanza con `INCR`. Pero Token Bucket y Sliding Window
Counter requieren leer, calcular y escribir, lo cual no se puede expresar
como un solo comando de Redis. Lua es la solución correcta.

---

## Consecuencias de esta decisión

**Positivas:**
- Consistencia garantizada bajo concurrencia alta y múltiples instancias.
- Latencia predecible: el script Lua agrega ~0.5–1ms sobre la latencia
  base de Redis, aceptable para un gateway.
- Redis maneja la expiración de keys sin proceso externo.
- Los scripts Lua se pueden cargar una vez al inicio con `SCRIPT LOAD`
  y llamar por SHA1 en producción, evitando el overhead de parseo.

**Negativas:**
- Los scripts Lua bloquean Redis durante su ejecución. Scripts muy largos
  o lentos pueden impactar a otros clientes de Redis. Los scripts del
  proyecto son simples (< 20 líneas) y de ejecución sub-milisegundo,
  por lo que el riesgo es mínimo.
- Debugging de Lua en Redis es más difícil que debugging de Go puro.
  Se mitiga con tests unitarios exhaustivos del comportamiento del
  script antes de integrarlo.
- Redis es un punto único de falla. Esto se aborda en ADR 003
  (fail-open vs fail-closed).

---

## Configuración de Redis para este proyecto

```yaml
# redis.conf relevante
maxmemory 256mb
maxmemory-policy allkeys-lru   # evictar keys menos usadas si llena
save ""                         # deshabilitar persistencia (no la necesitamos)
appendonly no                   # idem
```

La persistencia de Redis se deshabilita intencionalmente: los contadores
de rate limiting son estado efímero. Si Redis se reinicia, los contadores
empiezan en cero — lo cual es aceptable porque el impacto es que algunos
clientes temporalmente no tengan límite aplicado, no que el sistema
pierda datos críticos de negocio.

---

## Referencias

- Redis documentation — Atomicity with Lua
- Redis documentation — EVAL, SCRIPT LOAD, EVALSHA
- Cloudflare Blog — *How we built rate limiting* (uso de Lua en Redis)
- Stripe Engineering — *Scaling your API with rate limiters*
- Martin Kleppmann — *Designing Data-Intensive Applications*, Cap. 9
  (Consistency and Consensus)

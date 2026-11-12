# ADR 003 — Fail-open vs Fail-closed cuando Redis no responde

## Metadatos

| Campo       | Valor                          |
|-------------|-------------------------------|
| Estado      | Aceptado                      |
| Fecha       | 2025-06                       |
| Autor       | Fabricio Vera                 |
| Proyecto    | Rate Limiter / API Gateway    |

---

## Contexto

Como se estableció en ADR 002, Redis es el componente central de estado
del gateway. Todos los contadores de rate limiting viven ahí. Esto crea
una dependencia crítica: si Redis no responde, el gateway no puede
consultar ni actualizar el estado del rate limiter.

Esto puede ocurrir por múltiples razones en producción:

- Redis se reinicia por un deploy o crash.
- Latencia de red entre el gateway y Redis supera el timeout configurado.
- Redis está bajo presión de memoria y sus respuestas se vuelven lentas.
- El contenedor de Redis muere en Docker Compose o Kubernetes.

Cuando esto sucede, el gateway tiene exactamente dos opciones posibles
ante cada request entrante:

**Fail-open:** si no puedo consultar Redis, permito la request de todas
formas y dejo pasar al cliente.

**Fail-closed:** si no puedo consultar Redis, bloqueo la request y
respondo con error hasta que Redis vuelva.

Esta es una decisión de negocio disfrazada de decisión técnica. No existe
una respuesta universalmente correcta — depende de qué es más costoso
para el sistema: el abuso de clientes sin límite, o la indisponibilidad
total del servicio.

---

## Decisión

Se implementa **fail-open con timeout configurable y circuit breaker**.

El gateway intenta consultar Redis con un timeout de 50ms. Si Redis no
responde dentro de ese tiempo, permite la request, registra el evento
en el log de errores, e incrementa un contador de "decisiones sin
estado". Si ese contador supera un umbral configurable en una ventana
de tiempo, el circuit breaker se abre y el sistema cambia a fail-closed
temporalmente para evitar saturar Redis cuando vuelva a estar disponible.

---

## Análisis de las dos opciones

### Opción A: Fail-open

**Comportamiento:** cuando Redis no responde, el gateway deja pasar
todas las requests sin aplicar límite.

**Ventajas:**
- El servicio backend sigue disponible para los clientes legítimos.
- Una falla en el componente de rate limiting no se convierte en una
  falla total del sistema — el rate limiter es infraestructura de
  protección, no de negocio.
- Alineado con el principio de diseño: *degradar graciosamente* ante
  fallos parciales.

**Riesgos:**
- Durante la ventana de falla, un cliente malicioso puede enviar tráfico
  sin restricción al backend.
- Si el backend no tiene su propia protección (circuit breakers, pool
  limits), puede saturarse.
- La ventana de exposición depende de cuánto tarde Redis en recuperarse.

**Cuándo es la opción correcta:**
- Cuando la disponibilidad del servicio es más crítica que la protección
  ante abuso (APIs públicas de lectura, servicios de información).
- Cuando el backend tiene sus propias capas de protección.
- Cuando el costo de un falso negativo (bloquear a un usuario legítimo)
  es mayor que el costo de un falso positivo (dejar pasar tráfico
  excesivo temporalmente).

---

### Opción B: Fail-closed

**Comportamiento:** cuando Redis no responde, el gateway bloquea todas
las requests con un error 503 (Service Unavailable) hasta que Redis
vuelva.

**Ventajas:**
- Garantía total de que el límite nunca se excede, incluso durante fallos.
- Protección absoluta del backend contra tráfico no controlado.

**Riesgos:**
- Una falla en Redis (componente auxiliar) se convierte en una falla
  total del servicio para todos los clientes, incluyendo los legítimos.
- Crea una cadena de dependencias donde el rate limiter es tan crítico
  como el backend mismo — lo cual no debería ser el caso.
- En sistemas con SLA de disponibilidad (ej. 99.9%), un Redis caído
  durante 10 minutos ya viola el SLA, aunque el backend esté sano.

**Cuándo es la opción correcta:**
- Sistemas financieros o regulados donde cada request tiene un costo
  monetario real (ej. SMS OTP, llamadas a APIs de pago por uso).
- Cuando el backend no tiene ninguna protección propia y una ráfaga
  sin límite lo derrumbaría.
- Cuando la protección ante abuso es un requisito de negocio no
  negociable.

---

## Por qué fail-open para este proyecto

El gateway actúa como capa de protección frente a APIs que se asume
tienen su propia capacidad de carga. El objetivo del rate limiter es
proteger contra abuso sostenido, no contra ráfagas de segundos durante
una falla de infraestructura.

Bloquear todas las requests legítimas porque Redis tuvo un timeout de
50ms es un daño colateral desproporcionado. Un cliente legítimo no debería
experimentar un error 503 porque el sistema de protección tuvo un
problema interno.

La filosofía de diseño aplicada es: **las fallas en la infraestructura
de protección no deben convertirse en fallas del servicio protegido**.

---

## Implementación: timeout + circuit breaker

Solo fail-open puro tiene un problema: si Redis está degradado pero
responde lentamente (en lugar de no responder), cada request espera el
timeout completo antes de pasar — añadiendo latencia inaceptable. El
circuit breaker resuelve esto:

```
Estado normal:
  Redis responde en < 50ms → aplica rate limit normalmente

Redis no responde / timeout:
  → permite la request (fail-open)
  → incrementa contador de fallos
  → registra en log: "redis_timeout key=X duration=52ms"

Si fallos > umbral en ventana de tiempo (ej. 10 fallos en 5 segundos):
  → circuit breaker ABIERTO
  → todas las requests pasan sin consultar Redis
  → se intenta reconexión cada N segundos (half-open)
  → cuando Redis responde: circuit breaker CERRADO, vuelve a normal
```

```
                    ┌─────────────────────────────────────────┐
                    │           Circuit Breaker                │
                    │                                          │
         error      │  CERRADO ──────────────────► ABIERTO    │
        threshold   │  (normal)    fallos > N      (bypass)   │
         superado   │                                          │
                    │  CERRADO ◄────────────────── HALF-OPEN  │
                    │           Redis responde     (prueba)    │
                    └─────────────────────────────────────────┘
```

### Parámetros configurables (en rules.yaml)

```yaml
redis:
  timeout_ms: 50          # tiempo máximo de espera por respuesta
  circuit_breaker:
    enabled: true
    failure_threshold: 10  # fallos para abrir el circuito
    window_seconds: 5      # ventana de observación
    half_open_after: 30    # segundos antes de intentar reconexión
  on_failure: "open"       # "open" = fail-open | "closed" = fail-closed
```

Hacer `on_failure` configurable permite que quien opere el sistema
elija el comportamiento según su contexto de negocio, sin cambiar código.

---

## Observabilidad durante fallos

Fail-open sin visibilidad es peligroso: el sistema estaría degradado
en silencio. Por eso cada decisión sin estado debe ser observable:

```json
{
  "level": "warn",
  "event": "rate_limit_bypassed",
  "reason": "redis_timeout",
  "key": "ip:192.168.1.1",
  "duration_ms": 52,
  "circuit_state": "open",
  "timestamp": "2025-06-01T14:23:01Z"
}
```

El dashboard de React muestra en tiempo real:
- Estado del circuit breaker (cerrado / abierto / half-open)
- Requests que pasaron sin consultar Redis en los últimos N minutos
- Latencia de Redis en percentiles p50/p95/p99

Esto permite que el operador detecte inmediatamente si el sistema está
en modo degradado, cuánto tiempo lleva así, y cuánto tráfico sin límite
pasó durante la ventana.

---

## Consecuencias de esta decisión

**Positivas:**
- El servicio backend permanece disponible para clientes legítimos
  incluso cuando Redis falla.
- El circuit breaker evita saturar un Redis ya degradado con timeouts
  repetidos.
- La configurabilidad permite adaptar el comportamiento sin redeploy.
- La observabilidad garantiza que la degradación nunca pase desapercibida.

**Negativas:**
- Durante la ventana de falla, un cliente malicioso puede enviar tráfico
  sin restricción. Se acepta este riesgo porque la ventana es acotada
  (hasta que el circuit breaker cierre o Redis se recupere).
- El circuit breaker añade complejidad de estado interno al gateway.
  Se mitiga con una implementación simple basada en contadores atómicos
  en memoria (no en Redis, para evitar la dependencia circular).

---

## Nota sobre la implementación del circuit breaker

El estado del circuit breaker (contador de fallos, timestamp de apertura)
vive en **memoria local de cada instancia del gateway**, no en Redis.
Esto es intencional: usar Redis para gestionar el estado del circuit
breaker que protege a Redis crearía una dependencia circular que haría
el sistema imposible de recuperar.

La consecuencia es que cada instancia del gateway tiene su propio circuit
breaker independiente. En un escenario con 3 instancias, es posible que
una instancia abra su circuito antes que las otras. Esto es aceptable
porque el comportamiento de fail-open ya garantiza disponibilidad — la
inconsistencia entre instancias solo afecta al rate limiting, no al
servicio en sí.

---

## Referencias

- Michael Nygard — *Release It!*, Cap. 5 (Stability Patterns,
  Circuit Breaker)
- Netflix Tech Blog — *Fault Tolerance in a High Volume, Distributed
  System* (Hystrix, origen del patrón circuit breaker moderno)
- Martin Fowler — *CircuitBreaker* (martinfowler.com/bliki)
- AWS Well-Architected Framework — Reliability Pillar, graceful degradation

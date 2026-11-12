# ADR 001 — Algoritmos de Rate Limiting

## Metadatos

| Campo       | Valor                          |
|-------------|-------------------------------|
| Estado      | Aceptado                      |
| Fecha       | 2025-06                       |
| Autor       | Fabricio Vera                 |
| Proyecto    | Rate Limiter / API Gateway    |

---

## Contexto

El sistema necesita controlar cuántas requests puede hacer un cliente
en un periodo de tiempo determinado. Esta es la decisión más fundamental
del proyecto: el algoritmo elegido afecta directamente la precisión del
límite, el consumo de memoria, la complejidad de implementación y el
comportamiento bajo carga.

Existen cuatro algoritmos establecidos en la industria para este problema.
Cada uno resuelve el mismo objetivo con diferentes trade-offs. La pregunta
no es cuál es "el mejor", sino cuándo usar cada uno.

---

## Decisión

Se implementarán los cuatro algoritmos detrás de una interfaz común
(`Limiter`), lo que permite cambiar el algoritmo en tiempo de configuración
sin modificar el gateway ni el middleware. La interfaz expone un único
método:

```
Allow(key string) (allowed bool, info RateLimitInfo)
```

Esto significa que el resto del sistema (proxy, middleware, dashboard) es
completamente agnóstico al algoritmo que esté corriendo.

---

## Algoritmos considerados

### 1. Fixed Window

**Cómo funciona:** divide el tiempo en ventanas fijas (ej. cada 60
segundos) y mantiene un contador por ventana. Si el contador supera el
límite, bloquea hasta que empiece la siguiente ventana.

**Ventajas:**
- Implementación trivial: un `INCR` con TTL en Redis.
- Consumo de memoria mínimo: un solo entero por cliente.

**Desventaja crítica — el burst en el borde:**
Un cliente puede hacer el doble de requests permitidas en un instante.
Si el límite es 100 req/min y la ventana se resetea a las 12:01:00,
el cliente puede hacer 100 requests a las 12:00:59 y otras 100 requests
a las 12:01:01 — 200 requests en 2 segundos, sin violar técnicamente el
límite.

**Veredicto:** útil para entender el problema base y el bug del borde.
No recomendado para producción. Se implementa como referencia comparativa.

---

### 2. Token Bucket

**Cómo funciona:** cada cliente tiene un "balde" con capacidad máxima de
N tokens. Los tokens se recargan a tasa constante (ej. 10 tokens/segundo).
Cada request consume 1 token. Si el balde está vacío, la request se bloquea.

**Ventajas:**
- Permite ráfagas controladas: un cliente inactivo acumula tokens y puede
  hacer varias requests seguidas sin ser bloqueado, lo que es el
  comportamiento más natural para una API real.
- Es el algoritmo que usa Stripe en su API.
- Fácil de razonar: los usuarios entienden "tienes X créditos que se
  recargan a Y por segundo".

**Desventajas:**
- Requiere guardar dos valores por cliente: tokens disponibles y timestamp
  del último refill.
- El cálculo del refill debe hacerse en el momento de cada request
  (lazy refill), no en background.

**Veredicto:** algoritmo principal recomendado para la mayoría de casos
de uso. Buena experiencia para el cliente legítimo, control real del abuso.

---

### 3. Sliding Window Log

**Cómo funciona:** guarda el timestamp exacto de cada request en un sorted
set de Redis. Para decidir si una request pasa, cuenta cuántos timestamps
caen dentro de los últimos N segundos. Si la cantidad supera el límite,
bloquea.

**Ventajas:**
- Precisión perfecta: no hay efecto de borde, el límite es exactamente
  N requests en cualquier ventana de N segundos.

**Desventaja crítica — memoria:**
Para un límite de 1000 req/min, se guardan hasta 1000 timestamps por
cliente. Con millones de clientes activos, el consumo de memoria se
vuelve prohibitivo. En sistemas con muchos usuarios concurrentes, este
algoritmo no escala.

**Veredicto:** ideal para sistemas con pocos clientes privilegiados donde
la precisión es más importante que la memoria (ej. acceso a APIs internas,
partners con SLA). No recomendado para endpoints públicos de alta escala.

---

### 4. Sliding Window Counter

**Cómo funciona:** combina la eficiencia de Fixed Window con una
aproximación del comportamiento de Sliding Window. Mantiene el contador
de la ventana actual y de la ventana anterior, y calcula una aproximación
del conteo para la ventana deslizante:

```
conteo_estimado = (contador_anterior × (1 - fraccion_transcurrida))
                + contador_actual
```

Ejemplo: si la ventana actual tiene 40 segundos transcurridos de 60, la
fracción transcurrida es 0.67. El contador anterior se pondera con 0.33.

**Ventajas:**
- Memoria mínima: solo dos contadores por cliente (ventana actual y anterior).
- Precisión aceptable: el error de estimación es menor al 10% en la mayoría
  de escenarios de tráfico real, lo cual es suficiente para rate limiting.
- Escala muy bien: es el algoritmo que usa Cloudflare para su rate limiting
  a escala global.

**Desventajas:**
- El conteo es una estimación, no exacto. En escenarios de tráfico muy
  irregular puede tener un margen de error.

**Veredicto:** mejor balance entre precisión y eficiencia. Recomendado para
endpoints públicos de alta escala donde la memoria es una restricción real.

---

## Comparación directa

| Criterio            | Fixed Window | Token Bucket | Sliding Log | Sliding Counter |
|---------------------|:------------:|:------------:|:-----------:|:---------------:|
| Precisión           | Baja         | Alta         | Perfecta    | Alta (~90-95%)  |
| Memoria por cliente | 1 entero     | 2 valores    | N timestamps| 2 enteros       |
| Permite ráfagas     | Sí (bug)     | Sí (feature) | No          | No              |
| Complejidad impl.   | Trivial      | Baja         | Media       | Baja            |
| Escala en prod.     | No           | Sí           | Solo VIP    | Sí              |

---

## Consecuencias de esta decisión

**Positivas:**
- El sistema puede elegir el algoritmo óptimo por regla, no globalmente.
  Ejemplo: Token Bucket para la API pública, Sliding Log para el panel de
  admin, Fixed Window solo en demos educativos.
- Agregar un quinto algoritmo en el futuro no requiere tocar el gateway.
- Los benchmarks del dashboard son posibles porque todos los algoritmos
  exponen la misma interfaz.

**Negativas:**
- Requiere mantener 4 implementaciones con sus respectivos tests.
- El equipo que opere el sistema necesita entender los trade-offs para
  configurar las reglas correctamente.

---

## Alternativas descartadas

**Leaky Bucket:** conceptualmente similar a Token Bucket pero procesa
requests a tasa constante (cola FIFO), lo que introduce latencia
intencional. Útil para smoothing de tráfico, no para rate limiting puro.
Descartado porque añade complejidad de cola sin beneficio claro para
este proyecto.

**Algoritmos externos (Kong, Nginx):** usar un rate limiter ya existente
elimina el aprendizaje central del proyecto. El objetivo es entender la
implementación, no solo el uso.

---

## Referencias

- Stripe Engineering Blog — *Scaling your API with rate limiters* (2017)
- Cloudflare Blog — *How we built rate limiting capable of scaling to
  millions of domains* (2021)
- Redis documentation — INCR, ZADD, Lua scripting
- Martin Kleppmann — *Designing Data-Intensive Applications*, Cap. 8

# Diagramas de secuencia — implementación v1

Los cuatro diagramas corresponden al motor local con mutex. No representan Redis.

## Fixed Window

```mermaid
sequenceDiagram
    autonumber
    actor C as Cliente k6
    participant G as Gateway HTTP
    participant L as Limiter
    participant M as Estado en memoria
    participant B as Backend
    C->>G: Solicitud HTTP
    G->>G: Extraer identidad
    G->>L: Allow(ctx, clave)
    L->>L: Adquirir mutex y comprobar contexto
    L->>M: Limpiar expirados y buscar cliente
    alt Capacidad agotada para cliente nuevo
        M-->>L: Error de capacidad
        L->>L: Liberar mutex
        L-->>G: ErrCapacity
        G-->>C: HTTP 503
    else Estado disponible
        L->>M: Calcular inicio = truncar(ahora, ventana)
        L->>M: Si cambio ventana, reiniciar contador
        L->>M: Permitir si count menor que limite
        L->>M: Si permite: incrementar count
        L->>M: Si rechaza: retry = fin - ahora
        M-->>L: Decision y estado actualizado
        L->>L: Liberar mutex
        L-->>G: RateLimitInfo
        alt Solicitud admitida
            G->>B: Reenviar metodo, ruta, query y cuerpo
            B-->>G: Respuesta
            G-->>C: Respuesta y headers de cuota
        else Cuota agotada
            G-->>C: HTTP 429 y Retry-After
        end
    end
```

## Token Bucket

```mermaid
sequenceDiagram
    autonumber
    actor C as Cliente k6
    participant G as Gateway HTTP
    participant L as Limiter
    participant M as Estado en memoria
    participant B as Backend
    C->>G: Solicitud HTTP
    G->>G: Extraer identidad
    G->>L: Allow(ctx, clave)
    L->>L: Adquirir mutex y comprobar contexto
    L->>M: Limpiar expirados y buscar cliente
    alt Capacidad agotada para cliente nuevo
        M-->>L: Error de capacidad
        L->>L: Liberar mutex
        L-->>G: ErrCapacity
        G-->>C: HTTP 503
    else Estado disponible
        L->>M: Inicializar cliente nuevo con capacidad completa
        L->>M: Recargar = min(capacidad, tokens + transcurrido por tasa)
        L->>M: Permitir si tokens mayor o igual a 1
        L->>M: Si permite: consumir un token
        L->>M: Si rechaza: retry = (1 - tokens) dividido por tasa
        M-->>L: Decision y estado actualizado
        L->>L: Liberar mutex
        L-->>G: RateLimitInfo
        alt Solicitud admitida
            G->>B: Reenviar metodo, ruta, query y cuerpo
            B-->>G: Respuesta
            G-->>C: Respuesta y headers de cuota
        else Cuota agotada
            G-->>C: HTTP 429 y Retry-After
        end
    end
```

## Sliding Window Log

```mermaid
sequenceDiagram
    autonumber
    actor C as Cliente k6
    participant G as Gateway HTTP
    participant L as Limiter
    participant M as Estado en memoria
    participant B as Backend
    C->>G: Solicitud HTTP
    G->>G: Extraer identidad
    G->>L: Allow(ctx, clave)
    L->>L: Adquirir mutex y comprobar contexto
    L->>M: Limpiar expirados y buscar cliente
    alt Capacidad agotada para cliente nuevo
        M-->>L: Error de capacidad
        L->>L: Liberar mutex
        L-->>G: ErrCapacity
        G-->>C: HTTP 503
    else Estado disponible
        L->>M: Eliminar timestamps menores o iguales a ahora - ventana
        L->>M: Permitir si cantidad menor que limite
        L->>M: Si permite: agregar timestamp actual
        L->>M: Si rechaza: retry = mas antiguo + ventana - ahora
        M-->>L: Decision y estado actualizado
        L->>L: Liberar mutex
        L-->>G: RateLimitInfo
        alt Solicitud admitida
            G->>B: Reenviar metodo, ruta, query y cuerpo
            B-->>G: Respuesta
            G-->>C: Respuesta y headers de cuota
        else Cuota agotada
            G-->>C: HTTP 429 y Retry-After
        end
    end
```

## Sliding Window Counter

```mermaid
sequenceDiagram
    autonumber
    actor C as Cliente k6
    participant G as Gateway HTTP
    participant L as Limiter
    participant M as Estado en memoria
    participant B as Backend
    C->>G: Solicitud HTTP
    G->>G: Extraer identidad
    G->>L: Allow(ctx, clave)
    L->>L: Adquirir mutex y comprobar contexto
    L->>M: Limpiar expirados y buscar cliente
    alt Capacidad agotada para cliente nuevo
        M-->>L: Error de capacidad
        L->>L: Liberar mutex
        L-->>G: ErrCapacity
        G-->>C: HTTP 503
    else Estado disponible
        L->>M: Rotar ventanas y conservar anterior si es adyacente
        L->>M: Estimar = anterior por (1 - fraccion) + actual
        L->>M: Permitir si estimado + 1 menor o igual a limite
        L->>M: Si permite: incrementar actual
        L->>M: Si rechaza: resolver tiempo hasta espacio para una solicitud
        M-->>L: Decision y estado actualizado
        L->>L: Liberar mutex
        L-->>G: RateLimitInfo
        alt Solicitud admitida
            G->>B: Reenviar metodo, ruta, query y cuerpo
            B-->>G: Respuesta
            G-->>C: Respuesta y headers de cuota
        else Cuota agotada
            G-->>C: HTTP 429 y Retry-After
        end
    end
```


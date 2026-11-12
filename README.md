# Rate Limiter Gateway — v1

Gateway HTTP en Go para experimentos locales con cuatro algoritmos en memoria.
Esta versión usa solo la biblioteca estándar y una instancia. Redis, reglas por ruta,
circuit breaker y dashboard pertenecen a etapas posteriores (ADR 004).

## Inicio local (PowerShell o terminal)
Requiere Go 1.23+ y k6 instalado según https://grafana.com/docs/k6/latest/set-up/install-k6/.
Desde la raíz, en una primera terminal:
```sh
cd gateway
go run ./cmd/upstream
```
En otra terminal, desde la raíz:
```sh
cd gateway
go run ./cmd/gateway --algorithm token_bucket --limit 100 --window 1s --client-header X-Test-Client
```
En una tercera terminal, desde la raíz:
```sh
k6 run loadtest/sustained.js
k6 run loadtest/burst.js
```
`/healthz` devuelve salud del proceso, no del upstream. `/metrics` expone contadores JSON.
El backend de prueba responde 200 en cualquier ruta. También puedes configurar
`--upstream http://127.0.0.1:PUERTO` para tu propia API.

## Algoritmos y configuración
`--algorithm`: fixed_window, token_bucket, sliding_window_log, sliding_window_counter.
`none` permite medir el costo del proxy sin limitación.
La cuota se comparte entre todas las rutas por consumidor, excepto /healthz y /metrics.
Por defecto se usa la IP del socket, sin confiar en X-Forwarded-For.
`--client-header X-Test-Client` es exclusivamente para laboratorio: el cliente puede
cambiarlo libremente y eludir cuotas. No es autenticación.

- Fixed Window: ventanas alineadas, cuenta admisiones. Puede permitir 2N alrededor del borde.
- Token Bucket: capacidad `--capacity` (default limit), recarga `--rate` (default limit/window).
- Sliding Log: conserva admisiones en (ahora-window, ahora]; exacto para esa política.
- Sliding Counter: pondera ventana anterior; admite solo si estimado+1 <= limit. Aproximado.

`--max-keys` limita identidades activas (10000 por defecto); al llenarse responde 503
para nuevas identidades, sin borrar cuotas activas. Limpieza perezosa cada segundo
cuando llega tráfico; reiniciar elimina todo el estado. Sliding Log consume O(clientes*limit).
`--upstream-timeout` limita espera del upstream (10s). Errores de conexión responden 502.
`Retry-After` redondea hacia arriba a segundos (mínimo 1). X-RateLimit-Reset es Unix
seconds del momento en que se vacía el estado relevante sin nuevas solicitudes
(o se llena el bucket); no equivale siempre al próximo permiso. Los headers X-RateLimit
son convenciones de esta aplicación, no se presentan como estándar HTTP.

## Prueba funcional con k6
Reinicia el gateway con esta configuración y luego ejecuta smoke desde la raíz:
```sh
# terminal del gateway, dentro de gateway/
go run ./cmd/gateway --algorithm token_bucket --limit 2 --window 2s --client-header X-Test-Client
# otra terminal, raíz del repositorio
k6 run loadtest/smoke.js
```
Repite sustituyendo el nombre del algoritmo. Smoke valida permiso, bloqueo, aislamiento
y recuperación. La prueba requiere un equipo local sin pausas largas durante la ráfaga.

## Docker Compose (alternativa)
```sh
docker compose up --build -d
# esperar a que http://127.0.0.1:8080/healthz responda 200
docker compose run --rm k6
docker compose run --rm k6 run /scripts/burst.js
docker compose down
```
Variables Compose: ALGORITHM, LIMIT, WINDOW, RPS, CLIENTS, DURATION.
Para smoke: LIMIT=2, WINDOW=2s y recrear el gateway antes de ejecutar /scripts/smoke.js.
En PowerShell: `$env:LIMIT="2"; $env:WINDOW="2s"`.

## Verificación
Dentro de gateway/: `go test ./...`, `go vet ./...`, `go test -race ./...`.
Race requiere un compilador C compatible en Windows. CI usa Linux y además ejecuta
smoke k6 en Docker para cada algoritmo.

- [Diagramas de secuencia](docs/sequences.md)
- [Protocolo experimental](docs/benchmarks.md)
- [Arquitectura y revisión](docs/architecture.md)

// Package limiter implementa los cuatro algoritmos de rate limiting.
// Todos comparten la interfaz Limiter, lo que permite intercambiarlos
// sin modificar el gateway ni el middleware.
package limiter

import (
	"context"
	"time"
)

// RateLimitInfo contiene el resultado de una decisión de rate limiting.
// Se usa para construir los headers HTTP estándar de la respuesta.
type RateLimitInfo struct {
	Allowed    bool
	Limit      int           // X-RateLimit-Limit
	Remaining  int           // X-RateLimit-Remaining
	ResetAt    time.Time     // X-RateLimit-Reset
	RetryAfter time.Duration // Retry-After (solo cuando Allowed=false)
}

// Config contiene la configuración de un rate limiter.
// Algunos campos son específicos de ciertos algoritmos.
type Config struct {
	MaxKeys int // Maximum active client identities; zero defaults to 10000.
	// Campos comunes a todos los algoritmos
	Limit  int           // máximo de requests permitidas
	Window time.Duration // ventana de tiempo

	// Específicos de Token Bucket
	// Si Capacity=0 se usa Limit. Si Rate=0 se calcula como Limit/Window.
	Capacity int     // tamaño máximo del balde (burst máximo)
	Rate     float64 // tokens por segundo de recarga
}

// Limiter es la interfaz que todos los algoritmos deben implementar.
// El gateway y el middleware solo conocen esta interfaz, nunca la
// implementación concreta — eso permite cambiar el algoritmo en runtime.
type Limiter interface {
	// Allow decide si la request identificada por key puede pasar.
	// key puede ser una IP, API key, JWT subject, o combinación de ellos.
	Allow(ctx context.Context, key string) (RateLimitInfo, error)

	// Type retorna el nombre del algoritmo para logging y métricas.
	Type() string
}

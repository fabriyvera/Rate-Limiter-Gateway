package limiter

import (
	"context"
	"sync"
	"time"
)

// tokenBucketEntry guarda el estado del balde de un cliente.
type tokenBucketEntry struct {
	tokens     float64   // tokens disponibles actualmente
	lastRefill time.Time // timestamp del último refill
}

// TokenBucket implementa el algoritmo Token Bucket.
//
// Cada cliente tiene un balde con capacidad máxima de Capacity tokens.
// Los tokens se recargan a Rate tokens/segundo. Cada request consume 1 token.
// Si el balde está vacío, la request se bloquea.
//
// Ventaja clave: permite ráfagas controladas. Un cliente inactivo acumula
// tokens y puede hacer varias requests seguidas sin ser bloqueado, lo cual
// es el comportamiento más natural para una API real.
//
// Es el algoritmo que usa Stripe en producción.
type TokenBucket struct {
	config  Config
	mu      sync.Mutex
	entries map[string]*tokenBucketEntry
}

func NewTokenBucket(cfg Config) *TokenBucket {
	// Defaults: si no se especifica Capacity o Rate, se derivan de Limit y Window
	if cfg.Capacity == 0 {
		cfg.Capacity = cfg.Limit
	}
	if cfg.Rate == 0 {
		cfg.Rate = float64(cfg.Limit) / cfg.Window.Seconds()
	}
	return &TokenBucket{
		config:  cfg,
		entries: make(map[string]*tokenBucketEntry),
	}
}

func (t *TokenBucket) Type() string { return "token_bucket" }

func (t *TokenBucket) Allow(_ context.Context, key string) (RateLimitInfo, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now()
	entry, exists := t.entries[key]

	// Cliente nuevo: balde lleno
	if !exists {
		entry = &tokenBucketEntry{
			tokens:     float64(t.config.Capacity),
			lastRefill: now,
		}
		t.entries[key] = entry
	}

	// Lazy refill: calculamos los tokens ganados desde el último acceso.
	// Esto evita un goroutine de background recargando constantemente.
	elapsed := now.Sub(entry.lastRefill).Seconds()
	gained := elapsed * t.config.Rate
	entry.tokens = min(float64(t.config.Capacity), entry.tokens+gained)
	entry.lastRefill = now

	if entry.tokens < 1 {
		// Tiempo hasta que haya al menos 1 token disponible
		waitSeconds := (1 - entry.tokens) / t.config.Rate
		return RateLimitInfo{
			Allowed:    false,
			Limit:      t.config.Capacity,
			Remaining:  0,
			RetryAfter: time.Duration(waitSeconds * float64(time.Second)),
		}, nil
	}

	entry.tokens--

	return RateLimitInfo{
		Allowed:   true,
		Limit:     t.config.Capacity,
		Remaining: int(entry.tokens),
	}, nil
}

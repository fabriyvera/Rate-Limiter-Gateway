package limiter
import (
	"context"
	"sync"
	"time"
)

// SlidingWindowLog implementa el algoritmo Sliding Window Log.
//
// Guarda el timestamp exacto de cada request en un slice por cliente.
// Para cada nueva request, descarta los timestamps fuera de la ventana
// y cuenta cuántos quedan. Si supera el límite, bloquea.
//
// Ventaja: precisión perfecta — no hay efecto de borde.
// Desventaja: memoria proporcional al límite × número de clientes.
//   Con 1000 req/min y 1M de clientes activos → ~1B timestamps en memoria.
//
// Recomendado solo para sistemas con pocos clientes privilegiados
// donde la precisión supera el costo de memoria (ej. APIs internas, partners).
type SlidingWindowLog struct {
	config Config
	mu     sync.Mutex
	logs   map[string][]time.Time
}

func NewSlidingWindowLog(cfg Config) *SlidingWindowLog {
	return &SlidingWindowLog{
		config: cfg,
		logs:   make(map[string][]time.Time),
	}
}

func (s *SlidingWindowLog) Type() string { return "sliding_window_log" }

func (s *SlidingWindowLog) Allow(_ context.Context, key string) (RateLimitInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	windowStart := now.Add(-s.config.Window)

	// Filtrar timestamps fuera de la ventana deslizante
	log := s.logs[key]
	valid := log[:0] // reusar el slice subyacente para evitar allocations
	for _, t := range log {
		if t.After(windowStart) {
			valid = append(valid, t)
		}
	}

	if len(valid) >= s.config.Limit {
		// El cliente podrá volver cuando el timestamp más antiguo salga de la ventana
		oldestInWindow := valid[0]
		retryAfter := s.config.Window - now.Sub(oldestInWindow)

		s.logs[key] = valid
		return RateLimitInfo{
			Allowed:    false,
			Limit:      s.config.Limit,
			Remaining:  0,
			RetryAfter: retryAfter,
		}, nil
	}

	// Agregar el timestamp de la request actual
	valid = append(valid, now)
	s.logs[key] = valid

	return RateLimitInfo{
		Allowed:   true,
		Limit:     s.config.Limit,
		Remaining: s.config.Limit - len(valid),
	}, nil
}

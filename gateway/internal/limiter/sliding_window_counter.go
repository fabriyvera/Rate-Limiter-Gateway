package limiter

import (
	"context"
	"sync"
	"time"
)

// windowCounterEntry guarda los contadores de la ventana actual y la anterior.
type windowCounterEntry struct {
	prevCount   int       // contador de la ventana anterior
	currCount   int       // contador de la ventana actual
	windowStart time.Time // inicio de la ventana actual
}

// SlidingWindowCounter implementa el algoritmo Sliding Window Counter.
//
// Aproxima el comportamiento de SlidingWindowLog con memoria O(1) por cliente.
// En lugar de guardar timestamps, mantiene dos contadores:
//   - currCount: requests en la ventana actual
//   - prevCount: requests en la ventana anterior
//
// La estimación del conteo deslizante es:
//   estimado = prevCount × (1 - fracción_transcurrida) + currCount
//
// Ejemplo con límite=100/min, ventana actual con 40s transcurridos (fracción=0.67):
//   estimado = prevCount×0.33 + currCount
//
// El error máximo de estimación es <10% en tráfico real uniforme.
// Es el algoritmo que usa Cloudflare a escala global.
type SlidingWindowCounter struct {
	config  Config
	mu      sync.Mutex
	entries map[string]*windowCounterEntry
}

func NewSlidingWindowCounter(cfg Config) *SlidingWindowCounter {
	return &SlidingWindowCounter{
		config:  cfg,
		entries: make(map[string]*windowCounterEntry),
	}
}

func (s *SlidingWindowCounter) Type() string { return "sliding_window_counter" }

func (s *SlidingWindowCounter) Allow(_ context.Context, key string) (RateLimitInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	entry, exists := s.entries[key]

	if !exists {
		entry = &windowCounterEntry{
			prevCount:   0,
			currCount:   0,
			windowStart: now.Truncate(s.config.Window),
		}
		s.entries[key] = entry
	}

	elapsed := now.Sub(entry.windowStart)

	// Rotar ventana si corresponde
	if elapsed >= s.config.Window {
		if elapsed >= 2*s.config.Window {
			// Han pasado 2+ ventanas desde el último request: prev queda en 0
			entry.prevCount = 0
		} else {
			entry.prevCount = entry.currCount
		}
		entry.currCount = 0
		entry.windowStart = now.Truncate(s.config.Window)
		elapsed = now.Sub(entry.windowStart)
	}

	// Fracción de la ventana actual ya transcurrida (0.0 → 1.0)
	fraction := elapsed.Seconds() / s.config.Window.Seconds()

	// Estimación deslizante: peso del contador anterior decrece con el tiempo
	estimated := float64(entry.prevCount)*(1-fraction) + float64(entry.currCount)

	if int(estimated) >= s.config.Limit {
		// Tiempo aproximado hasta que la estimación baje del límite
		retryAfter := time.Duration((1 - fraction) * float64(s.config.Window))
		return RateLimitInfo{
			Allowed:    false,
			Limit:      s.config.Limit,
			Remaining:  0,
			RetryAfter: retryAfter,
		}, nil
	}

	entry.currCount++
	remaining := s.config.Limit - int(estimated) - 1
	if remaining < 0 {
		remaining = 0
	}

	return RateLimitInfo{
		Allowed:   true,
		Limit:     s.config.Limit,
		Remaining: remaining,
	}, nil
}

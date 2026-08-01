package limiter

import (
	"context"
	"sync"
	"time"
)

// fixedWindowEntry guarda el estado de un cliente en la ventana actual.
type fixedWindowEntry struct {
	count     int
	windowEnd time.Time
}

// FixedWindow implementa el algoritmo Fixed Window Counter.
//
// Divide el tiempo en ventanas fijas (ej. cada 60s) y cuenta requests
// por ventana. Al llegar al límite bloquea hasta la siguiente ventana.
//
// PROBLEMA CONOCIDO — burst en el borde:
// Un cliente puede hacer 2x el límite en un instante si envía N requests
// al final de una ventana y N más al inicio de la siguiente.
// Se implementa aquí como referencia comparativa, no para producción.
type FixedWindow struct {
	config  Config
	mu      sync.Mutex
	entries map[string]*fixedWindowEntry
}

func NewFixedWindow(cfg Config) *FixedWindow {
	return &FixedWindow{
		config:  cfg,
		entries: make(map[string]*fixedWindowEntry),
	}
}

func (f *FixedWindow) Type() string { return "fixed_window" }

func (f *FixedWindow) Allow(_ context.Context, key string) (RateLimitInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	now := time.Now()
	entry, exists := f.entries[key]

	// Si no existe o la ventana expiró, iniciar una nueva ventana
	if !exists || now.After(entry.windowEnd) {
		entry = &fixedWindowEntry{
			count:     0,
			windowEnd: now.Add(f.config.Window),
		}
		f.entries[key] = entry
	}

	if entry.count >= f.config.Limit {
		return RateLimitInfo{
			Allowed:    false,
			Limit:      f.config.Limit,
			Remaining:  0,
			ResetAt:    entry.windowEnd,
			RetryAfter: time.Until(entry.windowEnd),
		}, nil
	}

	entry.count++

	return RateLimitInfo{
		Allowed:   true,
		Limit:     f.config.Limit,
		Remaining: f.config.Limit - entry.count,
		ResetAt:   entry.windowEnd,
	}, nil
}

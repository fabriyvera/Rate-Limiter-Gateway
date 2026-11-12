package limiter

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"
)

var ErrCapacity = errors.New("limiter client capacity reached")

type entry struct {
	start           time.Time
	last            time.Time
	expires         time.Time
	count, previous int
	tokens          float64
	log             []time.Time
}

// Memory serializes decisions and bounds client cardinality. It is local to one process.
type Memory struct {
	mu        sync.Mutex
	cfg       Config
	algorithm string
	entries   map[string]*entry
	now       func() time.Time
	nextSweep time.Time
}

// New validates configuration before allocating state.
func New(algorithm string, cfg Config) (*Memory, error) {
	switch algorithm {
	case "fixed_window", "token_bucket", "sliding_window_log", "sliding_window_counter":
	default:
		return nil, fmt.Errorf("unknown algorithm %q", algorithm)
	}
	if cfg.Limit <= 0 || cfg.Limit > 1000000 || cfg.Window < time.Millisecond || cfg.Window > 24*time.Hour {
		return nil, errors.New("limit must be 1..1000000 and window 1ms..24h")
	}
	if cfg.Capacity == 0 {
		cfg.Capacity = cfg.Limit
	}
	if cfg.Rate == 0 {
		cfg.Rate = float64(cfg.Limit) / cfg.Window.Seconds()
	}
	if cfg.Capacity < 1 || cfg.Capacity > 1000000 || cfg.Rate <= 0 || math.IsNaN(cfg.Rate) || math.IsInf(cfg.Rate, 0) || float64(cfg.Capacity)/cfg.Rate > (24*time.Hour).Seconds() {
		return nil, errors.New("invalid capacity/refill rate (full refill must take at most 24h)")
	}
	if cfg.MaxKeys == 0 {
		cfg.MaxKeys = 10000
	}
	if cfg.MaxKeys < 1 {
		return nil, errors.New("max keys must be positive")
	}
	return &Memory{cfg: cfg, algorithm: algorithm, entries: make(map[string]*entry), now: time.Now}, nil
}
func mustNew(a string, c Config) *Memory {
	m, e := New(a, c)
	if e != nil {
		panic(e)
	}
	return m
}
func (m *Memory) Type() string { return m.algorithm }
func (m *Memory) Allow(ctx context.Context, key string) (RateLimitInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return RateLimitInfo{}, err
	}
	now := m.now()
	w := m.cfg.Window
	if !now.Before(m.nextSweep) {
		for k, e := range m.entries {
			if !now.Before(e.expires) {
				delete(m.entries, k)
			}
		}
		m.nextSweep = now.Add(time.Second)
	}
	e := m.entries[key]
	if e != nil && !now.Before(e.expires) {
		delete(m.entries, key)
		e = nil
	}
	if e == nil {
		if len(m.entries) >= m.cfg.MaxKeys {
			return RateLimitInfo{}, ErrCapacity
		}
		e = &entry{start: now.Truncate(w), last: now, tokens: float64(m.cfg.Capacity)}
		m.entries[key] = e
	}
	out := RateLimitInfo{Limit: m.cfg.Limit}
	switch m.algorithm {
	case "fixed_window":
		start := now.Truncate(w)
		if !start.Equal(e.start) {
			e.start = start
			e.count = 0
		}
		e.expires = e.start.Add(w)
		out.ResetAt = e.expires
		out.Allowed = e.count < m.cfg.Limit
		if out.Allowed {
			e.count++
		} else {
			out.RetryAfter = e.expires.Sub(now)
		}
		out.Remaining = m.cfg.Limit - e.count
	case "token_bucket":
		elapsed := math.Max(0, now.Sub(e.last).Seconds())
		e.tokens = math.Min(float64(m.cfg.Capacity), e.tokens+elapsed*m.cfg.Rate)
		e.last = now
		out.Limit = m.cfg.Capacity
		out.Allowed = e.tokens >= 1
		if out.Allowed {
			e.tokens--
		} else {
			out.RetryAfter = time.Duration(math.Ceil((1 - e.tokens) / m.cfg.Rate * float64(time.Second)))
		}
		out.Remaining = int(e.tokens)
		e.expires = now.Add(time.Duration(math.Ceil((float64(m.cfg.Capacity) - e.tokens) / m.cfg.Rate * float64(time.Second))))
		out.ResetAt = e.expires
	case "sliding_window_log":
		valid := e.log[:0]
		for _, stamp := range e.log {
			if stamp.After(now.Add(-w)) {
				valid = append(valid, stamp)
			}
		}
		e.log = valid
		out.Allowed = len(e.log) < m.cfg.Limit
		if out.Allowed {
			e.log = append(e.log, now)
		} else {
			out.RetryAfter = e.log[0].Add(w).Sub(now)
		}
		out.Remaining = m.cfg.Limit - len(e.log)
		e.expires = e.log[len(e.log)-1].Add(w)
		out.ResetAt = e.expires
	case "sliding_window_counter":
		start := now.Truncate(w)
		if !start.Equal(e.start) {
			e.previous = 0
			if start.Sub(e.start) == w {
				e.previous = e.count
			}
			e.count = 0
			e.start = start
		}
		fraction := now.Sub(e.start).Seconds() / w.Seconds()
		estimated := float64(e.previous)*(1-fraction) + float64(e.count)
		out.Allowed = estimated+1 <= float64(m.cfg.Limit)
		if out.Allowed {
			e.count++
			estimated++
		} else {
			// Solve when there is room for a complete request, possibly in the next window.
			seconds := w.Seconds() * (1 - fraction)
			if e.previous > 0 && e.count < m.cfg.Limit {
				seconds = (estimated + 1 - float64(m.cfg.Limit)) / float64(e.previous) * w.Seconds()
			} else if e.count > 0 {
				seconds += w.Seconds() / float64(e.count)
			}
			out.RetryAfter = time.Duration(math.Ceil(seconds * float64(time.Second)))
		}
		out.Remaining = max(0, int(math.Floor(float64(m.cfg.Limit)-estimated)))
		e.expires = e.start.Add(2 * w)
		out.ResetAt = e.expires
	}
	return out, nil
}

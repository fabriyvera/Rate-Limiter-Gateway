package limiter

// SlidingWindowCounter uses the shared bounded in-memory engine.
type SlidingWindowCounter struct{ *Memory }

// NewSlidingWindowCounter panics on invalid configuration. Use New for error handling.
func NewSlidingWindowCounter(cfg Config) *SlidingWindowCounter {
	return &SlidingWindowCounter{mustNew("sliding_window_counter", cfg)}
}

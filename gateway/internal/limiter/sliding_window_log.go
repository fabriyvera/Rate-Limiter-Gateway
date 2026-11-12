package limiter

// SlidingWindowLog uses the shared bounded in-memory engine.
type SlidingWindowLog struct{ *Memory }

// NewSlidingWindowLog panics on invalid configuration. Use New for error handling.
func NewSlidingWindowLog(cfg Config) *SlidingWindowLog {
	return &SlidingWindowLog{mustNew("sliding_window_log", cfg)}
}

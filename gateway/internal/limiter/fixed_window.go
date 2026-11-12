package limiter

// FixedWindow uses the shared bounded in-memory engine.
type FixedWindow struct{ *Memory }

// NewFixedWindow panics on invalid configuration. Use New for error handling.
func NewFixedWindow(cfg Config) *FixedWindow { return &FixedWindow{mustNew("fixed_window", cfg)} }

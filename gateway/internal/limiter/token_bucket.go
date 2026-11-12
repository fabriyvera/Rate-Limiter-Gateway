package limiter

// TokenBucket uses the shared bounded in-memory engine.
type TokenBucket struct{ *Memory }

// NewTokenBucket panics on invalid configuration. Use New for error handling.
func NewTokenBucket(cfg Config) *TokenBucket { return &TokenBucket{mustNew("token_bucket", cfg)} }

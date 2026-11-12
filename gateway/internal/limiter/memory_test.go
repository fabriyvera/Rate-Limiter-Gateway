package limiter

import (
	"context"
	"errors"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var algorithms = []string{"fixed_window", "token_bucket", "sliding_window_log", "sliding_window_counter"}

func TestConcurrentQuota(t *testing.T) {
	for _, a := range algorithms {
		t.Run(a, func(t *testing.T) {
			m, _ := New(a, Config{Limit: 20, Window: time.Second})
			now := time.Unix(100, 0)
			m.now = func() time.Time { return now }
			var n atomic.Int32
			var wg sync.WaitGroup
			for i := 0; i < 200; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					v, e := m.Allow(context.Background(), "same")
					if e != nil {
						t.Error(e)
					}
					if v.Allowed {
						n.Add(1)
					}
				}()
			}
			wg.Wait()
			if n.Load() != 20 {
				t.Fatalf("allowed %d", n.Load())
			}
		})
	}
}
func TestExpiryAndCapacity(t *testing.T) {
	for _, a := range algorithms {
		t.Run(a, func(t *testing.T) {
			m, _ := New(a, Config{Limit: 1, Window: time.Second, MaxKeys: 1})
			now := time.Unix(100, 0)
			m.now = func() time.Time { return now }
			m.Allow(context.Background(), "a")
			if _, e := m.Allow(context.Background(), "b"); !errors.Is(e, ErrCapacity) {
				t.Fatal(e)
			}
			now = now.Add(2 * time.Second)
			v, e := m.Allow(context.Background(), "b")
			if e != nil || !v.Allowed {
				t.Fatal(v, e)
			}
		})
	}
}
func TestFixedBoundary(t *testing.T) {
	m, _ := New("fixed_window", Config{Limit: 2, Window: time.Second})
	now := time.Unix(100, 999000000)
	m.now = func() time.Time { return now }
	for i := 0; i < 2; i++ {
		m.Allow(context.Background(), "a")
	}
	v, _ := m.Allow(context.Background(), "a")
	if v.Allowed {
		t.Fatal("quota")
	}
	now = time.Unix(101, 0)
	v, _ = m.Allow(context.Background(), "a")
	if !v.Allowed {
		t.Fatal("exact boundary must reset")
	}
}
func TestSlidingCounterFraction(t *testing.T) {
	m, _ := New("sliding_window_counter", Config{Limit: 2, Window: time.Second})
	now := time.Unix(100, 0)
	m.now = func() time.Time { return now }
	m.Allow(context.Background(), "a")
	m.Allow(context.Background(), "a")
	now = time.Unix(101, 250000000)
	v, _ := m.Allow(context.Background(), "a")
	if v.Allowed || v.RetryAfter != 250*time.Millisecond {
		t.Fatal(v)
	}
	now = now.Add(v.RetryAfter)
	v, _ = m.Allow(context.Background(), "a")
	if !v.Allowed {
		t.Fatal(v)
	}
}
func TestLogAndBucketRecovery(t *testing.T) {
	for _, a := range []string{"sliding_window_log", "token_bucket"} {
		t.Run(a, func(t *testing.T) {
			m, _ := New(a, Config{Limit: 2, Window: time.Second})
			now := time.Unix(100, 0)
			m.now = func() time.Time { return now }
			m.Allow(context.Background(), "a")
			m.Allow(context.Background(), "a")
			now = now.Add(500 * time.Millisecond)
			v, _ := m.Allow(context.Background(), "a")
			if v.Allowed != (a == "token_bucket") {
				t.Fatal(v)
			}
			now = time.Unix(101, 0)
			v, _ = m.Allow(context.Background(), "a")
			if !v.Allowed {
				t.Fatal(v)
			}
		})
	}
}
func TestValidationAndCancellation(t *testing.T) {
	for _, c := range []Config{{}, {Limit: 1, Window: -1}, {Limit: 1, Window: time.Second, Rate: math.NaN()}, {Limit: 1, Window: time.Second, MaxKeys: -1}} {
		if _, e := New("token_bucket", c); e == nil {
			t.Fatal(c)
		}
	}
	if _, e := New("unknown", Config{Limit: 1, Window: time.Second}); e == nil {
		t.Fatal("algorithm")
	}
	m, _ := New("fixed_window", Config{Limit: 1, Window: time.Second})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := m.Allow(ctx, "a"); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}

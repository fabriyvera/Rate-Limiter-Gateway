package proxy

import (
	"github.com/fabriyvera/Rate-Limiter-Gateway/internal/limiter"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestProxyFlow(t *testing.T) {
	var calls atomic.Int32
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/api" || r.URL.RawQuery != "q=1" {
			t.Error(r.URL)
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != "payload" {
			t.Error(string(body))
		}
		w.WriteHeader(201)
	}))
	defer b.Close()
	l, _ := limiter.New("token_bucket", limiter.Config{Limit: 1, Window: time.Hour})
	g, _ := New(b.URL, l, "X-Test-Client", time.Second)
	for i, want := range []int{201, 429, 201} {
		r := httptest.NewRequest("POST", "http://gateway/api?q=1", strings.NewReader("payload"))
		r.Header.Set("X-Test-Client", "a")
		if i == 2 {
			r.Header.Set("X-Test-Client", "b")
		}
		w := httptest.NewRecorder()
		g.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatal(w.Code, w.Body.String())
		}
		if want == 429 && w.Header().Get("Retry-After") == "" {
			t.Fatal("retry header")
		}
	}
	if calls.Load() != 2 {
		t.Fatal(calls.Load())
	}
	w := httptest.NewRecorder()
	g.ServeHTTP(w, httptest.NewRequest("GET", "/healthz", nil))
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
}
func TestIdentityAndFailure(t *testing.T) {
	g, _ := New("http://127.0.0.1:1", nil, "X-Test-Client", 50*time.Millisecond)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api", nil)
	g.ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatal(w.Code)
	}
	r.Header.Set("X-Test-Client", "a")
	w = httptest.NewRecorder()
	g.ServeHTTP(w, r)
	if w.Code != 502 {
		t.Fatal(w.Code)
	}
	if _, e := New("file:///tmp", nil, "", time.Second); e == nil {
		t.Fatal("target")
	}
}

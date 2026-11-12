// Package proxy connects the HTTP transport to the limiter without algorithm-specific logic.
package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/fabriyvera/Rate-Limiter-Gateway/internal/limiter"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"sync/atomic"
	"time"
)

type Gateway struct {
	limiter                                        limiter.Limiter
	proxy                                          *httputil.ReverseProxy
	clientHeader                                   string
	timeout                                        time.Duration
	allowed, rejected, unavailable, upstreamErrors atomic.Uint64
}

func New(target string, l limiter.Limiter, clientHeader string, timeout time.Duration) (*Gateway, error) {
	u, err := url.Parse(target)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Fragment != "" {
		return nil, fmt.Errorf("invalid HTTP upstream")
	}
	if timeout <= 0 {
		return nil, fmt.Errorf("timeout must be positive")
	}
	g := &Gateway{limiter: l, clientHeader: clientHeader, timeout: timeout}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.MaxIdleConns = 200
	tr.MaxIdleConnsPerHost = 100
	tr.ResponseHeaderTimeout = timeout
	g.proxy = &httputil.ReverseProxy{Transport: tr, Rewrite: func(r *httputil.ProxyRequest) { r.SetURL(u) }, ErrorHandler: func(w http.ResponseWriter, r *http.Request, e error) {
		g.upstreamErrors.Add(1)
		slog.Warn("upstream request failed", "error", e)
		http.Error(w, "upstream unavailable", http.StatusBadGateway)
	}}
	return g, nil
}
func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
		return
	}
	if r.URL.Path == "/metrics" {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]uint64{"allowed": g.allowed.Load(), "rejected": g.rejected.Load(), "unavailable": g.unavailable.Load(), "upstream_errors": g.upstreamErrors.Load()})
		return
	}
	key, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		key = r.RemoteAddr
	}
	if g.clientHeader != "" {
		key = r.Header.Get(g.clientHeader)
		if key == "" || len(key) > 128 {
			http.Error(w, "missing or oversized client identity", http.StatusBadRequest)
			return
		}
	}
	if g.limiter != nil {
		info, err := g.limiter.Allow(r.Context(), key)
		if err != nil {
			g.unavailable.Add(1)
			http.Error(w, "limiter unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("X-RateLimit-Limit", strconv.Itoa(info.Limit))
		w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(info.Remaining))
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(info.ResetAt.Unix(), 10))
		if !info.Allowed {
			g.rejected.Add(1)
			w.Header().Set("Retry-After", strconv.Itoa(max(1, int(math.Ceil(info.RetryAfter.Seconds())))))
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
	}
	g.allowed.Add(1)
	ctx, cancel := context.WithTimeout(r.Context(), g.timeout)
	defer cancel()
	g.proxy.ServeHTTP(w, r.WithContext(ctx))
}

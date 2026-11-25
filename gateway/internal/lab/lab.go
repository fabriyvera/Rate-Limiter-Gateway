// Package lab provides a loopback-only control panel for local experiments.
package lab

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fabriyvera/Rate-Limiter-Gateway/internal/limiter"
	"github.com/fabriyvera/Rate-Limiter-Gateway/internal/proxy"
)

//go:embed web/* scenario.js
var assets embed.FS

type Config struct {
	Algorithm string  `json:"algorithm"`
	Limit     int     `json:"limit"`
	WindowMS  int     `json:"window_ms"`
	Capacity  int     `json:"capacity"`
	Rate      float64 `json:"rate"`
}
type Lab struct {
	mu                     sync.Mutex
	current                atomic.Pointer[proxy.Gateway]
	config                 Config
	target, gatewayURL, k6 string
	client                 *http.Client
	runs                   []*Run
	active                 *Run
}

func New(target, gatewayURL string) (*Lab, error) {
	binary, _ := exec.LookPath("k6")
	l := &Lab{target: target, gatewayURL: gatewayURL, k6: binary, client: &http.Client{Timeout: 6 * time.Second}, runs: []*Run{}}
	if err := l.apply(Config{Algorithm: "token_bucket", Limit: 100, WindowMS: 1000, Capacity: 100, Rate: 100}); err != nil {
		return nil, err
	}
	return l, nil
}
func (l *Lab) apply(c Config) error {
	if c.WindowMS < 1 || c.WindowMS > 3600000 || c.Limit < 1 || c.Limit > 100000 || c.Capacity < 0 || c.Capacity > 100000 || c.Rate < 0 || c.Rate > 100000 {
		return fmt.Errorf("configuración fuera del rango permitido")
	}
	var implementation limiter.Limiter
	if c.Algorithm != "none" {
		m, err := limiter.New(c.Algorithm, limiter.Config{Limit: c.Limit, Window: time.Duration(c.WindowMS) * time.Millisecond, Capacity: c.Capacity, Rate: c.Rate, MaxKeys: 10000})
		if err != nil {
			return err
		}
		implementation = m
	}
	g, err := proxy.New(l.target, implementation, "X-Test-Client", 5*time.Second)
	if err != nil {
		return err
	}
	if previous := l.current.Swap(g); previous != nil {
		previous.CloseIdleConnections()
	}
	l.config = c
	return nil
}
func (l *Lab) Gateway(w http.ResponseWriter, r *http.Request) { l.current.Load().ServeHTTP(w, r) }
func respond(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func failure(w http.ResponseWriter, status int, msg string) {
	respond(w, status, map[string]string{"error": msg})
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		failure(w, 415, "Se requiere application/json")
		return false
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		failure(w, 400, "JSON inválido")
		return false
	}
	if d.Decode(&struct{}{}) != io.EOF {
		failure(w, 400, "Contenido adicional no permitido")
		return false
	}
	return true
}

// Host and Origin checks protect local mutation endpoints from foreign web pages.
func guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}
		if host != "localhost" && host != "127.0.0.1" && host != "::1" {
			http.Error(w, "loopback host required", 403)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+r.Host {
			http.Error(w, "foreign origin", 403)
			return
		}
		if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			http.Error(w, "cross-site request", 403)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' data:; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}
func (l *Lab) Handler() http.Handler {
	mux := http.NewServeMux()
	web, _ := fs.Sub(assets, "web")
	mux.Handle("GET /", http.FileServer(http.FS(web)))
	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		l.mu.Lock()
		defer l.mu.Unlock()
		respond(w, 200, map[string]any{"config": l.config, "metrics": l.current.Load().Snapshot(), "k6_available": l.k6 != "", "gateway_url": l.gatewayURL, "runs": l.runs, "active": l.active})
	})
	mux.HandleFunc("POST /api/config", func(w http.ResponseWriter, r *http.Request) {
		var c Config
		if !decode(w, r, &c) {
			return
		}
		l.mu.Lock()
		defer l.mu.Unlock()
		if l.active != nil {
			failure(w, 409, "Detén la prueba antes de cambiar la configuración")
			return
		}
		if err := l.apply(c); err != nil {
			failure(w, 400, err.Error())
			return
		}
		respond(w, 200, c)
	})
	mux.HandleFunc("POST /api/probe", l.probe)
	mux.HandleFunc("POST /api/runs", l.startRun)
	mux.HandleFunc("POST /api/stop", func(w http.ResponseWriter, r *http.Request) {
		l.mu.Lock()
		defer l.mu.Unlock()
		if l.active != nil {
			l.active.cancel()
			l.active.Status = "stopping"
		}
		respond(w, 200, map[string]bool{"ok": true})
	})
	return guard(mux)
}
func (l *Lab) probe(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Client string `json:"client"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Client == "" || len(in.Client) > 128 {
		failure(w, 400, "Identidad: entre 1 y 128 caracteres")
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.active != nil {
		failure(w, 409, "Prueba k6 en curso; las solicitudes manuales están bloqueadas")
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), "GET", l.gatewayURL+"/api/test", nil)
	if err != nil {
		failure(w, 500, err.Error())
		return
	}
	req.Header.Set("X-Test-Client", in.Client)
	start := time.Now()
	res, err := l.client.Do(req)
	if err != nil {
		failure(w, 502, "El gateway no responde")
		return
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 2048))
	respond(w, 200, map[string]any{"status": res.StatusCode, "duration_ms": float64(time.Since(start).Microseconds()) / 1000, "remaining": res.Header.Get("X-RateLimit-Remaining"), "retry_after": res.Header.Get("Retry-After"), "body": string(body)})
}
func (l *Lab) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.active != nil {
		l.active.cancel()
	}
	l.client.CloseIdleConnections()
	l.current.Load().CloseIdleConnections()
}

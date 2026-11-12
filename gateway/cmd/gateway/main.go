package main

import (
	"context"
	"errors"
	"flag"
	"github.com/fabriyvera/Rate-Limiter-Gateway/internal/limiter"
	"github.com/fabriyvera/Rate-Limiter-Gateway/internal/proxy"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	if err := run(); err != nil {
		slog.Error("gateway stopped", "error", err)
		os.Exit(1)
	}
}
func run() error {
	addr := flag.String("addr", "127.0.0.1:8080", "listen address")
	upstream := flag.String("upstream", "http://127.0.0.1:9090", "upstream HTTP URL")
	algorithm := flag.String("algorithm", "token_bucket", "fixed_window | token_bucket | sliding_window_log | sliding_window_counter | none (baseline)")
	header := flag.String("client-header", "", "lab-only client identity header; default uses peer IP")
	limit := flag.Int("limit", 100, "requests per window")
	window := flag.Duration("window", time.Second, "window duration")
	capacity := flag.Int("capacity", 0, "token bucket capacity; zero uses limit")
	rate := flag.Float64("rate", 0, "tokens/second; zero uses limit/window")
	keys := flag.Int("max-keys", 10000, "maximum active client identities")
	timeout := flag.Duration("upstream-timeout", 10*time.Second, "total upstream request timeout")
	flag.Parse()
	var l limiter.Limiter
	if *algorithm != "none" {
		m, e := limiter.New(*algorithm, limiter.Config{Limit: *limit, Window: *window, Capacity: *capacity, Rate: *rate, MaxKeys: *keys})
		if e != nil {
			return e
		}
		l = m
	}
	g, e := proxy.New(*upstream, l, *header, *timeout)
	if e != nil {
		return e
	}
	srv := &http.Server{Addr: *addr, Handler: g, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: *timeout + 5*time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	result := make(chan error, 1)
	go func() { result <- srv.ListenAndServe() }()
	slog.Info("gateway starting", "address", *addr, "algorithm", *algorithm, "storage", "memory")
	select {
	case err := <-result:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdown)
	}
}

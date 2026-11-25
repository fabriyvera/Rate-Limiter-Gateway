// lab starts the embedded dashboard, a demo backend and a real gateway.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"github.com/fabriyvera/Rate-Limiter-Gateway/internal/lab"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	port := flag.Int("port", 7070, "dashboard port on 127.0.0.1")
	gatewayPort := flag.Int("gateway-port", 8080, "gateway port on 127.0.0.1")
	upstream := flag.String("upstream", "", "optional backend; default starts a local demo")
	flag.Parse()
	if *port < 1 || *port > 65535 || *gatewayPort < 1 || *gatewayPort > 65535 {
		return fmt.Errorf("invalid port")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var servers []*http.Server
	defer func() {
		c, cancel := context.WithTimeout(context.Background(), 7*time.Second)
		defer cancel()
		for _, s := range servers {
			_ = s.Shutdown(c)
		}
	}()
	errorsCh := make(chan error, 3)
	start := func(addr string, h http.Handler) (string, error) {
		ln, e := net.Listen("tcp", addr)
		if e != nil {
			return "", e
		}
		s := &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 30 * time.Second}
		servers = append(servers, s)
		go func() {
			e := s.Serve(ln)
			if !errors.Is(e, http.ErrServerClosed) {
				errorsCh <- e
			}
		}()
		return "http://" + ln.Addr().String(), nil
	}
	if *upstream == "" {
		u, e := start("127.0.0.1:0", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true,"service":"demo"}`))
		}))
		if e != nil {
			return e
		}
		*upstream = u
	}
	gatewayURL := fmt.Sprintf("http://127.0.0.1:%d", *gatewayPort)
	l, e := lab.New(*upstream, gatewayURL)
	if e != nil {
		return e
	}
	defer l.Close()
	if _, e = start(fmt.Sprintf("127.0.0.1:%d", *gatewayPort), http.HandlerFunc(l.Gateway)); e != nil {
		return e
	}
	url, e := start(fmt.Sprintf("127.0.0.1:%d", *port), l.Handler())
	if e != nil {
		return e
	}
	fmt.Println("Rate Lab:", url, "| Gateway:", gatewayURL)
	select {
	case <-ctx.Done():
		return nil
	case e := <-errorsCh:
		return e
	}
}

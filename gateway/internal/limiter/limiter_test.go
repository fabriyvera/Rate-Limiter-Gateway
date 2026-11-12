package limiter_test

import (
	"context"
	"testing"
	"time"

	"github.com/fabriyvera/Rate-Limiter-Gateway/internal/limiter"
)

// newLimiters devuelve los 4 algoritmos con la misma config para tests comparativos.
func newLimiters(cfg limiter.Config) []limiter.Limiter {
	return []limiter.Limiter{
		limiter.NewFixedWindow(cfg),
		limiter.NewTokenBucket(cfg),
		limiter.NewSlidingWindowLog(cfg),
		limiter.NewSlidingWindowCounter(cfg),
	}
}

// ─── Tests comunes a todos los algoritmos ────────────────────────────────────

// TestAllow_UnderLimit verifica que requests dentro del límite son permitidas.
func TestAllow_UnderLimit(t *testing.T) {
	cfg := limiter.Config{Limit: 5, Window: time.Minute}
	ctx := context.Background()

	for _, l := range newLimiters(cfg) {
		t.Run(l.Type(), func(t *testing.T) {
			for i := 0; i < 5; i++ {
				info, err := l.Allow(ctx, "client-1")
				if err != nil {
					t.Fatalf("error inesperado: %v", err)
				}
				if !info.Allowed {
					t.Errorf("request %d debería estar permitida", i+1)
				}
			}
		})
	}
}

// TestAllow_AtLimit verifica que la request exactamente en el límite es bloqueada.
func TestAllow_AtLimit(t *testing.T) {
	cfg := limiter.Config{Limit: 3, Window: time.Minute}
	ctx := context.Background()

	for _, l := range newLimiters(cfg) {
		t.Run(l.Type(), func(t *testing.T) {
			// Consumir el límite
			for i := 0; i < 3; i++ {
				l.Allow(ctx, "client-1") //nolint:errcheck
			}

			// La siguiente debe ser bloqueada
			info, err := l.Allow(ctx, "client-1")
			if err != nil {
				t.Fatalf("error inesperado: %v", err)
			}
			if info.Allowed {
				t.Errorf("request debería estar bloqueada al superar el límite")
			}
			if info.RetryAfter <= 0 {
				t.Errorf("RetryAfter debería ser > 0 cuando está bloqueado, got: %v", info.RetryAfter)
			}
		})
	}
}

// TestAllow_DifferentKeys verifica que clientes diferentes tienen límites independientes.
func TestAllow_DifferentKeys(t *testing.T) {
	cfg := limiter.Config{Limit: 2, Window: time.Minute}
	ctx := context.Background()

	for _, l := range newLimiters(cfg) {
		t.Run(l.Type(), func(t *testing.T) {
			// Agotar límite del cliente A
			l.Allow(ctx, "client-A") //nolint:errcheck
			l.Allow(ctx, "client-A") //nolint:errcheck

			// cliente-A debe estar bloqueado
			infoA, _ := l.Allow(ctx, "client-A")
			if infoA.Allowed {
				t.Error("client-A debería estar bloqueado")
			}

			// cliente-B no debe estar bloqueado
			infoB, _ := l.Allow(ctx, "client-B")
			if !infoB.Allowed {
				t.Error("client-B no debería estar afectado por el límite de client-A")
			}
		})
	}
}

// TestAllow_LimitFieldCorrect verifica que el campo Limit en RateLimitInfo es correcto.
func TestAllow_LimitFieldCorrect(t *testing.T) {
	cfg := limiter.Config{Limit: 10, Window: time.Minute}
	ctx := context.Background()

	for _, l := range newLimiters(cfg) {
		t.Run(l.Type(), func(t *testing.T) {
			info, _ := l.Allow(ctx, "client-1")
			if info.Limit != 10 {
				t.Errorf("esperaba Limit=10, got %d", info.Limit)
			}
		})
	}
}

// TestAllow_RemainingDecreases verifica que Remaining disminuye con cada request.
func TestAllow_RemainingDecreases(t *testing.T) {
	cfg := limiter.Config{Limit: 5, Window: time.Minute}
	ctx := context.Background()

	for _, l := range newLimiters(cfg) {
		t.Run(l.Type(), func(t *testing.T) {
			prev := cfg.Limit
			for i := 0; i < 4; i++ {
				info, _ := l.Allow(ctx, "client-1")
				if !info.Allowed {
					t.Fatalf("request %d inesperadamente bloqueada", i+1)
				}
				if info.Remaining >= prev {
					t.Errorf("Remaining debería decrecer: antes=%d, ahora=%d", prev, info.Remaining)
				}
				prev = info.Remaining
			}
		})
	}
}

// ─── Tests específicos por algoritmo ─────────────────────────────────────────

// TestTokenBucket_AllowsBurst verifica que Token Bucket permite ráfagas
// cuando el cliente estuvo inactivo y acumuló tokens.
func TestTokenBucket_AllowsBurst(t *testing.T) {
	cfg := limiter.Config{
		Limit:    5,
		Window:   time.Second,
		Capacity: 5,
		Rate:     5,
	}
	ctx := context.Background()
	l := limiter.NewTokenBucket(cfg)

	// Simular cliente inactivo: crear con balde lleno (estado inicial)
	// y hacer 5 requests seguidas (ráfaga completa)
	for i := 0; i < 5; i++ {
		info, err := l.Allow(ctx, "burst-client")
		if err != nil {
			t.Fatalf("error en request %d: %v", i+1, err)
		}
		if !info.Allowed {
			t.Errorf("request %d de ráfaga debería estar permitida (balde lleno)", i+1)
		}
	}

	// La 6ta debe bloquearse: el balde está vacío
	info, _ := l.Allow(ctx, "burst-client")
	if info.Allowed {
		t.Error("request 6 debería bloquearse: balde vacío")
	}
}

// TestFixedWindow_BurstAtBoundary documenta el bug conocido de Fixed Window.
// Este test NO debería fallar — está aquí para demostrar el problema.
func TestFixedWindow_BurstAtBoundary(t *testing.T) {
	t.Log("NOTA: este test documenta el burst-at-boundary de Fixed Window.")
	t.Log("En producción esto significa que un cliente puede exceder el límite.")

	cfg := limiter.Config{Limit: 3, Window: 100 * time.Millisecond}
	ctx := context.Background()
	l := limiter.NewFixedWindow(cfg)

	// Agotar la ventana actual
	for i := 0; i < 3; i++ {
		l.Allow(ctx, "attacker") //nolint:errcheck
	}

	// Esperar a que la ventana se resetee
	time.Sleep(101 * time.Millisecond)

	// En el borde de la nueva ventana, el atacante puede hacer 3 más
	// El resultado: 6 requests en ~200ms con límite de 3/100ms
	count := 0
	for i := 0; i < 3; i++ {
		info, _ := l.Allow(ctx, "attacker")
		if info.Allowed {
			count++
		}
	}

	t.Logf("requests permitidas en la nueva ventana: %d (burst total ~%d en el borde)", count, 3+count)
}

// TestSlidingWindowLog_ExactPrecision verifica que no hay efecto de borde.
func TestSlidingWindowLog_ExactPrecision(t *testing.T) {
	cfg := limiter.Config{Limit: 3, Window: 200 * time.Millisecond}
	ctx := context.Background()
	l := limiter.NewSlidingWindowLog(cfg)

	// Consumir el límite
	for i := 0; i < 3; i++ {
		info, _ := l.Allow(ctx, "client-1")
		if !info.Allowed {
			t.Fatalf("request %d debería estar permitida", i+1)
		}
	}

	// Esperar que la mitad de la ventana pase
	time.Sleep(100 * time.Millisecond)

	// Aún dentro de la ventana deslizante: debe bloquearse
	info, _ := l.Allow(ctx, "client-1")
	if info.Allowed {
		t.Error("no debería estar permitida: los timestamps siguen dentro de la ventana deslizante")
	}

	// Esperar que todos los timestamps expiren
	time.Sleep(110 * time.Millisecond)

	// Ahora sí debe estar permitida
	info, _ = l.Allow(ctx, "client-1")
	if !info.Allowed {
		t.Error("debería estar permitida: todos los timestamps expiraron")
	}
}

// TestType verifica que cada algoritmo reporta su tipo correctamente.
func TestType(t *testing.T) {
	cfg := limiter.Config{Limit: 10, Window: time.Minute}
	cases := []struct {
		limiter  limiter.Limiter
		expected string
	}{
		{limiter.NewFixedWindow(cfg), "fixed_window"},
		{limiter.NewTokenBucket(cfg), "token_bucket"},
		{limiter.NewSlidingWindowLog(cfg), "sliding_window_log"},
		{limiter.NewSlidingWindowCounter(cfg), "sliding_window_counter"},
	}

	for _, tc := range cases {
		if tc.limiter.Type() != tc.expected {
			t.Errorf("esperaba %q, got %q", tc.expected, tc.limiter.Type())
		}
	}
}

package lab

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T) *Lab {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"ok":true}`)) }))
	t.Cleanup(upstream.Close)
	l, err := New(upstream.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	gateway := httptest.NewServer(http.HandlerFunc(l.Gateway))
	l.gatewayURL = gateway.URL
	t.Cleanup(gateway.Close)
	t.Cleanup(l.Close)
	return l
}
func call(l *Lab, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://127.0.0.1:7070"+path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	l.Handler().ServeHTTP(w, r)
	return w
}
func TestConfigurationAndRealProbe(t *testing.T) {
	for _, algorithm := range []string{"token_bucket", "fixed_window", "sliding_window_log", "sliding_window_counter"} {
		t.Run(algorithm, func(t *testing.T) {
			l := fixture(t)
			config := `{"algorithm":"` + algorithm + `","limit":2,"window_ms":3600000,"capacity":2,"rate":0.001}`
			if w := call(l, "POST", "/api/config", config); w.Code != 200 {
				t.Fatal(w.Body.String())
			}
			for i, want := range []int{200, 200, 429} {
				w := call(l, "POST", "/api/probe", `{"client":"one"}`)
				var result struct {
					Status int    `json:"status"`
					Retry  string `json:"retry_after"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				if result.Status != want {
					t.Fatalf("probe %d: %s", i, w.Body)
				}
				if want == 429 && result.Retry == "" {
					t.Fatal("missing Retry-After")
				}
			}
			w := call(l, "POST", "/api/probe", `{"client":"two"}`)
			if !strings.Contains(w.Body.String(), `"status":200`) {
				t.Fatal(w.Body.String())
			}
			m := l.current.Load().Snapshot()
			if m["allowed"] != 3 || m["rejected"] != 1 {
				t.Fatal(m)
			}
			if w = call(l, "POST", "/api/config", config); w.Code != 200 {
				t.Fatal(w.Body.String())
			}
			if l.current.Load().Snapshot()["allowed"] != 0 {
				t.Fatal("config must reset counters")
			}
		})
	}
}
func TestControlValidation(t *testing.T) {
	l := fixture(t)
	l.k6 = ""
	for _, tc := range []struct {
		path, body string
		want       int
	}{
		{"/api/config", `{"algorithm":"missing","limit":2,"window_ms":1000}`, 400},
		{"/api/config", `{"algorithm":"fixed_window","limit":0,"window_ms":1000}`, 400},
		{"/api/config", `{"unknown":1}`, 400},
		{"/api/probe", `{"client":""}`, 400},
		{"/api/probe", `{"client":"ok"} {}`, 400},
		{"/api/runs", `{"scenario":"sustained","rps":2001,"duration":5,"clients":1}`, 400},
		{"/api/runs", `{"scenario":"sustained","rps":20,"duration":5,"clients":1}`, 503},
	} {
		if w := call(l, "POST", tc.path, tc.body); w.Code != tc.want {
			t.Fatalf("%s: %d %s", tc.path, w.Code, w.Body)
		}
	}
	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	l.active = &Run{Status: "running", cancel: cancel}
	if w := call(l, "POST", "/api/config", `{"algorithm":"fixed_window","limit":2,"window_ms":1000}`); w.Code != 409 {
		t.Fatal(w.Code)
	}
	if w := call(l, "POST", "/api/probe", `{"client":"one"}`); w.Code != 409 {
		t.Fatal(w.Code)
	}
}
func TestOriginAndAssets(t *testing.T) {
	l := fixture(t)
	for _, path := range []string{"/", "/style.css", "/app.js"} {
		if w := call(l, "GET", path, ""); w.Code != 200 || w.Body.Len() == 0 {
			t.Fatalf("asset %s: %d", path, w.Code)
		}
	}
	for _, tc := range []struct{ host, origin, site string }{{"attacker.example", "", ""}, {"127.0.0.1:7070", "https://attacker.example", ""}, {"127.0.0.1:7070", "", "cross-site"}} {
		r := httptest.NewRequest("POST", "http://"+tc.host+"/api/stop", nil)
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("Sec-Fetch-Site", tc.site)
		w := httptest.NewRecorder()
		l.Handler().ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatal(w.Code)
		}
	}
}
func TestK6Execution(t *testing.T) {
	if _, err := exec.LookPath("k6"); err != nil {
		t.Skip("k6 is not on PATH")
	}
	l := fixture(t)
	if err := l.apply(Config{Algorithm: "fixed_window", Limit: 2, WindowMS: 3600000}); err != nil {
		t.Fatal(err)
	}
	w := call(l, "POST", "/api/runs", `{"scenario":"sustained","rps":10,"duration":5,"clients":1}`)
	if w.Code != 202 {
		t.Fatal(w.Body.String())
	}
	deadline := time.Now().Add(40 * time.Second)
	for time.Now().Before(deadline) {
		l.mu.Lock()
		done := l.active == nil
		var run Run
		if done {
			run = *l.runs[0]
		}
		l.mu.Unlock()
		if done {
			if run.Status != "completed" || !json.Valid(run.Summary) {
				t.Fatalf("%s: %s", run.Status, run.Log)
			}
			var summary struct {
				Metrics map[string]struct {
					Values map[string]float64 `json:"values"`
				} `json:"metrics"`
			}
			if err := json.Unmarshal(run.Summary, &summary); err != nil {
				t.Fatal(err)
			}
			if summary.Metrics["admitted"].Values["count"] != 2 || summary.Metrics["rejected"].Values["count"] < 40 {
				t.Fatalf("unexpected counts: %s", run.Summary)
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("k6 did not finish")
}

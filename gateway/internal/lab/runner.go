package lab

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type RunOptions struct {
	Scenario string `json:"scenario"`
	RPS      int    `json:"rps"`
	Duration int    `json:"duration"`
	Clients  int    `json:"clients"`
}
type Run struct {
	ID      string          `json:"id"`
	Started time.Time       `json:"started"`
	Status  string          `json:"status"`
	Config  Config          `json:"config"`
	Options RunOptions      `json:"options"`
	Summary json.RawMessage `json:"summary,omitempty"`
	Log     string          `json:"log"`
	cancel  context.CancelFunc
}

// tailWriter bounds subprocess output while satisfying io.Writer's contract.
type tailWriter struct {
	mu sync.Mutex
	b  []byte
}

func (t *tailWriter) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	n := len(p)
	t.b = append(t.b, p...)
	if len(t.b) > 64000 {
		t.b = t.b[len(t.b)-64000:]
	}
	return n, nil
}
func (t *tailWriter) String() string { t.mu.Lock(); defer t.mu.Unlock(); return string(t.b) }
func (l *Lab) startRun(w http.ResponseWriter, r *http.Request) {
	var in RunOptions
	if !decode(w, r, &in) {
		return
	}
	if (in.Scenario != "sustained" && in.Scenario != "burst") || in.RPS < 1 || in.RPS > 2000 || in.Duration < 5 || in.Duration > 120 || in.Clients < 1 || in.Clients > 1000 {
		failure(w, 400, "Escenario inválido: RPS 1–2000, duración 5–120 s, clientes 1–1000")
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.k6 == "" {
		failure(w, 503, "k6 no está instalado. Instálalo y reinicia el laboratorio.")
		return
	}
	if l.active != nil {
		failure(w, 409, "Ya hay una prueba en curso")
		return
	}
	// Each experiment starts from the same clean state and captures an immutable config.
	if err := l.apply(l.config); err != nil {
		failure(w, 500, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(in.Duration+45)*time.Second)
	run := &Run{ID: fmt.Sprintf("%d", time.Now().UnixNano()), Started: time.Now(), Status: "running", Config: l.config, Options: in, cancel: cancel}
	l.active = run
	l.runs = append([]*Run{run}, l.runs...)
	if len(l.runs) > 10 {
		l.runs = l.runs[:10]
	}
	go l.execute(ctx, run)
	respond(w, 202, run)
}
func (l *Lab) execute(ctx context.Context, run *Run) {
	defer run.cancel()
	dir, err := os.MkdirTemp("", "rate-lab-")
	if err != nil {
		l.finish(run, "failed", nil, err.Error())
		return
	}
	defer os.RemoveAll(dir)
	script, _ := assets.ReadFile("scenario.js")
	scriptPath := filepath.Join(dir, "scenario.js")
	if err = os.WriteFile(scriptPath, script, 0600); err != nil {
		l.finish(run, "failed", nil, err.Error())
		return
	}
	outPath := filepath.Join(dir, "summary.json")
	o := run.Options
	cmd := exec.CommandContext(ctx, l.k6, "run", "--quiet", "--no-color", "--summary-trend-stats=avg,min,med,max,p(95),p(99)", scriptPath)
	// Remove inherited k6 options so UI runs remain reproducible and local-only.
	for _, e := range os.Environ() {
		key := strings.ToUpper(strings.SplitN(e, "=", 2)[0])
		if !strings.HasPrefix(key, "K6_") && key != "BASE_URL" && key != "SUMMARY_PATH" && key != "RPS" && key != "CLIENTS" && key != "DURATION" && key != "SCENARIO" {
			cmd.Env = append(cmd.Env, e)
		}
	}
	cmd.Env = append(cmd.Env, "K6_NO_USAGE_REPORT=true", "BASE_URL="+l.gatewayURL, "SUMMARY_PATH="+outPath, "RPS="+fmt.Sprint(o.RPS), "CLIENTS="+fmt.Sprint(o.Clients), "DURATION="+fmt.Sprint(o.Duration), "SCENARIO="+o.Scenario)
	logs := &tailWriter{}
	cmd.Stdout = logs
	cmd.Stderr = logs
	err = cmd.Run()
	status := "completed"
	if err != nil {
		status = "failed"
	}
	if ctx.Err() != nil {
		status = "cancelled"
	}
	data, _ := os.ReadFile(outPath)
	if !json.Valid(data) {
		data = nil
	}
	log := logs.String()
	if err != nil {
		log += "\n" + err.Error()
	}
	l.finish(run, status, data, log)
}
func (l *Lab) finish(run *Run, status string, data []byte, log string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	run.Status = status
	run.Summary = data
	run.Log = log
	if l.active == run {
		l.active = nil
	}
}

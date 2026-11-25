'use strict';
const $ = (id) => document.getElementById(id);
const names = {token_bucket: 'Token Bucket', fixed_window: 'Fixed Window', sliding_window_log: 'Sliding Log', sliding_window_counter: 'Sliding Counter'};
const descriptions = {
  token_bucket: 'Consume un token por solicitud. La capacidad permite ráfagas y la recarga determina la tasa sostenible.',
  fixed_window: 'Cuenta solicitudes en ventanas fijas. En sus bordes puede admitir dos grupos muy próximos.',
  sliding_window_log: 'Guarda los instantes de admisión para aplicar el límite exacto sobre la última ventana.',
  sliding_window_counter: 'Combina la ventana actual y una fracción de la anterior. Consume menos memoria que el registro exacto.'
};
const labels = {running: 'En ejecución', stopping: 'Deteniendo', completed: 'Completada', failed: 'Falló la validación o ejecución', cancelled: 'Cancelada'};
let state, initialized = false, busy = false, online = false, selectedID, lastSample, toastTimer, historySignature = '';
const samples = [], requests = [];
const number = (v) => Number(v || 0).toLocaleString('es-BO');
const configForm = $('config-form'), runForm = $('run-form');

async function api(path, body) {
  const response = await fetch(path, body === undefined ? {} : {method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify(body)});
  const value = await response.json();
  if (!response.ok) throw new Error(value.error || `HTTP ${response.status}`);
  return value;
}
function toast(message) {
  $('toast').textContent = message; $('toast').hidden = false;
  clearTimeout(toastTimer); toastTimer = setTimeout(() => { $('toast').hidden = true; }, 6000);
}
function controls() {
  const locked = busy || !online || !!state?.active;
  $('config-fields').disabled = locked;
  $('client-id').disabled = locked;
  $('send-one').disabled = $('send-ten').disabled = locked;
  $('run-fields').disabled = locked;
  const dirty = initialized && Object.entries(state.config).some(([key, value]) => String(configForm.elements[key].value) !== String(value));
  $('start-run').disabled = locked || !state?.k6_available || dirty;
  if (!state?.active) $('run-state').textContent = dirty ? 'Hay cambios sin aplicar. Aplica la política antes de ejecutar k6.' : 'Listo para tu próximo experimento';
  $('stop-run').disabled = busy || !online || !state?.active || state.active.status === 'stopping';
  $('export-session').disabled = !state;
}
function algorithmHelp() {
  const algorithm = configForm.elements.algorithm.value;
  $('algorithm-description').textContent = descriptions[algorithm];
  $('token-fields').hidden = algorithm !== 'token_bucket';
}
configForm.addEventListener('change', () => { algorithmHelp(); controls(); });
configForm.addEventListener('input', controls);
function resetChart() { samples.length = 0; lastSample = undefined; }
function chart(metrics) {
  const now = performance.now();
  if (lastSample) {
    const seconds = (now - lastSample.time) / 1000;
    if (metrics.allowed < lastSample.allowed || metrics.rejected < lastSample.rejected) resetChart();
    else if (seconds >= .4) {
      samples.push([(metrics.allowed - lastSample.allowed) / seconds, (metrics.rejected - lastSample.rejected) / seconds]);
      if (samples.length > 45) samples.shift();
    } else return;
  }
  lastSample = {time: now, ...metrics};
  const svg = $('traffic-chart'), ns = 'http://www.w3.org/2000/svg';
  svg.replaceChildren();
  const max = Math.max(1, ...samples.flat());
  function draw(tag, attrs) { const node = document.createElementNS(ns, tag); for (const [key, value] of Object.entries(attrs)) node.setAttribute(key, value); svg.append(node); return node; }
  for (let i = 0; i < 4; i++) {
    const y = 12 + i * 48;
    draw('line', {x1: 45, x2: 895, y1: y, y2: y, stroke: '#e8edf0', 'stroke-dasharray': '4 5'});
    draw('text', {x: 0, y: y + 4, fill: '#78868c', 'font-size': 11}).textContent = Math.round(max * (1 - i / 3));
  }
  [0, 1].forEach((series) => {
    const points = samples.map((point, i) => `${45 + (45 - samples.length + i) * 850 / 44},${156 - point[series] / max * 144}`).join(' ');
    draw('polyline', {points, fill: 'none', stroke: series ? '#d66c32' : '#087d6b', 'stroke-width': 2.5, 'stroke-linejoin': 'round'});
  });
  $('chart-empty').hidden = samples.some((s) => s[0] || s[1]);
  $('chart-rate').textContent = `${number(Math.round((samples.at(-1) || [0, 0]).reduce((a, b) => a + b, 0)))} solicitudes/s`;
}
function renderResult(run) {
  $('run-result').hidden = !run;
  if (!run) return;
  selectedID = run.id;
  const metrics = run.summary?.metrics || {};
  const value = (name, field) => metrics[name]?.values?.[field];
  const latency = value('admitted_latency', 'p(95)');
  const failureRate = value('http_req_failed', 'rate');
  const data = [
    ['Admitidas (200)', number(value('admitted', 'count'))],
    ['Limitadas (429)', number(value('rejected', 'count'))],
    ['p95 admitidas', latency === undefined ? '—' : `${latency.toFixed(2)} ms`],
    ['Errores HTTP inesperados', failureRate === undefined ? '—' : `${(failureRate * 100).toFixed(2)} %`],
    ['Iteraciones no iniciadas', number(value('dropped_iterations', 'count'))]
  ];
  $('result-metrics').replaceChildren();
  if (run.summary) for (const [label, text] of data) {
    const card = document.createElement('div');
    const small = document.createElement('small'), strong = document.createElement('strong');
    small.textContent = label; strong.textContent = text; card.append(small, strong); $('result-metrics').append(card);
  }
  $('result-note').textContent = `${labels[run.status]} · ${names[run.config.algorithm] || run.config.algorithm} · ${run.options.rps} solicitudes/s · ${run.options.clients} cliente(s). ` +
    (run.summary ? '429 significa que se aplicó la cuota; no se cuenta como fallo HTTP. p95 considera solo respuestas 200. Si hay iteraciones no iniciadas, el generador no sostuvo la carga objetivo.' : 'El resumen estará disponible cuando k6 termine. Una cancelación puede dejar resultados incompletos.');
  $('run-log').textContent = run.log || 'Sin salida disponible todavía.';
}
function history() {
  $('history-count').textContent = state.runs.length;
  const signature = state.runs.map((run) => `${run.id}:${run.status}`).join(',');
  if (signature === historySignature) return;
  historySignature = signature;
  if (!state.runs.length) return;
  $('history-list').replaceChildren();
  for (const run of state.runs) {
    const button = document.createElement('button'); button.type = 'button'; button.className = 'history-item';
    const title = document.createElement('strong'), detail = document.createElement('span'), status = document.createElement('span');
    title.textContent = `${names[run.config.algorithm] || run.config.algorithm} · ${run.options.scenario === 'burst' ? 'Ráfaga' : 'Sostenida'}`;
    detail.textContent = `${new Date(run.started).toLocaleTimeString('es-BO')} · ${run.options.rps} req/s · ${run.options.duration} s · ${run.options.clients} cliente(s)`;
    status.textContent = labels[run.status]; button.append(title, detail, status);
    button.addEventListener('click', () => { renderResult(run); $('run-result').scrollIntoView({behavior: 'smooth', block: 'center'}); });
    $('history-list').append(button);
  }
}
async function refresh() {
  try {
    state = await api('/api/status'); online = true;
    $('connection').textContent = 'Laboratorio conectado';
    if (!initialized) {
      for (const [key, value] of Object.entries(state.config)) configForm.elements[key].value = value;
      algorithmHelp(); initialized = true;
    }
    $('gateway-url').textContent = state.gateway_url;
    $('active-algorithm').textContent = names[state.config.algorithm] || state.config.algorithm;
    const m = state.metrics;
    $('stat-total').textContent = number(m.allowed + m.rejected + m.unavailable);
    $('stat-allowed').textContent = number(m.allowed);
    $('stat-rejected').textContent = number(m.rejected);
    $('stat-errors').textContent = number(m.unavailable + m.upstream_errors);
    chart(m);
    $('k6-status').textContent = state.k6_available ? 'k6 disponible' : 'k6 no instalado';
    $('k6-notice').hidden = state.k6_available;
    $('run-state').textContent = state.active ? `${labels[state.active.status]} · ${Math.floor((Date.now() - Date.parse(state.active.started)) / 1000)} s` : 'Listo para tu próximo experimento';
    history();
    renderResult(state.runs.find((r) => r.id === selectedID) || state.runs[0]);
  } catch (error) { online = false; $('connection').textContent = 'Sin conexión al laboratorio'; }
  controls();
}
async function action(fn) {
  if (busy) return;
  busy = true; controls();
  try { await fn(); } catch (error) { toast(error.message); }
  finally { await refresh(); busy = false; controls(); }
}
configForm.addEventListener('submit', (event) => {
  event.preventDefault();
  action(async () => {
    const c = {algorithm: configForm.elements.algorithm.value};
    for (const key of ['limit', 'window_ms', 'capacity', 'rate']) c[key] = Number(configForm.elements[key].value);
    await api('/api/config', c); resetChart(); toast('Política aplicada. Cuotas y contadores reiniciados.');
  });
});
async function probe(count) {
  if (!$('client-id').reportValidity()) return;
  await action(async () => {
    const client = $('client-id').value;
    for (let i = 0; i < count; i++) {
      const result = await api('/api/probe', {client});
      requests.unshift({time: new Date().toISOString(), client, ...result});
      if (requests.length > 20) requests.pop();
      $('request-rows').replaceChildren();
      for (const request of requests) {
        const row = document.createElement('tr');
        const values = [new Date(request.time).toLocaleTimeString('es-BO'), request.status, `${request.duration_ms.toFixed(2)} ms`, request.remaining || '—', request.retry_after ? `${request.retry_after} s` : '—'];
        values.forEach((value, index) => { const cell = document.createElement('td'); cell.textContent = value;
          if (index === 1) cell.className = `status-code ${request.status === 429 ? 'limited' : request.status >= 400 ? 'failed' : ''}`;
          row.append(cell); });
        $('request-rows').append(row);
      }
    }
  });
}
$('send-one').addEventListener('click', () => probe(1));
$('send-ten').addEventListener('click', () => probe(10));
runForm.addEventListener('submit', (event) => {
  event.preventDefault();
  action(async () => {
    const options = {scenario: runForm.elements.scenario.value};
    for (const key of ['rps', 'duration', 'clients']) options[key] = Number(runForm.elements[key].value);
    const run = await api('/api/runs', options); selectedID = run.id; resetChart(); toast('Prueba k6 iniciada.');
  });
});
$('stop-run').addEventListener('click', () => action(() => api('/api/stop', {})));
function download(name, data) {
  const url = URL.createObjectURL(new Blob([JSON.stringify(data, null, 2)], {type: 'application/json'}));
  const link = document.createElement('a'); link.href = url; link.download = name;
  document.body.append(link); link.click(); link.remove();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}
$('download-run').addEventListener('click', () => { const run = state.runs.find((r) => r.id === selectedID); if (run) download(`rate-lab-${run.id}.json`, run); });
$('export-session').addEventListener('click', () => download('rate-lab-session.json', {exported_at: new Date().toISOString(), ...state, manual_requests: requests}));
async function poll() { await refresh(); setTimeout(poll, 1000); }
algorithmHelp(); controls(); poll();

import http from 'k6/http';
import { check } from 'k6';
import { Counter, Trend } from 'k6/metrics';
import exec from 'k6/execution';

export const base = __ENV.BASE_URL || 'http://127.0.0.1:8080';
const admitted = new Counter('admitted');
const rejected = new Counter('rejected');
const admittedLatency = new Trend('admitted_latency', true);
const rejectedLatency = new Trend('rejected_latency', true);
http.setResponseCallback(http.expectedStatuses(200, 429));
export const thresholds = { checks: ['rate==1'], http_req_failed: ['rate==0'], dropped_iterations: ['count==0'] };
export function request() {
  const clients = Number(__ENV.CLIENTS || 1);
  if (!Number.isInteger(clients) || clients < 1) throw new Error('CLIENTS must be a positive integer');
  const client = exec.scenario.iterationInTest % clients;
  const response = http.get(`${base}/api/test`, { headers: { 'X-Test-Client': `${__ENV.RUN_ID || 'load'}-${client}` }, redirects: 0 });
  check(response, { 'status is 200 or 429': r => r.status === 200 || r.status === 429,
    'rejection includes retry guidance': r => r.status !== 429 || Number(r.headers['Retry-After']) >= 1 });
  if (response.status === 200) { admitted.add(1); admittedLatency.add(response.timings.duration); }
  if (response.status === 429) { rejected.add(1); rejectedLatency.add(response.timings.duration); }
}

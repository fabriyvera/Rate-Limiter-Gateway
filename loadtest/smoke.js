// Run on a fresh gateway: --limit 2 --window 2s --capacity 2 --client-header X-Test-Client.
import http from 'k6/http';
import { check, sleep } from 'k6';
http.setResponseCallback(http.expectedStatuses(200, 429));
export const options = { vus: 1, iterations: 1, thresholds: { checks: ['rate==1'], http_req_failed: ['rate==0'] } };
export default function () {
  const base = __ENV.BASE_URL || 'http://127.0.0.1:8080';
  // Move away from fixed-window boundaries before the short sequential burst.
  sleep((2100 - (Date.now() % 2000)) / 1000);
  const id = `smoke-${Date.now()}`;
  const send = key => http.get(`${base}/api/test`, { headers: { 'X-Test-Client': key } });
  check(send(id), { 'first allowed': r => r.status === 200 });
  check(send(id), { 'second allowed': r => r.status === 200 });
  check(send(id), { 'third blocked': r => r.status === 429 && Number(r.headers['Retry-After']) >= 1 });
  check(send(`${id}-other`), { 'independent client': r => r.status === 200 });
  sleep(4.1);
  check(send(id), { 'quota recovers': r => r.status === 200 });
}

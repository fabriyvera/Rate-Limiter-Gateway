import http from 'k6/http';
import { check } from 'k6';
import { Counter, Trend } from 'k6/metrics';
import exec from 'k6/execution';
const admitted = new Counter('admitted'), rejected = new Counter('rejected');
const allowedTime = new Trend('admitted_latency', true), rejectedTime = new Trend('rejected_latency', true);
const rps = Number(__ENV.RPS), duration = Number(__ENV.DURATION);
const workload = __ENV.SCENARIO === 'burst'
  ? { executor: 'ramping-arrival-rate', startRate: 10, timeUnit: '1s', preAllocatedVUs: 50, maxVUs: 200, stages: [{target:10,duration:'3s'},{target:rps,duration:'1s'},{target:rps,duration:`${duration}s`},{target:10,duration:'1s'},{target:10,duration:'5s'}] }
  : { executor: 'constant-arrival-rate', rate:rps, timeUnit:'1s', duration:`${duration}s`, preAllocatedVUs:50, maxVUs:200 };
export const options = { discardResponseBodies:true, scenarios:{load:workload}, thresholds:{checks:['rate==1'],http_req_failed:['rate==0'],dropped_iterations:['count==0']} };
http.setResponseCallback(http.expectedStatuses(200,429));
export default function () {
 const id = exec.scenario.iterationInTest % Number(__ENV.CLIENTS);
 const r = http.get(`${__ENV.BASE_URL}/api/test`,{headers:{'X-Test-Client':`k6-${id}`},redirects:0,timeout:'6s'});
 check(r,{'200 o 429': r=>r.status===200||r.status===429,'Retry-After válido':r=>r.status!==429||Number(r.headers['Retry-After'])>=1});
 if(r.status===200){admitted.add(1);allowedTime.add(r.timings.duration);}
 if(r.status===429){rejected.add(1);rejectedTime.add(r.timings.duration);}
}
export function handleSummary(data){return {[__ENV.SUMMARY_PATH]:JSON.stringify(data)};}

import { request, thresholds } from './common.js';
export const options = {
  discardResponseBodies: true,
  scenarios: { burst: { executor: 'ramping-arrival-rate', startRate: 10, timeUnit: '1s', preAllocatedVUs: 50, maxVUs: 200,
    stages: [{ target: 10, duration: '5s' }, { target: Number(__ENV.RPS || 500), duration: '1s' }, { target: Number(__ENV.RPS || 500), duration: '5s' }, { target: 10, duration: '1s' }, { target: 10, duration: '10s' }] } },
  thresholds,
};
export default request;

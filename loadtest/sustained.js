import { request, thresholds } from './common.js';
export const options = {
  discardResponseBodies: true,
  scenarios: { sustained: { executor: 'constant-arrival-rate', rate: Number(__ENV.RPS || 200), timeUnit: '1s', duration: __ENV.DURATION || '30s', preAllocatedVUs: 50, maxVUs: 200 } },
  thresholds,
};
export default request;

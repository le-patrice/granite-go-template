import http from "k6/http";
import { check, sleep } from "k6";

export const options = {
  vus: 5,
  duration: "10s",
  thresholds: {
    http_req_failed: ["rate<0.01"],
    http_req_duration: ["p(95)<200"],
  },
};

const baseURL = __ENV.BASE_URL || "http://localhost:8080";

export default function () {
  const health = http.get(`${baseURL}/healthz`);
  check(health, {
    "healthz status is 200": (r) => r.status === 200,
  });

  const ready = http.get(`${baseURL}/readyz`);
  check(ready, {
    "readyz status is 200": (r) => r.status === 200,
  });

  sleep(0.2);
}

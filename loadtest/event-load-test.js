import http from "k6/http";
import { check, sleep, group } from "k6";
import { Counter, Rate, Trend } from "k6/metrics";
import exec from "k6/execution";

// ---------------------------------------------------------------------------
// Configuration — pass these via environment variables
// ---------------------------------------------------------------------------
const BASE_URL = __ENV.BASE_URL || "http://localhost:8080";
const AUTH_TOKEN = __ENV.AUTH_TOKEN || "mentiq_live_8dd428de-c380-4a4d-a17a-d8fffd56f341";
const PROJECT_ID = __ENV.PROJECT_ID || "911845df-80ba-4355-aaf7-c07d8a2d33ff";

// Custom metrics
const eventErrors = new Counter("event_errors");
const dashboardErrors = new Counter("dashboard_errors");
const rateLimited = new Counter("rate_limited_total");
const queueFull = new Counter("queue_full_total");
const eventDuration = new Trend("event_ingest_duration", true);
const batchDuration = new Trend("batch_ingest_duration", true);
const dashboardDuration = new Trend("dashboard_duration", true);
const successRate = new Rate("success_rate");

// ---------------------------------------------------------------------------
// Scenarios — focused on event ingestion + dashboard reads (no auth)
// ---------------------------------------------------------------------------
export const options = {
  scenarios: {
    // 1) Event ingestion: ramp up to 500 VUs sending events
    event_ingestion: {
      executor: "ramping-vus",
      exec: "ingestEvents",
      startVUs: 0,
      stages: [
        { duration: "10s", target: 100 },
        { duration: "20s", target: 300 },
        { duration: "30s", target: 500 },
        { duration: "20s", target: 500 },
        { duration: "10s", target: 0 },
      ],
      tags: { scenario: "event_ingestion" },
    },

    // 2) Dashboard reads: 200 VUs hitting analytics endpoints
    dashboard_reads: {
      executor: "ramping-vus",
      exec: "dashboardReads",
      startVUs: 0,
      startTime: "5s",
      stages: [
        { duration: "10s", target: 50 },
        { duration: "20s", target: 150 },
        { duration: "30s", target: 200 },
        { duration: "15s", target: 200 },
        { duration: "10s", target: 0 },
      ],
      tags: { scenario: "dashboard_reads" },
    },
  },

  thresholds: {
    http_req_duration: ["p(95)<3000", "p(99)<5000"],
    success_rate: ["rate>0.90"],
    event_ingest_duration: ["p(95)<1000"],
    batch_ingest_duration: ["p(95)<2000"],
    dashboard_duration: ["p(95)<3000"],
  },
};

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------
const headers = {
  "Content-Type": "application/json",
  Authorization: `ApiKey ${AUTH_TOKEN}`,
  "X-Project-ID": PROJECT_ID,
};

const EVENT_TYPES = [
  "page_view",
  "button_click",
  "form_submit",
  "sign_up",
  "purchase",
  "feature_used",
  "session_start",
  "session_end",
  "error",
  "scroll",
];

const PAGES = [
  "/home", "/pricing", "/dashboard", "/settings", "/profile",
  "/docs", "/blog", "/features", "/signup", "/checkout",
];

const BROWSERS = ["Chrome", "Firefox", "Safari", "Edge"];
const OS_LIST = ["Windows", "macOS", "Linux", "iOS", "Android"];
const DEVICES = ["Desktop", "Mobile", "Tablet"];
const CHANNELS = ["organic", "paid", "referral", "social", "email", "direct"];

function pick(arr) {
  return arr[Math.floor(Math.random() * arr.length)];
}

function makeEvent(userId, sessionId) {
  return {
    event_type: pick(EVENT_TYPES),
    user_id: userId,
    session_id: sessionId,
    timestamp: new Date().toISOString(),
    project_id: PROJECT_ID,
    properties: {
      page: pick(PAGES),
      referrer: Math.random() > 0.5 ? "https://google.com" : "",
      duration: Math.floor(Math.random() * 30000),
      value: Math.random() > 0.7 ? Math.floor(Math.random() * 100) : undefined,
    },
    browser: pick(BROWSERS),
    os: pick(OS_LIST),
    device: pick(DEVICES),
    channel: pick(CHANNELS),
    email: `${userId}@example.com`,
  };
}

// ---------------------------------------------------------------------------
// Scenario 1: Event Ingestion
// ---------------------------------------------------------------------------
export function ingestEvents() {
  const vuId = exec.vu.idInTest;
  const userId = `user_${vuId}_${Math.floor(Math.random() * 100)}`;
  const sessionId = `sess_${Date.now()}_${Math.floor(Math.random() * 10000)}`;

  // Batch ingestion (5-10 events, mimics SDK flush)
  group("Batch event ingestion", () => {
    const batchSize = 5 + Math.floor(Math.random() * 6);
    const events = [];
    for (let i = 0; i < batchSize; i++) {
      events.push(makeEvent(userId, sessionId));
    }

    const start = Date.now();
    const res = http.post(
      `${BASE_URL}/api/v1/events/batch`,
      JSON.stringify(events),
      { headers, tags: { name: "POST /api/v1/events/batch" } }
    );
    const elapsed = Date.now() - start;
    batchDuration.add(elapsed);
    eventDuration.add(elapsed);

    const ok = check(res, {
      "batch status 200/201/202": (r) =>
        r.status === 200 || r.status === 201 || r.status === 202,
    });

    if (!ok) {
      eventErrors.add(1);
      if (res.status === 429) rateLimited.add(1);
      if (res.status === 503) queueFull.add(1);
    }
    successRate.add(ok);
  });

  // Single event (40% of the time)
  if (Math.random() > 0.6) {
    group("Single event ingestion", () => {
      const start = Date.now();
      const res = http.post(
        `${BASE_URL}/api/v1/events`,
        JSON.stringify(makeEvent(userId, sessionId)),
        { headers, tags: { name: "POST /api/v1/events" } }
      );
      const elapsed = Date.now() - start;
      eventDuration.add(elapsed);

      const ok = check(res, {
        "single event status 200/201/202": (r) =>
          r.status === 200 || r.status === 201 || r.status === 202,
      });

      if (!ok) {
        eventErrors.add(1);
        if (res.status === 429) rateLimited.add(1);
        if (res.status === 503) queueFull.add(1);
      }
      successRate.add(ok);
    });
  }

  sleep(0.2 + Math.random() * 0.8);
}

// ---------------------------------------------------------------------------
// Scenario 2: Dashboard Reads
// ---------------------------------------------------------------------------
export function dashboardReads() {
  const pid = PROJECT_ID;

  const endpoints = [
    { name: "GET /api/v1/me", url: `${BASE_URL}/api/v1/me` },
    { name: "GET /api/v1/projects", url: `${BASE_URL}/api/v1/projects` },
    { name: "GET /api/v1/dashboard", url: `${BASE_URL}/api/v1/dashboard?project_id=${pid}` },
    { name: "GET /api/v1/analytics", url: `${BASE_URL}/api/v1/analytics?project_id=${pid}` },
    { name: "GET /analytics/retention", url: `${BASE_URL}/api/v1/analytics/retention?project_id=${pid}` },
    { name: "GET /projects/:id/analytics/cohorts", url: `${BASE_URL}/api/v1/projects/${pid}/analytics/cohorts` },
    { name: "GET /projects/:id/analytics/devices", url: `${BASE_URL}/api/v1/projects/${pid}/analytics/devices` },
    { name: "GET /projects/:id/sessions", url: `${BASE_URL}/api/v1/projects/${pid}/sessions` },
    { name: "GET /projects/:id/experiments", url: `${BASE_URL}/api/v1/projects/${pid}/experiments` },
    { name: "GET /projects/:id/heatmaps", url: `${BASE_URL}/api/v1/projects/${pid}/heatmaps` },
  ];

  const count = 3 + Math.floor(Math.random() * 3);
  for (let i = 0; i < count; i++) {
    const ep = pick(endpoints);

    group("Dashboard: " + ep.name, () => {
      const start = Date.now();
      const res = http.get(ep.url, {
        headers,
        tags: { name: ep.name },
      });
      dashboardDuration.add(Date.now() - start);

      const ok = check(res, {
        "dashboard status 200": (r) => r.status === 200,
      });

      if (!ok) {
        dashboardErrors.add(1);
        if (res.status === 429) rateLimited.add(1);
      }
      successRate.add(ok);
    });

    sleep(0.5 + Math.random() * 1.5);
  }

  sleep(1 + Math.random() * 2);
}

// ---------------------------------------------------------------------------
// Summary
// ---------------------------------------------------------------------------
export function handleSummary(data) {
  const lines = [
    "=".repeat(70),
    "  MENTIQ EVENT INGESTION LOAD TEST RESULTS",
    "=".repeat(70),
    "",
    `Total requests ........: ${data.metrics.http_reqs?.values?.count || 0}`,
    `Success rate ..........: ${((data.metrics.success_rate?.values?.rate || 0) * 100).toFixed(1)}%`,
    `Rate limited (429) ....: ${data.metrics.rate_limited_total?.values?.count || 0}`,
    `Queue full (503) ......: ${data.metrics.queue_full_total?.values?.count || 0}`,
    "",
    "--- Latency (p95) ---",
    `Event ingestion .......: ${(data.metrics.event_ingest_duration?.values?.["p(95)"] || 0).toFixed(0)} ms`,
    `Batch ingestion .......: ${(data.metrics.batch_ingest_duration?.values?.["p(95)"] || 0).toFixed(0)} ms`,
    `Dashboard reads .......: ${(data.metrics.dashboard_duration?.values?.["p(95)"] || 0).toFixed(0)} ms`,
    `Overall HTTP ..........: ${(data.metrics.http_req_duration?.values?.["p(95)"] || 0).toFixed(0)} ms`,
    "",
    "--- Latency (p99) ---",
    `Event ingestion .......: ${(data.metrics.event_ingest_duration?.values?.["p(99)"] || 0).toFixed(0)} ms`,
    `Dashboard reads .......: ${(data.metrics.dashboard_duration?.values?.["p(99)"] || 0).toFixed(0)} ms`,
    `Overall HTTP ..........: ${(data.metrics.http_req_duration?.values?.["p(99)"] || 0).toFixed(0)} ms`,
    "",
    "--- Errors ---",
    `Event errors ..........: ${data.metrics.event_errors?.values?.count || 0}`,
    `Dashboard errors ......: ${data.metrics.dashboard_errors?.values?.count || 0}`,
    "",
    "--- Throughput ---",
    `Requests/sec ..........: ${(data.metrics.http_reqs?.values?.rate || 0).toFixed(1)}`,
    "",
    "=".repeat(70),
  ];

  return {
    stdout: lines.join("\n") + "\n",
    "loadtest/event-results.json": JSON.stringify(data, null, 2),
  };
}

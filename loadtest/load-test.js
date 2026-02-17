import http from "k6/http";
import { check, sleep, group } from "k6";
import { Counter, Rate, Trend } from "k6/metrics";
import { SharedArray } from "k6/data";
import exec from "k6/execution";

// ---------------------------------------------------------------------------
// Configuration
// ---------------------------------------------------------------------------
const BASE_URL = __ENV.BASE_URL || "http://localhost:8080";
const TEST_PASSWORD = __ENV.TEST_PASSWORD || "LoadTest1234!";

// Custom metrics
const signupErrors = new Counter("signup_errors");
const loginErrors = new Counter("login_errors");
const eventErrors = new Counter("event_errors");
const dashboardErrors = new Counter("dashboard_errors");
const rateLimited = new Counter("rate_limited_total");
const queueFull = new Counter("queue_full_total"); // 503 backpressure from event queue
const loginDuration = new Trend("login_duration", true);
const eventDuration = new Trend("event_ingest_duration", true);
const dashboardDuration = new Trend("dashboard_duration", true);
const successRate = new Rate("success_rate");

// ---------------------------------------------------------------------------
// Scenarios — simulates 1000 concurrent users across different workloads
// ---------------------------------------------------------------------------
export const options = {
  scenarios: {
    // 1) Signup + login burst: 200 VUs sign up and log in over 1 minute
    signup_login: {
      executor: "ramping-vus",
      exec: "signupAndLogin",
      startVUs: 0,
      stages: [
        { duration: "15s", target: 100 },
        { duration: "30s", target: 200 },
        { duration: "15s", target: 0 },
      ],
      tags: { scenario: "signup_login" },
    },

    // 2) Event ingestion: 500 VUs sending events continuously
    event_ingestion: {
      executor: "ramping-vus",
      exec: "ingestEvents",
      startVUs: 0,
      startTime: "10s", // slight delay so some accounts exist
      stages: [
        { duration: "20s", target: 250 },
        { duration: "40s", target: 500 },
        { duration: "20s", target: 500 },
        { duration: "10s", target: 0 },
      ],
      tags: { scenario: "event_ingestion" },
    },

    // 3) Dashboard reads: 300 VUs hitting analytics endpoints
    dashboard_reads: {
      executor: "ramping-vus",
      exec: "dashboardReads",
      startVUs: 0,
      startTime: "15s",
      stages: [
        { duration: "15s", target: 150 },
        { duration: "40s", target: 300 },
        { duration: "15s", target: 300 },
        { duration: "10s", target: 0 },
      ],
      tags: { scenario: "dashboard_reads" },
    },
  },

  thresholds: {
    http_req_duration: ["p(95)<3000", "p(99)<5000"], // 95th < 3s, 99th < 5s
    success_rate: ["rate>0.90"],                      // >90% requests succeed
    login_duration: ["p(95)<2000"],
    event_ingest_duration: ["p(95)<1000"],
    dashboard_duration: ["p(95)<3000"],
  },
};

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------
const jsonHeaders = { "Content-Type": "application/json" };

function authHeader(token) {
  return {
    "Content-Type": "application/json",
    Authorization: `Bearer ${token}`,
  };
}

function uniqueEmail() {
  return `loadtest_${exec.vu.idInTest}_${Date.now()}@test.local`;
}

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
  "/home",
  "/pricing",
  "/dashboard",
  "/settings",
  "/profile",
  "/docs",
  "/blog",
  "/features",
  "/signup",
  "/checkout",
];

const BROWSERS = ["Chrome", "Firefox", "Safari", "Edge"];
const OS_LIST = ["Windows", "macOS", "Linux", "iOS", "Android"];
const DEVICES = ["Desktop", "Mobile", "Tablet"];
const CHANNELS = ["organic", "paid", "referral", "social", "email", "direct"];

function pick(arr) {
  return arr[Math.floor(Math.random() * arr.length)];
}

// ---------------------------------------------------------------------------
// Scenario 1: Signup + Login
// ---------------------------------------------------------------------------
export function signupAndLogin() {
  const email = uniqueEmail();
  const name = `Load Tester ${exec.vu.idInTest}`;

  group("Signup", () => {
    const res = http.post(
      `${BASE_URL}/signup`,
      JSON.stringify({ name, email, password: TEST_PASSWORD }),
      { headers: jsonHeaders, tags: { name: "POST /signup" } }
    );

    const ok = check(res, {
      "signup status 200 or 201": (r) => r.status === 200 || r.status === 201,
      "signup returns token": (r) => {
        try {
          const body = JSON.parse(r.body);
          return !!body.accessToken || !!body.token;
        } catch {
          return false;
        }
      },
    });

    if (!ok) {
      signupErrors.add(1);
      if (res.status === 429) rateLimited.add(1);
    }
    successRate.add(ok);
  });

  sleep(0.5 + Math.random());

  group("Login", () => {
    const start = Date.now();
    const res = http.post(
      `${BASE_URL}/login`,
      JSON.stringify({ email, password: TEST_PASSWORD }),
      { headers: jsonHeaders, tags: { name: "POST /login" } }
    );
    loginDuration.add(Date.now() - start);

    const ok = check(res, {
      "login status 200": (r) => r.status === 200,
      "login returns accessToken": (r) => {
        try {
          return !!JSON.parse(r.body).accessToken;
        } catch {
          return false;
        }
      },
    });

    if (!ok) {
      loginErrors.add(1);
      if (res.status === 429) rateLimited.add(1);
    }
    successRate.add(ok);

    // Check rate-limit headers
    check(res, {
      "has X-RateLimit-Limit header": (r) => !!r.headers["X-Ratelimit-Limit"],
    });
  });

  sleep(1 + Math.random() * 2);
}

// ---------------------------------------------------------------------------
// Scenario 2: Event Ingestion (SDK-like traffic)
// ---------------------------------------------------------------------------
export function ingestEvents() {
  // Each VU creates its own account, project, and API key, then sends events
  const vuState = getOrCreateVuAccount();

  if (!vuState.token) {
    sleep(1);
    return;
  }

  const userId = `user_${exec.vu.idInTest}_${Math.floor(Math.random() * 100)}`;
  const sessionId = `sess_${Date.now()}_${Math.floor(Math.random() * 10000)}`;

  // Send a batch of 5-10 events (mimics SDK flush)
  group("Batch event ingestion", () => {
    const batchSize = 5 + Math.floor(Math.random() * 6);
    const events = [];

    for (let i = 0; i < batchSize; i++) {
      events.push({
        event_type: pick(EVENT_TYPES),
        user_id: userId,
        session_id: sessionId,
        timestamp: new Date().toISOString(),
        project_id: vuState.projectId,
        properties: {
          page: pick(PAGES),
          referrer: Math.random() > 0.5 ? "https://google.com" : "",
          duration: Math.floor(Math.random() * 30000),
          value: Math.random() > 0.7 ? Math.floor(Math.random() * 100) : undefined,
        },
        user_agent: `Mozilla/5.0 (k6 load test; VU ${exec.vu.idInTest})`,
        browser: pick(BROWSERS),
        os: pick(OS_LIST),
        device: pick(DEVICES),
        channel: pick(CHANNELS),
        email: `${userId}@example.com`,
      });
    }

    const start = Date.now();
    const res = http.post(
      `${BASE_URL}/api/v1/events/batch`,
      JSON.stringify(events),
      {
        headers: authHeader(vuState.token),
        tags: { name: "POST /api/v1/events/batch" },
      }
    );
    eventDuration.add(Date.now() - start);

    const ok = check(res, {
      "batch ingest status 200/201/202": (r) =>
        r.status === 200 || r.status === 201 || r.status === 202,
    });

    if (!ok) {
      eventErrors.add(1);
      if (res.status === 429) rateLimited.add(1);
      if (res.status === 503) queueFull.add(1);
    }
    successRate.add(ok);
  });

  // Also send a single event occasionally
  if (Math.random() > 0.6) {
    group("Single event ingestion", () => {
      const start = Date.now();
      const res = http.post(
        `${BASE_URL}/api/v1/events`,
        JSON.stringify({
          event_type: pick(EVENT_TYPES),
          user_id: userId,
          session_id: sessionId,
          timestamp: new Date().toISOString(),
          project_id: vuState.projectId,
          properties: { page: pick(PAGES) },
          browser: pick(BROWSERS),
          os: pick(OS_LIST),
          device: pick(DEVICES),
          channel: pick(CHANNELS),
        }),
        {
          headers: authHeader(vuState.token),
          tags: { name: "POST /api/v1/events" },
        }
      );
      eventDuration.add(Date.now() - start);

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

  sleep(0.2 + Math.random() * 0.8); // SDK-like flush interval
}

// ---------------------------------------------------------------------------
// Scenario 3: Dashboard reads (authenticated users browsing analytics)
// ---------------------------------------------------------------------------
export function dashboardReads() {
  const vuState = getOrCreateVuAccount();

  if (!vuState.token || !vuState.projectId) {
    sleep(1);
    return;
  }

  const headers = authHeader(vuState.token);
  const pid = vuState.projectId;

  // Hit various analytics endpoints like a real user navigating the dashboard
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

  // Pick 3-5 random endpoints per iteration (simulates page navigation)
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

    sleep(0.5 + Math.random() * 1.5); // reading time between pages
  }

  sleep(1 + Math.random() * 2);
}

// ---------------------------------------------------------------------------
// VU account setup — each VU creates one account and reuses it
// ---------------------------------------------------------------------------
const vuAccounts = {};

function getOrCreateVuAccount() {
  const vuId = exec.vu.idInTest;

  if (vuAccounts[vuId] && vuAccounts[vuId].token) {
    return vuAccounts[vuId];
  }

  const email = `loadvu_${vuId}_${exec.scenario.name}@test.local`;
  const name = `VU ${vuId}`;

  // Signup
  const signupRes = http.post(
    `${BASE_URL}/signup`,
    JSON.stringify({ name, email, password: TEST_PASSWORD }),
    { headers: jsonHeaders, tags: { name: "POST /signup (setup)" } }
  );

  let token = null;
  let projectId = null;

  if (signupRes.status === 200 || signupRes.status === 201) {
    try {
      const body = JSON.parse(signupRes.body);
      token = body.accessToken || body.token;
      projectId = body.projectId;
    } catch {}
  }

  // If signup failed (duplicate), try login
  if (!token) {
    const loginRes = http.post(
      `${BASE_URL}/login`,
      JSON.stringify({ email, password: TEST_PASSWORD }),
      { headers: jsonHeaders, tags: { name: "POST /login (setup)" } }
    );

    if (loginRes.status === 200) {
      try {
        const body = JSON.parse(loginRes.body);
        token = body.accessToken || body.token;
        projectId = body.projectId;
      } catch {}
    }
  }

  // Create a project if we don't have one
  if (token && !projectId) {
    const projRes = http.post(
      `${BASE_URL}/api/v1/projects`,
      JSON.stringify({ name: `Load Test Project ${vuId}` }),
      { headers: authHeader(token), tags: { name: "POST /api/v1/projects (setup)" } }
    );

    if (projRes.status === 200 || projRes.status === 201) {
      try {
        const body = JSON.parse(projRes.body);
        projectId = body.id || body.project?.id;
      } catch {}
    }
  }

  // Create an API key for the project
  let apiKey = null;
  if (token && projectId) {
    const keyRes = http.post(
      `${BASE_URL}/api/v1/projects/${projectId}/apikeys`,
      JSON.stringify({ name: `loadtest-key-${vuId}` }),
      { headers: authHeader(token), tags: { name: "POST /apikeys (setup)" } }
    );

    if (keyRes.status === 200 || keyRes.status === 201) {
      try {
        const body = JSON.parse(keyRes.body);
        apiKey = body.key;
      } catch {}
    }
  }

  vuAccounts[vuId] = { token, projectId, apiKey, email };
  return vuAccounts[vuId];
}

// ---------------------------------------------------------------------------
// Summary
// ---------------------------------------------------------------------------
export function handleSummary(data) {
  const lines = [
    "=".repeat(70),
    "  MENTIQ LOAD TEST RESULTS",
    "=".repeat(70),
    "",
    `Total requests ........: ${data.metrics.http_reqs?.values?.count || 0}`,
    `Success rate ..........: ${((data.metrics.success_rate?.values?.rate || 0) * 100).toFixed(1)}%`,
    `Rate limited (429) ....: ${data.metrics.rate_limited_total?.values?.count || 0}`,
    `Queue full (503) ......: ${data.metrics.queue_full_total?.values?.count || 0}`,
    "",
    "--- Latency (p95) ---",
    `Login .................: ${(data.metrics.login_duration?.values?.["p(95)"] || 0).toFixed(0)} ms`,
    `Event ingestion .......: ${(data.metrics.event_ingest_duration?.values?.["p(95)"] || 0).toFixed(0)} ms`,
    `Dashboard reads .......: ${(data.metrics.dashboard_duration?.values?.["p(95)"] || 0).toFixed(0)} ms`,
    `Overall HTTP ..........: ${(data.metrics.http_req_duration?.values?.["p(95)"] || 0).toFixed(0)} ms`,
    "",
    "--- Errors ---",
    `Signup errors .........: ${data.metrics.signup_errors?.values?.count || 0}`,
    `Login errors ..........: ${data.metrics.login_errors?.values?.count || 0}`,
    `Event errors ..........: ${data.metrics.event_errors?.values?.count || 0}`,
    `Dashboard errors ......: ${data.metrics.dashboard_errors?.values?.count || 0}`,
    "",
    "=".repeat(70),
  ];

  return {
    stdout: lines.join("\n") + "\n",
    "loadtest/results.json": JSON.stringify(data, null, 2),
  };
}

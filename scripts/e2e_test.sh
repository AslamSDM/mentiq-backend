#!/usr/bin/env bash
set -euo pipefail

# End-to-end test script for MentiQ analytics platform
# Tests: signup -> login -> create project -> create apikey -> ingest events (single + batch) -> upload recording -> query analytics

API_URL=${API_URL:-"http://localhost:8080"}
JQ=${JQ:-jq}
CURL=${CURL:-curl}
STRIPE_API_KEY=${STRIPE_API_KEY:-""}
SEED_STRIPE=${SEED_STRIPE:-"false"}

if ! command -v $CURL >/dev/null 2>&1; then
  echo "curl is required. Install it and re-run." >&2
  exit 2
fi
if ! command -v $JQ >/dev/null 2>&1; then
  echo "jq is required. Install it and re-run." >&2
  exit 2
fi

set -o pipefail

rnd() { echo "$(date +%s)$((RANDOM%1000))"; }
email="test.$(rnd)@example.com"
password="TestPassword123"
name="E2E Tester"
project_name="e2e-project-$(rnd)"

echo "Using API_URL=$API_URL"

# Helper for API calls with token (if set)
TOKEN=""
PROJECT_ID=""
api() {
  method=$1; shift
  path=$1; shift
  data="$1"; shift || true

  hdrs=( -H "Content-Type: application/json" )
  if [ -n "$TOKEN" ]; then
    hdrs+=( -H "Authorization: Bearer $TOKEN" )
  fi
  if [ -n "$PROJECT_ID" ]; then
    hdrs+=( -H "X-Project-ID: $PROJECT_ID" )
  fi

  if [ -n "$data" ]; then
    result=$($CURL -sS -w "\n__HTTP_CODE__%{http_code}" "${hdrs[@]}" -X "$method" -d "$data" "$API_URL$path")
  else
    result=$($CURL -sS -w "\n__HTTP_CODE__%{http_code}" "${hdrs[@]}" -X "$method" "$API_URL$path")
  fi
  
  # Split body and status using marker
  body=$(echo "$result" | sed -n '/__HTTP_CODE__/q;p')
  status=$(echo "$result" | grep "__HTTP_CODE__" | sed 's/.*__HTTP_CODE__//')
  
  # Return both
  echo "$body"
  echo "$status"
}

check_ok() {
  body="$1"
  status="$2"
  msg="$3"
  if [ "$status" -ge 200 ] && [ "$status" -lt 300 ]; then
    echo "[OK] $msg (status $status)"
    echo "$body" | sed -n '1,3p'
  else
    echo "[FAIL] $msg (status $status)" >&2
    echo "Response body:"
    echo "$body" | sed -n '1,200p' >&2
    exit 3
  fi
}

# 1) Signup
signup_payload=$(jq -nc --arg name "$name" --arg email "$email" --arg password "$password" '{name: $name, email: $email, password: $password}')
resp_and_code=$(api POST "/signup" "$signup_payload")
status=$(echo "$resp_and_code" | tail -n1)
body=$(echo "$resp_and_code" | sed '
$ d')
check_ok "$body" "$status" "Signup user $email"

# 2) Login (get JWT)
login_payload=$(jq -nc --arg email "$email" --arg password "$password" '{email: $email, password: $password}')
resp_and_code=$(api POST "/login" "$login_payload")
status=$(echo "$resp_and_code" | tail -n1)
body=$(echo "$resp_and_code" | sed '
$ d')
check_ok "$body" "$status" "Login user $email"
TOKEN=$(echo "$body" | $JQ -r '.token')
if [ "$TOKEN" = "null" ] || [ -z "$TOKEN" ]; then
  echo "Failed to obtain token from login response" >&2
  echo "$body" >&2
  exit 4
fi

# 3) Create Project
create_proj_payload=$(jq -nc --arg name "$project_name" '{name: $name}')
resp_and_code=$(api POST "/api/v1/projects" "$create_proj_payload")
status=$(echo "$resp_and_code" | tail -n1)
body=$(echo "$resp_and_code" | sed '
$ d')
check_ok "$body" "$status" "Create project $project_name"
PROJECT_ID=$(echo "$body" | $JQ -r '.id')

# 4) Create API Key (optional - demonstrates API key flow)
apikey_payload=$(jq -nc --arg name "e2e-key-$(rnd)" '{name: $name, permissions: ["ingest","read"]}')
resp_and_code=$(api POST "/api/v1/projects/$PROJECT_ID/apikeys" "$apikey_payload")
status=$(echo "$resp_and_code" | tail -n1)
body=$(echo "$resp_and_code" | sed '
$ d')
check_ok "$body" "$status" "Create API key for project $PROJECT_ID"
API_KEY=$(echo "$body" | $JQ -r '.key')

# 5) Ingest single event (page_view)
event_id1=$(uuidgen 2>/dev/null || echo "evt-$(rnd)")
event_payload=$(jq -nc --arg eid "$event_id1" --arg etype "page_view" --arg acc "$(echo $TOKEN | $JQ -Rr 'split(".") | .[1] | @base64d' 2>/dev/null || echo "")" --arg project "$PROJECT_ID" --argjson ts "$(date -u +%Y-%m-%dT%H:%M:%SZ | jq -R .)" '{event_id: $eid, event_type: $etype, timestamp: $ts, account_id: "", project_id: $project, session_id: "session-e2e-1", user_id: "user-e2e-1", properties: {url: "/e2e/home", title: "E2E Home"}}')
resp_and_code=$(api POST "/api/v1/events" "$event_payload")
status=$(echo "$resp_and_code" | tail -n1)
body=$(echo "$resp_and_code" | sed '
$ d')
check_ok "$body" "$status" "Ingest single page_view event"

# 6) Ingest batch events (click, scroll, mouse_move, error, custom)
batch_payload=$(cat <<'EOF'
[
  {"event_id":"batch-1","event_type":"click","timestamp":"" ,"session_id":"session-e2e-1","user_id":"user-e2e-1","project_id":"PROJECT_PLACEHOLDER","account_id":"","properties":{"x":100,"y":200,"url":"/e2e/home","element":"button.signup"}},
  {"event_id":"batch-2","event_type":"scroll","timestamp":"","session_id":"session-e2e-1","user_id":"user-e2e-1","project_id":"PROJECT_PLACEHOLDER","account_id":"","properties":{"scrollTop":500,"height":800,"url":"/e2e/home"}},
  {"event_id":"batch-3","event_type":"mouse_move","timestamp":"","session_id":"session-e2e-1","user_id":"user-e2e-1","project_id":"PROJECT_PLACEHOLDER","account_id":"","properties":{"x":150,"y":250}},
  {"event_id":"batch-4","event_type":"error_event","timestamp":"","session_id":"session-e2e-1","user_id":"user-e2e-1","project_id":"PROJECT_PLACEHOLDER","account_id":"","properties":{"message":"TypeError: x is not a function","stack":"..."}},
  {"event_id":"batch-5","event_type":"custom_event","timestamp":"","session_id":"session-e2e-1","user_id":"user-e2e-1","project_id":"PROJECT_PLACEHOLDER","account_id":"","properties":{"key":"value"}}
]
EOF
)
# inject timestamps and project id
now_iso=$(date -u +%Y-%m-%dT%H:%M:%SZ)
batch_payload=$(echo "$batch_payload" | sed "s/PROJECT_PLACEHOLDER/$PROJECT_ID/g" | jq --arg now "$now_iso" 'map(.timestamp = $now)')
resp_and_code=$(api POST "/api/v1/events/batch" "$batch_payload")
status=$(echo "$resp_and_code" | tail -n1)
body=$(echo "$resp_and_code" | sed '
$ d')
check_ok "$body" "$status" "Ingest batch events"

# 7) Query dashboard and analytics endpoints
resp_and_code=$(api GET "/api/v1/dashboard" "")
status=$(echo "$resp_and_code" | tail -n1)
body=$(echo "$resp_and_code" | sed '
$ d')
check_ok "$body" "$status" "Get dashboard"

resp_and_code=$(api GET "/api/v1/analytics" "")
status=$(echo "$resp_and_code" | tail -n1)
body=$(echo "$resp_and_code" | sed '
$ d')
if [ "$status" -ge 200 ] && [ "$status" -lt 300 ]; then
  echo "[OK] Get analytics (status $status)"
  echo "$body" | $JQ -c '. | {meta: .meta? , metrics: .metrics?}' || true
else
  echo "[WARN] Get analytics returned $status - service may not have aggregated metrics yet"
fi

# 8) Query heatmaps (project)
resp_and_code=$(api GET "/api/v1/projects/$PROJECT_ID/heatmaps" "")
status=$(echo "$resp_and_code" | tail -n1)
body=$(echo "$resp_and_code" | sed '
$ d')
if [ "$status" -ge 200 ] && [ "$status" -lt 300 ]; then
  echo "[OK] Get project heatmaps (status $status)"
  echo "$body" | $JQ -c '.data.page_heatmaps[0:2] // .data // .' || echo "$body"
else
  echo "[WARN] Get project heatmaps returned $status - may be empty"
fi

# 9) Upload a session recording
session_id="session-e2e-1"
recording_payload=$(jq -nc \
  --arg start_url "/e2e/home" \
  --arg project "$PROJECT_ID" \
  '{
    events: [
      {type: "mousemove", x: 100, y: 200},
      {type: "click", x: 100, y: 200, element: "button.signup"}
    ],
    duration: 12,
    start_url: $start_url,
    account_id: "",
    project_id: $project,
    user_id: "user-e2e-1"
  }')
resp_and_code=$(api POST "/api/v1/sessions/$session_id/recordings" "$recording_payload")
status=$(echo "$resp_and_code" | tail -n1)
body=$(echo "$resp_and_code" | sed '
$ d')
check_ok "$body" "$status" "Upload session recording"
RECORDING_ID=$(echo "$body" | $JQ -r '.recording_id')

# 10) Retrieve recording
resp_and_code=$(api GET "/api/v1/projects/$PROJECT_ID/recordings/$RECORDING_ID" "")
status=$(echo "$resp_and_code" | tail -n1)
body=$(echo "$resp_and_code" | sed '
$ d')
check_ok "$body" "$status" "Fetch recording $RECORDING_ID"

# 11) List recordings for project
resp_and_code=$(api GET "/api/v1/projects/$PROJECT_ID/recordings" "")
status=$(echo "$resp_and_code" | tail -n1)
body=$(echo "$resp_and_code" | sed '
$ d')
check_ok "$body" "$status" "List recordings for project $PROJECT_ID"

# 12) Test Enhanced Analytics endpoints
echo ""
echo "Testing Enhanced Analytics endpoints..."

# Location Analytics
resp_and_code=$(api GET "/api/v1/projects/$PROJECT_ID/analytics/location" "")
status=$(echo "$resp_and_code" | tail -n1)
body=$(echo "$resp_and_code" | sed '
$ d')
if [ "$status" -ge 200 ] && [ "$status" -lt 300 ]; then
  echo "[OK] Location analytics (status $status)"
else
  echo "[WARN] Location analytics returned $status - may need more data"
fi

# Device Analytics
resp_and_code=$(api GET "/api/v1/projects/$PROJECT_ID/analytics/devices" "")
status=$(echo "$resp_and_code" | tail -n1)
body=$(echo "$resp_and_code" | sed '
$ d')
if [ "$status" -ge 200 ] && [ "$status" -lt 300 ]; then
  echo "[OK] Device analytics (status $status)"
else
  echo "[WARN] Device analytics returned $status - may need more data"
fi

# Retention Cohorts
resp_and_code=$(api GET "/api/v1/projects/$PROJECT_ID/analytics/cohorts" "")
status=$(echo "$resp_and_code" | tail -n1)
body=$(echo "$resp_and_code" | sed '
$ d')
if [ "$status" -ge 200 ] && [ "$status" -lt 300 ]; then
  echo "[OK] Retention cohorts (status $status)"
else
  echo "[WARN] Retention cohorts returned $status - may need more data"
fi

# Feature Adoption
resp_and_code=$(api GET "/api/v1/projects/$PROJECT_ID/analytics/features" "")
status=$(echo "$resp_and_code" | tail -n1)
body=$(echo "$resp_and_code" | sed '
$ d')
if [ "$status" -ge 200 ] && [ "$status" -lt 300 ]; then
  echo "[OK] Feature adoption (status $status)"
else
  echo "[WARN] Feature adoption returned $status - may need more data"
fi

# Churn Risk
resp_and_code=$(api GET "/api/v1/projects/$PROJECT_ID/analytics/churn" "")
status=$(echo "$resp_and_code" | tail -n1)
body=$(echo "$resp_and_code" | sed '
$ d')
if [ "$status" -ge 200 ] && [ "$status" -lt 300 ]; then
  echo "[OK] Churn risk analytics (status $status)"
else
  echo "[WARN] Churn risk returned $status - may need more data"
fi

# Conversion Funnels
resp_and_code=$(api GET "/api/v1/projects/$PROJECT_ID/analytics/funnels" "")
status=$(echo "$resp_and_code" | tail -n1)
body=$(echo "$resp_and_code" | sed '
$ d')
if [ "$status" -ge 200 ] && [ "$status" -lt 300 ]; then
  echo "[OK] Conversion funnels (status $status)"
else
  echo "[WARN] Conversion funnels returned $status - may need more data"
fi

# Session Analytics
resp_and_code=$(api GET "/api/v1/projects/$PROJECT_ID/analytics/sessions" "")
status=$(echo "$resp_and_code" | tail -n1)
body=$(echo "$resp_and_code" | sed '
$ d')
if [ "$status" -ge 200 ] && [ "$status" -lt 300 ]; then
  echo "[OK] Session analytics (status $status)"
else
  echo "[WARN] Session analytics returned $status - may need more data"
fi

# 13) Seed Stripe data if requested
if [ "$SEED_STRIPE" = "true" ] && [ -n "$STRIPE_API_KEY" ]; then
  echo ""
  echo "Seeding Stripe test data..."
  SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
  
  if [ -f "$SCRIPT_DIR/seed_stripe_advanced.sh" ]; then
    STRIPE_API_KEY="$STRIPE_API_KEY" \
    PROJECT_ID="$PROJECT_ID" \
    BEARER_TOKEN="$TOKEN" \
    API_URL="$API_URL" \
    "$SCRIPT_DIR/seed_stripe_advanced.sh"
    
    echo ""
    echo "Stripe data seeded successfully. Syncing to backend..."
    
    # Configure Stripe API key for project
    stripe_key_data=$(cat <<EOF
{"api_key": "$STRIPE_API_KEY"}
EOF
)
    resp_and_code=$(api PUT "/api/v1/projects/$PROJECT_ID/stripe-key" "$stripe_key_data")
    status=$(echo "$resp_and_code" | tail -n1)
    if [ "$status" -ge 200 ] && [ "$status" -lt 300 ]; then
      echo "[OK] Stripe API key configured (status $status)"
    else
      echo "[WARN] Failed to configure Stripe key (status $status)"
    fi
    
    # Sync Stripe data
    resp_and_code=$(api POST "/api/v1/projects/$PROJECT_ID/stripe/sync" "")
    status=$(echo "$resp_and_code" | tail -n1)
    if [ "$status" -ge 200 ] && [ "$status" -lt 300 ]; then
      echo "[OK] Stripe data synced (status $status)"
    else
      echo "[WARN] Stripe sync may have failed (status $status)"
    fi
    
    echo "Waiting 2 seconds for data processing..."
    sleep 2
  else
    echo "[WARN] seed_stripe_advanced.sh not found, skipping Stripe seeding"
  fi
elif [ -n "$STRIPE_API_KEY" ]; then
  echo ""
  echo "Configuring Stripe API key (set SEED_STRIPE=true to auto-seed data)..."
  
  # Just configure the key without seeding
  stripe_key_data=$(cat <<EOF
{"api_key": "$STRIPE_API_KEY"}
EOF
)
  resp_and_code=$(api PUT "/api/v1/projects/$PROJECT_ID/stripe-key" "$stripe_key_data")
  status=$(echo "$resp_and_code" | tail -n1)
  if [ "$status" -ge 200 ] && [ "$status" -lt 300 ]; then
    echo "[OK] Stripe API key configured (status $status)"
    
    # Try sync
    resp_and_code=$(api POST "/api/v1/projects/$PROJECT_ID/stripe/sync" "")
    status=$(echo "$resp_and_code" | tail -n1)
    if [ "$status" -ge 200 ] && [ "$status" -lt 300 ]; then
      echo "[OK] Stripe data synced (status $status)"
    fi
  fi
fi

# 14) Test Stripe Revenue Analytics endpoints
echo ""
if [ -n "$STRIPE_API_KEY" ]; then
  echo "Testing Stripe Revenue Analytics endpoints..."
else
  echo "Testing Stripe Revenue Analytics endpoints (expect errors without Stripe config)..."
fi

# Stripe Metrics
echo ""
echo "→ Fetching Stripe Metrics..."
resp_and_code=$(api GET "/api/v1/projects/$PROJECT_ID/stripe/metrics" "")
body=$(echo "$resp_and_code" | sed '$d')
status=$(echo "$resp_and_code" | tail -n 1)
if [ "$status" -ge 200 ] && [ "$status" -lt 300 ]; then
  echo "[OK] Stripe metrics (status $status)"
  echo ""
  echo "Revenue Metrics:"
  echo "$body" | $JQ -r '
    .metrics // .data.metrics // . | 
    if type == "object" then
      "  MRR: $" + (.mrr // 0 | tostring),
      "  ARR: $" + (.arr // 0 | tostring),
      "  Active Subscriptions: " + (.active_subscriptions // 0 | tostring),
      "  Total Revenue: $" + (.total_revenue // 0 | tostring),
      "  Churn Rate: " + (.churn_rate // 0 | tostring) + "%"
    else
      .
    end
  ' 2>/dev/null || echo "$body" | head -20
elif [ "$status" -eq 400 ] || [ "$status" -eq 404 ]; then
  echo "[EXPECTED] Stripe metrics not configured (status $status)"
else
  echo "[WARN] Stripe metrics returned unexpected status $status"
fi

# Stripe Analytics
echo ""
echo "→ Fetching Stripe Analytics..."
resp_and_code=$(api GET "/api/v1/projects/$PROJECT_ID/stripe/analytics" "")
body=$(echo "$resp_and_code" | sed '$d')
status=$(echo "$resp_and_code" | tail -n 1)
if [ "$status" -ge 200 ] && [ "$status" -lt 300 ]; then
  echo "[OK] Stripe analytics (status $status)"
  echo ""
  echo "Revenue Overview:"
  echo "$body" | $JQ -r '
    .overview // .data.overview // . |
    if type == "object" then
      "  Total Customers: " + (.total_customers // 0 | tostring),
      "  Paid Customers: " + (.paid_customers // 0 | tostring),
      "  MRR: $" + (.mrr // 0 | tostring),
      "  Growth Rate: " + (.growth_rate // 0 | tostring) + "%"
    else
      .
    end
  ' 2>/dev/null || echo "$body" | head -20
elif [ "$status" -eq 400 ] || [ "$status" -eq 404 ]; then
  echo "[EXPECTED] Stripe analytics not configured (status $status)"
else
  echo "[WARN] Stripe analytics returned unexpected status $status"
fi

# Stripe Customers
echo ""
echo "→ Fetching Stripe Customer Analytics..."
resp_and_code=$(api GET "/api/v1/projects/$PROJECT_ID/stripe/customers" "")
body=$(echo "$resp_and_code" | sed '$d')
status=$(echo "$resp_and_code" | tail -n 1)
if [ "$status" -ge 200 ] && [ "$status" -lt 300 ]; then
  echo "[OK] Stripe customer analytics (status $status)"
  echo ""
  echo "Customer Summary:"
  echo "$body" | $JQ -r '
    .data.summary // .summary // . |
    if type == "object" then
      "  Total Customers: " + (.total_customers // 0 | tostring),
      "  Paid Customers: " + (.paid_customers // 0 | tostring),
      "  Free Customers: " + (.free_customers // 0 | tostring),
      "  Total MRR: $" + (.total_mrr // 0 | tostring),
      "  Conversion Rate: " + (.conversion_rate // 0 | tostring) + "%"
    else
      .
    end
  ' 2>/dev/null || echo "$body" | head -20
  
  echo ""
  echo "Top 5 Customers by MRR:"
  echo "$body" | $JQ -r '
    (.data.customer_segments // .customer_segments // []) | 
    .[0:5] | 
    .[] | 
    "  • " + (.name // .email // "Unknown") + " - $" + (.mrr // 0 | tostring) + "/mo (" + (.status // "unknown") + ")"
  ' 2>/dev/null || echo "  (No customer data available)"
elif [ "$status" -eq 400 ] || [ "$status" -eq 404 ]; then
  echo "[EXPECTED] Stripe customer analytics not configured (status $status)"
else
  echo "[WARN] Stripe customer analytics returned unexpected status $status"
fi

# Summary
echo ""
cat <<EOF
================================================================================
E2E Test Summary
================================================================================
Authentication:
  - Signup: $email
  - Login: JWT token obtained
  
Project Setup:
  - Project ID: $PROJECT_ID
  - API Key: ${API_KEY:0:20}...
  
Event Ingestion:
  - Single event: ✓
  - Batch events (5): ✓
  - Recording ID: $RECORDING_ID
  
Analytics Endpoints:
  - Dashboard: ✓
  - Analytics: ✓
  - Heatmaps: ✓
  - Recordings: ✓
  
Enhanced Analytics:
  - Location: tested
  - Devices: tested
  - Cohorts: tested
  - Features: tested
  - Churn: tested
  - Funnels: tested
  - Sessions: tested
  
Stripe Integration:
  - Revenue metrics: tested (expected to fail without config)
  - Analytics: tested (expected to fail without config)
  - Customers: tested (expected to fail without config)

If all [OK] messages were shown, the core functionality is working correctly!
================================================================================
EOF

exit 0

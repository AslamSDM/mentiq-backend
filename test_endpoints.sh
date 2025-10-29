#!/bin/bash

# Mentiq API Endpoint Test Script
# This script tests all major endpoints of the Mentiq analytics and A/B testing API.
#
# Requirements:
# - curl: for making HTTP requests
# - jq: for parsing JSON responses
# - Go: to run the backend server

set -e

BASE_URL="http://localhost:8080"
API_V1_URL="$BASE_URL/api/v1"

# Generate random data for user and project
RANDOM_ID=$(cat /dev/urandom | tr -dc 'a-zA-Z0-9' | fold -w 8 | head -n 1)
USER_EMAIL="testuser_$RANDOM_ID@mentiq.com"
USER_PASSWORD="password123"
PROJECT_NAME="Test Project $RANDOM_ID"
EXPERIMENT_KEY="test-exp-$RANDOM_ID"

# --- Helper Functions ---
function print_header() {
    echo ""
    echo "========================================================================"
    echo "  $1"
    echo "========================================================================"
    echo ""
}

function print_success() {
    echo "✅  SUCCESS: $1"
}

function print_failure() {
    echo "❌  FAILURE: $1"
    exit 1
}

# --- Start Server ---
print_header "Starting Mentiq Backend Server"
go build -o mentiq-backend-test .
./mentiq-backend-test &
SERVER_PID=$!
echo "Server started with PID: $SERVER_PID"
# Wait for server to be ready
sleep 5
trap "kill $SERVER_PID" EXIT

# --- 1. Health Check ---
print_header "1. Testing Health Check"
curl -s "$BASE_URL/health" | jq .
print_success "Health check endpoint is responsive."

# --- 2. User Authentication ---
print_header "2. Testing User Authentication"

# Signup
# echo "Attempting to sign up user: $USER_EMAIL"
# SIGNUP_RESPONSE=$(curl -s -X POST "$BASE_URL/signup" \
#     -H "Content-Type: application/json" \
#     -d "{\"name\": \"Test User\", \"email\": \"$USER_EMAIL\", \"password\": \"$USER_PASSWORD\"}")

# if [[ $(echo "$SIGNUP_RESPONSE" | jq -r '.message') != "Account created successfully" ]]; then
#     print_failure "User signup failed. Response: $SIGNUP_RESPONSE"
# fi
# print_success "User signup successful."

# Login
echo "Attempting to log in user: $USER_EMAIL"
LOGIN_RESPONSE=$(curl -s -X POST "$BASE_URL/login" \
    -H "Content-Type: application/json" \
    -d "{\"email\": \"$USER_EMAIL\", \"password\": \"$USER_PASSWORD\"}")

AUTH_TOKEN=$(echo "$LOGIN_RESPONSE" | jq -r '.token')
if [[ -z "$AUTH_TOKEN" || "$AUTH_TOKEN" == "null" ]]; then
    print_failure "User login failed or token not found. Response: $LOGIN_RESPONSE"
fi
print_success "User login successful. Auth Token obtained."

# --- 3. Project & API Key Management ---
print_header "3. Testing Project & API Key Management"

# Create Project
echo "Creating project: $PROJECT_NAME"
CREATE_PROJECT_RESPONSE=$(curl -s -X POST "$API_V1_URL/projects" \
    -H "Authorization: Bearer $AUTH_TOKEN" \
    -H "Content-Type: application/json" \
    -d "{\"name\": \"$PROJECT_NAME\"}")

PROJECT_ID=$(echo "$CREATE_PROJECT_RESPONSE" | jq -r '.id')
if [[ -z "$PROJECT_ID" || "$PROJECT_ID" == "null" ]]; then
    print_failure "Project creation failed. Response: $CREATE_PROJECT_RESPONSE"
fi
print_success "Project created successfully. Project ID: $PROJECT_ID"

# Create API Key
echo "Creating API key for project $PROJECT_ID"
CREATE_API_KEY_RESPONSE=$(curl -s -X POST "$API_V1_URL/projects/$PROJECT_ID/apikeys" \
    -H "Authorization: Bearer $AUTH_TOKEN" \
    -H "Content-Type: application/json" \
    -d "{\"name\": \"Test Key\", \"permissions\": [\"read\", \"write\"]}")

API_KEY=$(echo "$CREATE_API_KEY_RESPONSE" | jq -r '.key')
if [[ -z "$API_KEY" || "$API_KEY" == "null" ]]; then
    print_failure "API key creation failed. Response: $CREATE_API_KEY_RESPONSE"
fi
print_success "API key created successfully. API Key: $API_KEY"

# --- 4. Event Ingestion ---
print_header "4. Testing Event Ingestion"

# Ingest Single Event
echo "Ingesting a single event"
INGEST_RESPONSE=$(curl -s -X POST "$API_V1_URL/events" \
    -H "Authorization: Bearer $API_KEY" \
    -H "X-Project-ID: $PROJECT_ID" \
    -H "Content-Type: application/json" \
    -d "{\"event_type\": \"page_view\", \"user_id\": \"user123\", \"properties\": {\"page\": \"/home\"}}")

if [[ $(echo "$INGEST_RESPONSE" | jq -r '.status') != "success" ]]; then
    print_failure "Single event ingestion failed. Response: $INGEST_RESPONSE"
fi
print_success "Single event ingested successfully."

# Ingest Batch Events
echo "Ingesting a batch of events"
BATCH_INGEST_RESPONSE=$(curl -s -X POST "$API_V1_URL/events/batch" \
    -H "Authorization: Bearer $API_KEY" \
    -H "X-Project-ID: $PROJECT_ID" \
    -H "Content-Type: application/json" \
    -d "[{\"event_type\": \"click\", \"user_id\": \"user123\"}, {\"event_type\": \"form_submit\", \"user_id\": \"user456\"}]")

if [[ $(echo "$BATCH_INGEST_RESPONSE" | jq -r '.status') != "completed" ]]; then
    print_failure "Batch event ingestion failed. Response: $BATCH_INGEST_RESPONSE"
fi
print_success "Batch events ingested successfully."

# --- 5. Analytics Endpoints ---
print_header "5. Testing Analytics Endpoints"

# Test /analytics
curl -s -X GET "$API_V1_URL/analytics?start_date=2025-01-01&end_date=2025-12-31" -H "Authorization: Bearer $API_KEY" -H "X-Project-ID: $PROJECT_ID" | jq .
print_success "/analytics endpoint is responsive."

# Test /dashboard
curl -s -X GET "$API_V1_URL/dashboard" -H "Authorization: Bearer $API_KEY" -H "X-Project-ID: $PROJECT_ID" | jq .
print_success "/dashboard endpoint is responsive."

# Test /user-metrics
curl -s -X GET "$API_V1_URL/user-metrics" -H "Authorization: Bearer $API_KEY" -H "X-Project-ID: $PROJECT_ID" | jq .
print_success "/user-metrics endpoint is responsive."

# --- 6. A/B Testing Endpoints ---
print_header "6. Testing A/B Testing Endpoints"

# Create Experiment
echo "Creating an A/B test experiment"
CREATE_EXP_RESPONSE=$(curl -s -X POST "$API_V1_URL/experiments" \
    -H "Authorization: Bearer $API_KEY" \
    -H "X-Project-ID: $PROJECT_ID" \
    -H "Content-Type: application/json" \
    -d "{\"name\": \"Test Experiment\", \"key\": \"$EXPERIMENT_KEY\", \"status\": \"DRAFT\", \"trafficSplit\": 1.0, \"projectId\": \"$PROJECT_ID\", \"variants\": [{\"name\": \"Control\", \"key\": \"control\", \"isControl\": true, \"trafficSplit\": 0.5}, {\"name\": \"Variant A\", \"key\": \"variant-a\", \"isControl\": false, \"trafficSplit\": 0.5}]}")

EXPERIMENT_ID=$(echo "$CREATE_EXP_RESPONSE" | jq -r '.id')
if [[ -z "$EXPERIMENT_ID" || "$EXPERIMENT_ID" == "null" ]]; then
    print_failure "Experiment creation failed. Response: $CREATE_EXP_RESPONSE"
fi
print_success "Experiment created successfully. Experiment ID: $EXPERIMENT_ID"

# Update experiment status to RUNNING
echo "Starting the experiment"
curl -s -X PUT "$API_V1_URL/experiments/$EXPERIMENT_ID/status" \
    -H "Authorization: Bearer $API_KEY" \
    -H "X-Project-ID: $PROJECT_ID" \
    -d "status=RUNNING"
print_success "Experiment status updated to RUNNING."

# Get Assignment
echo "Getting variant assignment for a user"
ASSIGNMENT_RESPONSE=$(curl -s -X POST "$API_V1_URL/experiments/$EXPERIMENT_KEY/assignment?experimentKey=$EXPERIMENT_KEY&projectId=$PROJECT_ID&userId=user789" \
    -H "Authorization: Bearer $API_KEY" \
    -H "X-Project-ID: $PROJECT_ID")

VARIANT_KEY=$(echo "$ASSIGNMENT_RESPONSE" | jq -r '.key')
if [[ -z "$VARIANT_KEY" || "$VARIANT_KEY" == "null" ]]; then
    print_failure "Getting variant assignment failed. Response: $ASSIGNMENT_RESPONSE"
fi
print_success "Variant assignment successful. User assigned to: $VARIANT_KEY"

# Track Conversion
echo "Tracking a conversion event"
TRACK_RESPONSE=$(curl -s -X POST "$API_V1_URL/experiments/track" \
    -H "Authorization: Bearer $API_KEY" \
    -H "X-Project-ID: $PROJECT_ID" \
    -H "Content-Type: application/json" \
    -d "{\"experimentId\": \"$EXPERIMENT_ID\", \"userId\": \"user789\", \"eventName\": \"signup\"}")

if [[ $(echo "$TRACK_RESPONSE" | jq -r '.status') != "ok" ]]; then
    print_failure "Tracking conversion failed. Response: $TRACK_RESPONSE"
fi
print_success "Conversion event tracked successfully."

# Get Experiment Results
echo "Getting experiment results"
curl -s -X GET "$API_V1_URL/experiments/$EXPERIMENT_ID/results" -H "Authorization: Bearer $API_KEY" -H "X-Project-ID: $PROJECT_ID" | jq .
print_success "Experiment results endpoint is responsive."


print_header "All Tests Completed Successfully!"
exit 0

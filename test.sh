#!/bin/bash

# Analytics Platform Test Script
# This script tests the analytics platform endpoints

API_URL="http://localhost:8080"
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo -e "${YELLOW}Analytics Platform Test Script${NC}"
echo "Testing API at: $API_URL"
echo

# Function to test an endpoint
test_endpoint() {
    local method=$1
    local endpoint=$2
    local data=$3
    local description=$4
    
    echo -e "${YELLOW}Testing: $description${NC}"
    
    if [ -z "$data" ]; then
        response=$(curl -s -w "\nHTTP_STATUS:%{http_code}" "$API_URL$endpoint")
    else
        response=$(curl -s -w "\nHTTP_STATUS:%{http_code}" -X $method -H "Content-Type: application/json" -d "$data" "$API_URL$endpoint")
    fi
    
    http_status=$(echo "$response" | grep "HTTP_STATUS" | cut -d: -f2)
    body=$(echo "$response" | sed '/HTTP_STATUS/d')
    
    if [ "$http_status" -ge 200 ] && [ "$http_status" -lt 300 ]; then
        echo -e "${GREEN}✓ SUCCESS (HTTP $http_status)${NC}"
        echo "Response: $body"
    else
        echo -e "${RED}✗ FAILED (HTTP $http_status)${NC}"
        echo "Response: $body"
    fi
    echo
}

# Test 1: Health Check
test_endpoint "GET" "/health" "" "Health Check"

# Test 2: Single Event
single_event='{
  "event_type": "test_event",
  "user_id": "test_user_123",
  "session_id": "test_session_456",
  "properties": {
    "page": "/test",
    "action": "script_test"
  }
}'
test_endpoint "POST" "/api/v1/events" "$single_event" "Single Event Ingestion"

# Test 3: Batch Events
batch_events='[
  {
    "event_type": "page_view",
    "user_id": "batch_user_1",
    "properties": {"page": "/home"}
  },
  {
    "event_type": "click", 
    "user_id": "batch_user_1",
    "properties": {"element": "button"}
  }
]'
test_endpoint "POST" "/api/v1/events/batch" "$batch_events" "Batch Event Ingestion"

# Test 4: Invalid Event (missing event_type)
invalid_event='{
  "user_id": "test_user",
  "properties": {"test": "data"}
}'
test_endpoint "POST" "/api/v1/events" "$invalid_event" "Invalid Event (should fail)"

echo -e "${YELLOW}Test completed!${NC}"

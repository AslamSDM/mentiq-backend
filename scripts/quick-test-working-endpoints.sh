#!/bin/bash

# 🧪 Quick Analytics Test - Working Endpoints Only
# Tests only the confirmed working endpoints

set -e

BASE_URL="http://localhost:8080"
PROJECT_ID="${PROJECT_ID:-test_project_$(date +%s)}"
API_KEY="${API_KEY:-test_api_key_123}"
AUTH_HEADER="Authorization: Bearer ${API_KEY}"
PROJECT_HEADER="X-Project-ID: ${PROJECT_ID}"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

TOTAL_TESTS=0
PASSED_TESTS=0

echo -e "${BLUE}🧪 Quick Analytics Test - Working Endpoints${NC}"
echo -e "${CYAN}📊 Base URL: $BASE_URL${NC}"
echo -e "${CYAN}📁 Project ID: $PROJECT_ID${NC}"
echo ""

call_api() {
    local method=$1
    local endpoint=$2
    local data=$3
    local description=$4
    local expected_status=${5:-200}
    
    TOTAL_TESTS=$((TOTAL_TESTS + 1))
    
    echo -ne "${YELLOW}🔍 Testing: $description${NC} ... "
    
    local curl_cmd="curl -s --max-time 10 -w \"\\n%{http_code}\" \
        -H \"${AUTH_HEADER}\" \
        -H \"${PROJECT_HEADER}\" \
        -H \"Content-Type: application/json\""
    
    if [ "$method" = "GET" ]; then
        response=$(eval "$curl_cmd \"$BASE_URL$endpoint\"" 2>/dev/null) || response="Connection failed"
    else
        response=$(eval "$curl_cmd -X $method -d '$data' \"$BASE_URL$endpoint\"" 2>/dev/null) || response="Connection failed"
    fi
    
    if [ -n "$response" ] && [ "$response" != "Connection failed" ]; then
        http_code=$(echo "$response" | tail -n1)
        body=$(echo "$response" | sed '$d')
    else
        http_code="0"
        body="Connection failed"
    fi
    
    if [ "$http_code" = "$expected_status" ]; then
        echo -e "${GREEN}✅ PASS ($http_code)${NC}"
        PASSED_TESTS=$((PASSED_TESTS + 1))
    else
        echo -e "${RED}❌ FAIL ($http_code, expected $expected_status)${NC}"
        if [ -n "$body" ]; then
            echo -e "   📄 $body"
        fi
    fi
}

# Test connectivity
echo -e "${YELLOW}🔗 Checking server connectivity...${NC}"
if curl -s --max-time 3 "$BASE_URL/health" > /dev/null 2>&1; then
    echo -e "${GREEN}✅ Server is responding${NC}"
else
    echo -e "${RED}❌ Cannot connect to server${NC}"
    exit 1
fi
echo ""

# Test working endpoints
echo -e "${BLUE}=== HEALTH CHECK ===${NC}"
call_api "GET" "/health" "" "Health Check"
echo ""

echo -e "${BLUE}=== EVENT INGESTION ===${NC}"
call_api "POST" "/api/v1/events" '{"event_type":"page_view","user_id":"test_user","properties":{"page_url":"/test"}}' "Event Ingestion"
echo ""

echo -e "${BLUE}=== ANALYTICS ENDPOINTS ===${NC}"
call_api "GET" "/api/v1/analytics" "" "Basic Analytics"
call_api "GET" "/api/v1/dashboard" "" "Dashboard Analytics"
call_api "GET" "/api/v1/realtime" "" "Real-time Analytics"
call_api "GET" "/api/v1/user-metrics" "" "User Metrics (DAU/WAU/MAU)"
echo ""

echo -e "${BLUE}=== PROJECT-SPECIFIC ANALYTICS ===${NC}"
call_api "GET" "/api/v1/projects/$PROJECT_ID/analytics/location" "" "Location Analytics"
call_api "GET" "/api/v1/projects/$PROJECT_ID/analytics/devices" "" "Device Analytics"
call_api "GET" "/api/v1/projects/$PROJECT_ID/analytics/features" "" "Feature Analytics"
call_api "GET" "/api/v1/projects/$PROJECT_ID/heatmaps" "" "Heatmaps"
call_api "GET" "/api/v1/projects/$PROJECT_ID/sessions" "" "Sessions List"
echo ""

# Summary
echo -e "${BLUE}=== SUMMARY ===${NC}"
echo -e "📊 Total Tests: $TOTAL_TESTS"
echo -e "✅ Passed: $PASSED_TESTS"
echo -e "❌ Failed: $((TOTAL_TESTS - PASSED_TESTS))"

success_rate=$((PASSED_TESTS * 100 / TOTAL_TESTS))
echo -e "📈 Success Rate: ${success_rate}%"

if [ $PASSED_TESTS -eq $TOTAL_TESTS ]; then
    echo -e "${GREEN}🎉 All tests passed!${NC}"
else
    echo -e "${YELLOW}⚠️  Some tests failed. This is expected if you don't have valid API keys/project setup.${NC}"
fi
#!/bin/bash

# 🧪 Comprehensive Mentiq Analytics Test Suite
# Tests all analytics endpoints including R2 storage, enhanced analytics, sessions, and heatmaps

set -e  # Exit on any error

BASE_URL="http://localhost:8080"
ACCOUNT_ID="test_account_$(date +%s)"
PROJECT_ID="test_project_$(date +%s)"

# Authentication - You'll need to update these with real values
API_KEY="${API_KEY:-test_api_key_123}"
AUTH_HEADER="Authorization: Bearer ${API_KEY}"
PROJECT_HEADER="X-Project-ID: ${PROJECT_ID}"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
PURPLE='\033[0;35m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color

# Test counters
TOTAL_TESTS=0
PASSED_TESTS=0
FAILED_TESTS=0

echo -e "${BLUE}🧪 Mentiq Analytics Comprehensive Test Suite${NC}"
echo -e "${CYAN}📊 Base URL: $BASE_URL${NC}"
echo -e "${CYAN}🏢 Account ID: $ACCOUNT_ID${NC}"
echo -e "${CYAN}📁 Project ID: $PROJECT_ID${NC}"
echo ""

# Helper function for API calls
call_api() {
    local method=$1
    local endpoint=$2
    local data=$3
    local description=$4
    local expected_status=${5:-200}
    
    TOTAL_TESTS=$((TOTAL_TESTS + 1))
    
    echo -ne "${YELLOW}🔍 Testing: $description${NC} ... "
    
    # Build curl command with timeout and better error handling
    local curl_cmd="curl -s --max-time 10 --connect-timeout 5 -w \"\\n%{http_code}\" \
        -H \"${AUTH_HEADER}\" \
        -H \"${PROJECT_HEADER}\" \
        -H \"X-Account-ID: $ACCOUNT_ID\" \
        -H \"Content-Type: application/json\""
    
    # Execute curl command with error handling
    local curl_exit_code=0
    if [ "$method" = "GET" ]; then
        response=$(eval "$curl_cmd \"$BASE_URL$endpoint\"" 2>/dev/null) || curl_exit_code=$?
    else
        response=$(eval "$curl_cmd -X $method -d '$data' \"$BASE_URL$endpoint\"" 2>/dev/null) || curl_exit_code=$?
    fi
    
    # Parse response more safely
    if [ $curl_exit_code -eq 0 ] && [ -n "$response" ]; then
        http_code=$(echo "$response" | tail -n1)
        body=$(echo "$response" | sed '$d')  # Remove last line (safer than head -n -1)
    else
        http_code="0"
        body="Connection failed (curl exit code: $curl_exit_code)"
    fi
    
    if [ "$http_code" = "$expected_status" ]; then
        echo -e "${GREEN}✅ PASS ($http_code)${NC}"
        PASSED_TESTS=$((PASSED_TESTS + 1))
        
        # Show abbreviated response for successful tests
        if [ ${#body} -gt 100 ]; then
            echo -e "   ${CYAN}📄 $(echo "$body" | cut -c1-80)...${NC}"
        else
            echo -e "   ${CYAN}📄 $body${NC}"
        fi
    else
        echo -e "${RED}❌ FAIL ($http_code, expected $expected_status)${NC}"
        FAILED_TESTS=$((FAILED_TESTS + 1))
        echo -e "   ${RED}📄 $body${NC}"
    fi
    echo ""
}

# Wait for server
echo -e "${PURPLE}🚀 Make sure the analytics server is running!${NC}"
echo -e "${PURPLE}   Start with: ./start-r2-analytics.sh${NC}"
echo -e "${PURPLE}   Or run: go run analytics_server.go r2_analytics.go enhanced_analytics_handlers.go session_heatmap_handlers.go models.go${NC}"
echo ""
read -p "Press Enter when server is ready..."
echo ""

# Quick connectivity check
echo -e "${YELLOW}🔗 Checking server connectivity...${NC}"
if curl -s --max-time 3 "$BASE_URL/health" > /dev/null 2>&1; then
    echo -e "${GREEN}✅ Server is responding${NC}"
else
    echo -e "${RED}❌ Cannot connect to server at $BASE_URL${NC}"
    echo -e "${RED}   Make sure the server is running and accessible${NC}"
    exit 1
fi
echo ""

echo -e "${BLUE}=== 0. TEST SETUP ===${NC}"
echo -e "${PURPLE}🔧 Setting up test data (account, project, API key)...${NC}"
echo -e "${YELLOW}ℹ️  For now, using mock credentials. In production, you'll need:${NC}"
echo -e "${YELLOW}   1. Create an account via signup${NC}"
echo -e "${YELLOW}   2. Create a project for the account${NC}"
echo -e "${YELLOW}   3. Generate an API key for the project${NC}"
echo -e "${YELLOW}   4. Set API_KEY environment variable${NC}"
echo -e "${YELLOW}   Example: API_KEY=your_real_api_key ./test-all-analytics.sh${NC}"
echo ""

echo -e "${BLUE}=== 1. HEALTH CHECK ===${NC}"

call_api "GET" "/health" "" "Health Check"

echo -e "${BLUE}=== 2. EVENT INGESTION TESTS ===${NC}"

# Basic event
basic_event='{
    "event_type": "page_view",
    "user_id": "test_user_1",
    "session_id": "test_session_1",
    "properties": {
        "page_url": "/dashboard",
        "page_title": "Analytics Dashboard",
        "referrer": "google.com"
    }
}'
call_api "POST" "/api/v1/events" "$basic_event" "Basic Event Ingestion"

# User signup event
signup_event='{
    "event_type": "signup",
    "user_id": "test_user_2",
    "properties": {
        "source": "organic",
        "plan": "free"
    }
}'
call_api "POST" "/api/v1/events" "$signup_event" "Signup Event"

# Feature usage events
feature_events=(
    '{"event_type":"onboarding_complete","user_id":"test_user_1","properties":{"steps_completed":5}}'
    '{"event_type":"first_action","user_id":"test_user_1","properties":{"action":"create_project"}}'
    '{"event_type":"login","user_id":"test_user_2","properties":{"device":"mobile","location":"US"}}'
    '{"event_type":"click","user_id":"test_user_1","properties":{"element":"pricing_button","page":"/pricing"}}'
    '{"event_type":"purchase","user_id":"test_user_2","properties":{"amount":29.99,"plan":"pro"}}'
    '{"event_type":"page_view","user_id":"test_user_3","properties":{"page_url":"/features","country":"Canada"}}'
)

for i in "${!feature_events[@]}"; do
    call_api "POST" "/api/v1/events" "${feature_events[i]}" "Feature Event $((i+1))"
done

echo -e "${BLUE}=== 3. ENHANCED ANALYTICS TESTS ===${NC}"

# Location Analytics
call_api "GET" "/api/v1/projects/$PROJECT_ID/analytics/location" "" "Location Analytics"
call_api "GET" "/api/v1/projects/$PROJECT_ID/analytics/location?start_date=2024-01-01&end_date=2024-12-31" "" "Location Analytics with Date Range"

# Device Analytics
call_api "GET" "/api/v1/projects/$PROJECT_ID/analytics/devices" "" "Device Analytics"

# Retention Analytics
call_api "GET" "/api/v1/analytics/retention" "" "User Retention Analytics"
call_api "GET" "/api/v1/analytics/retention?start_date=2024-01-01" "" "Retention with Custom Date"

# Feature Adoption Analytics
call_api "GET" "/api/v1/projects/$PROJECT_ID/analytics/features" "" "Feature Adoption Analytics"

# Churn Analysis
call_api "GET" "/api/v1/projects/$PROJECT_ID/analytics/churn" "" "Churn Analysis"
call_api "GET" "/api/v1/projects/$PROJECT_ID/analytics/churn?churn_days=14" "" "Churn Analysis (14-day threshold)"

# Conversion Analytics
call_api "GET" "/api/v1/projects/$PROJECT_ID/analytics/funnels" "" "Conversion Analytics"

# Dashboard Analytics (Legacy compatibility)
call_api "GET" "/api/v1/dashboard" "" "Dashboard Analytics (Legacy)"

echo -e "${BLUE}=== 4. SESSION RECORDING TESTS ===${NC}"

# Start a session
session_start='{
    "user_id": "test_user_session",
    "user_agent": "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36",
    "url": "/dashboard",
    "viewport": {
        "width": 1920,
        "height": 1080
    },
    "metadata": {
        "test": true,
        "browser": "Chrome"
    }
}'
call_api "POST" "/api/v1/sessions/start" "$session_start" "Start Session Recording"

# Use a fixed session ID for testing
SESSION_ID="test_session_$(date +%s)"

# Record individual event
session_event='{
    "type": "click",
    "x": 150,
    "y": 300,
    "element": "nav-button",
    "timestamp": "'$(date -u +%Y-%m-%dT%H:%M:%S.%3NZ)'"
}'
call_api "POST" "/api/v1/sessions/$SESSION_ID/events" "$session_event" "Record Session Event"

# Record batch of events
batch_events='{
    "events": [
        {
            "type": "mousemove",
            "x": 100,
            "y": 200,
            "timestamp": "'$(date -u +%Y-%m-%dT%H:%M:%S.%3NZ)'"
        },
        {
            "type": "click",
            "x": 250,
            "y": 400,
            "element": "submit-button",
            "timestamp": "'$(date -u +%Y-%m-%dT%H:%M:%S.%3NZ)'"
        },
        {
            "type": "scroll",
            "scrollY": 500,
            "timestamp": "'$(date -u +%Y-%m-%dT%H:%M:%S.%3NZ)'"
        }
    ]
}'
call_api "POST" "/api/v1/sessions/$SESSION_ID/batch" "$batch_events" "Record Event Batch"

# End session
session_end='{
    "duration": 120000,
    "events": [
        {
            "event_type": "session_end",
            "timestamp": "'$(date -u +%Y-%m-%dT%H:%M:%S.%3NZ)'"
        }
    ],
    "metadata": {
        "pages_visited": 3,
        "actions_taken": 5
    }
}'
call_api "PUT" "/api/v1/sessions/$SESSION_ID/end" "$session_end" "End Session"

# Get session data
call_api "GET" "/api/v1/sessions/$SESSION_ID" "" "Get Session Data"

# Get session recording
call_api "GET" "/api/v1/sessions/$SESSION_ID/recording" "" "Get Session Recording"

# List all sessions
call_api "GET" "/api/v1/sessions/" "" "List All Sessions"
call_api "GET" "/api/v1/sessions/?start_date=2024-01-01&end_date=2025-12-31" "" "List Sessions with Date Filter"

echo -e "${BLUE}=== 5. HEATMAP ANALYTICS TESTS ===${NC}"

# Record comprehensive heatmap data
heatmap_data='{
    "page_url": "/dashboard",
    "clicks": [
        {"x": 150, "y": 300, "count": 5, "element": "nav-menu"},
        {"x": 400, "y": 500, "count": 3, "element": "chart-area"},
        {"x": 250, "y": 200, "count": 8, "element": "header-logo"},
        {"x": 600, "y": 400, "count": 2, "element": "sidebar-item"}
    ],
    "scrolls": [
        {"depth": 0.25, "max_depth": 1.0},
        {"depth": 0.50, "max_depth": 1.0},
        {"depth": 0.75, "max_depth": 1.0},
        {"depth": 1.0, "max_depth": 1.0}
    ],
    "hovers": [
        {"x": 300, "y": 250, "duration": 2500, "element": "tooltip-trigger"},
        {"x": 450, "y": 350, "duration": 1200, "element": "info-icon"}
    ],
    "viewport_data": {
        "width": 1920,
        "height": 1080
    }
}'
call_api "POST" "/api/v1/heatmaps/record" "$heatmap_data" "Record Heatmap Data"

# Record individual click
click_data='{
    "page_url": "/pricing",
    "x": 320,
    "y": 480,
    "element": "price-card-pro"
}'
call_api "POST" "/api/v1/heatmaps/click" "$click_data" "Record Individual Click"

# Record scroll event
scroll_data='{
    "page_url": "/features",
    "depth": 0.65,
    "max_depth": 0.85
}'
call_api "POST" "/api/v1/heatmaps/scroll" "$scroll_data" "Record Scroll Event"

# Record more clicks for different pages
additional_clicks=(
    '{"page_url":"/dashboard","x":180,"y":350,"element":"settings-button"}'
    '{"page_url":"/dashboard","x":500,"y":280,"element":"notification-bell"}'
    '{"page_url":"/analytics","x":220,"y":450,"element":"filter-dropdown"}'
    '{"page_url":"/analytics","x":380,"y":520,"element":"export-button"}'
)

for click in "${additional_clicks[@]}"; do
    call_api "POST" "/api/v1/heatmaps/click" "$click" "Additional Click Event"
done

# Get heatmap for specific page
call_api "GET" "/api/v1/heatmaps/page?page_url=/dashboard" "" "Get Dashboard Heatmap"
call_api "GET" "/api/v1/heatmaps/page?page_url=/pricing" "" "Get Pricing Page Heatmap"
call_api "GET" "/api/v1/heatmaps/page?page_url=/dashboard&start_date=2024-01-01&end_date=2025-12-31" "" "Get Heatmap with Date Range"

# Get all heatmaps
call_api "GET" "/api/v1/heatmaps/all" "" "Get All Heatmaps"
call_api "GET" "/api/v1/heatmaps/all?start_date=2024-01-01&end_date=2025-12-31" "" "Get All Heatmaps with Date Filter"

# Legacy heatmap endpoint
call_api "GET" "/api/v1/heatmaps/?page_url=/dashboard" "" "Legacy Heatmap Endpoint"

echo -e "${BLUE}=== 6. ERROR HANDLING TESTS ===${NC}"

# Test invalid endpoints
call_api "GET" "/api/v1/nonexistent" "" "Non-existent Endpoint" 404

# Test missing required parameters
call_api "GET" "/api/v1/heatmaps/page" "" "Missing Required Parameter" 400

# Test invalid JSON
call_api "POST" "/api/v1/events" '{"invalid": json}' "Invalid JSON Body" 400

# Test invalid session ID
call_api "GET" "/api/v1/sessions/invalid_session_id" "" "Invalid Session ID" 404

echo -e "${BLUE}=== 7. PERFORMANCE AND CACHING TESTS ===${NC}"

# Test caching by calling same endpoint twice
echo -e "${YELLOW}🔄 Testing cache performance...${NC}"

start_time=$(date +%s)
call_api "GET" "/api/v1/projects/$PROJECT_ID/analytics" "" "Dashboard Analytics (First Call - Cache Miss)"
first_call_time=$(date +%s)

call_api "GET" "/api/v1/projects/$PROJECT_ID/analytics" "" "Dashboard Analytics (Second Call - Cache Hit)"
second_call_time=$(date +%s)

first_duration=$((first_call_time - start_time))
second_duration=$((second_call_time - first_call_time))

echo -e "${CYAN}⚡ Performance Results:${NC}"
echo -e "   First call (cache miss): ${first_duration}s"
echo -e "   Second call (cache hit): ${second_duration}s"

if [ $second_duration -lt $first_duration ]; then
    echo -e "   ${GREEN}✅ Caching is working (${second_duration}s < ${first_duration}s)${NC}"
else
    echo -e "   ${YELLOW}⚠️  Caching may not be optimal${NC}"
fi
echo ""

echo -e "${BLUE}=== 8. BULK DATA TESTS ===${NC}"

# Generate multiple events for better analytics
echo -e "${YELLOW}📊 Generating bulk test data...${NC}"

users=("user_bulk_1" "user_bulk_2" "user_bulk_3" "user_bulk_4" "user_bulk_5")
pages=("/dashboard" "/analytics" "/settings" "/profile" "/pricing")
events=("page_view" "click" "scroll" "hover" "form_submit")
countries=("US" "Canada" "UK" "Germany" "Australia")
devices=("desktop" "mobile" "tablet")

for i in {1..20}; do
    user=${users[$((RANDOM % ${#users[@]}))]}
    page=${pages[$((RANDOM % ${#pages[@]}))]}
    event=${events[$((RANDOM % ${#events[@]}))]}
    country=${countries[$((RANDOM % ${#countries[@]}))]}
    device=${devices[$((RANDOM % ${#devices[@]}))]}
    
    bulk_event="{
        \"event_type\": \"$event\",
        \"user_id\": \"$user\",
        \"session_id\": \"session_$i\",
        \"properties\": {
            \"page_url\": \"$page\",
            \"country\": \"$country\",
            \"device\": \"$device\",
            \"timestamp\": \"$(date -u +%Y-%m-%dT%H:%M:%S.%3NZ)\"
        }
    }"
    
    curl -s -H "X-Account-ID: $ACCOUNT_ID" \
         -H "X-Project-ID: $PROJECT_ID" \
         -H "Content-Type: application/json" \
         -X POST \
         -d "$bulk_event" \
         "$BASE_URL/api/v1/events" > /dev/null
done

echo -e "${GREEN}✅ Generated 20 bulk events${NC}"

# Test analytics with bulk data
echo -e "${YELLOW}📈 Testing analytics with bulk data...${NC}"
call_api "GET" "/api/v1/projects/$PROJECT_ID/analytics/location" "" "Location Analytics (with bulk data)"
call_api "GET" "/api/v1/projects/$PROJECT_ID/analytics/devices" "" "Device Analytics (with bulk data)"
call_api "GET" "/api/v1/projects/$PROJECT_ID/analytics/features" "" "Feature Analytics (with bulk data)"

echo -e "${BLUE}=== TEST SUMMARY ===${NC}"
echo ""
echo -e "${CYAN}📊 Test Results Summary:${NC}"
echo -e "   Total Tests: $TOTAL_TESTS"
echo -e "   ${GREEN}✅ Passed: $PASSED_TESTS${NC}"
echo -e "   ${RED}❌ Failed: $FAILED_TESTS${NC}"

success_rate=$(( (PASSED_TESTS * 100) / TOTAL_TESTS ))
echo -e "   Success Rate: $success_rate%"
echo ""

if [ $FAILED_TESTS -eq 0 ]; then
    echo -e "${GREEN}🎉 All tests passed! Analytics system is working perfectly.${NC}"
    exit 0
else
    echo -e "${RED}⚠️  Some tests failed. Check the output above for details.${NC}"
    exit 1
fi
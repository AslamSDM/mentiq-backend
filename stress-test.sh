#!/bin/bash

# Analytics API Stress Test Script
# Usage: ./stress-test.sh [concurrent_users] [requests_per_user] [server_url]

# Default values
CONCURRENT_USERS=${1:-10}
REQUESTS_PER_USER=${2:-100}
SERVER_URL=${3:-"http://localhost:8080"}
TOTAL_REQUESTS=$((CONCURRENT_USERS * REQUESTS_PER_USER))

echo "🚀 Starting Analytics API Stress Test"
echo "=================================="
echo "Concurrent Users: $CONCURRENT_USERS"
echo "Requests per User: $REQUESTS_PER_USER"
echo "Total Requests: $TOTAL_REQUESTS"
echo "Server URL: $SERVER_URL"
echo "=================================="

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Test data arrays
EVENT_TYPES=("page_view" "button_click" "form_submit" "user_signup" "purchase" "logout" "search" "download")
PAGES=("/home" "/dashboard" "/profile" "/settings" "/checkout" "/products" "/about" "/contact")
DEVICES=("desktop" "mobile" "tablet")
BROWSERS=("Chrome" "Firefox" "Safari" "Edge")

# Function to generate random user ID
generate_user_id() {
    echo "user_$(shuf -i 1000-9999 -n 1)"
}

# Function to generate random session ID
generate_session_id() {
    echo "session_$(openssl rand -hex 8)"
}

# Function to get random element from array
get_random_element() {
    local arr=("$@")
    local random_index=$((RANDOM % ${#arr[@]}))
    echo "${arr[$random_index]}"
}

# Function to generate test event
generate_event_json() {
    local user_id=$(generate_user_id)
    local session_id=$(generate_session_id)
    local event_type=$(get_random_element "${EVENT_TYPES[@]}")
    local page=$(get_random_element "${PAGES[@]}")
    local device=$(get_random_element "${DEVICES[@]}")
    local browser=$(get_random_element "${BROWSERS[@]}")
    
    cat <<EOF
{
    "event_type": "$event_type",
    "user_id": "$user_id",
    "session_id": "$session_id",
    "properties": {
        "page": "$page",
        "device": "$device",
        "browser": "$browser",
        "timestamp_client": "$(date -u +%Y-%m-%dT%H:%M:%S.%3NZ)",
        "test_run": true,
        "random_value": $((RANDOM % 1000))
    }
}
EOF
}

# Function to generate batch events
generate_batch_events() {
    local batch_size=${1:-5}
    echo "["
    for ((i=1; i<=batch_size; i++)); do
        generate_event_json
        if [ $i -lt $batch_size ]; then
            echo ","
        fi
    done
    echo "]"
}

# Function to test single event endpoint
test_single_event() {
    local user_id=$1
    local request_num=$2
    local start_time=$(date +%s%N)
    
    local response=$(curl -s -w "\n%{http_code}\n%{time_total}" \
        -X POST "$SERVER_URL/api/v1/events" \
        -H "Content-Type: application/json" \
        -H "User-Agent: StressTest-User-$user_id" \
        -d "$(generate_event_json)")
    
    local end_time=$(date +%s%N)
    local http_code=$(echo "$response" | tail -n 2 | head -n 1)
    local response_time=$(echo "$response" | tail -n 1)
    
    if [ "$http_code" = "200" ]; then
        echo "✅ User $user_id - Request $request_num: ${response_time}s"
    else
        echo "❌ User $user_id - Request $request_num: HTTP $http_code"
    fi
}

# Function to test batch endpoint
test_batch_events() {
    local user_id=$1
    local request_num=$2
    local batch_size=${3:-3}
    
    local response=$(curl -s -w "\n%{http_code}\n%{time_total}" \
        -X POST "$SERVER_URL/api/v1/events/batch" \
        -H "Content-Type: application/json" \
        -H "User-Agent: StressTest-Batch-User-$user_id" \
        -d "$(generate_batch_events $batch_size)")
    
    local http_code=$(echo "$response" | tail -n 2 | head -n 1)
    local response_time=$(echo "$response" | tail -n 1)
    
    if [ "$http_code" = "200" ]; then
        echo "✅ Batch User $user_id - Request $request_num: ${response_time}s (${batch_size} events)"
    else
        echo "❌ Batch User $user_id - Request $request_num: HTTP $http_code"
    fi
}

# Function to simulate one user
simulate_user() {
    local user_id=$1
    local requests=$2
    
    echo "🔄 Starting user $user_id with $requests requests"
    
    for ((i=1; i<=requests; i++)); do
        # Mix of single events and batch events (70% single, 30% batch)
        if [ $((RANDOM % 10)) -lt 7 ]; then
            test_single_event $user_id $i
        else
            test_batch_events $user_id $i $((RANDOM % 5 + 1))
        fi
        
        # Small random delay between requests (0-100ms)
        sleep 0.$(printf "%02d" $((RANDOM % 10)))
    done
    
    echo "✅ User $user_id completed all requests"
}

# Health check first
echo -e "${BLUE}🏥 Checking server health...${NC}"
health_response=$(curl -s "$SERVER_URL/health")
if [ $? -eq 0 ]; then
    echo -e "${GREEN}✅ Server is healthy${NC}"
    echo "$health_response" | jq . 2>/dev/null || echo "$health_response"
else
    echo -e "${RED}❌ Server health check failed. Is the server running?${NC}"
    exit 1
fi

# Create results directory
mkdir -p stress_test_results
TIMESTAMP=$(date +%Y%m%d_%H%M%S)
RESULTS_FILE="stress_test_results/stress_test_$TIMESTAMP.log"

echo -e "\n${YELLOW}📊 Starting stress test...${NC}"
echo "Results will be logged to: $RESULTS_FILE"

# Start timing
START_TIME=$(date +%s)

# Start all users in parallel
for ((i=1; i<=CONCURRENT_USERS; i++)); do
    simulate_user $i $REQUESTS_PER_USER >> "$RESULTS_FILE" 2>&1 &
done

# Wait for all background jobs to complete
wait

# End timing
END_TIME=$(date +%s)
DURATION=$((END_TIME - START_TIME))

echo -e "\n${GREEN}🎉 Stress test completed!${NC}"
echo "=================================="
echo "Duration: ${DURATION}s"
echo "Total Requests: $TOTAL_REQUESTS"
echo "Requests per second: $(echo "scale=2; $TOTAL_REQUESTS / $DURATION" | bc -l)"

# Analyze results
echo -e "\n${BLUE}📈 Results Summary:${NC}"
SUCCESS_COUNT=$(grep -c "✅" "$RESULTS_FILE")
ERROR_COUNT=$(grep -c "❌" "$RESULTS_FILE")

echo "Successful requests: $SUCCESS_COUNT"
echo "Failed requests: $ERROR_COUNT"
echo "Success rate: $(echo "scale=2; $SUCCESS_COUNT * 100 / $TOTAL_REQUESTS" | bc -l)%"

if [ $ERROR_COUNT -gt 0 ]; then
    echo -e "\n${RED}❌ Errors found:${NC}"
    grep "❌" "$RESULTS_FILE" | head -10
fi

echo -e "\n${YELLOW}📋 Full results saved to: $RESULTS_FILE${NC}"

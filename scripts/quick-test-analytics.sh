#!/bin/bash

# 🚀 Quick Analytics Test - Rapid testing for development

BASE_URL="http://localhost:8080"
ACCOUNT_ID="quick_test_account"
PROJECT_ID="quick_test_project"

# Colors
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m'

echo -e "${YELLOW}🚀 Quick Analytics Test${NC}"
echo ""

# Quick API call helper
quick_test() {
    local method=$1
    local endpoint=$2
    local data=$3
    local name=$4
    
    echo -ne "Testing $name... "
    
    if [ "$method" = "GET" ]; then
        response=$(curl -s -w "%{http_code}" \
            -H "X-Account-ID: $ACCOUNT_ID" \
            -H "X-Project-ID: $PROJECT_ID" \
            "$BASE_URL$endpoint")
    else
        response=$(curl -s -w "%{http_code}" \
            -X $method \
            -H "Content-Type: application/json" \
            -H "X-Account-ID: $ACCOUNT_ID" \
            -H "X-Project-ID: $PROJECT_ID" \
            -d "$data" \
            "$BASE_URL$endpoint")
    fi
    
    http_code="${response: -3}"
    if [ "$http_code" = "200" ]; then
        echo -e "${GREEN}✅${NC}"
    else
        echo -e "${RED}❌ ($http_code)${NC}"
    fi
}

# Basic connectivity
quick_test "GET" "/health" "" "Health Check"

# Event ingestion
event='{"event_type":"test","user_id":"user1","properties":{"test":true}}'
quick_test "POST" "/api/v1/events" "$event" "Event Ingestion"

# Analytics endpoints
quick_test "GET" "/api/v1/analytics/dashboard" "" "Dashboard"
quick_test "GET" "/api/v1/analytics/location" "" "Location"
quick_test "GET" "/api/v1/analytics/devices" "" "Devices"
quick_test "GET" "/api/v1/analytics/retention" "" "Retention"
quick_test "GET" "/api/v1/analytics/features" "" "Features"
quick_test "GET" "/api/v1/analytics/churn" "" "Churn"
quick_test "GET" "/api/v1/analytics/conversion" "" "Conversion"

# Session recording
session='{"user_id":"test","user_agent":"Test","url":"/test","viewport":{"width":1920,"height":1080}}'
quick_test "POST" "/api/v1/sessions/start" "$session" "Start Session"

# Heatmaps
click='{"page_url":"/test","x":100,"y":200}'
quick_test "POST" "/api/v1/heatmaps/click" "$click" "Record Click"
quick_test "GET" "/api/v1/heatmaps/all" "" "Get Heatmaps"

echo -e "\n${GREEN}Quick test complete!${NC}"
echo -e "For comprehensive testing, run: ${YELLOW}./test-all-analytics.sh${NC}"
#!/bin/bash

# Quick Stress Test - Lightweight version for rapid testing
echo "⚡ Quick Analytics Stress Test"
echo "============================="

SERVER_URL="http://localhost:8080"
API_KEY="cmfu4slqu00009kc5am5gcns9"  # This is actually the account ID
PROJECT_ID="cmfu4sm1y00019kc5j74httyf"

# Check server health
echo "🔍 Checking server..."
if ! curl -s -f "$SERVER_URL/health" > /dev/null; then
    echo "❌ Server not responding. Start with: go run main.go auth.go analytics.go"
    exit 1
fi
echo "✅ Server is healthy"

# Quick parallel event ingestion test
echo ""
echo "📤 Testing event ingestion (50 events in parallel)..."
for i in {1..50}; do
    (
        curl -s -X POST "$SERVER_URL/api/v1/events" \
        -H "Content-Type: application/json" \
        -H "Authorization: ApiKey $API_KEY" \
        -H "X-Project-ID: $PROJECT_ID" \
        -d "{
            \"event_type\": \"test_event\",
            \"user_id\": \"user_$((i % 10))\",
            \"session_id\": \"sess_$i\",
            \"properties\": {
                \"test_run\": true,
                \"batch_id\": $i
            }
        }" > /dev/null && echo "Event $i: ✅" || echo "Event $i: ❌"
    ) &
done
wait

echo ""
echo "📊 Testing dashboard calls (10 parallel calls)..."
for i in {1..10}; do
    (
        start_time=$(date +%s%N)
        response=$(curl -s -w "%{http_code}" -H "Authorization: ApiKey $API_KEY" -H "X-Project-ID: $PROJECT_ID" "$SERVER_URL/api/v1/dashboard")
        end_time=$(date +%s%N)
        duration=$(( (end_time - start_time) / 1000000 )) # Convert to milliseconds
        
        http_code="${response: -3}"
        if [ "$http_code" = "200" ]; then
            echo "Dashboard call $i: ✅ (${duration}ms)"
        else
            echo "Dashboard call $i: ❌ (HTTP $http_code)"
        fi
    ) &
done
wait

echo ""
echo "🧹 Triggering cache flush..."
curl -s -X POST -H "Authorization: ApiKey $API_KEY" -H "X-Project-ID: $PROJECT_ID" "$SERVER_URL/api/v1/flush-cache" | jq '.message' 2>/dev/null || echo "Cache flush triggered"

echo ""
echo "📈 Final dashboard call (post-flush)..."
start_time=$(date +%s%N)
curl -s -H "Authorization: ApiKey $API_KEY" -H "X-Project-ID: $PROJECT_ID" "$SERVER_URL/api/v1/dashboard" > /dev/null
end_time=$(date +%s%N)
duration=$(( (end_time - start_time) / 1000000 ))
echo "Dashboard response time: ${duration}ms"

echo ""
echo "🎯 Quick test completed!"
echo "   For comprehensive testing, use: ./run_stress_test.sh"
#!/bin/bash

# 100K Users Stress Test - Extreme Load Testing
echo "🚀 100K Users Analytics Stress Test"
echo "==================================="

# Configuration for 100k concurrent users simulation
SERVER_URL="http://localhost:8080"
API_KEY="cmfu4slqu00009kc5am5gcns9"
PROJECT_ID="cmfu4sm1y00019kc5j74httyf"

# Test configurations for different scenarios
echo "Select test scenario:"
echo "1. Quick burst (1 minute, extreme load)"
echo "2. Sustained load (5 minutes, high throughput)"
echo "3. Gradual ramp-up (10 minutes, realistic scaling)"
echo "4. Custom configuration"
echo ""
read -p "Enter choice (1-4): " choice

case $choice in
    1)
        # Quick burst - simulate 100k users hitting the system in 1 minute
        DURATION=1
        EVENT_INGESTORS=200
        DASHBOARD_CALLERS=50
        EVENTS_PER_SECOND=50
        DASHBOARD_CALLS_PER_MINUTE=60
        echo "🔥 Quick Burst Test: 100k+ events in 1 minute (single account, many users)"
        UNIQUE_ACCOUNTS=""
        ;;
    2)
        # Sustained load - maintain high throughput for 5 minutes
        DURATION=5
        EVENT_INGESTORS=100
        DASHBOARD_CALLERS=30
        EVENTS_PER_SECOND=50
        DASHBOARD_CALLS_PER_MINUTE=60
        echo "⚡ Sustained Load Test: ~1.5M events over 5 minutes (single account, many users)"
        UNIQUE_ACCOUNTS=""
        ;;
    3)
        # Gradual ramp-up - realistic scaling scenario
        DURATION=10
        EVENT_INGESTORS=50
        DASHBOARD_CALLERS=20
        EVENTS_PER_SECOND=40
        DASHBOARD_CALLS_PER_MINUTE=30
        echo "📈 Gradual Ramp-up Test: ~1.2M events over 10 minutes (single account, many users)"
        UNIQUE_ACCOUNTS=""
        ;;
    4)
        # Custom configuration
        echo "Enter custom configuration:"
        read -p "Duration (minutes): " DURATION
        read -p "Event ingestors: " EVENT_INGESTORS
        read -p "Dashboard callers: " DASHBOARD_CALLERS
        read -p "Events per second per worker: " EVENTS_PER_SECOND
        read -p "Dashboard calls per minute per worker: " DASHBOARD_CALLS_PER_MINUTE
        read -p "Use unique accounts? (y/N): " use_unique
        if [[ $use_unique =~ ^[Yy]$ ]]; then
            UNIQUE_ACCOUNTS="--unique-accounts --create-test-accounts"
            echo "⚠️  Note: This will attempt to create test accounts in the database"
        else
            UNIQUE_ACCOUNTS=""
        fi
        echo "🛠️ Custom Test Configuration"
        ;;
    *)
        echo "Invalid choice. Using default sustained load test."
        DURATION=5
        EVENT_INGESTORS=100
        DASHBOARD_CALLERS=30
        EVENTS_PER_SECOND=50
        DASHBOARD_CALLS_PER_MINUTE=60
        UNIQUE_ACCOUNTS=""
        ;;
esac

# Calculate expected load
TOTAL_EVENTS_PER_SECOND=$((EVENT_INGESTORS * EVENTS_PER_SECOND))
TOTAL_EVENTS=$((TOTAL_EVENTS_PER_SECOND * DURATION * 60))
TOTAL_DASHBOARD_CALLS_PER_SECOND=$((DASHBOARD_CALLERS * DASHBOARD_CALLS_PER_MINUTE / 60))

# Calculate simulated users (different calculation for single vs multiple accounts)
if [[ $UNIQUE_ACCOUNTS == *"--unique-accounts"* ]]; then
    TOTAL_UNIQUE_ACCOUNTS=$((EVENT_INGESTORS * 10 + DASHBOARD_CALLERS * 5))
    echo ""
    echo "👥 Multiple Accounts Simulation:"
    echo "   Event Accounts: $((EVENT_INGESTORS * 10)) (10 per worker)"
    echo "   Dashboard Accounts: $((DASHBOARD_CALLERS * 5)) (5 per worker)"
    echo "   Total Unique Accounts: $TOTAL_UNIQUE_ACCOUNTS"
    echo "   Users per Account: ~100"
    echo "   Estimated Total Users: $((TOTAL_UNIQUE_ACCOUNTS * 100))"
else
    TOTAL_USERS=$((EVENT_INGESTORS * 1000))
    echo ""
    echo "👥 Single Account Simulation:"
    echo "   Account: $API_KEY"
    echo "   Project: $PROJECT_ID"
    echo "   Unique Users: ~$TOTAL_USERS (1000 per worker)"
    echo "   User Pattern: Different users across workers"
fi

echo ""
echo "📊 Test Parameters:"
echo "   Duration: $DURATION minutes"
echo "   Event Workers: $EVENT_INGESTORS"
echo "   Dashboard Workers: $DASHBOARD_CALLERS"
echo "   Expected Events/sec: $TOTAL_EVENTS_PER_SECOND"
echo "   Expected Total Events: $TOTAL_EVENTS"
echo "   Expected Dashboard Calls/sec: $TOTAL_DASHBOARD_CALLS_PER_SECOND"
echo ""

# Warn about system resources
echo "⚠️  WARNING: This test will generate extreme load!"
echo "   - Monitor CPU, memory, and network usage"
echo "   - Ensure adequate system resources"
echo "   - Consider adjusting OS limits (ulimit -n 65536)"
echo ""

# Check system limits
CURRENT_ULIMIT=$(ulimit -n)
if [ "$CURRENT_ULIMIT" -lt 10000 ]; then
    echo "🚨 Current file descriptor limit is low: $CURRENT_ULIMIT"
    echo "   Recommend increasing with: ulimit -n 65536"
    echo ""
fi

read -p "Continue with the test? (y/N): " confirm
if [[ ! $confirm =~ ^[Yy]$ ]]; then
    echo "Test cancelled."
    exit 0
fi

# Check if server is running
echo "🔍 Checking server health..."
if ! curl -s -f "$SERVER_URL/health" > /dev/null; then
    echo "❌ Server is not responding at $SERVER_URL"
    echo "   Please make sure the analytics server is running:"
    echo "   go run main.go auth.go analytics.go"
    exit 1
fi
echo "✅ Server is healthy"

# Build stress test if needed
if [ ! -f "./stress_test_analytics" ] || [ "./stress_test_analytics.go" -nt "./stress_test_analytics" ]; then
    echo "🔨 Building stress test..."
    if ! go build -o stress_test_analytics stress_test_analytics.go; then
        echo "❌ Failed to build stress test"
        exit 1
    fi
    echo "✅ Stress test built successfully"
fi

# Create results directory
RESULTS_DIR="stress_test_results"
mkdir -p "$RESULTS_DIR"
TIMESTAMP=$(date +"%Y%m%d_%H%M%S")
RESULT_FILE="$RESULTS_DIR/100k_users_test_$TIMESTAMP.log"

echo ""
echo "🚀 Starting 100K Users Stress Test..."
echo "   Results will be saved to: $RESULT_FILE"
echo "   Press Ctrl+C to stop the test early"
echo ""

# Start system monitoring in background
echo "📊 Starting system monitoring..."
(
    echo "=== SYSTEM MONITORING START ===" >> "$RESULT_FILE.system"
    while true; do
        echo "$(date): $(top -l 1 -n 0 | grep "CPU usage" || echo "CPU: monitoring...")" >> "$RESULT_FILE.system"
        echo "$(date): $(ps aux | grep '[g]o run\|[s]tress_test' | wc -l) processes running" >> "$RESULT_FILE.system"
        sleep 5
    done
) &
MONITOR_PID=$!

# Function to cleanup on exit
cleanup() {
    echo ""
    echo "🛑 Stopping test and cleaning up..."
    kill $MONITOR_PID 2>/dev/null
    pkill -f stress_test_analytics 2>/dev/null
    echo "✅ Cleanup completed"
    exit 0
}

# Set trap for cleanup
trap cleanup SIGINT SIGTERM

# Run the extreme stress test
./stress_test_analytics \
  --server="$SERVER_URL" \
  --event-ingestors="$EVENT_INGESTORS" \
  --dashboard-callers="$DASHBOARD_CALLERS" \
  --duration="$DURATION" \
  --events-per-second="$EVENTS_PER_SECOND" \
  --dashboard-calls-per-minute="$DASHBOARD_CALLS_PER_MINUTE" \
  --api-key="$API_KEY" \
  --project-id="$PROJECT_ID" \
  $UNIQUE_ACCOUNTS \
  2>&1 | tee "$RESULT_FILE"

# Stop monitoring
kill $MONITOR_PID 2>/dev/null

echo ""
echo "🎯 100K Users Test Completed!"
echo "   Main results: $RESULT_FILE"
echo "   System monitoring: $RESULT_FILE.system"

# Generate performance summary
echo ""
echo "📈 Performance Summary:"
echo "========================================"

# Extract key metrics from the results
if [ -f "$RESULT_FILE" ]; then
    echo "📊 Event Ingestion:"
    grep "Total Events Sent:" "$RESULT_FILE" | tail -1
    grep "Successful:" "$RESULT_FILE" | grep "Event" | tail -1
    grep "Average Latency:" "$RESULT_FILE" | head -1
    grep "Throughput:" "$RESULT_FILE" | tail -1
    
    echo ""
    echo "📈 Dashboard Performance:"
    grep "Total Dashboard Calls:" "$RESULT_FILE" | tail -1
    grep "Successful:" "$RESULT_FILE" | grep -v "Event" | tail -1
    grep "Cache Hit Rate:" "$RESULT_FILE" | tail -1
    grep "Average Latency:" "$RESULT_FILE" | tail -1
    
    echo ""
    echo "🎯 Performance Analysis:"
    grep -A 10 "Performance Analysis:" "$RESULT_FILE" | grep -E "✅|⚠️|❌"
fi

echo ""
echo "💾 Server Status Check:"
echo "======================="

# Check server status after test
if curl -s -f "$SERVER_URL/health" > /dev/null; then
    echo "✅ Server is still responding"
    
    # Check cache status
    echo "📦 Checking cache status..."
    curl -s -H "Authorization: ApiKey $API_KEY" -H "X-Project-ID: $PROJECT_ID" \
         "$SERVER_URL/api/v1/flush-cache" | \
         jq -r '.message // "Cache status retrieved"' 2>/dev/null || echo "Cache flush triggered"
else
    echo "❌ Server appears to be unresponsive"
fi

echo ""
echo "📋 Next Steps:"
echo "=============="
echo "1. Review detailed results in $RESULT_FILE"
echo "2. Check system monitoring data in $RESULT_FILE.system"
echo "3. Analyze server logs for any errors or warnings"
echo "4. Monitor server recovery and cache rebuild"
echo "5. Consider optimizations based on bottlenecks identified"

# Cleanup any remaining processes
pkill -f stress_test_analytics 2>/dev/null
#!/bin/bash

# Analytics Backend Stress Test Runner
# This script runs comprehensive stress tests against the analytics backend

echo "🚀 Analytics Backend Stress Test Runner"
echo "======================================="

# Default configuration
SERVER_URL="http://localhost:8080"
EVENT_INGESTORS=10
DASHBOARD_CALLERS=5
TEST_DURATION=5
EVENTS_PER_SECOND=20
DASHBOARD_CALLS_PER_MINUTE=30
API_KEY="cmfu4slqu00009kc5am5gcns9"  # This is actually the account ID
PROJECT_ID="cmfu4sm1y00019kc5j74httyf"

# Parse command line arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --server)
            SERVER_URL="$2"
            shift 2
            ;;
        --event-ingestors)
            EVENT_INGESTORS="$2"
            shift 2
            ;;
        --dashboard-callers)
            DASHBOARD_CALLERS="$2"
            shift 2
            ;;
        --duration)
            TEST_DURATION="$2"
            shift 2
            ;;
        --events-per-second)
            EVENTS_PER_SECOND="$2"
            shift 2
            ;;
        --dashboard-calls-per-minute)
            DASHBOARD_CALLS_PER_MINUTE="$2"
            shift 2
            ;;
        --api-key)
            API_KEY="$2"
            shift 2
            ;;
        --project-id)
            PROJECT_ID="$2"
            shift 2
            ;;
        --help)
            echo "Usage: $0 [OPTIONS]"
            echo ""
            echo "Options:"
            echo "  --server URL                    Server URL (default: http://localhost:8080)"
            echo "  --event-ingestors N            Number of event ingestion workers (default: 10)"
            echo "  --dashboard-callers N          Number of dashboard calling workers (default: 5)"
            echo "  --duration MINUTES             Test duration in minutes (default: 5)"
            echo "  --events-per-second N          Events per second per worker (default: 20)"
            echo "  --dashboard-calls-per-minute N Dashboard calls per minute per worker (default: 30)"
            echo "  --api-key KEY                  API key for authentication (account ID)"
            echo "  --project-id ID                Project ID"
            echo "  --help                         Show this help message"
            echo ""
            echo "Examples:"
            echo "  $0                                           # Run with default settings"
            echo "  $0 --duration 10 --event-ingestors 20       # 10-minute test with 20 ingestors"
            echo "  $0 --server http://prod.example.com         # Test against production server"
            exit 0
            ;;
        *)
            echo "Unknown option: $1"
            echo "Use --help for usage information"
            exit 1
            ;;
    esac
done

echo "📋 Test Configuration:"
echo "   Server: $SERVER_URL"
echo "   Event Ingestors: $EVENT_INGESTORS"
echo "   Dashboard Callers: $DASHBOARD_CALLERS"
echo "   Duration: $TEST_DURATION minutes"
echo "   Events/second: $EVENTS_PER_SECOND"
echo "   Dashboard calls/minute: $DASHBOARD_CALLS_PER_MINUTE"
echo "   API Key: $API_KEY"
echo "   Project ID: $PROJECT_ID"
echo ""

# Check if server is running
echo "🔍 Checking server health..."
if ! curl -s -f "$SERVER_URL/health" > /dev/null; then
    echo "❌ Server is not responding at $SERVER_URL"
    echo "   Please make sure the analytics server is running:"
    echo "   cd /path/to/mentiq-backend && go run main.go auth.go analytics.go"
    exit 1
fi
echo "✅ Server is healthy"
echo ""

# Check if stress test binary needs to be built
if [ ! -f "./stress_test_analytics" ] || [ "./stress_test_analytics.go" -nt "./stress_test_analytics" ]; then
    echo "🔨 Building stress test..."
    if ! go build -o stress_test_analytics stress_test_analytics.go; then
        echo "❌ Failed to build stress test"
        exit 1
    fi
    echo "✅ Stress test built successfully"
    echo ""
fi

# Create results directory
RESULTS_DIR="stress_test_results"
mkdir -p "$RESULTS_DIR"

# Generate timestamp for this test run
TIMESTAMP=$(date +"%Y%m%d_%H%M%S")
RESULT_FILE="$RESULTS_DIR/stress_test_$TIMESTAMP.log"

echo "📊 Starting stress test..."
echo "   Results will be saved to: $RESULT_FILE"
echo ""

# We need to pass the command line arguments to the Go program
./stress_test_analytics \
  --server="$SERVER_URL" \
  --event-ingestors="$EVENT_INGESTORS" \
  --dashboard-callers="$DASHBOARD_CALLERS" \
  --duration="$TEST_DURATION" \
  --events-per-second="$EVENTS_PER_SECOND" \
  --dashboard-calls-per-minute="$DASHBOARD_CALLS_PER_MINUTE" \
  --api-key="$API_KEY" \
  --project-id="$PROJECT_ID" \
  2>&1 | tee "$RESULT_FILE"

echo ""
echo "📋 Stress test completed!"
echo "   Results saved to: $RESULT_FILE"

# Generate a quick summary
echo ""
echo "📈 Quick Summary:"
grep -E "(Final Stress Test Results|Total Events Sent|Successful.*events|Average Latency|Total Dashboard Calls|Cache Hit Rate)" "$RESULT_FILE" | tail -10

# Suggest next steps based on results
echo ""
echo "💡 Suggested Next Steps:"
echo "   1. Review detailed results in $RESULT_FILE"
echo "   2. Check server logs for any errors during the test"
echo "   3. Monitor system resources (CPU, memory, network)"
echo "   4. Consider running longer tests for production validation"
echo ""

# Archive old results (keep last 10)
echo "🧹 Cleaning up old test results..."
cd "$RESULTS_DIR"
ls -t stress_test_*.log | tail -n +11 | xargs -r rm
cd ..
echo "✅ Cleanup completed"
#!/bin/bash

echo "🚀 Analytics Test Script"
echo "======================="

# Check if .env files exist
if [ ! -f ".env.local" ] && [ ! -f ".env" ]; then
    echo "❌ Error: No .env.local or .env file found!"
    echo "Please create one with the following variables:"
    echo "  DATABASE_URL=your_database_url"
    echo "  S3_BUCKET_NAME=your_bucket_name"
    echo "  AWS_ACCESS_KEY_ID=your_access_key"
    echo "  AWS_SECRET_ACCESS_KEY=your_secret_key"
    exit 1
fi

# Check if the server is running
echo "🔍 Checking if server is running..."
if ! curl -s http://65.109.6.92:8080/health > /dev/null; then
    echo "⚠️  Server not running. Starting server in background..."
    
    # Start the server in background
    go run . &
    SERVER_PID=$!
    
    # Wait for server to start
    echo "⏳ Waiting for server to start..."
    for i in {1..30}; do
        if curl -s http://65.109.6.92:8080/health > /dev/null; then
            echo "✅ Server is running!"
            break
        fi
        if [ $i -eq 30 ]; then
            echo "❌ Server failed to start within 30 seconds"
            kill $SERVER_PID 2>/dev/null
            exit 1
        fi
        sleep 1
    done
    
    STARTED_SERVER=true
else
    echo "✅ Server is already running!"
    STARTED_SERVER=false
fi

# Run the test
echo ""
echo "🧪 Running analytics tests..."
echo ""

if go run test_analytics.go; then
    echo ""
    echo "🎉 All tests passed successfully!"
    exit_code=0
else
    echo ""
    echo "❌ Tests failed!"
    exit_code=1
fi

# Stop the server if we started it
if [ "$STARTED_SERVER" = true ]; then
    echo ""
    echo "🛑 Stopping test server..."
    kill $SERVER_PID 2>/dev/null
    wait $SERVER_PID 2>/dev/null
    echo "✅ Server stopped"
fi

exit $exit_code
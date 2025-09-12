#!/bin/bash

# Analytics Platform Startup Script

echo "🚀 Starting Analytics Platform..."

# Check if .env.local exists
if [ ! -f ".env.local" ]; then
    echo "⚠️  .env.local not found. Creating from template..."
    cp .env.example .env.local
    echo "📝 Please edit .env.local with your Cloudflare R2 credentials before running again."
    exit 1
fi

# Build the application
echo "🔨 Building application..."
go build -o analytics-platform main.go analytics.go

if [ $? -ne 0 ]; then
    echo "❌ Build failed!"
    exit 1
fi

echo "✅ Build successful!"

# Start the server
echo "🌐 Starting server..."
echo "📊 Dashboard will be available at: http://localhost:8080"
echo "🔧 API endpoints:"
echo "   - Analytics: http://localhost:8080/api/v1/analytics"
echo "   - Dashboard: http://localhost:8080/api/v1/dashboard"
echo "   - Health: http://localhost:8080/health"
echo ""
echo "Press Ctrl+C to stop the server"
echo ""

./analytics-platform

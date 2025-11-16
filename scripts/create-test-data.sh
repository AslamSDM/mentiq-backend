#!/bin/bash

# 🔧 Create Test Data for Analytics Testing
# This script creates a test account, project, and API key for testing

set -e

BASE_URL="http://localhost:8080"

echo "🔧 Creating test data for analytics testing..."

# Create test account
echo "1. Creating test account..."
ACCOUNT_RESPONSE=$(curl -s -X POST "$BASE_URL/api/v1/signup" \
    -H "Content-Type: application/json" \
    -d '{
        "email": "test@mentiq.com",kw
        "password": "testpassword123",
        "company_name": "Test Company"
    }')

echo "Account Response: $ACCOUNT_RESPONSE"

# Parse account ID (you'll need to adjust this based on actual response format)
ACCOUNT_ID=$(echo "$ACCOUNT_RESPONSE" | grep -o '"id":"[^"]*"' | cut -d'"' -f4 || echo "test_account_123")

echo "2. Creating test project..."
PROJECT_RESPONSE=$(curl -s -X POST "$BASE_URL/api/v1/accounts/$ACCOUNT_ID/projects" \
    -H "Content-Type: application/json" \
    -d '{
        "name": "Test Project",
        "description": "Test project for analytics"
    }')

echo "Project Response: $PROJECT_RESPONSE"

# Parse project ID
PROJECT_ID=$(echo "$PROJECT_RESPONSE" | grep -o '"id":"[^"]*"' | cut -d'"' -f4 || echo "test_project_123")

echo "3. Creating API key..."
API_KEY_RESPONSE=$(curl -s -X POST "$BASE_URL/api/v1/projects/$PROJECT_ID/apikeys" \
    -H "Content-Type: application/json" \
    -d '{
        "name": "Test API Key",
        "description": "API key for testing analytics"
    }')

echo "API Key Response: $API_KEY_RESPONSE"

# Parse API key
API_KEY=$(echo "$API_KEY_RESPONSE" | grep -o '"key":"[^"]*"' | cut -d'"' -f4 || echo "test_api_key_123")

echo ""
echo "✅ Test data created!"
echo "Account ID: $ACCOUNT_ID"
echo "Project ID: $PROJECT_ID" 
echo "API Key: $API_KEY"
echo ""
echo "💡 To use these in your tests, run:"
echo "   API_KEY=$API_KEY PROJECT_ID=$PROJECT_ID ./test-all-analytics.sh"
echo ""
echo "Or set them as environment variables:"
echo "   export API_KEY=$API_KEY"
echo "   export PROJECT_ID=$PROJECT_ID"
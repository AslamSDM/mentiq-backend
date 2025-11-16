#!/bin/bash

# Test script for Session Recording endpoints
# Make sure the server is running before executing this script

BASE_URL="http://localhost:8080"
API_BASE="$BASE_URL/api/v1"

echo "==================================="
echo "Session Recording API Test Script"
echo "==================================="
echo ""

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Step 1: Create account
echo -e "${BLUE}Step 1: Creating test account...${NC}"
SIGNUP_RESPONSE=$(curl -s -X POST "$BASE_URL/signup" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "Test User",
    "email": "test_recording_'$(date +%s)'@example.com",
    "password": "testpassword123"
  }')

echo "Signup Response: $SIGNUP_RESPONSE"
ACCOUNT_ID=$(echo $SIGNUP_RESPONSE | grep -o '"account_id":"[^"]*"' | cut -d'"' -f4)
echo -e "${GREEN}Account ID: $ACCOUNT_ID${NC}"
echo ""

# Step 2: Login to get JWT token
echo -e "${BLUE}Step 2: Logging in...${NC}"
EMAIL=$(echo $SIGNUP_RESPONSE | grep -o '"email":"[^"]*"' | cut -d'"' -f4)
LOGIN_RESPONSE=$(curl -s -X POST "$BASE_URL/login" \
  -H "Content-Type: application/json" \
  -d '{
    "email": "'$EMAIL'",
    "password": "testpassword123"
  }')

echo "Login Response: $LOGIN_RESPONSE"
JWT_TOKEN=$(echo $LOGIN_RESPONSE | grep -o '"token":"[^"]*"' | cut -d'"' -f4)
echo -e "${GREEN}JWT Token: ${JWT_TOKEN:0:50}...${NC}"
echo ""

# Step 3: Create project
echo -e "${BLUE}Step 3: Creating test project...${NC}"
PROJECT_RESPONSE=$(curl -s -X POST "$API_BASE/projects" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $JWT_TOKEN" \
  -d '{
    "name": "Recording Test Project",
    "website": "https://example.com"
  }')

echo "Project Response: $PROJECT_RESPONSE"
PROJECT_ID=$(echo $PROJECT_RESPONSE | grep -o '"id":"[^"]*"' | cut -d'"' -f4)
echo -e "${GREEN}Project ID: $PROJECT_ID${NC}"
echo ""

# Step 4: Create API key
echo -e "${BLUE}Step 4: Creating API key...${NC}"
APIKEY_RESPONSE=$(curl -s -X POST "$API_BASE/projects/$PROJECT_ID/apikeys" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $JWT_TOKEN" \
  -d '{
    "name": "Recording Test Key"
  }')

echo "API Key Response: $APIKEY_RESPONSE"
API_KEY=$(echo $APIKEY_RESPONSE | grep -o '"key":"[^"]*"' | cut -d'"' -f4)
echo -e "${GREEN}API Key: $API_KEY${NC}"
echo ""

# Step 5: Ingest a recording
echo -e "${BLUE}Step 5: Ingesting session recording...${NC}"
SESSION_ID="sess_test_$(date +%s)"

RECORDING_RESPONSE=$(curl -s -X POST "$API_BASE/sessions/$SESSION_ID/recordings" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $API_KEY" \
  -d '{
    "account_id": "'$ACCOUNT_ID'",
    "project_id": "'$PROJECT_ID'",
    "user_id": "user_test_123",
    "events": [
      {
        "type": 2,
        "timestamp": 1234567890,
        "data": {
          "node": {
            "id": 1,
            "type": 0,
            "tagName": "html"
          }
        }
      },
      {
        "type": 3,
        "timestamp": 1234567891,
        "data": {
          "source": 0,
          "positions": [
            {
              "x": 100,
              "y": 200,
              "id": 5,
              "timeOffset": 10
            }
          ]
        }
      },
      {
        "type": 3,
        "timestamp": 1234567892,
        "data": {
          "source": 1,
          "text": "Button clicked"
        }
      }
    ],
    "duration": 45,
    "start_url": "https://example.com/home"
  }')

echo "Recording Ingest Response: $RECORDING_RESPONSE"
RECORDING_ID=$(echo $RECORDING_RESPONSE | grep -o '"recording_id":"[^"]*"' | cut -d'"' -f4)
echo -e "${GREEN}Recording ID: $RECORDING_ID${NC}"
echo ""

# Step 6: List recordings
echo -e "${BLUE}Step 6: Listing recordings for project...${NC}"
LIST_RESPONSE=$(curl -s -X GET "$API_BASE/recordings?project_id=$PROJECT_ID&limit=10" \
  -H "Authorization: Bearer $JWT_TOKEN")

echo "List Response: $LIST_RESPONSE"
TOTAL_RECORDINGS=$(echo $LIST_RESPONSE | grep -o '"total":[0-9]*' | cut -d':' -f2)
echo -e "${GREEN}Total Recordings: $TOTAL_RECORDINGS${NC}"
echo ""

# Step 7: Get specific recording
echo -e "${BLUE}Step 7: Getting specific recording...${NC}"
GET_RESPONSE=$(curl -s -X GET "$API_BASE/recordings/$RECORDING_ID" \
  -H "Authorization: Bearer $JWT_TOKEN")

echo "Get Recording Response (first 500 chars):"
echo "${GET_RESPONSE:0:500}..."
echo ""

# Step 8: Update recording (send more events)
echo -e "${BLUE}Step 8: Updating recording with more events...${NC}"
UPDATE_RESPONSE=$(curl -s -X POST "$API_BASE/sessions/$SESSION_ID/recordings" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $API_KEY" \
  -d '{
    "account_id": "'$ACCOUNT_ID'",
    "project_id": "'$PROJECT_ID'",
    "user_id": "user_test_123",
    "events": [
      {
        "type": 2,
        "timestamp": 1234567890,
        "data": {
          "node": {
            "id": 1,
            "type": 0,
            "tagName": "html"
          }
        }
      },
      {
        "type": 3,
        "timestamp": 1234567893,
        "data": {
          "source": 2,
          "id": 10,
          "x": 150,
          "y": 250
        }
      },
      {
        "type": 3,
        "timestamp": 1234567894,
        "data": {
          "source": 0,
          "positions": [
            {
              "x": 200,
              "y": 300,
              "id": 15,
              "timeOffset": 20
            }
          ]
        }
      }
    ],
    "duration": 60,
    "start_url": "https://example.com/home"
  }')

echo "Update Response: $UPDATE_RESPONSE"
echo ""

# Step 9: List with filters
echo -e "${BLUE}Step 9: Testing filters - by session_id...${NC}"
FILTER_RESPONSE=$(curl -s -X GET "$API_BASE/recordings?project_id=$PROJECT_ID&session_id=$SESSION_ID" \
  -H "Authorization: Bearer $JWT_TOKEN")

echo "Filtered List Response: $FILTER_RESPONSE"
echo ""

# Summary
echo "==================================="
echo -e "${GREEN}✓ Test Complete!${NC}"
echo "==================================="
echo ""
echo "Summary:"
echo "- Account ID: $ACCOUNT_ID"
echo "- Project ID: $PROJECT_ID"
echo "- Session ID: $SESSION_ID"
echo "- Recording ID: $RECORDING_ID"
echo "- Total Recordings: $TOTAL_RECORDINGS"
echo ""
echo "You can now replay the recording using rrweb-player with the events from:"
echo "$API_BASE/recordings/$RECORDING_ID"
echo ""

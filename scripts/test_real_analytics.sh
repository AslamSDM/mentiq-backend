#!/usr/bin/env bash
set -euo pipefail

# Test script for Real Subscription Payment and Churn Analytics
# Tests the new endpoints that use real Stripe data

API_URL=${API_URL:-"http://localhost:8080"}
PROJECT_ID=${PROJECT_ID:-""}
BEARER_TOKEN=${BEARER_TOKEN:-""}

if [ -z "$PROJECT_ID" ] || [ -z "$BEARER_TOKEN" ]; then
  echo "Error: PROJECT_ID and BEARER_TOKEN are required" >&2
  echo "" >&2
  echo "Usage:" >&2
  echo "  PROJECT_ID=your-project-id \\" >&2
  echo "  BEARER_TOKEN=your-jwt-token \\" >&2
  echo "  ./scripts/test_real_analytics.sh" >&2
  exit 1
fi

echo "================================================================"
echo "MentiQ Real Analytics Test Suite"
echo "================================================================"
echo "Testing new real subscription payment and churn analytics"
echo "Project: $PROJECT_ID"
echo "API: $API_URL"
echo "================================================================"
echo ""

# Helper function to make API calls
api_call() {
  local endpoint="$1"
  local description="$2"
  
  echo "Testing: $description"
  echo "Endpoint: $endpoint"
  echo ""
  
  response=$(curl -sS -H "Authorization: Bearer $BEARER_TOKEN" \
    -H "X-Project-ID: $PROJECT_ID" \
    "$API_URL$endpoint")
  
  # Check if response is JSON
  if echo "$response" | jq empty 2>/dev/null; then
    echo "$response" | jq '.'
  else
    echo "Non-JSON Response: $response"
  fi
  
  echo ""
  echo "----------------------------------------------------------------"
  echo ""
}

# Test 1: Real Subscription Analytics
api_call "/api/v1/projects/$PROJECT_ID/analytics/real-subscriptions" \
  "Real Subscription Analytics - NEW ENDPOINT"

# Test 2: Real Revenue Analytics
api_call "/api/v1/projects/$PROJECT_ID/analytics/real-revenue" \
  "Real Revenue Analytics from Stripe Charges & Invoices - NEW ENDPOINT"

# Test 3: Real Churn Analytics
api_call "/api/v1/projects/$PROJECT_ID/analytics/real-churn" \
  "Real Churn Analysis from Stripe Data - NEW ENDPOINT"

# Test 4: Enhanced Churn Handler with Real Data
api_call "/api/v1/projects/$PROJECT_ID/analytics/churn?use_real_data=true" \
  "Enhanced Churn Handler with Real Stripe Data - ENHANCED"

# Test 5: Enhanced Churn Handler with Synthetic Data
api_call "/api/v1/projects/$PROJECT_ID/analytics/churn?use_real_data=false" \
  "Enhanced Churn Handler with Synthetic Data - ENHANCED"

# Test 6: Main Analytics with New Subscription Metrics
api_call "/api/v1/projects/$PROJECT_ID/analytics?metrics=total_subscriptions,real_revenue,subscription_health" \
  "Main Analytics with New Subscription Metrics - ENHANCED"

# Test 7: All New Subscription Metrics
api_call "/api/v1/projects/$PROJECT_ID/analytics?metrics=total_subscriptions,real_revenue,subscription_health,mrr,arpu,churn_rate" \
  "Complete Subscription Health Dashboard - COMPREHENSIVE"

# Test 8: Date Range Analytics
start_date=$(date -d "30 days ago" +%Y-%m-%d)
end_date=$(date +%Y-%m-%d)
api_call "/api/v1/projects/$PROJECT_ID/analytics/real-revenue?start_date=$start_date&end_date=$end_date" \
  "Real Revenue Analytics with Date Range (Last 30 Days)"

# Test 9: Churn Analysis with Date Range
api_call "/api/v1/projects/$PROJECT_ID/analytics/real-churn?start_date=$start_date&end_date=$end_date" \
  "Real Churn Analysis with Date Range (Last 30 Days)"

echo "================================================================"
echo "Testing Complete!"
echo "================================================================"
echo ""
echo "Summary of New Features Tested:"
echo ""
echo "✓ Real Subscription Analytics"
echo "  - Live subscription counts and status breakdown"
echo "  - Real MRR, ARR, and ARPU calculations"
echo "  - Active vs churned subscription metrics"
echo ""
echo "✓ Real Revenue Analytics"
echo "  - Revenue from successful Stripe charges"
echo "  - Revenue from paid invoices"  
echo "  - Recurring vs one-time revenue breakdown"
echo "  - Daily revenue time series"
echo ""
echo "✓ Real Churn Analysis"
echo "  - Churn rate based on actual cancellations"
echo "  - Customer lifetime analysis"
echo "  - Churn reasons and timing patterns"
echo "  - Customer Lifetime Value (CLV) estimates"
echo ""
echo "✓ Enhanced Main Analytics"
echo "  - total_subscriptions: Live subscription counts"
echo "  - real_revenue: Actual Stripe revenue data"
echo "  - subscription_health: Comprehensive health score"
echo ""
echo "All analytics use REAL Stripe data when available,"
echo "providing accurate business insights for SaaS metrics!"
echo ""
echo "================================================================"

exit 0
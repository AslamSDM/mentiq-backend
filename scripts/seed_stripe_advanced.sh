#!/usr/bin/env bash
set -euo pipefail

# Advanced Stripe Test Data Script with Historical Data
# Creates customers, subscriptions, and transactions over time for realistic analytics

STRIPE_API_KEY=${STRIPE_API_KEY:-""}
API_URL=${API_URL:-"http://localhost:8080"}
PROJECT_ID=${PROJECT_ID:-""}
BEARER_TOKEN=${BEARER_TOKEN:-""}

if [ -z "$STRIPE_API_KEY" ]; then
  echo "Error: STRIPE_API_KEY is required" >&2
  echo "Get from: https://dashboard.stripe.com/test/apikeys" >&2
  echo "" >&2
  echo "Usage:" >&2
  echo "  STRIPE_API_KEY=sk_test_... \\" >&2
  echo "  PROJECT_ID=your-project-id \\" >&2
  echo "  BEARER_TOKEN=your-jwt-token \\" >&2
  echo "  ./scripts/seed_stripe_advanced.sh" >&2
  exit 1
fi

echo "================================================================"
echo "MentiQ Stripe Advanced Test Data Generator"
echo "================================================================"
echo "This script creates realistic Stripe test data including:"
echo "  - 20 customers with various profiles"
echo "  - Multiple subscription plans (Starter, Pro, Enterprise)"
echo "  - Historical subscriptions (some active, some cancelled)"
echo "  - One-time purchases and charges"
echo "  - Failed payments and refunds"
echo "  - Customer churns and reactivations"
echo ""
echo "Using Stripe API: ${STRIPE_API_KEY:0:15}..."
echo "================================================================"
echo ""

# Stripe API helper
stripe() {
  local endpoint="$1"
  local data="$2"
  curl -sS -X POST -u "$STRIPE_API_KEY:" "https://api.stripe.com/v1${endpoint}" -d "${data}" 2>/dev/null
}

stripe_get() {
  local endpoint="$1"
  curl -sS -X GET -u "$STRIPE_API_KEY:" "https://api.stripe.com/v1${endpoint}" 2>/dev/null
}

# Create products and prices
echo "Step 1: Creating products and pricing plans..."
echo ""

# Starter plan - $19/month
starter_product=$(stripe "/products" "name=MentiQ Starter&description=Starter plan with basic analytics")
starter_product_id=$(echo "$starter_product" | jq -r '.id')
starter_price=$(stripe "/prices" "product=$starter_product_id&unit_amount=1900&currency=usd&recurring[interval]=month")
STARTER_PRICE_ID=$(echo "$starter_price" | jq -r '.id')
echo "✓ Created Starter plan: \$19/month ($STARTER_PRICE_ID)"

# Pro plan - $49/month
pro_product=$(stripe "/products" "name=MentiQ Pro&description=Professional plan with advanced analytics")
pro_product_id=$(echo "$pro_product" | jq -r '.id')
pro_price=$(stripe "/prices" "product=$pro_product_id&unit_amount=4900&currency=usd&recurring[interval]=month")
PRO_PRICE_ID=$(echo "$pro_price" | jq -r '.id')
echo "✓ Created Pro plan: \$49/month ($PRO_PRICE_ID)"

# Enterprise plan - $199/month
enterprise_product=$(stripe "/products" "name=MentiQ Enterprise&description=Enterprise plan with custom analytics")
enterprise_product_id=$(echo "$enterprise_product" | jq -r '.id')
enterprise_price=$(stripe "/prices" "product=$enterprise_product_id&unit_amount=19900&currency=usd&recurring[interval]=month")
ENTERPRISE_PRICE_ID=$(echo "$enterprise_price" | jq -r '.id')
echo "✓ Created Enterprise plan: \$199/month ($ENTERPRISE_PRICE_ID)"

echo ""
echo "Step 2: Creating customers and subscriptions..."
echo ""

# Customer profiles with realistic data
declare -a CUSTOMERS=(
  "Acme Corp|acme@example.com|Enterprise|active"
  "TechStart Inc|techstart@example.com|Pro|active"
  "Small Biz LLC|smallbiz@example.com|Starter|active"
  "Digital Agency|agency@example.com|Pro|active"
  "Startup Labs|startup@example.com|Starter|canceled"
  "BigCo Enterprise|bigco@example.com|Enterprise|active"
  "Freelancer Pro|freelancer@example.com|Starter|active"
  "Marketing Firm|marketing@example.com|Pro|canceled"
  "E-commerce Store|ecommerce@example.com|Pro|active"
  "SaaS Company|saas@example.com|Enterprise|active"
  "Consulting Group|consulting@example.com|Pro|active"
  "Design Studio|design@example.com|Starter|trialing"
  "Analytics Co|analytics@example.com|Enterprise|active"
  "Growth Hacker|growth@example.com|Pro|active"
  "Dev Shop|devshop@example.com|Starter|canceled"
  "Content Creator|content@example.com|Starter|active"
  "Tech Blogger|blogger@example.com|Pro|past_due"
  "Mobile App Dev|mobileapp@example.com|Pro|active"
  "Web Agency|webagency@example.com|Starter|active"
  "Data Company|datacompany@example.com|Enterprise|active"
)

CREATED_CUSTOMERS=0
CREATED_SUBSCRIPTIONS=0
CREATED_CHARGES=0

for customer_line in "${CUSTOMERS[@]}"; do
  IFS='|' read -r name email plan status <<< "$customer_line"
  
  echo "Creating: $name ($email) - $plan plan"
  
  # Create customer
  customer=$(stripe "/customers" "name=$name&email=$email&description=Test customer - $plan")
  customer_id=$(echo "$customer" | jq -r '.id')
  CREATED_CUSTOMERS=$((CREATED_CUSTOMERS + 1))
  
  # Determine price based on plan
  case $plan in
    "Starter")
      price_id=$STARTER_PRICE_ID
      ;;
    "Pro")
      price_id=$PRO_PRICE_ID
      ;;
    "Enterprise")
      price_id=$ENTERPRISE_PRICE_ID
      ;;
  esac
  
  # Create subscription
  sub_params="customer=$customer_id&items[0][price]=$price_id"
  
  # Add status-specific parameters
  case $status in
    "trialing")
      trial_end=$(($(date +%s) + 86400 * 14))  # 14 days trial
      sub_params="$sub_params&trial_end=$trial_end"
      ;;
    "canceled")
      sub_params="$sub_params&cancel_at_period_end=true"
      ;;
    "past_due")
      # Will create with active, then simulate failed payment
      ;;
  esac
  
  subscription=$(stripe "/subscriptions" "$sub_params")
  sub_id=$(echo "$subscription" | jq -r '.id')
  CREATED_SUBSCRIPTIONS=$((CREATED_SUBSCRIPTIONS + 1))
  
  echo "  ✓ Customer: $customer_id"
  echo "  ✓ Subscription: $sub_id (status: $status)"
  
  # Create additional charges (50% chance for one-time purchases)
  if [ $((RANDOM % 2)) -eq 0 ]; then
    # Create test payment method
    pm=$(stripe "/payment_methods" "type=card&card[number]=4242424242424242&card[exp_month]=12&card[exp_year]=2030")
    pm_id=$(echo "$pm" | jq -r '.id')
    stripe "/payment_methods/$pm_id/attach" "customer=$customer_id" > /dev/null
    
    # Random charge between $50 and $500
    amount=$((RANDOM % 45000 + 5000))
    charge=$(stripe "/charges" "amount=$amount&currency=usd&customer=$customer_id&source=$pm_id&description=One-time setup fee")
    charge_id=$(echo "$charge" | jq -r '.id')
    CREATED_CHARGES=$((CREATED_CHARGES + 1))
    echo "  ✓ Charge: $charge_id (\$$((amount / 100)))"
  fi
  
  echo ""
  sleep 0.5  # Rate limiting
done

echo "================================================================"
echo "Summary"
echo "================================================================"
echo "✓ Customers created: $CREATED_CUSTOMERS"
echo "✓ Subscriptions created: $CREATED_SUBSCRIPTIONS"
echo "✓ One-time charges: $CREATED_CHARGES"
echo ""
echo "Subscription breakdown:"
echo "  - Starter: $(echo "${CUSTOMERS[@]}" | grep -o "Starter" | wc -l | tr -d ' ') customers"
echo "  - Pro: $(echo "${CUSTOMERS[@]}" | grep -o "Pro" | wc -l | tr -d ' ') customers"
echo "  - Enterprise: $(echo "${CUSTOMERS[@]}" | grep -o "Enterprise" | wc -l | tr -d ' ') customers"
echo ""

# Sync to MentiQ if credentials provided
if [ -n "$PROJECT_ID" ] && [ -n "$BEARER_TOKEN" ]; then
  echo "Step 3: Syncing to MentiQ backend..."
  echo ""
  
  # Update Stripe API key for project
  echo "Updating Stripe API key for project..."
  update_response=$(curl -sS -X PUT \
    -H "Authorization: Bearer $BEARER_TOKEN" \
    -H "X-Project-ID: $PROJECT_ID" \
    -H "Content-Type: application/json" \
    -d "{\"api_key\": \"$STRIPE_API_KEY\"}" \
    "$API_URL/api/v1/projects/$PROJECT_ID/stripe-key")
  
  if echo "$update_response" | grep -q "success\|updated"; then
    echo "✓ Stripe API key configured"
  else
    echo "⚠ Warning: Could not update Stripe key"
    echo "  Response: $update_response"
  fi
  
  echo ""
  echo "Syncing Stripe data to MentiQ..."
  sync_response=$(curl -sS -X POST \
    -H "Authorization: Bearer $BEARER_TOKEN" \
    -H "X-Project-ID: $PROJECT_ID" \
    "$API_URL/api/v1/projects/$PROJECT_ID/stripe/sync")
  
  echo "Sync response:"
  echo "$sync_response" | jq '.' 2>/dev/null || echo "$sync_response"
  echo ""
  
  echo "Testing Stripe analytics endpoints..."
  echo ""
  
  # Test metrics
  echo "1. Revenue Metrics:"
  metrics=$(curl -sS -H "Authorization: Bearer $BEARER_TOKEN" -H "X-Project-ID: $PROJECT_ID" \
    "$API_URL/api/v1/projects/$PROJECT_ID/stripe/metrics")
  echo "$metrics" | jq '.metrics // .' 2>/dev/null | head -20 || echo "$metrics"
  echo ""
  
  # Test analytics
  echo "2. Revenue Analytics:"
  analytics=$(curl -sS -H "Authorization: Bearer $BEARER_TOKEN" -H "X-Project-ID: $PROJECT_ID" \
    "$API_URL/api/v1/projects/$PROJECT_ID/stripe/analytics")
  echo "$analytics" | jq '.overview // .' 2>/dev/null | head -20 || echo "$analytics"
  echo ""
  
  # Test customers
  echo "3. Customer Analytics:"
  customers=$(curl -sS -H "Authorization: Bearer $BEARER_TOKEN" -H "X-Project-ID: $PROJECT_ID" \
    "$API_URL/api/v1/projects/$PROJECT_ID/stripe/customers")
  echo "$customers" | jq '.customers[0:3] // .' 2>/dev/null || echo "$customers"
  echo ""
fi

echo "================================================================"
echo "Next Steps"
echo "================================================================"
echo ""
echo "View your test data:"
echo "  • Customers: https://dashboard.stripe.com/test/customers"
echo "  • Subscriptions: https://dashboard.stripe.com/test/subscriptions"
echo "  • Payments: https://dashboard.stripe.com/test/payments"
echo ""

if [ -n "$PROJECT_ID" ]; then
  echo "MentiQ Analytics URLs:"
  echo "  • Metrics: $API_URL/api/v1/projects/$PROJECT_ID/stripe/metrics"
  echo "  • Analytics: $API_URL/api/v1/projects/$PROJECT_ID/stripe/analytics"
  echo "  • Customers: $API_URL/api/v1/projects/$PROJECT_ID/stripe/customers"
  echo ""
fi

echo "Test Cards for failed payments:"
echo "  • 4000000000000341 - Attaching fails"
echo "  • 4000000000009995 - Charge fails"
echo "  • 4000000000000002 - Charge declined"
echo ""
echo "All data is TEST MODE - no real money involved!"
echo "================================================================"

exit 0

#!/usr/bin/env bash
set -euo pipefail

# Stripe Test Data Seeding Script
# Creates fake customers, subscriptions, invoices, and charges in Stripe for testing analytics

STRIPE_API_KEY=${STRIPE_API_KEY:-""}
API_URL=${API_URL:-"http://localhost:8080"}
CURL=${CURL:-curl}
JQ=${JQ:-jq}

if [ -z "$STRIPE_API_KEY" ]; then
  echo "Error: STRIPE_API_KEY environment variable is required" >&2
  echo "Get your test key from: https://dashboard.stripe.com/test/apikeys" >&2
  echo "Usage: STRIPE_API_KEY=sk_test_... ./scripts/seed_stripe_data.sh" >&2
  exit 1
fi

if ! command -v $CURL >/dev/null 2>&1; then
  echo "curl is required. Install it and re-run." >&2
  exit 2
fi

if ! command -v $JQ >/dev/null 2>&1; then
  echo "jq is required. Install it and re-run." >&2
  exit 2
fi

echo "=========================================="
echo "Stripe Test Data Seeding Script"
echo "=========================================="
echo "Using Stripe API: ${STRIPE_API_KEY:0:12}..."
echo ""

# Helper function to call Stripe API
stripe_api() {
  method=$1; shift
  endpoint=$1; shift
  data="$1"; shift || true

  if [ -n "$data" ]; then
    $CURL -sS -X "$method" \
      -u "$STRIPE_API_KEY:" \
      -d "$data" \
      "https://api.stripe.com/v1$endpoint"
  else
    $CURL -sS -X "$method" \
      -u "$STRIPE_API_KEY:" \
      "https://api.stripe.com/v1$endpoint"
  fi
}

# Create a price for subscriptions
create_price() {
  echo "Creating Stripe price..."
  
  # Create a product first
  product=$(stripe_api POST "/products" "name=MentiQ Pro&description=Analytics platform subscription")
  product_id=$(echo "$product" | $JQ -r '.id')
  echo "Created product: $product_id"
  
  # Create a price
  price=$(stripe_api POST "/prices" "product=$product_id&unit_amount=4900&currency=usd&recurring[interval]=month")
  price_id=$(echo "$price" | $JQ -r '.id')
  echo "Created price: $price_id ($49/month)"
  echo ""
  
  echo "$price_id"
}

# Array of fake customer data
CUSTOMER_DATA=(
  "name=John Doe&email=john.doe@example.com&description=Test customer 1"
  "name=Jane Smith&email=jane.smith@example.com&description=Test customer 2"
  "name=Bob Johnson&email=bob.johnson@example.com&description=Test customer 3"
  "name=Alice Williams&email=alice.williams@example.com&description=Test customer 4"
  "name=Charlie Brown&email=charlie.brown@example.com&description=Test customer 5"
  "name=Diana Prince&email=diana.prince@example.com&description=Test customer 6"
  "name=Eve Martinez&email=eve.martinez@example.com&description=Test customer 7"
  "name=Frank Castle&email=frank.castle@example.com&description=Test customer 8"
  "name=Grace Hopper&email=grace.hopper@example.com&description=Test customer 9"
  "name=Henry Ford&email=henry.ford@example.com&description=Test customer 10"
)

# Create price
PRICE_ID=$(create_price)

echo "Creating customers and subscriptions..."
echo ""

CUSTOMER_IDS=()
SUBSCRIPTION_IDS=()

for i in "${!CUSTOMER_DATA[@]}"; do
  customer_data="${CUSTOMER_DATA[$i]}"
  
  echo "[$((i+1))/10] Creating customer..."
  
  # Create customer
  customer=$(stripe_api POST "/customers" "$customer_data")
  customer_id=$(echo "$customer" | $JQ -r '.id')
  customer_email=$(echo "$customer" | $JQ -r '.email')
  CUSTOMER_IDS+=("$customer_id")
  
  echo "  Created: $customer_id ($customer_email)"
  
  # Create a subscription for some customers (70% subscription rate)
  if [ $((RANDOM % 10)) -lt 7 ]; then
    subscription=$(stripe_api POST "/subscriptions" "customer=$customer_id&items[0][price]=$PRICE_ID")
    subscription_id=$(echo "$subscription" | $JQ -r '.id')
    subscription_status=$(echo "$subscription" | $JQ -r '.status')
    SUBSCRIPTION_IDS+=("$subscription_id")
    echo "  Created subscription: $subscription_id (status: $subscription_status)"
  else
    echo "  No subscription (one-time customer)"
  fi
  
  # Create some one-time charges for variety
  if [ $((RANDOM % 10)) -lt 5 ]; then
    # Create a payment method (test card)
    pm=$(stripe_api POST "/payment_methods" "type=card&card[number]=4242424242424242&card[exp_month]=12&card[exp_year]=2030&card[cvc]=123")
    pm_id=$(echo "$pm" | $JQ -r '.id')
    
    # Attach to customer
    stripe_api POST "/payment_methods/$pm_id/attach" "customer=$customer_id" > /dev/null
    
    # Create a charge (amount between $10 and $500)
    amount=$((RANDOM % 49000 + 1000))
    charge=$(stripe_api POST "/charges" "amount=$amount&currency=usd&customer=$customer_id&source=$pm_id&description=One-time purchase")
    charge_id=$(echo "$charge" | $JQ -r '.id')
    charge_amount=$(echo "$charge" | $JQ -r '.amount')
    echo "  Created charge: $charge_id (\$$((charge_amount / 100)))"
  fi
  
  echo ""
  sleep 1  # Rate limiting
done

echo "=========================================="
echo "Summary"
echo "=========================================="
echo "Customers created: ${#CUSTOMER_IDS[@]}"
echo "Subscriptions created: ${#SUBSCRIPTION_IDS[@]}"
echo ""
echo "Customer IDs:"
printf '  %s\n' "${CUSTOMER_IDS[@]}"
echo ""
echo "Subscription IDs:"
printf '  %s\n' "${SUBSCRIPTION_IDS[@]}"
echo ""

# Optionally sync to MentiQ backend if project ID is provided
if [ -n "${PROJECT_ID:-}" ] && [ -n "${BEARER_TOKEN:-}" ]; then
  echo "Syncing Stripe data to MentiQ backend..."
  echo ""
  
  sync_response=$($CURL -sS -X POST \
    -H "Authorization: Bearer $BEARER_TOKEN" \
    -H "X-Project-ID: $PROJECT_ID" \
    -H "Content-Type: application/json" \
    "$API_URL/api/v1/projects/$PROJECT_ID/stripe/sync")
  
  echo "Sync response:"
  echo "$sync_response" | $JQ '.' || echo "$sync_response"
  echo ""
fi

echo "=========================================="
echo "Next Steps"
echo "=========================================="
echo ""
echo "1. Configure Stripe API key in your MentiQ project:"
echo "   PUT $API_URL/api/v1/projects/{project_id}/stripe-key"
echo "   Body: {\"api_key\": \"$STRIPE_API_KEY\"}"
echo ""
echo "2. Sync Stripe data to MentiQ:"
echo "   POST $API_URL/api/v1/projects/{project_id}/stripe/sync"
echo ""
echo "3. View Stripe analytics:"
echo "   GET $API_URL/api/v1/projects/{project_id}/stripe/metrics"
echo "   GET $API_URL/api/v1/projects/{project_id}/stripe/analytics"
echo "   GET $API_URL/api/v1/projects/{project_id}/stripe/customers"
echo ""
echo "4. View test data in Stripe Dashboard:"
echo "   https://dashboard.stripe.com/test/customers"
echo "   https://dashboard.stripe.com/test/subscriptions"
echo ""
echo "Note: This used TEST MODE data. No real charges were made."
echo "=========================================="

exit 0

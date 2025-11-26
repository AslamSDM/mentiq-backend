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

# Verify it's a test key
if [[ ! "$STRIPE_API_KEY" =~ ^sk_test_ ]] && [[ ! "$STRIPE_API_KEY" =~ ^rk_test_ ]]; then
  echo "Error: Please use a TEST mode Stripe API key (sk_test_... or rk_test_...)" >&2
  echo "Never use live keys for seeding test data!" >&2
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

# Stripe API helper with better error handling
stripe_post() {
  local endpoint="$1"
  local data="$2"
  local response
  response=$(curl -sS -X POST -u "$STRIPE_API_KEY:" "https://api.stripe.com/v1${endpoint}" -d "${data}" 2>&1)
  
  # Check for error
  if echo "$response" | jq -e '.error' > /dev/null 2>&1; then
    local error_msg=$(echo "$response" | jq -r '.error.message // .error.type')
    echo "  ⚠ API Error: $error_msg" >&2
    echo ""
    return 1
  fi
  
  echo "$response"
}

stripe_get() {
  local endpoint="$1"
  curl -sS -X GET -u "$STRIPE_API_KEY:" "https://api.stripe.com/v1${endpoint}" 2>/dev/null
}

stripe_delete() {
  local endpoint="$1"
  curl -sS -X DELETE -u "$STRIPE_API_KEY:" "https://api.stripe.com/v1${endpoint}" 2>/dev/null
}

# Create products and prices
echo "Step 1: Creating products and pricing plans..."
echo ""

# Starter plan - $19/month
starter_product=$(stripe_post "/products" "name=MentiQ Starter&description=Starter plan with basic analytics") || exit 1
starter_product_id=$(echo "$starter_product" | jq -r '.id')
starter_price=$(stripe_post "/prices" "product=$starter_product_id&unit_amount=1900&currency=usd&recurring[interval]=month") || exit 1
STARTER_PRICE_ID=$(echo "$starter_price" | jq -r '.id')
echo "✓ Created Starter plan: \$19/month ($STARTER_PRICE_ID)"

# Pro plan - $49/month
pro_product=$(stripe_post "/products" "name=MentiQ Pro&description=Professional plan with advanced analytics") || exit 1
pro_product_id=$(echo "$pro_product" | jq -r '.id')
pro_price=$(stripe_post "/prices" "product=$pro_product_id&unit_amount=4900&currency=usd&recurring[interval]=month") || exit 1
PRO_PRICE_ID=$(echo "$pro_price" | jq -r '.id')
echo "✓ Created Pro plan: \$49/month ($PRO_PRICE_ID)"

# Enterprise plan - $199/month
enterprise_product=$(stripe_post "/products" "name=MentiQ Enterprise&description=Enterprise plan with custom analytics") || exit 1
enterprise_product_id=$(echo "$enterprise_product" | jq -r '.id')
enterprise_price=$(stripe_post "/prices" "product=$enterprise_product_id&unit_amount=19900&currency=usd&recurring[interval]=month") || exit 1
ENTERPRISE_PRICE_ID=$(echo "$enterprise_price" | jq -r '.id')
echo "✓ Created Enterprise plan: \$199/month ($ENTERPRISE_PRICE_ID)"

# Annual plans for variety
starter_annual=$(stripe_post "/prices" "product=$starter_product_id&unit_amount=19000&currency=usd&recurring[interval]=year") || exit 1
STARTER_ANNUAL_ID=$(echo "$starter_annual" | jq -r '.id')
echo "✓ Created Starter Annual: \$190/year ($STARTER_ANNUAL_ID)"

pro_annual=$(stripe_post "/prices" "product=$pro_product_id&unit_amount=49000&currency=usd&recurring[interval]=year") || exit 1
PRO_ANNUAL_ID=$(echo "$pro_annual" | jq -r '.id')
echo "✓ Created Pro Annual: \$490/year ($PRO_ANNUAL_ID)"

echo ""
echo "Step 2: Creating customers and subscriptions..."
echo ""

# Customer profiles with realistic data and payment scenarios
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
  "Tech Blogger|blogger@example.com|Pro|active"
  "Mobile App Dev|mobileapp@example.com|Pro|active"
  "Web Agency|webagency@example.com|Starter|active"
  "Data Company|datacompany@example.com|Enterprise|active"
  "FinTech Startup|fintech@example.com|Pro|active"
  "AI Research Lab|ailab@example.com|Enterprise|active"
  "Social Media Co|social@example.com|Pro|canceled"
  "Gaming Studio|gaming@example.com|Starter|active"
  "EdTech Platform|edtech@example.com|Pro|active"
  "HealthTech Inc|healthtech@example.com|Enterprise|trialing"
  "Crypto Exchange|crypto@example.com|Enterprise|active"
  "Food Delivery|fooddelivery@example.com|Pro|active"
  "Travel Booking|travel@example.com|Starter|active"
  "Real Estate|realestate@example.com|Pro|active"
)

# Test card tokens for Stripe test mode
# These are special tokens that work without creating PaymentMethods
TEST_CARD_TOKEN="tok_visa"              # Successful Visa card
TEST_CARD_MASTERCARD="tok_mastercard"   # Successful Mastercard
TEST_CARD_AMEX="tok_amex"               # Successful Amex

CREATED_CUSTOMERS=0
CREATED_SUBSCRIPTIONS=0
CREATED_CHARGES=0
TOTAL_REVENUE=0

for customer_line in "${CUSTOMERS[@]}"; do
  IFS='|' read -r name email plan status <<< "$customer_line"
  
  echo "Creating: $name ($email) - $plan plan [$status]"
  
  # Create customer with a source (card) directly using test token
  customer=$(stripe_post "/customers" "name=$name&email=$email&description=Test customer - $plan&source=$TEST_CARD_TOKEN")
  if [ $? -ne 0 ] || [ -z "$customer" ]; then
    echo "  ⚠ Failed to create customer, skipping..."
    continue
  fi
  
  customer_id=$(echo "$customer" | jq -r '.id')
  if [ "$customer_id" == "null" ] || [ -z "$customer_id" ]; then
    echo "  ⚠ Invalid customer ID, skipping..."
    continue
  fi
  
  CREATED_CUSTOMERS=$((CREATED_CUSTOMERS + 1))
  echo "  ✓ Customer: $customer_id"
  
  # Determine price based on plan (occasionally use annual)
  use_annual=$((RANDOM % 5))  # 20% chance of annual
  case $plan in
    "Starter")
      if [ $use_annual -eq 0 ]; then
        price_id=$STARTER_ANNUAL_ID
        price_desc="annual"
      else
        price_id=$STARTER_PRICE_ID
        price_desc="monthly"
      fi
      ;;
    "Pro")
      if [ $use_annual -eq 0 ]; then
        price_id=$PRO_ANNUAL_ID
        price_desc="annual"
      else
        price_id=$PRO_PRICE_ID
        price_desc="monthly"
      fi
      ;;
    "Enterprise")
      price_id=$ENTERPRISE_PRICE_ID
      price_desc="monthly"
      ;;
  esac
  
  # Build subscription parameters
  sub_params="customer=$customer_id&items[0][price]=$price_id"
  
  # Add status-specific parameters
  case $status in
    "trialing")
      trial_end=$(($(date +%s) + 86400 * 14))  # 14 days trial
      sub_params="$sub_params&trial_end=$trial_end"
      ;;
    "canceled")
      # Create active first, then cancel
      ;;
  esac
  
  # Create subscription
  subscription=$(stripe_post "/subscriptions" "$sub_params")
  if [ $? -ne 0 ] || [ -z "$subscription" ]; then
    echo "  ⚠ Failed to create subscription"
    continue
  fi
  
  sub_id=$(echo "$subscription" | jq -r '.id')
  sub_status=$(echo "$subscription" | jq -r '.status')
  
  if [ "$sub_id" == "null" ] || [ -z "$sub_id" ]; then
    echo "  ⚠ Invalid subscription ID"
    continue
  fi
  
  CREATED_SUBSCRIPTIONS=$((CREATED_SUBSCRIPTIONS + 1))
  echo "  ✓ Subscription: $sub_id ($price_desc) - status: $sub_status"
  
  # Handle post-creation subscription actions
  if [ "$status" == "canceled" ]; then
    cancel_result=$(stripe_post "/subscriptions/$sub_id" "cancel_at_period_end=true")
    echo "  ✓ Marked for cancellation at period end"
  fi
  
  # Create one-time charges for some customers (40% chance)
  if [ $((RANDOM % 10)) -lt 4 ]; then
    # Random charge between $25 and $250
    amount=$((RANDOM % 22500 + 2500))
    
    charge=$(stripe_post "/charges" "amount=$amount&currency=usd&customer=$customer_id&description=One-time purchase - Setup fee")
    if [ $? -eq 0 ]; then
      charge_id=$(echo "$charge" | jq -r '.id')
      charge_status=$(echo "$charge" | jq -r '.status')
      
      if [ "$charge_status" == "succeeded" ]; then
        CREATED_CHARGES=$((CREATED_CHARGES + 1))
        TOTAL_REVENUE=$((TOTAL_REVENUE + amount))
        echo "  ✓ One-time charge: \$$((amount / 100)) ($charge_id)"
      fi
    fi
  fi
  
  echo ""
  sleep 0.3  # Rate limiting
done

echo ""
echo "Step 3: Creating additional payment scenarios..."
echo ""

FAILED_CHARGES=0
REFUNDS_CREATED=0

# Create some customers with failed payments using declining test cards
echo "Creating customers with failed payments..."

declare -a FAILED_CUSTOMERS=(
  "Broke Startup|broke@example.com|Pro"
  "No Funds LLC|nofunds@example.com|Starter"
  "Declined Inc|declined@example.com|Enterprise"
)

for customer_line in "${FAILED_CUSTOMERS[@]}"; do
  IFS='|' read -r name email plan <<< "$customer_line"
  
  echo "Creating failed payment scenario: $name"
  
  # Create customer with declining card token
  failed_customer=$(stripe_post "/customers" "name=$name&email=$email&description=Customer with failed payment&source=tok_chargeDeclined")
  
  if [ $? -eq 0 ]; then
    failed_customer_id=$(echo "$failed_customer" | jq -r '.id')
    
    if [ "$failed_customer_id" != "null" ] && [ -n "$failed_customer_id" ]; then
      CREATED_CUSTOMERS=$((CREATED_CUSTOMERS + 1))
      
      # Try to charge (will fail)
      case $plan in
        "Starter") amount=1900 ;;
        "Pro") amount=4900 ;;
        "Enterprise") amount=19900 ;;
      esac
      
      # This charge will fail due to the declining card
      failed_charge=$(stripe_post "/charges" "amount=$amount&currency=usd&customer=$failed_customer_id&description=Failed subscription payment" 2>/dev/null) || true
      
      FAILED_CHARGES=$((FAILED_CHARGES + 1))
      echo "  ✓ Customer with failed payment: $failed_customer_id"
    fi
  fi
  
  sleep 0.3
done

# Create some refunds for existing successful charges
echo ""
echo "Creating sample refunds..."

# Get recent successful charges
recent_charges=$(stripe_get "/charges?limit=10")
charge_ids=$(echo "$recent_charges" | jq -r '.data[] | select(.status == "succeeded" and .refunded == false and .amount > 1000) | .id' 2>/dev/null | head -3)

for charge_id in $charge_ids; do
  if [ -n "$charge_id" ] && [ "$charge_id" != "null" ]; then
    # Get charge details
    charge_details=$(stripe_get "/charges/$charge_id")
    charge_amount=$(echo "$charge_details" | jq -r '.amount')
    
    # Refund 50% of the charge (partial refund)
    refund_amount=$((charge_amount / 2))
    
    if [ $refund_amount -gt 100 ]; then
      refund=$(stripe_post "/refunds" "charge=$charge_id&amount=$refund_amount&reason=requested_by_customer")
      
      if [ $? -eq 0 ]; then
        refund_status=$(echo "$refund" | jq -r '.status')
        
        if [ "$refund_status" == "succeeded" ]; then
          REFUNDS_CREATED=$((REFUNDS_CREATED + 1))
          echo "  ✓ Partial refund: \$$((refund_amount / 100)) for charge $charge_id"
        fi
      fi
    fi
    
    sleep 0.3
  fi
done

# Create some invoices with different statuses
echo ""
echo "Creating additional invoices..."

INVOICES_CREATED=0

# Get a few customers to create invoices for
customer_list=$(stripe_get "/customers?limit=5")
invoice_customers=$(echo "$customer_list" | jq -r '.data[].id' 2>/dev/null | head -3)

for cust_id in $invoice_customers; do
  if [ -n "$cust_id" ] && [ "$cust_id" != "null" ]; then
    # Create an invoice item
    item_amount=$((RANDOM % 5000 + 1000))  # $10-$60
    invoice_item=$(stripe_post "/invoiceitems" "customer=$cust_id&amount=$item_amount&currency=usd&description=Consulting services")
    
    if [ $? -eq 0 ]; then
      # Create and finalize the invoice
      invoice=$(stripe_post "/invoices" "customer=$cust_id&auto_advance=false")
      
      if [ $? -eq 0 ]; then
        invoice_id=$(echo "$invoice" | jq -r '.id')
        
        if [ "$invoice_id" != "null" ] && [ -n "$invoice_id" ]; then
          # Finalize the invoice
          stripe_post "/invoices/$invoice_id/finalize" "" > /dev/null 2>&1
          
          # Pay the invoice (50% chance)
          if [ $((RANDOM % 2)) -eq 0 ]; then
            stripe_post "/invoices/$invoice_id/pay" "" > /dev/null 2>&1
            echo "  ✓ Created and paid invoice: $invoice_id (\$$((item_amount / 100)))"
          else
            echo "  ✓ Created open invoice: $invoice_id (\$$((item_amount / 100)))"
          fi
          
          INVOICES_CREATED=$((INVOICES_CREATED + 1))
        fi
      fi
    fi
    
    sleep 0.3
  fi
done

echo ""
echo "================================================================"
echo "Summary"
echo "================================================================"
echo "✓ Customers created: $CREATED_CUSTOMERS"
echo "✓ Subscriptions created: $CREATED_SUBSCRIPTIONS"
echo "✓ One-time charges: $CREATED_CHARGES"
echo "✓ Failed charges simulated: $FAILED_CHARGES"
echo "✓ Refunds processed: $REFUNDS_CREATED"
echo "✓ Invoices created: $INVOICES_CREATED"
echo ""
echo "Subscription breakdown:"
active_count=$(echo "${CUSTOMERS[@]}" | tr ' ' '\n' | grep -c "active" || echo "0")
canceled_count=$(echo "${CUSTOMERS[@]}" | tr ' ' '\n' | grep -c "canceled" || echo "0")
trialing_count=$(echo "${CUSTOMERS[@]}" | tr ' ' '\n' | grep -c "trialing" || echo "0")
echo "  - Active: $active_count subscriptions"
echo "  - Canceled: $canceled_count subscriptions"
echo "  - Trialing: $trialing_count subscriptions"
echo ""
echo "Plan breakdown:"
starter_count=$(echo "${CUSTOMERS[@]}" | tr ' ' '\n' | grep -c "Starter" || echo "0")
pro_count=$(echo "${CUSTOMERS[@]}" | tr ' ' '\n' | grep -c "Pro" || echo "0")
enterprise_count=$(echo "${CUSTOMERS[@]}" | tr ' ' '\n' | grep -c "Enterprise" || echo "0")
echo "  - Starter (\$19/month): $starter_count customers"
echo "  - Pro (\$49/month): $pro_count customers"
echo "  - Enterprise (\$199/month): $enterprise_count customers"
echo ""

# Sync to MentiQ if credentials provided
if [ -n "$PROJECT_ID" ] && [ -n "$BEARER_TOKEN" ]; then
  echo "Step 4: Syncing to MentiQ backend..."
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
echo "View your test data in Stripe Dashboard:"
echo "  • Customers: https://dashboard.stripe.com/test/customers"
echo "  • Subscriptions: https://dashboard.stripe.com/test/subscriptions"
echo "  • Payments: https://dashboard.stripe.com/test/payments"
echo "  • Invoices: https://dashboard.stripe.com/test/invoices"
echo ""

if [ -n "$PROJECT_ID" ]; then
  echo "MentiQ Analytics URLs:"
  echo "  • Metrics: $API_URL/api/v1/projects/$PROJECT_ID/stripe/metrics"
  echo "  • Analytics: $API_URL/api/v1/projects/$PROJECT_ID/stripe/analytics"
  echo "  • Customers: $API_URL/api/v1/projects/$PROJECT_ID/stripe/customers"
  echo ""
fi

echo "Test Cards used (Stripe test mode tokens):"
echo "  • tok_visa - Successful Visa payments"
echo "  • tok_mastercard - Successful Mastercard payments"
echo "  • tok_chargeDeclined - Declined payments"
echo ""
echo "Data created:"
echo "  • Multiple subscription plans (Starter, Pro, Enterprise)"
echo "  • Monthly and annual billing intervals"
echo "  • Active, canceled, and trialing subscriptions"
echo "  • One-time charges and invoices"
echo "  • Partial refunds"
echo "  • Failed payment scenarios"
echo ""
echo "All data is in TEST MODE - no real money involved!"
echo "================================================================"

exit 0

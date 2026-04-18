package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
	"github.com/stripe/stripe-go/v72"
	"github.com/stripe/stripe-go/v72/checkout/session"
)

// CheckoutPlan holds Stripe price IDs + trial config for a tier.
type CheckoutPlan struct {
	Name             string
	BasePrice        int
	BasePriceID      string
	OveragePriceIDs  []string
	TrialDays        int64
}

// Shared overage meter price IDs — same for Starter and Growth.
var sharedOveragePriceIDs = []string{
	"price_1TMFQyEnZGKTei6CO6FqOf2s", // Paid Users overage
	"price_1TMFQzEnZGKTei6CKgpZfC0B", // Session Replays overage
	"price_1TMFR1EnZGKTei6C34YBJEkm", // Emails overage
	"price_1TMFR3EnZGKTei6Cr0Q3WpnU", // AI Generations overage
	"price_1TMFR4EnZGKTei6CZ1juzUMV", // Team Members overage
}

// CheckoutPlans maps tier ID → Stripe config.
var CheckoutPlans = map[string]CheckoutPlan{
	"starter": {
		Name:            "Starter",
		BasePrice:       21,
		BasePriceID:     "price_1TMFQwEnZGKTei6CeoxV2frF",
		OveragePriceIDs: sharedOveragePriceIDs,
		TrialDays:       3,
	},
	"growth": {
		Name:            "Growth",
		BasePrice:       199,
		BasePriceID:     "price_1TMFR6EnZGKTei6CBzMBAqtB",
		OveragePriceIDs: sharedOveragePriceIDs,
		TrialDays:       3,
	},
}

// CreateCheckoutRequest is the body for POST /api/v1/stripe/checkout.
type CreateCheckoutRequest struct {
	TierID       string `json:"tier_id" binding:"required"`
	IsSignupFlow bool   `json:"is_signup_flow"`
	SuccessURL   string `json:"success_url"`
	CancelURL    string `json:"cancel_url"`
}

// createCheckoutSessionHandler builds a Stripe Checkout Session for the
// authenticated account and returns the redirect URL.
func (s *Server) createCheckoutSessionHandler(c *gin.Context) {
	accountID := c.GetString("account_id")
	if accountID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	var req CreateCheckoutRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	plan, ok := CheckoutPlans[req.TierID]
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid tier"})
		return
	}

	var account Account
	if err := s.db.Where("id = ?", accountID).First(&account).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Account not found"})
		return
	}

	stripeKey := os.Getenv("STRIPE_API_KEY")
	if stripeKey == "" {
		stripeKey = os.Getenv("STRIPE_SECRET_KEY")
	}
	if stripeKey == "" {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Stripe not configured"})
		return
	}
	stripe.Key = stripeKey

	// Base price has quantity 1; overage meters have no quantity.
	lineItems := []*stripe.CheckoutSessionLineItemParams{
		{Price: stripe.String(plan.BasePriceID), Quantity: stripe.Int64(1)},
	}
	for _, id := range plan.OveragePriceIDs {
		lineItems = append(lineItems, &stripe.CheckoutSessionLineItemParams{
			Price: stripe.String(id),
		})
	}

	appURL := os.Getenv("APP_URL")
	if appURL == "" {
		appURL = os.Getenv("NEXTAUTH_URL")
	}
	if appURL == "" {
		appURL = "http://localhost:3000"
	}

	successURL := req.SuccessURL
	cancelURL := req.CancelURL
	if successURL == "" {
		if req.IsSignupFlow {
			successURL = fmt.Sprintf("%s/dashboard/onboarding?success=true&session_id={CHECKOUT_SESSION_ID}", appURL)
		} else {
			successURL = fmt.Sprintf("%s/dashboard/pricing?success=true&session_id={CHECKOUT_SESSION_ID}", appURL)
		}
	}
	if cancelURL == "" {
		if req.IsSignupFlow {
			cancelURL = fmt.Sprintf("%s/signup?canceled=true", appURL)
		} else {
			cancelURL = fmt.Sprintf("%s/dashboard/pricing?canceled=true", appURL)
		}
	}

	params := &stripe.CheckoutSessionParams{
		PaymentMethodTypes: stripe.StringSlice([]string{"card"}),
		Mode:               stripe.String(string(stripe.CheckoutSessionModeSubscription)),
		CustomerEmail:      stripe.String(account.Email),
		LineItems:          lineItems,
		SuccessURL:         stripe.String(successURL),
		CancelURL:          stripe.String(cancelURL),
		AllowPromotionCodes: stripe.Bool(true),
		SubscriptionData: &stripe.CheckoutSessionSubscriptionDataParams{},
	}

	if req.IsSignupFlow && plan.TrialDays > 0 {
		params.SubscriptionData.TrialPeriodDays = stripe.Int64(plan.TrialDays)
	}

	params.AddMetadata("accountId", accountID)
	params.AddMetadata("tier", req.TierID)
	params.AddMetadata("price", fmt.Sprintf("%d", plan.BasePrice))
	if req.IsSignupFlow {
		params.AddMetadata("isSignupFlow", "true")
	}
	params.SubscriptionData.AddMetadata("accountId", accountID)
	params.SubscriptionData.AddMetadata("tier", req.TierID)

	sess, err := session.New(params)
	if err != nil {
		log.Printf("Stripe checkout error for account %s: %v", accountID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create checkout session"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"url": sess.URL, "session_id": sess.ID})
}

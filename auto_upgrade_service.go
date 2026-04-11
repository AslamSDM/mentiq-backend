package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stripe/stripe-go/v72"
	"github.com/stripe/stripe-go/v72/sub"
	"gorm.io/gorm"
)

// PricingTier represents a subscription tier with its limits and overage rates
type PricingTier struct {
	ID        string
	Name      string
	MaxUsers  int
	BasePrice int64 // in dollars

	// Included limits
	IncludedPaidUsers       int
	IncludedSessionReplays  int
	IncludedAutomatedEmails int
	IncludedAIGenerations   int
	IncludedTeamMembers     int // 0 = unlimited

	// Overage rates (in cents)
	OveragePaidUsersPer100     int64 // cents per 100 extra paid users
	OverageReplaysPer500       int64 // cents per 500 extra replays
	OverageEmailsPer10k        int64 // cents per 10k extra emails
	OverageAIGenerationsPer100 int64 // cents per 100 extra AI generations
	OverageTeamMembersPer1     int64 // cents per 1 extra team member
}

// Tier progression order — recurring tiers
var TierOrder = []PricingTier{
	{
		ID: "starter", Name: "Starter", MaxUsers: 500, BasePrice: 59,
		IncludedPaidUsers: 150, IncludedSessionReplays: 100,
		IncludedAutomatedEmails: 5000, IncludedAIGenerations: 25,
		IncludedTeamMembers: 1,
		OveragePaidUsersPer100: 1500, OverageReplaysPer500: 1000,
		OverageEmailsPer10k: 400, OverageAIGenerationsPer100: 800,
		OverageTeamMembersPer1: 800,
	},
	{
		ID: "growth", Name: "Growth", MaxUsers: 5000, BasePrice: 149,
		IncludedPaidUsers: 2000, IncludedSessionReplays: 750,
		IncludedAutomatedEmails: 50000, IncludedAIGenerations: 250,
		IncludedTeamMembers: 5,
		OveragePaidUsersPer100: 1200, OverageReplaysPer500: 800,
		OverageEmailsPer10k: 300, OverageAIGenerationsPer100: 600,
		OverageTeamMembersPer1: 600,
	},
}

// LifetimeTier is the one-time purchase lifetime plan. It is NOT in TierOrder
// because it is not part of the recurring upgrade path. Activated via license keys only.
var LifetimeTier = PricingTier{
	ID: "lifetime", Name: "Lifetime", MaxUsers: 10000, BasePrice: 0,
	IncludedPaidUsers: 1000, IncludedSessionReplays: 500,
	IncludedAutomatedEmails: 25000, IncludedAIGenerations: 150,
	IncludedTeamMembers: 5,
	// Overage rates apply only when a payment method is on file
	OveragePaidUsersPer100: 1000, OverageReplaysPer500: 600,
	OverageEmailsPer10k: 250, OverageAIGenerationsPer100: 500,
	OverageTeamMembersPer1: 500,
}

// IsLifetimeTier returns true if the tier is the lifetime plan
func IsLifetimeTier(tierID string) bool {
	return tierID == "lifetime"
}

// AutoUpgradeService handles automatic subscription upgrades
type AutoUpgradeService struct {
	db *gorm.DB
}

// NewAutoUpgradeService creates a new AutoUpgradeService
func NewAutoUpgradeService(db *gorm.DB) *AutoUpgradeService {
	return &AutoUpgradeService{db: db}
}

// GetTierByID returns a tier by its ID (includes lifetime tier)
func GetTierByID(tierID string) *PricingTier {
	if tierID == LifetimeTier.ID {
		return &LifetimeTier
	}
	for _, tier := range TierOrder {
		if tier.ID == tierID {
			return &tier
		}
	}
	return nil
}

// GetTierForUserCount returns the appropriate tier for a given user count
func GetTierForUserCount(userCount int) *PricingTier {
	for _, tier := range TierOrder {
		if userCount <= tier.MaxUsers {
			return &tier
		}
	}
	// If > 10000 users, return nil (enterprise, needs manual handling)
	return nil
}

// GetNextTier returns the next tier in progression
func GetNextTier(currentTierID string) *PricingTier {
	for i, tier := range TierOrder {
		if tier.ID == currentTierID && i < len(TierOrder)-1 {
			return &TierOrder[i+1]
		}
	}
	return nil
}

// CheckAndUpgradeAccount checks if an account needs an upgrade and performs it
func (s *AutoUpgradeService) CheckAndUpgradeAccount(accountID string, actualUserCount int) (*UpgradeResult, error) {
	// Get current subscription
	var subscription AccountSubscription
	if err := s.db.Where("account_id = ?", accountID).First(&subscription).Error; err != nil {
		return nil, fmt.Errorf("subscription not found: %v", err)
	}

	// Skip if not active
	if subscription.Status != "active" && subscription.Status != "trialing" {
		return &UpgradeResult{Upgraded: false, Reason: "subscription not active"}, nil
	}

	// Skip if developer tier (free) or lifetime
	if subscription.Tier == "developer" {
		return &UpgradeResult{Upgraded: false, Reason: "developer tier"}, nil
	}
	if IsLifetimeTier(subscription.Tier) {
		return &UpgradeResult{Upgraded: false, Reason: "lifetime tier"}, nil
	}

	// Get current tier limits
	currentTier := GetTierByID(subscription.Tier)
	if currentTier == nil {
		return &UpgradeResult{Upgraded: false, Reason: "unknown tier"}, nil
	}

	// Check if user count exceeds tier limit
	if actualUserCount <= currentTier.MaxUsers {
		return &UpgradeResult{Upgraded: false, Reason: "within tier limit"}, nil
	}

	// Find the appropriate tier for the user count
	newTier := GetTierForUserCount(actualUserCount)
	if newTier == nil {
		// Enterprise tier needed - flag for manual handling
		log.Printf("⚠️ Account %s needs enterprise tier (user count: %d)", accountID, actualUserCount)
		return &UpgradeResult{
			Upgraded:     false,
			Reason:       "enterprise tier required",
			RequiresDemo: true,
		}, nil
	}

	// Already on the correct tier (shouldn't happen but check anyway)
	if newTier.ID == currentTier.ID {
		return &UpgradeResult{Upgraded: false, Reason: "already on correct tier"}, nil
	}

	// Perform the upgrade
	if err := s.UpgradeSubscription(accountID, &subscription, newTier); err != nil {
		return nil, fmt.Errorf("upgrade failed: %v", err)
	}

	log.Printf("✅ Auto-upgraded account %s from %s to %s (user count: %d)",
		accountID, currentTier.Name, newTier.Name, actualUserCount)

	return &UpgradeResult{
		Upgraded:  true,
		OldTier:   currentTier.ID,
		NewTier:   newTier.ID,
		OldPrice:  currentTier.BasePrice,
		NewPrice:  newTier.BasePrice,
		UserCount: actualUserCount,
	}, nil
}

// UpgradeResult represents the result of an upgrade check
type UpgradeResult struct {
	Upgraded     bool   `json:"upgraded"`
	Reason       string `json:"reason,omitempty"`
	OldTier      string `json:"old_tier,omitempty"`
	NewTier      string `json:"new_tier,omitempty"`
	OldPrice     int64  `json:"old_price,omitempty"`
	NewPrice     int64  `json:"new_price,omitempty"`
	UserCount    int    `json:"user_count,omitempty"`
	RequiresDemo bool   `json:"requires_demo,omitempty"`
}

// UpgradeSubscription upgrades a subscription to a new tier via Stripe
func (s *AutoUpgradeService) UpgradeSubscription(accountID string, subscription *AccountSubscription, newTier *PricingTier) error {
	// Initialize Stripe with API key
	stripeKey := os.Getenv("STRIPE_API_KEY")
	if stripeKey == "" {
		return fmt.Errorf("STRIPE_API_KEY not configured")
	}
	stripe.Key = stripeKey

	// Check if subscription has Stripe ID
	if subscription.StripeSubscriptionID == "" {
		// No Stripe subscription - just update database
		return s.updateSubscriptionInDB(subscription, newTier)
	}

	// Get the current Stripe subscription
	stripeSubscription, err := sub.Get(subscription.StripeSubscriptionID, nil)
	if err != nil {
		return fmt.Errorf("failed to get Stripe subscription: %v", err)
	}

	if len(stripeSubscription.Items.Data) == 0 {
		return fmt.Errorf("no items in subscription")
	}

	// Create new price for the upgraded tier
	// We'll create the price on the fly since we use price_data in checkout
	itemID := stripeSubscription.Items.Data[0].ID

	// Update the subscription with new price
	params := &stripe.SubscriptionParams{
		Items: []*stripe.SubscriptionItemsParams{
			{
				ID: stripe.String(itemID),
				PriceData: &stripe.SubscriptionItemPriceDataParams{
					Currency: stripe.String("usd"),
					Product:  stripe.String(stripeSubscription.Items.Data[0].Price.Product.ID),
					Recurring: &stripe.SubscriptionItemPriceDataRecurringParams{
						Interval: stripe.String("month"),
					},
					UnitAmount: stripe.Int64(newTier.BasePrice * 100), // Convert to cents
				},
			},
		},
		ProrationBehavior: stripe.String("create_prorations"), // Charge difference immediately
	}

	// Update metadata
	params.AddMetadata("tier", newTier.ID)
	params.AddMetadata("accountId", accountID)
	params.AddMetadata("auto_upgraded", "true")
	params.AddMetadata("upgraded_at", time.Now().Format(time.RFC3339))

	_, err = sub.Update(subscription.StripeSubscriptionID, params)
	if err != nil {
		return fmt.Errorf("failed to update Stripe subscription: %v", err)
	}

	// Update database
	return s.updateSubscriptionInDB(subscription, newTier)
}

// updateSubscriptionInDB updates the subscription record in the database
func (s *AutoUpgradeService) updateSubscriptionInDB(subscription *AccountSubscription, newTier *PricingTier) error {
	updates := map[string]interface{}{
		"tier":          newTier.ID,
		"monthly_price": newTier.BasePrice * 100, // Store in cents
		"user_count":    newTier.MaxUsers,        // Update to new tier max
		"updated_at":    time.Now(),
	}

	return s.db.Model(subscription).Updates(updates).Error
}

// GetPaidUserCount calculates the actual paid user count for an account
// It counts unique users with active paid subscriptions from the account's project analytics
func (s *AutoUpgradeService) GetPaidUserCount(accountID string) (int, error) {
	// Get the account's project
	var project Project
	if err := s.db.Where("account_id = ?", accountID).First(&project).Error; err != nil {
		return 0, fmt.Errorf("no project found for account: %v", err)
	}

	// Method 1: Count from StripeCustomer table (if Stripe is connected)
	var stripeCustomerCount int64
	err := s.db.Model(&StripeCustomer{}).
		Where("project_id = ? AND deleted = false", project.ID).
		Count(&stripeCustomerCount).Error
	if err == nil && stripeCustomerCount > 0 {
		// Count customers with active subscriptions
		var activeCustomers int64
		s.db.Model(&StripeSubscription{}).
			Where("project_id = ? AND status IN ?", project.ID, []string{"active", "trialing"}).
			Distinct("customer_id").
			Count(&activeCustomers)
		if activeCustomers > 0 {
			log.Printf("📊 Account %s has %d active Stripe customers", accountID, activeCustomers)
			return int(activeCustomers), nil
		}
	}

	// Method 2: Count from events - unique users with paid subscription traits
	var paidUserCount int64
	s.db.Model(&Event{}).
		Where("project_id = ? AND event_type = 'identify'", project.ID).
		Where("traits->>'subscription' IS NOT NULL").
		Where("traits->>'subscription'->>'status' = 'active' OR traits->>'status' = 'active'").
		Distinct("user_id").
		Count(&paidUserCount)

	if paidUserCount > 0 {
		log.Printf("📊 Account %s has %d paid users from events", accountID, paidUserCount)
		return int(paidUserCount), nil
	}

	// Method 3: Fallback - count all unique users
	var totalUsers int64
	s.db.Model(&Event{}).
		Where("project_id = ?", project.ID).
		Where("user_id IS NOT NULL AND user_id != ''").
		Distinct("user_id").
		Count(&totalUsers)

	log.Printf("📊 Account %s has %d total unique users (no subscription data)", accountID, totalUsers)
	return int(totalUsers), nil
}

// CheckUpgradesHandler is an HTTP handler to check and process upgrades for all accounts
func (s *AutoUpgradeService) CheckUpgradesHandler(c *gin.Context) {
	// This endpoint should be protected (admin only or via cron secret)
	cronSecret := c.GetHeader("X-Cron-Secret")
	expectedSecret := os.Getenv("CRON_SECRET")
	if expectedSecret != "" && cronSecret != expectedSecret {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	// Get all active subscriptions
	var subscriptions []AccountSubscription
	if err := s.db.Where("status IN ?", []string{"active", "trialing"}).Find(&subscriptions).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch subscriptions"})
		return
	}

	results := []map[string]interface{}{}
	upgraded := 0
	checked := 0

	for _, sub := range subscriptions {
		checked++

		// Calculate actual paid user count from analytics data
		actualUserCount, err := s.GetPaidUserCount(sub.AccountID)
		if err != nil {
			log.Printf("Error getting user count for account %s: %v", sub.AccountID, err)
			// Fall back to stored user count
			actualUserCount = sub.UserCount
		}

		result, err := s.CheckAndUpgradeAccount(sub.AccountID, actualUserCount)
		if err != nil {
			log.Printf("Error checking upgrade for account %s: %v", sub.AccountID, err)
			results = append(results, map[string]interface{}{
				"account_id": sub.AccountID,
				"error":      err.Error(),
			})
			continue
		}

		if result.Upgraded {
			upgraded++
		}

		results = append(results, map[string]interface{}{
			"account_id":       sub.AccountID,
			"calculated_users": actualUserCount,
			"result":           result,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"message":  "Upgrade check completed",
		"checked":  checked,
		"upgraded": upgraded,
		"results":  results,
	})
}

// CheckSingleAccountUpgradeHandler checks and upgrades a single account
// User count is automatically calculated from the account's analytics data
func (s *AutoUpgradeService) CheckSingleAccountUpgradeHandler(c *gin.Context) {
	accountID := c.Param("account_id")
	if accountID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Account ID required"})
		return
	}

	// Calculate actual paid user count from analytics data
	actualUserCount, err := s.GetPaidUserCount(accountID)
	if err != nil {
		// Fall back to stored user count
		var sub AccountSubscription
		if err := s.db.Where("account_id = ?", accountID).First(&sub).Error; err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "Subscription not found"})
			return
		}
		actualUserCount = sub.UserCount
		log.Printf("⚠️ Using stored user count for account %s: %d", accountID, actualUserCount)
	}

	result, err := s.CheckAndUpgradeAccount(accountID, actualUserCount)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"account_id":       accountID,
		"calculated_users": actualUserCount,
		"result":           result,
	})
}

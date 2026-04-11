package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stripe/stripe-go/v72"
	"github.com/stripe/stripe-go/v72/paymentmethod"
	"gorm.io/gorm"
)

// LifetimeService handles lifetime key generation, redemption, and billing
type LifetimeService struct {
	db           *gorm.DB
	emailService *EmailService
}

// NewLifetimeService creates a new LifetimeService
func NewLifetimeService(db *gorm.DB, emailService *EmailService) *LifetimeService {
	return &LifetimeService{db: db, emailService: emailService}
}

// generateKey creates a cryptographically random key like "MQ-XXXX-XXXX-XXXX-XXXX"
func generateKey() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	h := hex.EncodeToString(b)
	return fmt.Sprintf("MQ-%s-%s-%s-%s", h[0:4], h[4:8], h[8:12], h[12:16]), nil
}

// --- Admin Endpoints ---

// AdminGenerateKeysRequest is the request body for generating lifetime keys
type AdminGenerateKeysRequest struct {
	Count int    `json:"count" binding:"required,min=1,max=100"`
	Note  string `json:"note"`
}

// AdminGenerateKeysHandler generates one or more lifetime activation keys
func (s *LifetimeService) AdminGenerateKeysHandler(c *gin.Context) {
	adminAccountID, _ := c.Get("account_id")

	var req AdminGenerateKeysRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var keys []LifetimeKey
	for i := 0; i < req.Count; i++ {
		keyStr, err := generateKey()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate key"})
			return
		}

		key := LifetimeKey{
			ID:        uuid.New().String(),
			Key:       keyStr,
			CreatedAt: time.Now(),
			CreatedBy: adminAccountID.(string),
			Note:      req.Note,
		}
		if err := s.db.Create(&key).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save key"})
			return
		}
		keys = append(keys, key)
	}

	c.JSON(http.StatusOK, gin.H{
		"message": fmt.Sprintf("Generated %d key(s)", len(keys)),
		"keys":    keys,
	})
}

// AdminListKeysHandler lists all lifetime keys with optional filters
func (s *LifetimeService) AdminListKeysHandler(c *gin.Context) {
	filter := c.DefaultQuery("filter", "all") // all, redeemed, available

	query := s.db.Model(&LifetimeKey{}).Order("created_at DESC")
	switch filter {
	case "redeemed":
		query = query.Where("is_redeemed = true")
	case "available":
		query = query.Where("is_redeemed = false")
	}

	var keys []LifetimeKey
	query.Find(&keys)

	c.JSON(http.StatusOK, gin.H{"keys": keys})
}

// --- User Endpoints ---

// RedeemKeyRequest is the request body for redeeming a lifetime key
type RedeemKeyRequest struct {
	Key string `json:"key" binding:"required"`
}

// RedeemKeyHandler allows a user to redeem a lifetime activation key
func (s *LifetimeService) RedeemKeyHandler(c *gin.Context) {
	accountID, _ := c.Get("account_id")
	accID := accountID.(string)

	var req RedeemKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Key is required"})
		return
	}

	// Check if account already has a lifetime plan
	var existingSub AccountSubscription
	if err := s.db.Where("account_id = ?", accID).First(&existingSub).Error; err == nil {
		if IsLifetimeTier(existingSub.Tier) {
			c.JSON(http.StatusConflict, gin.H{"error": "Account already has a lifetime plan"})
			return
		}
	}

	// Find and validate the key
	var key LifetimeKey
	if err := s.db.Where("key = ?", req.Key).First(&key).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Invalid activation key"})
		return
	}

	if key.IsRedeemed {
		c.JSON(http.StatusConflict, gin.H{"error": "This key has already been redeemed"})
		return
	}

	now := time.Now()

	// Use a transaction: mark key redeemed + create/update subscription
	txErr := s.db.Transaction(func(tx *gorm.DB) error {
		// Mark key as redeemed
		if err := tx.Model(&key).Updates(map[string]interface{}{
			"is_redeemed": true,
			"redeemed_at": now,
			"redeemed_by": accID,
		}).Error; err != nil {
			return err
		}

		// Upsert subscription to lifetime tier
		var sub AccountSubscription
		err := tx.Where("account_id = ?", accID).First(&sub).Error
		if err != nil {
			// Create new subscription
			sub = AccountSubscription{
				ID:                 uuid.New().String(),
				AccountID:          accID,
				Tier:               "lifetime",
				Status:             "active",
				MonthlyPrice:       0,
				CurrentPeriodStart: now,
				CurrentPeriodEnd:   now.AddDate(100, 0, 0), // effectively forever
				CreatedAt:          now,
				UpdatedAt:          now,
			}
			return tx.Create(&sub).Error
		}

		// Update existing subscription
		return tx.Model(&sub).Updates(map[string]interface{}{
			"tier":                 "lifetime",
			"status":              "active",
			"monthly_price":       0,
			"current_period_start": now,
			"current_period_end":   now.AddDate(100, 0, 0),
			"cancel_at_period_end": false,
			"canceled_at":          nil,
			"usage_paused":         false,
			"usage_paused_at":      nil,
			"updated_at":           now,
		}).Error
	})

	if txErr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to activate lifetime plan"})
		return
	}

	log.Printf("Lifetime key redeemed: key=%s account=%s", key.Key, accID)

	c.JSON(http.StatusOK, gin.H{
		"message": "Lifetime plan activated successfully",
		"tier":    "lifetime",
	})
}

// --- Subscription Info & Cancel ---

// GetSubscriptionHandler returns the current account subscription info
func (s *LifetimeService) GetSubscriptionHandler(c *gin.Context) {
	accountID, _ := c.Get("account_id")
	accID := accountID.(string)

	var sub AccountSubscription
	if err := s.db.Where("account_id = ?", accID).First(&sub).Error; err != nil {
		c.JSON(http.StatusOK, gin.H{"subscription": nil})
		return
	}

	tier := GetTierByID(sub.Tier)
	tierName := sub.Tier
	if tier != nil {
		tierName = tier.Name
	}

	hasCard := s.accountHasPaymentMethod(accID)

	c.JSON(http.StatusOK, gin.H{
		"subscription": gin.H{
			"id":                   sub.ID,
			"tier":                 sub.Tier,
			"tier_name":            tierName,
			"status":               sub.Status,
			"monthly_price":        sub.MonthlyPrice,
			"current_period_start": sub.CurrentPeriodStart,
			"current_period_end":   sub.CurrentPeriodEnd,
			"cancel_at_period_end": sub.CancelAtPeriodEnd,
			"is_lifetime":          IsLifetimeTier(sub.Tier),
			"usage_paused":         sub.UsagePaused,
			"usage_paused_reason":  sub.UsagePausedReason,
		},
		"has_payment_method": hasCard,
	})
}

// RequestCancelHandler sends a cancellation request email to info@mentiq.com.
// No input required — just a single-click action.
func (s *LifetimeService) RequestCancelHandler(c *gin.Context) {
	accountID, _ := c.Get("account_id")
	accID := accountID.(string)

	var account Account
	if err := s.db.Where("id = ?", accID).First(&account).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Account not found"})
		return
	}

	var sub AccountSubscription
	s.db.Where("account_id = ?", accID).First(&sub)

	subject := fmt.Sprintf("Cancellation Request - %s (%s)", account.Name, account.Email)
	body := fmt.Sprintf(`
		<h2>Cancellation Request</h2>
		<p><strong>Account:</strong> %s</p>
		<p><strong>Email:</strong> %s</p>
		<p><strong>Account ID:</strong> %s</p>
		<p><strong>Current Tier:</strong> %s</p>
		<p><strong>Requested At:</strong> %s</p>
	`, account.Name, account.Email, accID, sub.Tier, time.Now().Format(time.RFC3339))

	if err := s.emailService.sendEmail("info@mentiq.com", "MentiQ Team", subject, body, ""); err != nil {
		log.Printf("Failed to send cancellation email for account %s: %v", accID, err)
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Your cancellation request has been submitted. Our team will get back to you shortly.",
	})
}

// --- Stripe Payment Method Check ---

// accountHasPaymentMethod checks Stripe for a payment method on file.
// Falls back to the DB flag if Stripe is not configured or the customer has no Stripe ID.
func (s *LifetimeService) accountHasPaymentMethod(accountID string) bool {
	var account Account
	if err := s.db.Where("id = ?", accountID).First(&account).Error; err != nil {
		return false
	}

	// If no Stripe customer, fall back to DB flag
	if account.StripeCustomerID == "" {
		return account.HasPaymentMethod
	}

	stripeKey := os.Getenv("STRIPE_API_KEY")
	if stripeKey == "" {
		return account.HasPaymentMethod
	}
	stripe.Key = stripeKey

	params := &stripe.PaymentMethodListParams{
		Customer: stripe.String(account.StripeCustomerID),
		Type:     stripe.String("card"),
	}
	params.Filters.AddFilter("limit", "", "1")

	iter := paymentmethod.List(params)
	hasCard := iter.Next() // true if at least one card exists

	// Sync the DB flag so other code paths have a cached value
	if hasCard != account.HasPaymentMethod {
		s.db.Model(&account).Update("has_payment_method", hasCard)
	}

	return hasCard
}

// --- Lifetime Overage Check ---

// CheckLifetimeOverage checks if a lifetime account has hit its limits.
// If no payment method is on file, pauses usage. Otherwise allows overage billing.
// Returns true if usage should be blocked.
func (s *LifetimeService) CheckLifetimeOverage(accountID string) (paused bool) {
	var sub AccountSubscription
	if err := s.db.Where("account_id = ?", accountID).First(&sub).Error; err != nil {
		return false
	}

	if !IsLifetimeTier(sub.Tier) {
		return false
	}

	if sub.UsagePaused {
		return true
	}

	// Check if any resource is over limit
	tier := &LifetimeTier
	usageSvc := NewUsageService(s.db)
	usage, err := usageSvc.GetOrCreateUsage(accountID)
	if err != nil {
		return false
	}

	isOverLimit := false
	for _, r := range AllResources {
		current := usageSvc.getCountFromUsage(usage, r)
		limit := usageSvc.getIncludedLimit(tier, r)
		if current > limit {
			isOverLimit = true
			break
		}
	}

	if !isOverLimit {
		return false
	}

	// Over limit — check Stripe for a real payment method
	if s.accountHasPaymentMethod(accountID) {
		return false // has card → allow overages (they will be billed)
	}

	// No card: pause usage
	now := time.Now()
	reason := "Usage limit reached. Please add a payment method to continue."
	s.db.Model(&sub).Updates(map[string]interface{}{
		"usage_paused":        true,
		"usage_paused_at":     now,
		"usage_paused_reason": reason,
	})

	log.Printf("Paused usage for lifetime account %s — no payment method on file", accountID)
	return true
}

// UnpauseUsageHandler allows a user to unpause usage after adding a payment method
func (s *LifetimeService) UnpauseUsageHandler(c *gin.Context) {
	accountID, _ := c.Get("account_id")
	accID := accountID.(string)

	if !s.accountHasPaymentMethod(accID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Please add a payment method in Stripe first"})
		return
	}

	s.db.Model(&AccountSubscription{}).Where("account_id = ?", accID).Updates(map[string]interface{}{
		"usage_paused":        false,
		"usage_paused_at":     nil,
		"usage_paused_reason": nil,
	})

	c.JSON(http.StatusOK, gin.H{"message": "Usage has been resumed"})
}

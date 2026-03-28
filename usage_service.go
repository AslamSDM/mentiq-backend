package main

import (
	"fmt"
	"log"
	"math"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// UsageService handles usage tracking, limit checking, and overage calculations
type UsageService struct {
	db *gorm.DB
}

// NewUsageService creates a new UsageService
func NewUsageService(db *gorm.DB) *UsageService {
	return &UsageService{db: db}
}

// ResourceType identifies a billable resource
type ResourceType string

const (
	ResourcePaidUsers       ResourceType = "paid_users"
	ResourceSessionReplays  ResourceType = "session_replays"
	ResourceAutomatedEmails ResourceType = "automated_emails"
	ResourceAIGenerations   ResourceType = "ai_generations"
)

// UsageStatus represents the current usage state for a resource
type UsageStatus struct {
	Resource     ResourceType `json:"resource"`
	CurrentUsage int          `json:"current_usage"`
	Limit        int          `json:"limit"`
	IsOverride   bool         `json:"is_override"` // true if admin override is active
	Overage      int          `json:"overage"`      // amount over limit (0 if within)
	OverageCost  int64        `json:"overage_cost"` // projected overage cost in cents
}

// AccountUsageSummary is the full usage picture for an account
type AccountUsageSummary struct {
	AccountID  string        `json:"account_id"`
	Tier       string        `json:"tier"`
	TierName   string        `json:"tier_name"`
	BasePrice  int64         `json:"base_price"` // in cents
	Resources  []UsageStatus `json:"resources"`
	TotalOverageCost int64   `json:"total_overage_cost"` // in cents
	ProjectedBill    int64   `json:"projected_bill"`     // base + overages in cents
}

// GetOrCreateUsage returns the current billing period usage record, creating one if needed
func (s *UsageService) GetOrCreateUsage(accountID string) (*AccountUsage, error) {
	now := time.Now()
	var usage AccountUsage
	err := s.db.Where("account_id = ? AND billing_period_start <= ? AND billing_period_end >= ?",
		accountID, now, now).First(&usage).Error

	if err == nil {
		return &usage, nil
	}

	// Determine billing period from subscription, or default to current month
	periodStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	periodEnd := periodStart.AddDate(0, 1, 0).Add(-time.Second)

	var sub AccountSubscription
	if err := s.db.Where("account_id = ?", accountID).First(&sub).Error; err == nil {
		if sub.CurrentPeriodEnd.After(now) {
			periodStart = sub.CurrentPeriodStart
			periodEnd = sub.CurrentPeriodEnd
		}
	}

	usage = AccountUsage{
		ID:                 uuid.New().String(),
		AccountID:          accountID,
		BillingPeriodStart: periodStart,
		BillingPeriodEnd:   periodEnd,
		CreatedAt:          now,
		UpdatedAt:          now,
	}

	if err := s.db.Create(&usage).Error; err != nil {
		// Might have been created concurrently
		if err2 := s.db.Where("account_id = ? AND billing_period_start <= ? AND billing_period_end >= ?",
			accountID, now, now).First(&usage).Error; err2 != nil {
			return nil, fmt.Errorf("failed to create usage record: %v", err)
		}
	}

	return &usage, nil
}

// IncrementUsage increments a usage counter for the current billing period
func (s *UsageService) IncrementUsage(accountID string, resource ResourceType, amount int) error {
	usage, err := s.GetOrCreateUsage(accountID)
	if err != nil {
		return err
	}

	var column string
	switch resource {
	case ResourcePaidUsers:
		column = "paid_users_count"
	case ResourceSessionReplays:
		column = "session_replays_count"
	case ResourceAutomatedEmails:
		column = "automated_emails_count"
	case ResourceAIGenerations:
		column = "ai_generations_count"
	default:
		return fmt.Errorf("unknown resource type: %s", resource)
	}

	return s.db.Model(&AccountUsage{}).Where("id = ?", usage.ID).
		Update(column, gorm.Expr(column+" + ?", amount)).Error
}

// SetUsage sets a usage counter to an exact value (used for paid users which are recalculated)
func (s *UsageService) SetUsage(accountID string, resource ResourceType, value int) error {
	usage, err := s.GetOrCreateUsage(accountID)
	if err != nil {
		return err
	}

	var column string
	switch resource {
	case ResourcePaidUsers:
		column = "paid_users_count"
	case ResourceSessionReplays:
		column = "session_replays_count"
	case ResourceAutomatedEmails:
		column = "automated_emails_count"
	case ResourceAIGenerations:
		column = "ai_generations_count"
	default:
		return fmt.Errorf("unknown resource type: %s", resource)
	}

	return s.db.Model(&AccountUsage{}).Where("id = ?", usage.ID).
		Update(column, value).Error
}

// GetEffectiveLimit returns the effective limit for a resource (override if set, else plan default)
func (s *UsageService) GetEffectiveLimit(accountID string, resource ResourceType) (limit int, isOverride bool, err error) {
	// Check for admin override
	var limits AccountLimits
	if err := s.db.Where("account_id = ?", accountID).First(&limits).Error; err == nil {
		switch resource {
		case ResourcePaidUsers:
			if limits.PaidUsersOverride != nil {
				return *limits.PaidUsersOverride, true, nil
			}
		case ResourceSessionReplays:
			if limits.SessionReplaysOverride != nil {
				return *limits.SessionReplaysOverride, true, nil
			}
		case ResourceAutomatedEmails:
			if limits.AutomatedEmailsOverride != nil {
				return *limits.AutomatedEmailsOverride, true, nil
			}
		case ResourceAIGenerations:
			if limits.AIGenerationsOverride != nil {
				return *limits.AIGenerationsOverride, true, nil
			}
		}
	}

	// Fall back to plan default
	var sub AccountSubscription
	s.db.Where("account_id = ?", accountID).First(&sub)

	tier := GetTierByID(sub.Tier)
	if tier == nil {
		// Default to starter tier for accounts without subscription or with legacy tiers
		tier = &TierOrder[0]
	}

	switch resource {
	case ResourcePaidUsers:
		return tier.IncludedPaidUsers, false, nil
	case ResourceSessionReplays:
		return tier.IncludedSessionReplays, false, nil
	case ResourceAutomatedEmails:
		return tier.IncludedAutomatedEmails, false, nil
	case ResourceAIGenerations:
		return tier.IncludedAIGenerations, false, nil
	}

	return 0, false, fmt.Errorf("unknown resource: %s", resource)
}

// CalculateOverageCost calculates the overage cost for a single resource
func CalculateOverageCost(tier *PricingTier, resource ResourceType, overage int) int64 {
	if overage <= 0 {
		return 0
	}

	switch resource {
	case ResourcePaidUsers:
		// $12 per +100 users (starter), charged per block
		blocks := int64(math.Ceil(float64(overage) / 100.0))
		return blocks * tier.OveragePaidUsersPer100
	case ResourceSessionReplays:
		blocks := int64(math.Ceil(float64(overage) / 500.0))
		return blocks * tier.OverageReplaysPer500
	case ResourceAutomatedEmails:
		blocks := int64(math.Ceil(float64(overage) / 10000.0))
		return blocks * tier.OverageEmailsPer10k
	case ResourceAIGenerations:
		blocks := int64(math.Ceil(float64(overage) / 100.0))
		return blocks * tier.OverageAIGenerationsPer100
	}
	return 0
}

// GetAccountUsageSummary returns the full usage summary for an account
func (s *UsageService) GetAccountUsageSummary(accountID string) (*AccountUsageSummary, error) {
	// Verify account exists
	var account Account
	if err := s.db.Where("id = ?", accountID).First(&account).Error; err != nil {
		return nil, fmt.Errorf("account not found")
	}

	var sub AccountSubscription
	s.db.Where("account_id = ?", accountID).First(&sub)

	tier := GetTierByID(sub.Tier)
	if tier == nil {
		// Fall back to starter tier for accounts with no subscription or legacy tiers
		tier = &TierOrder[0]
	}

	usage, err := s.GetOrCreateUsage(accountID)
	if err != nil {
		return nil, err
	}

	resources := []ResourceType{ResourcePaidUsers, ResourceSessionReplays, ResourceAutomatedEmails, ResourceAIGenerations}
	usageCounts := map[ResourceType]int{
		ResourcePaidUsers:       usage.PaidUsersCount,
		ResourceSessionReplays:  usage.SessionReplaysCount,
		ResourceAutomatedEmails: usage.AutomatedEmailsCount,
		ResourceAIGenerations:   usage.AIGenerationsCount,
	}

	var statuses []UsageStatus
	var totalOverage int64

	for _, r := range resources {
		limit, isOverride, _ := s.GetEffectiveLimit(accountID, r)
		current := usageCounts[r]
		overage := 0
		if current > limit {
			overage = current - limit
		}
		cost := CalculateOverageCost(tier, r, overage)
		totalOverage += cost

		statuses = append(statuses, UsageStatus{
			Resource:     r,
			CurrentUsage: current,
			Limit:        limit,
			IsOverride:   isOverride,
			Overage:      overage,
			OverageCost:  cost,
		})
	}

	return &AccountUsageSummary{
		AccountID:        accountID,
		Tier:             tier.ID,
		TierName:         tier.Name,
		BasePrice:        tier.BasePrice * 100, // convert to cents
		Resources:        statuses,
		TotalOverageCost: totalOverage,
		ProjectedBill:    tier.BasePrice*100 + totalOverage,
	}, nil
}

// --- Admin Handlers ---

// AdminGetAccountLimitsHandler returns the limits and usage for an account
func (s *UsageService) AdminGetAccountLimitsHandler(c *gin.Context) {
	accountID := c.Param("account_id")

	summary, err := s.GetAccountUsageSummary(accountID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	// Also return the raw override values
	var limits AccountLimits
	s.db.Where("account_id = ?", accountID).First(&limits)

	c.JSON(http.StatusOK, gin.H{
		"summary":   summary,
		"overrides": limits,
	})
}

// AdminUpdateLimitsRequest is the request body for updating account limits
type AdminUpdateLimitsRequest struct {
	PaidUsersOverride       *int `json:"paid_users_override"`
	SessionReplaysOverride  *int `json:"session_replays_override"`
	AutomatedEmailsOverride *int `json:"automated_emails_override"`
	AIGenerationsOverride   *int `json:"ai_generations_override"`
}

// AdminUpdateAccountLimitsHandler sets admin overrides on an account's limits
func (s *UsageService) AdminUpdateAccountLimitsHandler(c *gin.Context) {
	accountID := c.Param("account_id")

	var req AdminUpdateLimitsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Verify account exists
	var account Account
	if err := s.db.Where("id = ?", accountID).First(&account).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Account not found"})
		return
	}

	// Upsert limits
	var limits AccountLimits
	err := s.db.Where("account_id = ?", accountID).First(&limits).Error
	if err != nil {
		// Create new
		limits = AccountLimits{
			ID:                      uuid.New().String(),
			AccountID:               accountID,
			PaidUsersOverride:       req.PaidUsersOverride,
			SessionReplaysOverride:  req.SessionReplaysOverride,
			AutomatedEmailsOverride: req.AutomatedEmailsOverride,
			AIGenerationsOverride:   req.AIGenerationsOverride,
			CreatedAt:               time.Now(),
			UpdatedAt:               time.Now(),
		}
		if err := s.db.Create(&limits).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create limits"})
			return
		}
	} else {
		// Update existing
		updates := map[string]interface{}{
			"paid_users_override":       req.PaidUsersOverride,
			"session_replays_override":  req.SessionReplaysOverride,
			"automated_emails_override": req.AutomatedEmailsOverride,
			"ai_generations_override":   req.AIGenerationsOverride,
			"updated_at":               time.Now(),
		}
		if err := s.db.Model(&limits).Updates(updates).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update limits"})
			return
		}
	}

	log.Printf("Admin updated limits for account %s: users=%v, replays=%v, emails=%v, ai=%v",
		accountID, req.PaidUsersOverride, req.SessionReplaysOverride,
		req.AutomatedEmailsOverride, req.AIGenerationsOverride)

	// Return updated summary
	summary, _ := s.GetAccountUsageSummary(accountID)
	c.JSON(http.StatusOK, gin.H{
		"message":   "Limits updated successfully",
		"summary":   summary,
		"overrides": limits,
	})
}

// AdminResetAccountLimitHandler removes an admin override for a specific resource
func (s *UsageService) AdminResetAccountLimitHandler(c *gin.Context) {
	accountID := c.Param("account_id")
	resource := c.Param("resource")

	var column string
	switch ResourceType(resource) {
	case ResourcePaidUsers:
		column = "paid_users_override"
	case ResourceSessionReplays:
		column = "session_replays_override"
	case ResourceAutomatedEmails:
		column = "automated_emails_override"
	case ResourceAIGenerations:
		column = "ai_generations_override"
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid resource type"})
		return
	}

	if err := s.db.Model(&AccountLimits{}).Where("account_id = ?", accountID).
		Update(column, nil).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to reset limit"})
		return
	}

	summary, _ := s.GetAccountUsageSummary(accountID)
	c.JSON(http.StatusOK, gin.H{
		"message": fmt.Sprintf("Reset %s limit to plan default", resource),
		"summary": summary,
	})
}

// AdminGetAccountUsageHandler returns just the usage counters for an account
func (s *UsageService) AdminGetAccountUsageHandler(c *gin.Context) {
	accountID := c.Param("account_id")

	usage, err := s.GetOrCreateUsage(accountID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, usage)
}

// GetPlanDefaults returns the default limits for a tier
func GetPlanDefaults(tierID string) map[string]int {
	tier := GetTierByID(tierID)
	if tier == nil {
		return nil
	}
	return map[string]int{
		"paid_users":       tier.IncludedPaidUsers,
		"session_replays":  tier.IncludedSessionReplays,
		"automated_emails": tier.IncludedAutomatedEmails,
		"ai_generations":   tier.IncludedAIGenerations,
		"team_members":     tier.IncludedTeamMembers,
	}
}

// AdminGetPlanDefaultsHandler returns plan defaults for a given tier
func AdminGetPlanDefaultsHandler(c *gin.Context) {
	tierID := c.Param("tier_id")
	defaults := GetPlanDefaults(tierID)
	if defaults == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Unknown tier"})
		return
	}
	c.JSON(http.StatusOK, defaults)
}

// AdminGetAllTiersHandler returns all available tiers with their limits and overage rates
func AdminGetAllTiersHandler(c *gin.Context) {
	type TierResponse struct {
		ID                      string `json:"id"`
		Name                    string `json:"name"`
		BasePrice               int64  `json:"base_price"`
		IncludedPaidUsers       int    `json:"included_paid_users"`
		IncludedSessionReplays  int    `json:"included_session_replays"`
		IncludedAutomatedEmails int    `json:"included_automated_emails"`
		IncludedAIGenerations   int    `json:"included_ai_generations"`
		IncludedTeamMembers     int    `json:"included_team_members"`
		OveragePaidUsersPer100     int64 `json:"overage_paid_users_per_100"`
		OverageReplaysPer500       int64 `json:"overage_replays_per_500"`
		OverageEmailsPer10k        int64 `json:"overage_emails_per_10k"`
		OverageAIGenerationsPer100 int64 `json:"overage_ai_generations_per_100"`
	}

	var tiers []TierResponse
	for _, t := range TierOrder {
		tiers = append(tiers, TierResponse{
			ID:                      t.ID,
			Name:                    t.Name,
			BasePrice:               t.BasePrice,
			IncludedPaidUsers:       t.IncludedPaidUsers,
			IncludedSessionReplays:  t.IncludedSessionReplays,
			IncludedAutomatedEmails: t.IncludedAutomatedEmails,
			IncludedAIGenerations:   t.IncludedAIGenerations,
			IncludedTeamMembers:     t.IncludedTeamMembers,
			OveragePaidUsersPer100:     t.OveragePaidUsersPer100,
			OverageReplaysPer500:       t.OverageReplaysPer500,
			OverageEmailsPer10k:        t.OverageEmailsPer10k,
			OverageAIGenerationsPer100: t.OverageAIGenerationsPer100,
		})
	}

	c.JSON(http.StatusOK, gin.H{"tiers": tiers})
}

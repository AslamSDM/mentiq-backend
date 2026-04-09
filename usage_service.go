package main

import (
	"errors"
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
	ResourceTeamMembers     ResourceType = "team_members"
)

// AllResources is the ordered list of all billable resources
var AllResources = []ResourceType{
	ResourcePaidUsers, ResourceSessionReplays, ResourceAutomatedEmails,
	ResourceAIGenerations, ResourceTeamMembers,
}

// UsageStatus represents the current usage state for a resource
type UsageStatus struct {
	Resource     ResourceType `json:"resource"`
	CurrentUsage int          `json:"current_usage"`
	Limit        int          `json:"limit"`
	IsOverride   bool         `json:"is_override"` // true if admin override is active
	Overage      int          `json:"overage"`      // amount over limit (0 if within)
	OverageCost  int64        `json:"overage_cost"` // projected overage cost in cents
	Percentage   float64      `json:"percentage"`   // usage as % of limit (can exceed 100)
}

// ThresholdAlert is emitted when usage crosses 80% or 100% of a limit
type ThresholdAlert struct {
	Resource   ResourceType `json:"resource"`
	Threshold  int          `json:"threshold"` // 80 or 100
	Current    int          `json:"current_usage"`
	Limit      int          `json:"limit"`
	Percentage float64      `json:"percentage"`
}

// AccountUsageSummary is the full usage picture for an account
type AccountUsageSummary struct {
	AccountID          string        `json:"account_id"`
	Tier               string        `json:"tier"`
	TierName           string        `json:"tier_name"`
	BasePrice          int64         `json:"base_price"` // in cents
	BillingPeriodStart time.Time     `json:"billing_period_start"`
	BillingPeriodEnd   time.Time     `json:"billing_period_end"`
	Resources          []UsageStatus `json:"resources"`
	TotalOverageCost   int64         `json:"total_overage_cost"` // in cents
	ProjectedBill      int64         `json:"projected_bill"`     // base + overages in cents
	Alerts             []ThresholdAlert `json:"alerts,omitempty"`
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

	// Archive any expired usage record before creating a new one
	s.archiveExpiredUsage(accountID, periodStart)

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

// archiveExpiredUsage snapshots the previous period's usage into UsageHistory
func (s *UsageService) archiveExpiredUsage(accountID string, newPeriodStart time.Time) {
	var old AccountUsage
	err := s.db.Where("account_id = ? AND billing_period_end < ?", accountID, newPeriodStart).
		Order("billing_period_end DESC").First(&old).Error
	if err != nil {
		return // no previous record to archive
	}

	// Check if already archived
	var count int64
	s.db.Model(&UsageHistory{}).Where("account_id = ? AND billing_period_start = ?",
		accountID, old.BillingPeriodStart).Count(&count)
	if count > 0 {
		return // already archived
	}

	var sub AccountSubscription
	s.db.Where("account_id = ?", accountID).First(&sub)
	tier := GetTierByID(sub.Tier)
	if tier == nil {
		tier = &TierOrder[0]
	}

	// Calculate final overages
	totalOverage := int64(0)
	for _, r := range AllResources {
		current := s.getCountFromUsage(&old, r)
		limit := s.getIncludedLimit(tier, r)
		if current > limit {
			totalOverage += CalculateOverageCost(tier, r, current-limit)
		}
	}

	history := UsageHistory{
		ID:                   uuid.New().String(),
		AccountID:            accountID,
		CreatedAt:            time.Now(),
		BillingPeriodStart:   old.BillingPeriodStart,
		BillingPeriodEnd:     old.BillingPeriodEnd,
		Tier:                 tier.ID,
		PaidUsersCount:       old.PaidUsersCount,
		SessionReplaysCount:  old.SessionReplaysCount,
		AutomatedEmailsCount: old.AutomatedEmailsCount,
		AIGenerationsCount:   old.AIGenerationsCount,
		TeamMembersCount:     old.TeamMembersCount,
		TotalOverageCost:     totalOverage,
		ProjectedBill:        tier.BasePrice*100 + totalOverage,
	}

	// Use a transaction to archive and delete atomically
	txErr := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&history).Error; err != nil {
			return fmt.Errorf("failed to create history: %w", err)
		}
		if err := tx.Delete(&old).Error; err != nil {
			return fmt.Errorf("failed to delete old usage: %w", err)
		}
		return nil
	})
	if txErr != nil {
		log.Printf("Failed to archive usage for account %s: %v", accountID, txErr)
	}
}

// IncrementUsage increments a usage counter for the current billing period
func (s *UsageService) IncrementUsage(accountID string, resource ResourceType, amount int) error {
	usage, err := s.GetOrCreateUsage(accountID)
	if err != nil {
		return err
	}

	column := resourceColumn(resource)
	if column == "" {
		return fmt.Errorf("unknown resource type: %s", resource)
	}

	var newTotal int
	txErr := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&AccountUsage{}).Where("id = ?", usage.ID).
			Update(column, gorm.Expr(column+" + ?", amount)).Error; err != nil {
			return err
		}
		// Re-read within the same transaction for an accurate total
		if err := tx.First(usage, "id = ?", usage.ID).Error; err != nil {
			return err
		}
		newTotal = s.getCountFromUsage(usage, resource)
		return nil
	})
	if txErr != nil {
		return txErr
	}

	s.writeAuditLog(accountID, resource, "increment", amount, newTotal, "api", "")

	return nil
}

// SetUsage sets a usage counter to an exact value (used for paid users which are recalculated)
func (s *UsageService) SetUsage(accountID string, resource ResourceType, value int) error {
	usage, err := s.GetOrCreateUsage(accountID)
	if err != nil {
		return err
	}

	column := resourceColumn(resource)
	if column == "" {
		return fmt.Errorf("unknown resource type: %s", resource)
	}

	if err := s.db.Model(&AccountUsage{}).Where("id = ?", usage.ID).
		Update(column, value).Error; err != nil {
		return err
	}

	s.writeAuditLog(accountID, resource, "set", value, value, "api", "")

	return nil
}

// SetTeamMembersPeak updates team members count only if the new value exceeds the current peak
func (s *UsageService) SetTeamMembersPeak(accountID string, currentCount int) error {
	usage, err := s.GetOrCreateUsage(accountID)
	if err != nil {
		return err
	}

	if currentCount <= usage.TeamMembersCount {
		return nil // not a new peak
	}

	if err := s.db.Model(&AccountUsage{}).Where("id = ?", usage.ID).
		Update("team_members_count", currentCount).Error; err != nil {
		return err
	}

	s.writeAuditLog(accountID, ResourceTeamMembers, "set", currentCount, currentCount, "api", "peak update")

	return nil
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
		case ResourceTeamMembers:
			if limits.TeamMembersOverride != nil {
				return *limits.TeamMembersOverride, true, nil
			}
		}
	}

	// Fall back to plan default
	var sub AccountSubscription
	s.db.Where("account_id = ?", accountID).First(&sub)

	tier := GetTierByID(sub.Tier)
	if tier == nil {
		tier = &TierOrder[0]
	}

	return s.getIncludedLimit(tier, resource), false, nil
}

// CalculateOverageCost calculates the overage cost for a single resource
func CalculateOverageCost(tier *PricingTier, resource ResourceType, overage int) int64 {
	if overage <= 0 {
		return 0
	}

	switch resource {
	case ResourcePaidUsers:
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
	case ResourceTeamMembers:
		return int64(overage) * tier.OverageTeamMembersPer1
	}
	return 0
}

// ErrAccountNotFound is returned when an account does not exist
var ErrAccountNotFound = errors.New("account not found")

// GetAccountUsageSummary returns the full usage summary for an account
func (s *UsageService) GetAccountUsageSummary(accountID string) (*AccountUsageSummary, error) {
	var account Account
	if err := s.db.Where("id = ?", accountID).First(&account).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrAccountNotFound
		}
		return nil, fmt.Errorf("failed to look up account: %w", err)
	}

	var sub AccountSubscription
	s.db.Where("account_id = ?", accountID).First(&sub)

	tier := GetTierByID(sub.Tier)
	if tier == nil {
		tier = &TierOrder[0]
	}

	usage, err := s.GetOrCreateUsage(accountID)
	if err != nil {
		return nil, err
	}

	var statuses []UsageStatus
	var alerts []ThresholdAlert
	var totalOverage int64

	for _, r := range AllResources {
		limit, isOverride, _ := s.GetEffectiveLimit(accountID, r)
		current := s.getCountFromUsage(usage, r)
		overage := 0
		if current > limit {
			overage = current - limit
		}
		cost := CalculateOverageCost(tier, r, overage)
		totalOverage += cost

		pct := float64(0)
		if limit > 0 {
			pct = float64(current) / float64(limit) * 100
		}

		statuses = append(statuses, UsageStatus{
			Resource:     r,
			CurrentUsage: current,
			Limit:        limit,
			IsOverride:   isOverride,
			Overage:      overage,
			OverageCost:  cost,
			Percentage:   math.Round(pct*10) / 10,
		})

		// Check thresholds
		if pct >= 100 {
			alerts = append(alerts, ThresholdAlert{
				Resource: r, Threshold: 100, Current: current, Limit: limit, Percentage: pct,
			})
		} else if pct >= 80 {
			alerts = append(alerts, ThresholdAlert{
				Resource: r, Threshold: 80, Current: current, Limit: limit, Percentage: pct,
			})
		}
	}

	return &AccountUsageSummary{
		AccountID:          accountID,
		Tier:               tier.ID,
		TierName:           tier.Name,
		BasePrice:          tier.BasePrice * 100,
		BillingPeriodStart: usage.BillingPeriodStart,
		BillingPeriodEnd:   usage.BillingPeriodEnd,
		Resources:          statuses,
		TotalOverageCost:   totalOverage,
		ProjectedBill:      tier.BasePrice*100 + totalOverage,
		Alerts:             alerts,
	}, nil
}

// --- Helper methods ---

func resourceColumn(r ResourceType) string {
	switch r {
	case ResourcePaidUsers:
		return "paid_users_count"
	case ResourceSessionReplays:
		return "session_replays_count"
	case ResourceAutomatedEmails:
		return "automated_emails_count"
	case ResourceAIGenerations:
		return "ai_generations_count"
	case ResourceTeamMembers:
		return "team_members_count"
	}
	return ""
}

func (s *UsageService) getCountFromUsage(u *AccountUsage, r ResourceType) int {
	switch r {
	case ResourcePaidUsers:
		return u.PaidUsersCount
	case ResourceSessionReplays:
		return u.SessionReplaysCount
	case ResourceAutomatedEmails:
		return u.AutomatedEmailsCount
	case ResourceAIGenerations:
		return u.AIGenerationsCount
	case ResourceTeamMembers:
		return u.TeamMembersCount
	}
	return 0
}

func (s *UsageService) getIncludedLimit(tier *PricingTier, r ResourceType) int {
	switch r {
	case ResourcePaidUsers:
		return tier.IncludedPaidUsers
	case ResourceSessionReplays:
		return tier.IncludedSessionReplays
	case ResourceAutomatedEmails:
		return tier.IncludedAutomatedEmails
	case ResourceAIGenerations:
		return tier.IncludedAIGenerations
	case ResourceTeamMembers:
		return tier.IncludedTeamMembers
	}
	return 0
}

func (s *UsageService) writeAuditLog(accountID string, resource ResourceType, action string, amount, newTotal int, source, detail string) {
	entry := UsageAuditLog{
		ID:        uuid.New().String(),
		AccountID: accountID,
		CreatedAt: time.Now(),
		Resource:  string(resource),
		Action:    action,
		Amount:    amount,
		NewTotal:  newTotal,
		Source:    source,
		Detail:    detail,
	}
	if err := s.db.Create(&entry).Error; err != nil {
		log.Printf("Failed to write usage audit log for account %s: %v", accountID, err)
	}
}

// --- User-facing Handler ---

// GetUsageSummaryHandler returns the usage summary for the authenticated account
func (s *UsageService) GetUsageSummaryHandler(c *gin.Context) {
	accountID, exists := c.Get("account_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Not authenticated"})
		return
	}

	summary, err := s.GetAccountUsageSummary(accountID.(string))
	if err != nil {
		if errors.Is(err, ErrAccountNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load usage data"})
		}
		return
	}

	// Include upgrade suggestion if overages are significant
	var upgradeSuggestion *map[string]interface{}
	if summary.TotalOverageCost > 2000 { // > $20 in overages
		nextTier := GetNextTier(summary.Tier)
		if nextTier != nil {
			suggestion := map[string]interface{}{
				"tier":       nextTier.ID,
				"name":       nextTier.Name,
				"base_price": nextTier.BasePrice * 100,
				"message":    fmt.Sprintf("Upgrade to %s to get higher included limits and lower overage rates", nextTier.Name),
			}
			upgradeSuggestion = &suggestion
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"summary":            summary,
		"upgrade_suggestion": upgradeSuggestion,
	})
}

// GetUsageHistoryHandler returns historical monthly usage for the authenticated account
func (s *UsageService) GetUsageHistoryHandler(c *gin.Context) {
	accountID, exists := c.Get("account_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Not authenticated"})
		return
	}

	var history []UsageHistory
	s.db.Where("account_id = ?", accountID.(string)).
		Order("billing_period_start DESC").
		Limit(12).
		Find(&history)

	c.JSON(http.StatusOK, gin.H{"history": history})
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
	TeamMembersOverride     *int `json:"team_members_override"`
}

// AdminUpdateAccountLimitsHandler sets admin overrides on an account's limits
func (s *UsageService) AdminUpdateAccountLimitsHandler(c *gin.Context) {
	accountID := c.Param("account_id")

	var req AdminUpdateLimitsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Validate that overrides are non-negative
	for _, v := range []*int{req.PaidUsersOverride, req.SessionReplaysOverride, req.AutomatedEmailsOverride, req.AIGenerationsOverride, req.TeamMembersOverride} {
		if v != nil && *v < 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Override values must be non-negative"})
			return
		}
	}

	var account Account
	if err := s.db.Where("id = ?", accountID).First(&account).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Account not found"})
		return
	}

	var limits AccountLimits
	err := s.db.Where("account_id = ?", accountID).First(&limits).Error
	if err != nil {
		limits = AccountLimits{
			ID:                      uuid.New().String(),
			AccountID:               accountID,
			PaidUsersOverride:       req.PaidUsersOverride,
			SessionReplaysOverride:  req.SessionReplaysOverride,
			AutomatedEmailsOverride: req.AutomatedEmailsOverride,
			AIGenerationsOverride:   req.AIGenerationsOverride,
			TeamMembersOverride:     req.TeamMembersOverride,
			CreatedAt:               time.Now(),
			UpdatedAt:               time.Now(),
		}
		if err := s.db.Create(&limits).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create limits"})
			return
		}
	} else {
		updates := map[string]interface{}{
			"paid_users_override":       req.PaidUsersOverride,
			"session_replays_override":  req.SessionReplaysOverride,
			"automated_emails_override": req.AutomatedEmailsOverride,
			"ai_generations_override":   req.AIGenerationsOverride,
			"team_members_override":     req.TeamMembersOverride,
			"updated_at":               time.Now(),
		}
		if err := s.db.Model(&limits).Updates(updates).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update limits"})
			return
		}
	}

	log.Printf("Admin updated limits for account %s: users=%v, replays=%v, emails=%v, ai=%v, team=%v",
		accountID, req.PaidUsersOverride, req.SessionReplaysOverride,
		req.AutomatedEmailsOverride, req.AIGenerationsOverride, req.TeamMembersOverride)

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

	column := ""
	switch ResourceType(resource) {
	case ResourcePaidUsers:
		column = "paid_users_override"
	case ResourceSessionReplays:
		column = "session_replays_override"
	case ResourceAutomatedEmails:
		column = "automated_emails_override"
	case ResourceAIGenerations:
		column = "ai_generations_override"
	case ResourceTeamMembers:
		column = "team_members_override"
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
		ID                         string `json:"id"`
		Name                       string `json:"name"`
		BasePrice                  int64  `json:"base_price"`
		IncludedPaidUsers          int    `json:"included_paid_users"`
		IncludedSessionReplays     int    `json:"included_session_replays"`
		IncludedAutomatedEmails    int    `json:"included_automated_emails"`
		IncludedAIGenerations      int    `json:"included_ai_generations"`
		IncludedTeamMembers        int    `json:"included_team_members"`
		OveragePaidUsersPer100     int64  `json:"overage_paid_users_per_100"`
		OverageReplaysPer500       int64  `json:"overage_replays_per_500"`
		OverageEmailsPer10k        int64  `json:"overage_emails_per_10k"`
		OverageAIGenerationsPer100 int64  `json:"overage_ai_generations_per_100"`
		OverageTeamMembersPer1     int64  `json:"overage_team_members_per_1"`
	}

	var tiers []TierResponse
	for _, t := range TierOrder {
		tiers = append(tiers, TierResponse{
			ID:                         t.ID,
			Name:                       t.Name,
			BasePrice:                  t.BasePrice,
			IncludedPaidUsers:          t.IncludedPaidUsers,
			IncludedSessionReplays:     t.IncludedSessionReplays,
			IncludedAutomatedEmails:    t.IncludedAutomatedEmails,
			IncludedAIGenerations:      t.IncludedAIGenerations,
			IncludedTeamMembers:        t.IncludedTeamMembers,
			OveragePaidUsersPer100:     t.OveragePaidUsersPer100,
			OverageReplaysPer500:       t.OverageReplaysPer500,
			OverageEmailsPer10k:        t.OverageEmailsPer10k,
			OverageAIGenerationsPer100: t.OverageAIGenerationsPer100,
			OverageTeamMembersPer1:     t.OverageTeamMembersPer1,
		})
	}

	c.JSON(http.StatusOK, gin.H{"tiers": tiers})
}

package main

import (
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// AutomationExecutor handles background execution of automation campaigns
type AutomationExecutor struct {
	db                *gorm.DB
	automationService *AutomationService
	mailchimpService  *MailchimpService
	stopChan          chan struct{}
	ticker            *time.Ticker
}

// NewAutomationExecutor creates a new automation executor
func NewAutomationExecutor(db *gorm.DB, automationService *AutomationService, mailchimpService *MailchimpService) *AutomationExecutor {
	return &AutomationExecutor{
		db:                db,
		automationService: automationService,
		mailchimpService:  mailchimpService,
		stopChan:          make(chan struct{}),
	}
}

// Start begins the background execution loop
func (e *AutomationExecutor) Start() {
	log.Println("Starting automation executor...")
	e.ticker = time.NewTicker(15 * time.Minute) // Check every 15 minutes

	go func() {
		// Run immediately on start
		e.processAutomations()

		for {
			select {
			case <-e.ticker.C:
				e.processAutomations()
			case <-e.stopChan:
				log.Println("Stopping automation executor...")
				e.ticker.Stop()
				return
			}
		}
	}()
}

// Stop halts the background execution loop
func (e *AutomationExecutor) Stop() {
	close(e.stopChan)
}

// processAutomations checks all enabled automations and processes eligible users
func (e *AutomationExecutor) processAutomations() {
	var automations []AutomationSettings
	if err := e.db.Where("is_enabled = ?", true).Find(&automations).Error; err != nil {
		log.Printf("Error fetching enabled automations: %v", err)
		return
	}

	for _, automation := range automations {
		e.processAutomation(&automation)
	}
}

// ProcessSingleAutomation is a public method to manually trigger an automation
func (e *AutomationExecutor) ProcessSingleAutomation(automation *AutomationSettings) {
	log.Printf("Manually triggering automation: %s (%s)", automation.Name, automation.ID)
	e.processAutomation(automation)
}

// processAutomation handles a single automation's execution
func (e *AutomationExecutor) processAutomation(automation *AutomationSettings) {
	switch automation.Type {
	case "churn_prevention":
		e.processChurnPrevention(automation)
	case "feature_adoption":
		e.processFeatureAdoption(automation)
	case "engagement":
		e.processEngagement(automation)
	default:
		log.Printf("Unknown automation type: %s", automation.Type)
	}
}

// processChurnPrevention identifies at-risk users and sends retention campaigns
func (e *AutomationExecutor) processChurnPrevention(automation *AutomationSettings) {
	log.Printf("Processing churn prevention automation: %s", automation.ID)

	// Get churn prevention config
	config, ok := automation.Config["churn_prevention"].(map[string]interface{})
	if !ok {
		log.Printf("Invalid churn prevention config for automation %s", automation.ID)
		return
	}

	riskThreshold := getInt(config, "risk_threshold")
	maxCampaignsPerUser := getInt(config, "max_campaigns_per_user")

	// Find at-risk users who haven't been recently contacted
	var atRiskUsers []struct {
		UserID         string  `json:"user_id"`
		Email          string  `json:"email"`
		Name           string  `json:"name"`
		RiskScore      float64 `json:"risk_score"`
		LastActiveDays int     `json:"last_active_days"`
	}

	// Query to find users above risk threshold who haven't been contacted recently
	query := e.db.Raw(`
		SELECT 
			u.user_id,
			MAX(u.email) as email,
			MAX(u.name) as name,
			AVG(c.risk_score) as risk_score,
			EXTRACT(DAY FROM NOW() - MAX(u.last_active_at)) as last_active_days
		FROM users u
		JOIN churn_analytics c ON c.user_id = u.user_id
		WHERE u.project_id = ? 
			AND c.risk_score >= ? 
			AND u.last_active_at >= NOW() - INTERVAL '90 days'
			AND u.user_id NOT IN (
				SELECT user_id FROM automation_executions 
				WHERE automation_id = ? 
				AND created_at >= NOW() - INTERVAL '30 days'
				AND status IN ('sent', 'pending')
			)
		GROUP BY u.user_id
		HAVING COUNT(DISTINCT c.id) >= 3
		ORDER BY risk_score DESC
		LIMIT 100
	`, automation.ProjectID, riskThreshold, automation.ID).Scan(&atRiskUsers)

	if query.Error != nil {
		log.Printf("Error finding at-risk users: %v", query.Error)
		return
	}

	for _, user := range atRiskUsers {
		// Check if user has exceeded max campaigns
		var campaignCount int64
		e.db.Model(&AutomationExecution{}).
			Where("automation_id = ? AND user_id = ? AND status = 'sent'", automation.ID, user.UserID).
			Count(&campaignCount)

		if campaignCount >= int64(maxCampaignsPerUser) {
			continue
		}

		// Create execution record
		execution := AutomationExecution{
			ID:            uuid.New().String(),
			AutomationID:  automation.ID,
			ProjectID:     automation.ProjectID,
			UserID:        user.UserID,
			Status:        "pending",
			TriggerReason: fmt.Sprintf("churn_risk_%.0f", user.RiskScore),
			Personalization: map[string]interface{}{
				"user_name":        user.Name,
				"user_email":       user.Email,
				"risk_score":       user.RiskScore,
				"discount_percent": getInt(config, "discount_percentage"),
			},
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}

		if err := e.db.Create(&execution).Error; err != nil {
			log.Printf("Error creating execution record: %v", err)
			continue
		}

		// Generate personalized content using Claude
		userInfo := map[string]interface{}{
			"name":             user.Name,
			"email":            user.Email,
			"user_id":          user.UserID,
			"churn_risk_score": user.RiskScore,
			"last_active_days": user.LastActiveDays,
		}

		content, err := e.automationService.GeneratePersonalizedCampaign(automation.ProjectID, automation, userInfo)
		if err != nil {
			log.Printf("Error generating content for user %s: %v", user.UserID, err)
			continue
		}

		// Create Mailchimp campaign
		campaignRequest := &MailchimpCampaignRequest{
			Type: "regular",
			Recipients: struct {
				ListID string `json:"list_id"`
			}{
				ListID: getString(automation.Config, "audience_id"),
			},
			Settings: struct {
				SubjectLine string `json:"subject_line"`
				PreviewText string `json:"preview_text"`
				Title       string `json:"title"`
				FromName    string `json:"from_name"`
				ReplyTo     string `json:"reply_to"`
			}{
				SubjectLine: content.Subject,
				PreviewText: "Special offer just for you",
				Title:       fmt.Sprintf("Churn Prevention - %s", user.Name),
				FromName:    "Customer Success Team",
				ReplyTo:     "support@yourcompany.com",
			},
			Tracking: struct {
				Opens      bool `json:"opens"`
				HtmlClicks bool `json:"html_clicks"`
				TextClicks bool `json:"text_clicks"`
			}{
				Opens:      true,
				HtmlClicks: true,
				TextClicks: true,
			},
		}

		// Send campaign
		_, err = e.mailchimpService.CreateAndSendCampaign(automation.ProjectID, campaignRequest, content.HTMLContent)
		if err != nil {
			log.Printf("Error sending campaign for user %s: %v", user.UserID, err)
			execution.Status = "failed"
			execution.FailedAt = &[]time.Time{time.Now()}[0]
			execution.FailureReason = err.Error()
			e.db.Save(&execution)
			continue
		}

		// Update execution as sent
		now := time.Now()
		execution.Status = "sent"
		execution.SentAt = &now
		execution.ExecutionResult = map[string]interface{}{
			"subject":        content.Subject,
			"content_length": len(content.HTMLContent),
		}
		e.db.Save(&execution)

		log.Printf("Sent churn prevention campaign to user %s with risk score %.2f", user.UserID, user.RiskScore)
	}
}

// processFeatureAdoption identifies users not using features and sends educational campaigns
func (e *AutomationExecutor) processFeatureAdoption(automation *AutomationSettings) {
	log.Printf("Processing feature adoption automation: %s", automation.ID)

	// Get feature adoption config
	config, ok := automation.Config["feature_adoption"].(map[string]interface{})
	if !ok {
		log.Printf("Invalid feature adoption config for automation %s", automation.ID)
		return
	}

	unusedFeaturesThreshold := getInt(config, "unused_features_threshold")

	// Find users with unused features
	var unusedFeatureUsers []struct {
		UserID         string   `json:"user_id"`
		Email          string   `json:"email"`
		Name           string   `json:"name"`
		UnusedFeatures []string `json:"unused_features"`
	}

	// Query to find users with unused features
	query := e.db.Raw(`
		SELECT 
			u.user_id,
			MAX(u.email) as email,
			MAX(u.name) as name,
			ARRAY_AGG(f.name) as unused_features
		FROM users u
		LEFT JOIN user_features f ON f.user_id = u.user_id 
			AND f.last_used_at < NOW() - INTERVAL '%d days'
		WHERE u.project_id = ? 
			AND u.user_id NOT IN (
				SELECT user_id FROM automation_executions 
				WHERE automation_id = ? 
				AND created_at >= NOW() - INTERVAL '30 days'
				AND status IN ('sent', 'pending')
			)
		GROUP BY u.user_id
		HAVING COUNT(f.id) > 0
		LIMIT 50
	`, unusedFeaturesThreshold, automation.ProjectID, automation.ID).Scan(&unusedFeatureUsers)

	if query.Error != nil {
		log.Printf("Error finding unused feature users: %v", query.Error)
		return
	}

	for _, user := range unusedFeatureUsers {
		// Create execution record
		execution := AutomationExecution{
			ID:            uuid.New().String(),
			AutomationID:  automation.ID,
			ProjectID:     automation.ProjectID,
			UserID:        user.UserID,
			Status:        "pending",
			TriggerReason: "feature_adoption",
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
		}

		if err := e.db.Create(&execution).Error; err != nil {
			log.Printf("Error creating execution record: %v", err)
			continue
		}

		// Generate personalized content for feature adoption
		userInfo := map[string]interface{}{
			"name":            user.Name,
			"email":           user.Email,
			"user_id":         user.UserID,
			"unused_features": user.UnusedFeatures,
		}

		content, err := e.automationService.GeneratePersonalizedCampaign(automation.ProjectID, automation, userInfo)
		if err != nil {
			log.Printf("Error generating content for user %s: %v", user.UserID, err)
			continue
		}

		// Create Mailchimp campaign for feature adoption
		campaignRequest := &MailchimpCampaignRequest{
			Type: "regular",
			Recipients: struct {
				ListID string `json:"list_id"`
			}{
				ListID: getString(automation.Config, "audience_id"),
			},
			Settings: struct {
				SubjectLine string `json:"subject_line"`
				PreviewText string `json:"preview_text"`
				Title       string `json:"title"`
				FromName    string `json:"from_name"`
				ReplyTo     string `json:"reply_to"`
			}{
				SubjectLine: content.Subject,
				PreviewText: "Discover powerful features you're missing",
				Title:       fmt.Sprintf("Feature Adoption - %s", user.Name),
				FromName:    "Product Team",
				ReplyTo:     "support@yourcompany.com",
			},
			Tracking: struct {
				Opens      bool `json:"opens"`
				HtmlClicks bool `json:"html_clicks"`
				TextClicks bool `json:"text_clicks"`
			}{
				Opens:      true,
				HtmlClicks: true,
				TextClicks: true,
			},
		}

		// Send campaign
		_, err = e.mailchimpService.CreateAndSendCampaign(automation.ProjectID, campaignRequest, content.HTMLContent)
		if err != nil {
			log.Printf("Error sending campaign for user %s: %v", user.UserID, err)
			now := time.Now()
			execution.Status = "failed"
			execution.FailedAt = &now
			execution.FailureReason = err.Error()
			e.db.Save(&execution)
			continue
		}

		// Update execution as sent
		now := time.Now()
		execution.Status = "sent"
		execution.SentAt = &now
		execution.ExecutionResult = map[string]interface{}{
			"subject":        content.Subject,
			"content_length": len(content.HTMLContent),
			"features":       user.UnusedFeatures,
		}
		e.db.Save(&execution)

		log.Printf("Sent feature adoption campaign to user %s for features: %v", user.UserID, user.UnusedFeatures)
	}
}

// processEngagement identifies low-engagement users and sends re-engagement campaigns
func (e *AutomationExecutor) processEngagement(automation *AutomationSettings) {
	log.Printf("Processing engagement automation: %s", automation.ID)

	// Get engagement config
	config, ok := automation.Config["engagement"].(map[string]interface{})
	if !ok {
		log.Printf("Invalid engagement config for automation %s", automation.ID)
		return
	}

	engagementThreshold := getInt(config, "engagement_threshold")

	// Find low-engagement users
	var lowEngagementUsers []struct {
		UserID          string  `json:"user_id"`
		Email           string  `json:"email"`
		Name            string  `json:"name"`
		EngagementScore float64 `json:"engagement_score"`
		InactivityDays  int     `json:"inactivity_days"`
	}

	// Query to find users with low engagement
	query := e.db.Raw(`
		SELECT 
			u.user_id,
			MAX(u.email) as email,
			MAX(u.name) as name,
			AVG(u.engagement_score) as engagement_score,
			EXTRACT(DAY FROM NOW() - MAX(u.last_active_at)) as inactivity_days
		FROM users u
		WHERE u.project_id = ? 
			AND u.engagement_score < ?
			AND u.user_id NOT IN (
				SELECT user_id FROM automation_executions 
				WHERE automation_id = ? 
				AND created_at >= NOW() - INTERVAL '30 days'
				AND status IN ('sent', 'pending')
			)
		GROUP BY u.user_id
		ORDER BY u.engagement_score ASC
		LIMIT 50
	`, automation.ProjectID, engagementThreshold, automation.ID).Scan(&lowEngagementUsers)

	if query.Error != nil {
		log.Printf("Error finding low engagement users: %v", query.Error)
		return
	}

	for _, user := range lowEngagementUsers {
		// Create execution record
		execution := AutomationExecution{
			ID:            uuid.New().String(),
			AutomationID:  automation.ID,
			ProjectID:     automation.ProjectID,
			UserID:        user.UserID,
			Status:        "pending",
			TriggerReason: "low_engagement",
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
		}

		if err := e.db.Create(&execution).Error; err != nil {
			log.Printf("Error creating execution record: %v", err)
			continue
		}

		// Generate personalized content for re-engagement
		userInfo := map[string]interface{}{
			"name":             user.Name,
			"email":            user.Email,
			"user_id":          user.UserID,
			"engagement_score": user.EngagementScore,
			"inactivity_days":  user.InactivityDays,
		}

		content, err := e.automationService.GeneratePersonalizedCampaign(automation.ProjectID, automation, userInfo)
		if err != nil {
			log.Printf("Error generating content for user %s: %v", user.UserID, err)
			continue
		}

		// Create Mailchimp campaign for re-engagement
		campaignRequest := &MailchimpCampaignRequest{
			Type: "regular",
			Recipients: struct {
				ListID string `json:"list_id"`
			}{
				ListID: getString(automation.Config, "audience_id"),
			},
			Settings: struct {
				SubjectLine string `json:"subject_line"`
				PreviewText string `json:"preview_text"`
				Title       string `json:"title"`
				FromName    string `json:"from_name"`
				ReplyTo     string `json:"reply_to"`
			}{
				SubjectLine: content.Subject,
				PreviewText: "We miss you! Here's what's new",
				Title:       fmt.Sprintf("Re-engagement - %s", user.Name),
				FromName:    "Customer Success Team",
				ReplyTo:     "support@yourcompany.com",
			},
			Tracking: struct {
				Opens      bool `json:"opens"`
				HtmlClicks bool `json:"html_clicks"`
				TextClicks bool `json:"text_clicks"`
			}{
				Opens:      true,
				HtmlClicks: true,
				TextClicks: true,
			},
		}

		// Send campaign
		_, err = e.mailchimpService.CreateAndSendCampaign(automation.ProjectID, campaignRequest, content.HTMLContent)
		if err != nil {
			log.Printf("Error sending campaign for user %s: %v", user.UserID, err)
			now := time.Now()
			execution.Status = "failed"
			execution.FailedAt = &now
			execution.FailureReason = err.Error()
			e.db.Save(&execution)
			continue
		}

		// Update execution as sent
		now := time.Now()
		execution.Status = "sent"
		execution.SentAt = &now
		execution.ExecutionResult = map[string]interface{}{
			"subject":        content.Subject,
			"content_length": len(content.HTMLContent),
		}
		e.db.Save(&execution)

		log.Printf("Sent re-engagement campaign to user %s with engagement score %.2f", user.UserID, user.EngagementScore)
	}
}

// getString and getInt helper functions are defined in automation_service.go

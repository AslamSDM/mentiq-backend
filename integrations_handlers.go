package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// IntegrationsService handles all integration-related API calls
type IntegrationsService struct {
	db               *gorm.DB
	mailchimpService *MailchimpService
}

// NewIntegrationsService creates a new integrations service
func NewIntegrationsService(db *gorm.DB) *IntegrationsService {
	return &IntegrationsService{
		db:               db,
		mailchimpService: NewMailchimpService(db),
	}
}

// GetIntegrationsHandler returns all integrations for a project
func (s *IntegrationsService) GetIntegrationsHandler(c *gin.Context) {
	projectID := c.Param("project_id")

	var integrations []ProjectIntegration
	err := s.db.Where("project_id = ?", projectID).Find(&integrations).Error
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch integrations"})
		return
	}

	c.JSON(http.StatusOK, integrations)
}

// GetIntegrationHandler returns a specific integration
func (s *IntegrationsService) GetIntegrationHandler(c *gin.Context) {
	projectID := c.Param("project_id")
	provider := c.Param("provider")

	var integration ProjectIntegration
	err := s.db.Where("project_id = ? AND provider = ?", projectID, provider).First(&integration).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "Integration not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch integration"})
		return
	}

	c.JSON(http.StatusOK, integration)
}

// ==========================================
// MAILCHIMP HANDLERS
// ==========================================

// ConnectMailchimpHandler initiates Mailchimp OAuth flow
func (s *IntegrationsService) ConnectMailchimpHandler(c *gin.Context) {
	projectID := c.Param("project_id")

	// Check if Mailchimp is configured
	if !s.mailchimpService.IsConfigured() {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Mailchimp integration is not configured. Please set MAILCHIMP_CLIENT_ID, MAILCHIMP_CLIENT_SECRET, and MAILCHIMP_REDIRECT_URI environment variables.",
		})
		return
	}

	// Return the OAuth authorization URL
	authURL := s.mailchimpService.GetAuthURL(projectID)

	c.JSON(http.StatusOK, gin.H{
		"auth_url": authURL,
	})
}

// MailchimpCallbackHandler handles the OAuth callback from Mailchimp
func (s *IntegrationsService) MailchimpCallbackHandler(c *gin.Context) {
	code := c.Query("code")
	projectID := c.Query("state") // We passed projectID in state

	if code == "" {
		// Check for error
		errorMsg := c.Query("error")
		if errorMsg != "" {
			log.Printf("Mailchimp OAuth error: %s", errorMsg)
			c.Redirect(http.StatusTemporaryRedirect,
				"/dashboard/settings/integrations?error=oauth_denied")
			return
		}
		c.Redirect(http.StatusTemporaryRedirect,
			"/dashboard/settings/integrations?error=no_code")
		return
	}

	if projectID == "" {
		c.Redirect(http.StatusTemporaryRedirect,
			"/dashboard/settings/integrations?error=no_project")
		return
	}

	// Exchange code for token
	integration, err := s.mailchimpService.ExchangeCodeForToken(projectID, code)
	if err != nil {
		log.Printf("Failed to exchange Mailchimp code: %v", err)
		c.Redirect(http.StatusTemporaryRedirect,
			"/dashboard/settings/integrations?error=token_exchange_failed")
		return
	}

	log.Printf("Mailchimp connected for project %s, integration ID: %s", projectID, integration.ID)

	// Redirect to success page
	c.Redirect(http.StatusTemporaryRedirect,
		"/dashboard/settings/integrations/mailchimp?success=connected")
}

// MailchimpCallbackAPIHandler handles the OAuth callback via API (POST with code in body)
func (s *IntegrationsService) MailchimpCallbackAPIHandler(c *gin.Context) {
	projectID := c.Param("project_id")

	var req struct {
		Code string `json:"code"`
	}

	if err := c.ShouldBindJSON(&req); err != nil || req.Code == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Missing authorization code"})
		return
	}

	// Exchange code for token
	integration, err := s.mailchimpService.ExchangeCodeForToken(projectID, req.Code)
	if err != nil {
		log.Printf("Failed to exchange Mailchimp code: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to exchange authorization code"})
		return
	}

	log.Printf("Mailchimp connected for project %s, integration ID: %s", projectID, integration.ID)

	c.JSON(http.StatusOK, gin.H{
		"message":     "Mailchimp connected successfully",
		"integration": integration,
	})
}

// DisconnectMailchimpHandler removes Mailchimp integration
func (s *IntegrationsService) DisconnectMailchimpHandler(c *gin.Context) {
	projectID := c.Param("project_id")

	err := s.mailchimpService.Disconnect(projectID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to disconnect Mailchimp"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Mailchimp disconnected successfully"})
}

// GetMailchimpAudiencesHandler returns available Mailchimp audiences
func (s *IntegrationsService) GetMailchimpAudiencesHandler(c *gin.Context) {
	projectID := c.Param("project_id")

	audiences, err := s.mailchimpService.GetAudiences(projectID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"audiences": audiences})
}

// UpdateMailchimpSettingsRequest represents settings update request
type UpdateMailchimpSettingsRequest struct {
	AudienceID    string `json:"audience_id"`
	AudienceName  string `json:"audience_name"`
	SyncHighRisk  *bool  `json:"sync_high_risk"`
	RiskThreshold *int   `json:"risk_threshold"`
	AddTags       *bool  `json:"add_tags"`
	TagName       string `json:"tag_name"`
}

// UpdateMailchimpSettingsHandler updates Mailchimp integration settings
func (s *IntegrationsService) UpdateMailchimpSettingsHandler(c *gin.Context) {
	projectID := c.Param("project_id")

	var req UpdateMailchimpSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
		return
	}

	settings := make(map[string]interface{})

	if req.AudienceID != "" {
		settings["audience_id"] = req.AudienceID
	}
	if req.AudienceName != "" {
		settings["audience_name"] = req.AudienceName
	}
	if req.SyncHighRisk != nil {
		settings["sync_high_risk"] = *req.SyncHighRisk
	}
	if req.RiskThreshold != nil {
		settings["risk_threshold"] = *req.RiskThreshold
	}
	if req.AddTags != nil {
		settings["add_tags"] = *req.AddTags
	}
	if req.TagName != "" {
		settings["tag_name"] = req.TagName
	}

	err := s.mailchimpService.UpdateSettings(projectID, settings)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Return updated integration
	integration, _ := s.mailchimpService.GetIntegration(projectID)
	c.JSON(http.StatusOK, integration)
}

// TriggerMailchimpSyncHandler manually triggers a sync
func (s *IntegrationsService) TriggerMailchimpSyncHandler(c *gin.Context) {
	projectID := c.Param("project_id")

	syncLog, err := s.mailchimpService.SyncHighRiskUsers(projectID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":          "Sync completed",
		"contacts_synced":  syncLog.ContactsSynced,
		"contacts_failed":  syncLog.ContactsFailed,
		"contacts_skipped": syncLog.ContactsSkipped,
		"duration_ms":      syncLog.Duration,
	})
}

// GetMailchimpSyncLogsHandler returns sync history
func (s *IntegrationsService) GetMailchimpSyncLogsHandler(c *gin.Context) {
	projectID := c.Param("project_id")

	// Get integration first
	var integration ProjectIntegration
	err := s.db.Where("project_id = ? AND provider = ?", projectID, "mailchimp").
		First(&integration).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "Mailchimp integration not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch integration"})
		return
	}

	// Get sync logs
	var logs []IntegrationSyncLog
	err = s.db.Where("integration_id = ?", integration.ID).
		Order("created_at DESC").
		Limit(50).
		Find(&logs).Error
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch sync logs"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"logs": logs})
}

// ==========================================
// RESEND HANDLERS
// ==========================================

type ConnectAPIKeyRequest struct {
	APIKey    string `json:"api_key" binding:"required"`
	FromEmail string `json:"from_email"`
	FromName  string `json:"from_name"`
}

// ConnectResendHandler saves a Resend API key as an integration
func (s *IntegrationsService) ConnectResendHandler(c *gin.Context) {
	projectID := c.Param("project_id")

	var req ConnectAPIKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "API key is required"})
		return
	}

	// Verify the key works by making a test call
	sender := NewResendEmailSender(req.APIKey)
	if sender.client == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid API key"})
		return
	}

	// Upsert integration record
	var integration ProjectIntegration
	err := s.db.Where("project_id = ? AND provider = ?", projectID, "resend").First(&integration).Error

	settings := map[string]interface{}{
		"from_email": req.FromEmail,
		"from_name":  req.FromName,
	}

	if err == gorm.ErrRecordNotFound {
		integration = ProjectIntegration{
			ID:          uuid.New().String(),
			ProjectID:   projectID,
			Provider:    "resend",
			AccessToken: req.APIKey,
			IsActive:    true,
			Settings:    settings,
			SyncStatus:  "idle",
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}
		if err := s.db.Create(&integration).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save integration"})
			return
		}
	} else if err == nil {
		integration.AccessToken = req.APIKey
		integration.IsActive = true
		integration.Settings = settings
		integration.UpdatedAt = time.Now()
		s.db.Save(&integration)
	} else {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}

	log.Printf("Resend connected for project %s", projectID)
	c.JSON(http.StatusOK, integration)
}

// DisconnectResendHandler removes the Resend integration
func (s *IntegrationsService) DisconnectResendHandler(c *gin.Context) {
	projectID := c.Param("project_id")

	result := s.db.Where("project_id = ? AND provider = ?", projectID, "resend").Delete(&ProjectIntegration{})
	if result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to disconnect Resend"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Resend disconnected successfully"})
}

// UpdateResendSettingsHandler updates Resend integration settings
func (s *IntegrationsService) UpdateResendSettingsHandler(c *gin.Context) {
	projectID := c.Param("project_id")

	var req struct {
		APIKey    string `json:"api_key"`
		FromEmail string `json:"from_email"`
		FromName  string `json:"from_name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
		return
	}

	var integration ProjectIntegration
	err := s.db.Where("project_id = ? AND provider = ?", projectID, "resend").First(&integration).Error
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Resend integration not found"})
		return
	}

	if req.APIKey != "" {
		integration.AccessToken = req.APIKey
	}
	if integration.Settings == nil {
		integration.Settings = make(map[string]interface{})
	}
	if req.FromEmail != "" {
		integration.Settings["from_email"] = req.FromEmail
	}
	if req.FromName != "" {
		integration.Settings["from_name"] = req.FromName
	}
	integration.UpdatedAt = time.Now()
	s.db.Save(&integration)

	c.JSON(http.StatusOK, integration)
}

// TestResendHandler sends a test email to verify the integration
func (s *IntegrationsService) TestResendHandler(c *gin.Context) {
	projectID := c.Param("project_id")

	var req struct {
		ToEmail string `json:"to_email" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "to_email is required"})
		return
	}

	var integration ProjectIntegration
	err := s.db.Where("project_id = ? AND provider = ?", projectID, "resend").First(&integration).Error
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Resend integration not found"})
		return
	}

	apiKey := integration.AccessToken
	if apiKey == "" {
		apiKey = os.Getenv("RESEND_API_KEY")
	}

	sender := NewResendEmailSender(apiKey)
	fromEmail, _ := integration.Settings["from_email"].(string)
	fromName, _ := integration.Settings["from_name"].(string)
	if fromEmail == "" {
		fromEmail = "noreply@yourcompany.com"
	}
	if fromName == "" {
		fromName = "Mentiq"
	}

	result, err := sender.SendEmail(AutomationEmailRequest{
		To:          req.ToEmail,
		From:        fromEmail,
		FromName:    fromName,
		Subject:     "Mentiq - Resend Integration Test",
		HTMLContent: "<h2>It works!</h2><p>Your Resend integration with Mentiq is configured correctly.</p>",
		PlainText:   "It works! Your Resend integration with Mentiq is configured correctly.",
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Test email failed: %v", err)})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Test email sent", "message_id": result.MessageID})
}

// ==========================================
// SENDGRID HANDLERS
// ==========================================

// ConnectSendGridHandler saves a SendGrid API key as an integration
func (s *IntegrationsService) ConnectSendGridHandler(c *gin.Context) {
	projectID := c.Param("project_id")

	var req ConnectAPIKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "API key is required"})
		return
	}

	// Upsert integration record
	var integration ProjectIntegration
	err := s.db.Where("project_id = ? AND provider = ?", projectID, "sendgrid").First(&integration).Error

	settings := map[string]interface{}{
		"from_email": req.FromEmail,
		"from_name":  req.FromName,
	}

	if err == gorm.ErrRecordNotFound {
		integration = ProjectIntegration{
			ID:          uuid.New().String(),
			ProjectID:   projectID,
			Provider:    "sendgrid",
			AccessToken: req.APIKey,
			IsActive:    true,
			Settings:    settings,
			SyncStatus:  "idle",
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}
		if err := s.db.Create(&integration).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save integration"})
			return
		}
	} else if err == nil {
		integration.AccessToken = req.APIKey
		integration.IsActive = true
		integration.Settings = settings
		integration.UpdatedAt = time.Now()
		s.db.Save(&integration)
	} else {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error"})
		return
	}

	log.Printf("SendGrid connected for project %s", projectID)
	c.JSON(http.StatusOK, integration)
}

// DisconnectSendGridHandler removes the SendGrid integration
func (s *IntegrationsService) DisconnectSendGridHandler(c *gin.Context) {
	projectID := c.Param("project_id")

	result := s.db.Where("project_id = ? AND provider = ?", projectID, "sendgrid").Delete(&ProjectIntegration{})
	if result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to disconnect SendGrid"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "SendGrid disconnected successfully"})
}

// UpdateSendGridSettingsHandler updates SendGrid integration settings
func (s *IntegrationsService) UpdateSendGridSettingsHandler(c *gin.Context) {
	projectID := c.Param("project_id")

	var req struct {
		APIKey    string `json:"api_key"`
		FromEmail string `json:"from_email"`
		FromName  string `json:"from_name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
		return
	}

	var integration ProjectIntegration
	err := s.db.Where("project_id = ? AND provider = ?", projectID, "sendgrid").First(&integration).Error
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "SendGrid integration not found"})
		return
	}

	if req.APIKey != "" {
		integration.AccessToken = req.APIKey
	}
	if integration.Settings == nil {
		integration.Settings = make(map[string]interface{})
	}
	if req.FromEmail != "" {
		integration.Settings["from_email"] = req.FromEmail
	}
	if req.FromName != "" {
		integration.Settings["from_name"] = req.FromName
	}
	integration.UpdatedAt = time.Now()
	s.db.Save(&integration)

	c.JSON(http.StatusOK, integration)
}

// TestSendGridHandler sends a test email to verify the integration
func (s *IntegrationsService) TestSendGridHandler(c *gin.Context) {
	projectID := c.Param("project_id")

	var req struct {
		ToEmail string `json:"to_email" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "to_email is required"})
		return
	}

	var integration ProjectIntegration
	err := s.db.Where("project_id = ? AND provider = ?", projectID, "sendgrid").First(&integration).Error
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "SendGrid integration not found"})
		return
	}

	apiKey := integration.AccessToken
	if apiKey == "" {
		apiKey = os.Getenv("SENDGRID_API_KEY")
	}

	sender := NewSendGridEmailSender(apiKey)
	fromEmail, _ := integration.Settings["from_email"].(string)
	fromName, _ := integration.Settings["from_name"].(string)
	if fromEmail == "" {
		fromEmail = "noreply@yourcompany.com"
	}
	if fromName == "" {
		fromName = "Mentiq"
	}

	result, err := sender.SendEmail(AutomationEmailRequest{
		To:          req.ToEmail,
		From:        fromEmail,
		FromName:    fromName,
		Subject:     "Mentiq - SendGrid Integration Test",
		HTMLContent: "<h2>It works!</h2><p>Your SendGrid integration with Mentiq is configured correctly.</p>",
		PlainText:   "It works! Your SendGrid integration with Mentiq is configured correctly.",
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Test email failed: %v", err)})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Test email sent", "message_id": result.MessageID})
}

package main

import (
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
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
	projectID := c.Param("projectId")

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
	projectID := c.Param("projectId")
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
	projectID := c.Param("projectId")

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

// DisconnectMailchimpHandler removes Mailchimp integration
func (s *IntegrationsService) DisconnectMailchimpHandler(c *gin.Context) {
	projectID := c.Param("projectId")

	err := s.mailchimpService.Disconnect(projectID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to disconnect Mailchimp"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Mailchimp disconnected successfully"})
}

// GetMailchimpAudiencesHandler returns available Mailchimp audiences
func (s *IntegrationsService) GetMailchimpAudiencesHandler(c *gin.Context) {
	projectID := c.Param("projectId")

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
	projectID := c.Param("projectId")

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
	projectID := c.Param("projectId")

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
	projectID := c.Param("projectId")

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

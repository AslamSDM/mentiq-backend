package main

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"net/http"
	"time"
)

// AutomationRequest represents request payload for creating automation settings
type AutomationRequest struct {
	Name         string                 `json:"name" binding:"required"`
	Description  string                 `json:"description"`
	Type         string                 `json:"type" binding:"required,oneof=churn_prevention feature_adoption engagement"`
	Config       map[string]interface{} `json:"config"`
	IsEnabled    bool                   `json:"is_enabled"`
	CustomPrompt string                 `json:"custom_prompt"`
}

// AutomationUpdateRequest represents request payload for updating automation settings (all fields optional)
type AutomationUpdateRequest struct {
	Name         *string                `json:"name"`
	Description  *string                `json:"description"`
	Type         *string                `json:"type"`
	Config       map[string]interface{} `json:"config"`
	IsEnabled    *bool                  `json:"is_enabled"`
	CustomPrompt *string                `json:"custom_prompt"`
}

// EmailTemplateRequest represents request payload for email templates
type EmailTemplateRequest struct {
	Name                string   `json:"name" binding:"required"`
	Type                string   `json:"type" binding:"required,oneof=churn_prevention feature_adoption engagement discount"`
	SubjectTemplate     string   `json:"subject_template" binding:"required"`
	ContentTemplate     string   `json:"content_template" binding:"required"`
	PersonalizationVars []string `json:"personalization_vars"`
	IsActive            bool     `json:"is_active"`
}

// DiscountCodeRequest represents request payload for discount codes
type DiscountCodeRequest struct {
	Code            string  `json:"code" binding:"required"`
	DiscountPercent int     `json:"discount_percent" binding:"min=1,max=100"`
	ValidUntil      *string `json:"valid_until"`
	MaxUses         int     `json:"max_uses" binding:"min=1"`
	IsActive        bool    `json:"is_active"`
}

// AutomationExecutionResponse represents execution tracking response
type AutomationExecutionResponse struct {
	ID              string                 `json:"id"`
	AutomationID    string                 `json:"automation_id"`
	ProjectID       string                 `json:"project_id"`
	UserID          string                 `json:"user_id"`
	EmailTemplateID string                 `json:"email_template_id"`
	CampaignID      *string                `json:"campaign_id,omitempty"`
	Status          string                 `json:"status"`
	TriggerReason   string                 `json:"trigger_reason"`
	Personalization map[string]interface{} `json:"personalization"`
	ScheduledAt     *time.Time             `json:"scheduled_at,omitempty"`
	SentAt          *time.Time             `json:"sent_at,omitempty"`
	CreatedAt       time.Time              `json:"created_at"`
}

// =====================
// AUTOMATION SETTINGS API
// =====================

func (s *Server) createAutomationHandler(c *gin.Context) {
	projectID := c.Param("project_id")
	if projectID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Project ID is required"})
		return
	}

	var req AutomationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	automation := &AutomationSettings{
		ID:           uuid.New().String(),
		ProjectID:    projectID,
		Name:         req.Name,
		Description:  req.Description,
		Type:         req.Type,
		IsEnabled:    req.IsEnabled,
		Config:       req.Config,
		CustomPrompt: req.CustomPrompt,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}

	if err := s.db.Create(automation).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create automation"})
		return
	}

	c.JSON(http.StatusCreated, automation)
}

func (s *Server) getAutomationsHandler(c *gin.Context) {
	projectID := c.Param("project_id")
	if projectID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Project ID is required"})
		return
	}

	var automations []AutomationSettings
	if err := s.db.Where("project_id = ?", projectID).Find(&automations).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch automations"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"automations": automations})
}

func (s *Server) getAutomationHandler(c *gin.Context) {
	projectID := c.Param("project_id")
	automationID := c.Param("automation_id")

	if projectID == "" || automationID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Project ID and Automation ID are required"})
		return
	}

	var automation AutomationSettings
	if err := s.db.Where("id = ? AND project_id = ?", automationID, projectID).First(&automation).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Automation not found"})
		return
	}

	c.JSON(http.StatusOK, automation)
}

func (s *Server) updateAutomationHandler(c *gin.Context) {
	projectID := c.Param("project_id")
	automationID := c.Param("automation_id")

	if projectID == "" || automationID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Project ID and Automation ID are required"})
		return
	}

	var req AutomationUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var automation AutomationSettings
	if err := s.db.Where("id = ? AND project_id = ?", automationID, projectID).First(&automation).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Automation not found"})
		return
	}

	// Only update fields that are provided
	if req.Name != nil {
		automation.Name = *req.Name
	}
	if req.Description != nil {
		automation.Description = *req.Description
	}
	if req.Type != nil {
		automation.Type = *req.Type
	}
	if req.IsEnabled != nil {
		automation.IsEnabled = *req.IsEnabled
	}
	if req.Config != nil {
		automation.Config = req.Config
	}
	if req.CustomPrompt != nil {
		automation.CustomPrompt = *req.CustomPrompt
	}
	automation.UpdatedAt = time.Now()

	if err := s.db.Save(&automation).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update automation"})
		return
	}

	c.JSON(http.StatusOK, automation)
}

func (s *Server) deleteAutomationHandler(c *gin.Context) {
	projectID := c.Param("project_id")
	automationID := c.Param("automation_id")

	if projectID == "" || automationID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Project ID and Automation ID are required"})
		return
	}

	if err := s.db.Where("id = ? AND project_id = ?", automationID, projectID).Delete(&AutomationSettings{}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete automation"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Automation deleted successfully"})
}

// =====================
// EMAIL TEMPLATES API
// =====================

func (s *Server) createEmailTemplateHandler(c *gin.Context) {
	projectID := c.Param("project_id")
	if projectID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Project ID is required"})
		return
	}

	var req EmailTemplateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	template := &EmailTemplate{
		ID:                  uuid.New().String(),
		ProjectID:           projectID,
		Name:                req.Name,
		Type:                req.Type,
		SubjectTemplate:     req.SubjectTemplate,
		ContentTemplate:     req.ContentTemplate,
		PersonalizationVars: req.PersonalizationVars,
		IsActive:            req.IsActive,
		CreatedAt:           time.Now(),
		UpdatedAt:           time.Now(),
	}

	if err := s.db.Create(template).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create email template"})
		return
	}

	c.JSON(http.StatusCreated, template)
}

func (s *Server) getEmailTemplatesHandler(c *gin.Context) {
	projectID := c.Param("project_id")
	if projectID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Project ID is required"})
		return
	}

	templateType := c.Query("type")

	var templates []EmailTemplate
	query := s.db.Where("project_id = ?", projectID)
	if templateType != "" {
		query = query.Where("type = ?", templateType)
	}

	if err := query.Find(&templates).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch email templates"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"templates": templates})
}

func (s *Server) updateEmailTemplateHandler(c *gin.Context) {
	projectID := c.Param("project_id")
	templateID := c.Param("template_id")

	if projectID == "" || templateID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Project ID and Template ID are required"})
		return
	}

	var req EmailTemplateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var template EmailTemplate
	if err := s.db.Where("id = ? AND project_id = ?", templateID, projectID).First(&template).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Email template not found"})
		return
	}

	template.Name = req.Name
	template.Type = req.Type
	template.SubjectTemplate = req.SubjectTemplate
	template.ContentTemplate = req.ContentTemplate
	template.PersonalizationVars = req.PersonalizationVars
	template.IsActive = req.IsActive
	template.UpdatedAt = time.Now()

	if err := s.db.Save(&template).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update email template"})
		return
	}

	c.JSON(http.StatusOK, template)
}

func (s *Server) deleteEmailTemplateHandler(c *gin.Context) {
	projectID := c.Param("project_id")
	templateID := c.Param("template_id")

	if projectID == "" || templateID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Project ID and Template ID are required"})
		return
	}

	if err := s.db.Where("id = ? AND project_id = ?", templateID, projectID).Delete(&EmailTemplate{}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete email template"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Email template deleted successfully"})
}

// =====================
// DISCOUNT CODES API
// =====================

func (s *Server) createDiscountCodeHandler(c *gin.Context) {
	projectID := c.Param("project_id")
	if projectID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Project ID is required"})
		return
	}

	var req DiscountCodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var validUntil *time.Time
	if req.ValidUntil != nil {
		parsed, err := time.Parse(time.RFC3339, *req.ValidUntil)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid valid_until date format"})
			return
		}
		validUntil = &parsed
	}

	discountCode := &DiscountCode{
		ID:              uuid.New().String(),
		ProjectID:       projectID,
		Code:            req.Code,
		DiscountPercent: req.DiscountPercent,
		ValidUntil:      validUntil,
		MaxUses:         req.MaxUses,
		UsedCount:       0,
		IsActive:        req.IsActive,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}

	if err := s.db.Create(discountCode).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create discount code"})
		return
	}

	c.JSON(http.StatusCreated, discountCode)
}

func (s *Server) getDiscountCodesHandler(c *gin.Context) {
	projectID := c.Param("project_id")
	if projectID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Project ID is required"})
		return
	}

	automationID := c.Query("automation_id")

	var codes []DiscountCode
	query := s.db.Where("project_id = ?", projectID)
	if automationID != "" {
		query = query.Where("automation_id = ?", automationID)
	}

	if err := query.Find(&codes).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch discount codes"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"discount_codes": codes})
}

func (s *Server) updateDiscountCodeHandler(c *gin.Context) {
	projectID := c.Param("project_id")
	codeID := c.Param("code_id")

	if projectID == "" || codeID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Project ID and Code ID are required"})
		return
	}

	var req DiscountCodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var discountCode DiscountCode
	if err := s.db.Where("id = ? AND project_id = ?", codeID, projectID).First(&discountCode).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Discount code not found"})
		return
	}

	discountCode.Code = req.Code
	discountCode.DiscountPercent = req.DiscountPercent

	if req.ValidUntil != nil {
		parsed, err := time.Parse(time.RFC3339, *req.ValidUntil)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid valid_until date format"})
			return
		}
		discountCode.ValidUntil = &parsed
	}

	discountCode.MaxUses = req.MaxUses
	discountCode.IsActive = req.IsActive
	discountCode.UpdatedAt = time.Now()

	if err := s.db.Save(&discountCode).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update discount code"})
		return
	}

	c.JSON(http.StatusOK, discountCode)
}

// =====================
// AUTOMATION EXECUTION API
// =====================

func (s *Server) getAutomationExecutionsHandler(c *gin.Context) {
	projectID := c.Param("project_id")
	if projectID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Project ID is required"})
		return
	}

	automationID := c.Query("automation_id")
	userID := c.Query("user_id")
	status := c.Query("status")

	var executions []AutomationExecution
	query := s.db.Where("project_id = ?", projectID)

	if automationID != "" {
		query = query.Where("automation_id = ?", automationID)
	}
	if userID != "" {
		query = query.Where("user_id = ?", userID)
	}
	if status != "" {
		query = query.Where("status = ?", status)
	}

	if err := query.Find(&executions).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch automation executions"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"executions": executions})
}

// =====================
// MANUAL TRIGGER & TEST API
// =====================

// triggerAutomationHandler manually triggers an automation for testing
func (s *Server) triggerAutomationHandler(c *gin.Context) {
	projectID := c.Param("project_id")
	automationID := c.Param("automation_id")

	if projectID == "" || automationID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Project ID and Automation ID are required"})
		return
	}

	var automation AutomationSettings
	if err := s.db.Where("id = ? AND project_id = ?", automationID, projectID).First(&automation).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Automation not found"})
		return
	}

	// Trigger the automation executor for this specific automation
	go s.automationExecutor.ProcessSingleAutomation(&automation)

	c.JSON(http.StatusOK, gin.H{
		"message":       "Automation triggered successfully",
		"automation_id": automationID,
		"type":          automation.Type,
	})
}

// testAutomationHandler creates a test execution to verify the automation flow
func (s *Server) testAutomationHandler(c *gin.Context) {
	projectID := c.Param("project_id")
	automationID := c.Param("automation_id")

	if projectID == "" || automationID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Project ID and Automation ID are required"})
		return
	}

	var req struct {
		UserID string `json:"user_id"`
		Email  string `json:"email"`
		Name   string `json:"name"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "user_id, email, and name are required"})
		return
	}

	var automation AutomationSettings
	if err := s.db.Where("id = ? AND project_id = ?", automationID, projectID).First(&automation).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Automation not found"})
		return
	}

	// Create a test execution
	execution := AutomationExecution{
		ID:            uuid.New().String(),
		AutomationID:  automationID,
		ProjectID:     projectID,
		UserID:        req.UserID,
		Status:        "pending",
		TriggerReason: "manual_test",
		Personalization: map[string]interface{}{
			"user_name":        req.Name,
			"user_email":       req.Email,
			"risk_score":       85.0, // Simulated high risk
			"discount_percent": 20,
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if err := s.db.Create(&execution).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create test execution"})
		return
	}

	// If automation service is available, generate email content
	var emailContent map[string]string
	if s.automationService != nil {
		genReq := GenerateEmailContentRequest{
			TemplateType: automation.Type,
			UserContext: map[string]interface{}{
				"name":             req.Name,
				"email":            req.Email,
				"user_id":          req.UserID,
				"churn_risk_score": 85.0,
			},
			ProductContext: map[string]interface{}{
				"product_name":    "Your Product",
				"discount_code":   "TEST20",
				"discount_amount": "20%",
			},
			Personalization: map[string]interface{}{
				"discount_code":   "TEST20",
				"discount_amount": "20%",
			},
		}

		// Use custom prompt from automation if available
		genReq.CustomPrompt = automation.CustomPrompt

		content, err := s.automationService.GenerateEmailContent(genReq)
		if err == nil {
			emailContent = map[string]string{
				"subject": content.Subject,
				"html":    content.HTMLContent,
				"text":    content.PlainText,
			}
			// Store the full email content on the execution record
			execution.EmailSubject = content.Subject
			execution.EmailHTML = content.HTMLContent
			execution.EmailPlainText = content.PlainText
			execution.Personalization["generated_subject"] = content.Subject
			execution.Personalization["generated_body"] = content.HTMLContent
			s.db.Save(&execution)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"message":       "Test execution created",
		"execution":     execution,
		"email_content": emailContent,
	})
}

// =====================
// SINGLE EXECUTION & PREVIEW API
// =====================

// getAutomationExecutionHandler returns a single execution with its stored email content
func (s *Server) getAutomationExecutionHandler(c *gin.Context) {
	projectID := c.Param("project_id")
	executionID := c.Param("execution_id")

	if projectID == "" || executionID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Project ID and Execution ID are required"})
		return
	}

	var execution AutomationExecution
	if err := s.db.Where("id = ? AND project_id = ?", executionID, projectID).
		Preload("Automation").First(&execution).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Execution not found"})
		return
	}

	c.JSON(http.StatusOK, execution)
}

// previewEmailHandler generates a preview email using the automation's prompt (or a custom one)
// without creating an execution record
func (s *Server) previewEmailHandler(c *gin.Context) {
	projectID := c.Param("project_id")
	automationID := c.Param("automation_id")

	if projectID == "" || automationID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Project ID and Automation ID are required"})
		return
	}

	var req struct {
		CustomPrompt string `json:"custom_prompt"`
		UserName     string `json:"user_name"`
		UserEmail    string `json:"user_email"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var automation AutomationSettings
	if err := s.db.Where("id = ? AND project_id = ?", automationID, projectID).First(&automation).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Automation not found"})
		return
	}

	if s.automationService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "AI generation not available"})
		return
	}

	// Use the provided prompt for preview, fall back to automation's saved prompt
	promptToUse := req.CustomPrompt
	if promptToUse == "" {
		promptToUse = automation.CustomPrompt
	}

	// Default sample user data
	userName := req.UserName
	if userName == "" {
		userName = "Jane Doe"
	}
	userEmail := req.UserEmail
	if userEmail == "" {
		userEmail = "jane@example.com"
	}

	genReq := GenerateEmailContentRequest{
		TemplateType: automation.Type,
		UserContext: map[string]interface{}{
			"name":             userName,
			"email":            userEmail,
			"churn_risk_score": 75.0,
			"last_active_days": 14,
		},
		ProductContext: map[string]interface{}{
			"product_name": "Your Product",
		},
		Personalization: map[string]interface{}{
			"discount_code":    "PREVIEW20",
			"discount_percent": 20,
			"user_name":        userName,
		},
		PersonalizationVars: []string{"user_name", "discount_code", "discount_percent", "product_name"},
		CustomPrompt:        promptToUse,
	}

	content, err := s.automationService.GenerateEmailContent(genReq)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate preview: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"subject":    content.Subject,
		"html":       content.HTMLContent,
		"plain_text": content.PlainText,
		"prompt_used": promptToUse,
	})
}

// getDefaultPromptHandler returns the default system prompt for an automation type
func (s *Server) getDefaultPromptHandler(c *gin.Context) {
	automationType := c.Param("type")

	prompts := map[string]string{
		"churn_prevention": "You are an expert email marketing copywriter specializing in SaaS user engagement.\n\nGoal: Persuade the user to stay by highlighting value and offering assistance.\nTone: Empathetic, supportive, and value-focused.\n\nInclude a special discount offer if a discount code is available.\nFocus on the value the user has already gotten from the product and what they'd miss.\nMake the user feel valued and understood.",
		"feature_adoption": "You are an expert email marketing copywriter specializing in SaaS user engagement.\n\nGoal: Introduce unused features that would benefit the user based on their usage patterns.\nTone: Helpful, educational, and excited.\n\nHighlight specific features the user hasn't tried yet.\nExplain the benefits in terms of outcomes, not just functionality.\nInclude a clear call-to-action to try the feature.",
		"engagement":       "You are an expert email marketing copywriter specializing in SaaS user engagement.\n\nGoal: Bring the user back to the product with compelling reasons and social proof.\nTone: Energetic, encouraging, and community-focused.\n\nMention what's new since they were last active.\nUse social proof (e.g., what other users are achieving).\nCreate a sense of excitement about returning.",
	}

	prompt, ok := prompts[automationType]
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "Unknown automation type"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"type":           automationType,
		"default_prompt": prompt,
	})
}

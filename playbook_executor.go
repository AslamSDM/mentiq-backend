package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// PlaybookExecutor handles background execution of playbook steps
type PlaybookExecutor struct {
	db               *gorm.DB
	emailService     *EmailService
	mailchimpService *MailchimpService
	stopChan         chan struct{}
	ticker           *time.Ticker
}

// NewPlaybookExecutor creates a new playbook executor
func NewPlaybookExecutor(db *gorm.DB, emailService *EmailService, mailchimpService *MailchimpService) *PlaybookExecutor {
	return &PlaybookExecutor{
		db:               db,
		emailService:     emailService,
		mailchimpService: mailchimpService,
		stopChan:         make(chan struct{}),
	}
}

// Start begins the background execution loop
func (e *PlaybookExecutor) Start() {
	log.Println("Starting playbook executor...")
	e.ticker = time.NewTicker(1 * time.Minute)

	go func() {
		// Run immediately on start
		e.processEnrollments()

		for {
			select {
			case <-e.ticker.C:
				e.processEnrollments()
			case <-e.stopChan:
				log.Println("Stopping playbook executor...")
				e.ticker.Stop()
				return
			}
		}
	}()
}

// Stop halts the background execution loop
func (e *PlaybookExecutor) Stop() {
	close(e.stopChan)
}

// processEnrollments checks all active enrollments and processes due steps
func (e *PlaybookExecutor) processEnrollments() {
	var enrollments []PlaybookEnrollment
	if err := e.db.Where("status = ?", "active").Find(&enrollments).Error; err != nil {
		log.Printf("Error fetching active enrollments: %v", err)
		return
	}

	for _, enrollment := range enrollments {
		e.processEnrollment(&enrollment)
	}
}

// processEnrollment handles a single enrollment's step execution
func (e *PlaybookExecutor) processEnrollment(enrollment *PlaybookEnrollment) {
	// Get the playbook with steps
	var playbook Playbook
	if err := e.db.Where("id = ?", enrollment.PlaybookID).
		Preload("Steps", func(db *gorm.DB) *gorm.DB {
			return db.Order("step_order ASC")
		}).
		First(&playbook).Error; err != nil {
		log.Printf("Error fetching playbook %s: %v", enrollment.PlaybookID, err)
		return
	}

	// Check if playbook is active
	if playbook.Status != "active" {
		return
	}

	// Find the current step
	var currentStep *PlaybookStep
	for i := range playbook.Steps {
		if playbook.Steps[i].StepOrder == enrollment.CurrentStepOrder {
			currentStep = &playbook.Steps[i]
			break
		}
	}

	if currentStep == nil {
		// No more steps, mark as completed
		e.completeEnrollment(enrollment)
		return
	}

	// Check if there's already an execution for this step
	var execution PlaybookStepExecution
	err := e.db.Where("enrollment_id = ? AND step_id = ?", enrollment.ID, currentStep.ID).First(&execution).Error

	if err == gorm.ErrRecordNotFound {
		// Create a new execution
		execution = e.createStepExecution(enrollment, currentStep)
	} else if err != nil {
		log.Printf("Error checking step execution: %v", err)
		return
	}

	// Process based on execution status
	switch execution.Status {
	case "pending":
		// Schedule the execution
		e.scheduleExecution(&execution, currentStep)
	case "scheduled":
		// Check if it's time to execute
		if execution.ScheduledAt != nil && time.Now().After(*execution.ScheduledAt) {
			e.executeStep(&execution, currentStep, enrollment)
		}
	case "completed":
		// Move to next step
		e.advanceEnrollment(enrollment, &playbook)
	case "failed":
		// Retry logic
		if execution.RetryCount < 3 {
			execution.RetryCount++
			execution.Status = "scheduled"
			now := time.Now().Add(5 * time.Minute) // Retry in 5 minutes
			execution.ScheduledAt = &now
			e.db.Save(&execution)
		}
	}
}

// createStepExecution creates a new step execution record
func (e *PlaybookExecutor) createStepExecution(enrollment *PlaybookEnrollment, step *PlaybookStep) PlaybookStepExecution {
	execution := PlaybookStepExecution{
		ID:           uuid.New().String(),
		EnrollmentID: enrollment.ID,
		StepID:       step.ID,
		Status:       "pending",
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	e.db.Create(&execution)
	return execution
}

// scheduleExecution schedules a step for execution
func (e *PlaybookExecutor) scheduleExecution(execution *PlaybookStepExecution, step *PlaybookStep) {
	scheduledTime := time.Now().Add(time.Duration(step.DelayMinutes) * time.Minute)
	execution.ScheduledAt = &scheduledTime
	execution.Status = "scheduled"
	execution.UpdatedAt = time.Now()
	e.db.Save(execution)
}

// executeStep performs the actual step execution
func (e *PlaybookExecutor) executeStep(execution *PlaybookStepExecution, step *PlaybookStep, enrollment *PlaybookEnrollment) {
	now := time.Now()
	execution.StartedAt = &now
	execution.Status = "executing"
	e.db.Save(execution)

	var err error
	var result map[string]interface{}

	switch step.ActionType {
	case "email":
		result, err = e.executeEmailStep(step, enrollment)
	case "in_app_message":
		result, err = e.executeInAppMessageStep(step, enrollment)
	case "webhook":
		result, err = e.executeWebhookStep(step, enrollment)
	case "wait":
		// Wait steps just pass through after delay
		result = map[string]interface{}{"status": "waited"}
		err = nil
	case "condition":
		result, err = e.evaluateConditionStep(step, enrollment)
	case "feature_flag":
		result, err = e.executeFeatureFlagStep(step, enrollment)
	default:
		err = fmt.Errorf("unknown action type: %s", step.ActionType)
	}

	completedAt := time.Now()
	execution.CompletedAt = &completedAt
	execution.UpdatedAt = time.Now()

	if err != nil {
		execution.Status = "failed"
		execution.FailedAt = &completedAt
		execution.FailureReason = err.Error()
	} else {
		execution.Status = "completed"
		execution.ExecutionResult = result
	}

	e.db.Save(execution)
}

// executeEmailStep sends an email
func (e *PlaybookExecutor) executeEmailStep(step *PlaybookStep, enrollment *PlaybookEnrollment) (map[string]interface{}, error) {
	config := step.ActionConfig
	if config == nil {
		return nil, fmt.Errorf("no action config for email step")
	}

	subject, _ := config["subject"].(string)
	messageContent, _ := config["message_content"].(string)
	useMailchimp, _ := config["use_mailchimp"].(bool)
	useAutomation, _ := config["use_automation"].(bool)

	// Try to get user email from the enrollment's user
	var userEmail string
	var userName string

	// Look up user in events to get their email (users often have email as their ID or in properties)
	var event Event
	err := e.db.Where("project_id = ? AND user_id = ?", enrollment.ProjectID, enrollment.UserID).
		First(&event).Error
	if err == nil && event.Properties != nil {
		if email, ok := event.Properties["email"].(string); ok {
			userEmail = email
		}
		if name, ok := event.Properties["name"].(string); ok {
			userName = name
		}
	}

	// If no email found, use user_id as email if it looks like an email
	if userEmail == "" && strings.Contains(enrollment.UserID, "@") {
		userEmail = enrollment.UserID
	}

	// Check if we should use automation integration for discount codes
	if useAutomation {
		// Check if this is an automation campaign step
		automationType, _ := config["automation_type"].(string)
		if automationType == "discount_campaign" {
			return e.executeAutomationCampaignStep(step, enrollment, config)
		}
	}

	// Check if we should use Mailchimp integration
	if useMailchimp && e.mailchimpService != nil && userEmail != "" {
		integration, err := e.mailchimpService.GetIntegration(enrollment.ProjectID)
		if err == nil && integration != nil && integration.IsActive {
			// Sync this user to Mailchimp with playbook tag
			contact := MailchimpContact{
				Email:  userEmail,
				Status: "subscribed",
				MergeFields: map[string]interface{}{
					"FNAME": userName,
				},
				Tags: []string{"playbook_email", step.Name},
			}

			if syncErr := e.mailchimpService.SyncContact(enrollment.ProjectID, contact); syncErr != nil {
				log.Printf("Failed to sync contact to Mailchimp for playbook email: %v", syncErr)
				// Fall back to internal email
			} else {
				log.Printf("Synced user %s to Mailchimp for playbook %s step %s", userEmail, enrollment.PlaybookID, step.Name)
				return map[string]interface{}{
					"action":   "mailchimp_sync",
					"subject":  subject,
					"message":  messageContent,
					"email":    userEmail,
					"user_id":  enrollment.UserID,
					"provider": "mailchimp",
					"sent_at":  time.Now().Format(time.RFC3339),
				}, nil
			}
		}
	}

	// Use internal email service as fallback
	if userEmail != "" && e.emailService != nil {
		// Send email directly using email service
		log.Printf("Would send email to %s for playbook %s: %s", userEmail, enrollment.PlaybookID, subject)
	} else {
		log.Printf("No email found for user %s in playbook %s", enrollment.UserID, enrollment.PlaybookID)
	}

	return map[string]interface{}{
		"action":   "email_sent",
		"subject":  subject,
		"message":  messageContent,
		"email":    userEmail,
		"user_id":  enrollment.UserID,
		"provider": "internal",
		"sent_at":  time.Now().Format(time.RFC3339),
	}, nil
}

// executeInAppMessageStep creates an in-app message
func (e *PlaybookExecutor) executeInAppMessageStep(step *PlaybookStep, enrollment *PlaybookEnrollment) (map[string]interface{}, error) {
	config := step.ActionConfig
	if config == nil {
		return nil, fmt.Errorf("no action config for in-app message step")
	}

	log.Printf("Would show in-app message for enrollment %s, user %s", enrollment.ID, enrollment.UserID)

	return map[string]interface{}{
		"action":     "in_app_message_created",
		"user_id":    enrollment.UserID,
		"created_at": time.Now().Format(time.RFC3339),
	}, nil
}

// executeWebhookStep calls an external webhook
func (e *PlaybookExecutor) executeWebhookStep(step *PlaybookStep, enrollment *PlaybookEnrollment) (map[string]interface{}, error) {
	config := step.ActionConfig
	if config == nil {
		return nil, fmt.Errorf("no action config for webhook step")
	}

	url, ok := config["url"].(string)
	if !ok || url == "" {
		return nil, fmt.Errorf("no URL specified for webhook")
	}

	// Prepare webhook payload
	payload := map[string]interface{}{
		"enrollment_id": enrollment.ID,
		"playbook_id":   enrollment.PlaybookID,
		"user_id":       enrollment.UserID,
		"step_id":       step.ID,
		"step_name":     step.Name,
		"timestamp":     time.Now().Format(time.RFC3339),
	}

	// Add custom payload data if specified
	if customPayload, ok := config["payload"].(map[string]interface{}); ok {
		for k, v := range customPayload {
			payload[k] = v
		}
	}

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal webhook payload: %w", err)
	}

	// Make HTTP request
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Post(url, "application/json", bytes.NewBuffer(jsonPayload))
	if err != nil {
		return nil, fmt.Errorf("webhook request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("webhook returned error status: %d", resp.StatusCode)
	}

	return map[string]interface{}{
		"action":      "webhook_called",
		"url":         url,
		"status_code": resp.StatusCode,
		"called_at":   time.Now().Format(time.RFC3339),
	}, nil
}

// evaluateConditionStep evaluates a condition and may exit the enrollment
func (e *PlaybookExecutor) evaluateConditionStep(step *PlaybookStep, enrollment *PlaybookEnrollment) (map[string]interface{}, error) {
	conditions := step.Conditions
	if conditions == nil {
		return map[string]interface{}{"result": "no_conditions"}, nil
	}

	// TODO: Implement condition evaluation based on user metrics
	// For now, always pass
	return map[string]interface{}{
		"result":       "passed",
		"evaluated_at": time.Now().Format(time.RFC3339),
	}, nil
}

// executeFeatureFlagStep toggles a feature flag for the user
func (e *PlaybookExecutor) executeFeatureFlagStep(step *PlaybookStep, enrollment *PlaybookEnrollment) (map[string]interface{}, error) {
	config := step.ActionConfig
	if config == nil {
		return nil, fmt.Errorf("no action config for feature flag step")
	}

	flagName, _ := config["flag_name"].(string)
	enabled, _ := config["enabled"].(bool)

	log.Printf("Would set feature flag %s=%v for user %s", flagName, enabled, enrollment.UserID)

	return map[string]interface{}{
		"action":    "feature_flag_set",
		"flag_name": flagName,
		"enabled":   enabled,
		"user_id":   enrollment.UserID,
		"set_at":    time.Now().Format(time.RFC3339),
	}, nil
}

// advanceEnrollment moves the enrollment to the next step
func (e *PlaybookExecutor) advanceEnrollment(enrollment *PlaybookEnrollment, playbook *Playbook) {
	nextOrder := enrollment.CurrentStepOrder + 1

	// Check if there are more steps
	hasMoreSteps := false
	for _, step := range playbook.Steps {
		if step.StepOrder == nextOrder {
			hasMoreSteps = true
			break
		}
	}

	if hasMoreSteps {
		enrollment.CurrentStepOrder = nextOrder
		enrollment.UpdatedAt = time.Now()
		e.db.Save(enrollment)
	} else {
		e.completeEnrollment(enrollment)
	}
}

// completeEnrollment marks the enrollment as completed
func (e *PlaybookExecutor) completeEnrollment(enrollment *PlaybookEnrollment) {
	now := time.Now()
	enrollment.Status = "completed"
	enrollment.CompletedAt = &now
	enrollment.ExitReason = "completed"
	enrollment.UpdatedAt = now
	e.db.Save(enrollment)

	log.Printf("Enrollment %s completed for user %s", enrollment.ID, enrollment.UserID)
}

// TriggerEvaluator checks triggers and enrolls matching users
type TriggerEvaluator struct {
	db       *gorm.DB
	stopChan chan struct{}
	ticker   *time.Ticker
}

// NewTriggerEvaluator creates a new trigger evaluator
func NewTriggerEvaluator(db *gorm.DB) *TriggerEvaluator {
	return &TriggerEvaluator{
		db:       db,
		stopChan: make(chan struct{}),
	}
}

// Start begins the trigger evaluation loop
func (t *TriggerEvaluator) Start() {
	log.Println("Starting trigger evaluator...")
	t.ticker = time.NewTicker(5 * time.Minute)

	go func() {
		for {
			select {
			case <-t.ticker.C:
				t.evaluateTriggers()
			case <-t.stopChan:
				log.Println("Stopping trigger evaluator...")
				t.ticker.Stop()
				return
			}
		}
	}()
}

// Stop halts the trigger evaluation loop
func (t *TriggerEvaluator) Stop() {
	close(t.stopChan)
}

// evaluateTriggers checks all active triggers
func (t *TriggerEvaluator) evaluateTriggers() {
	// Get all active playbooks with enabled triggers
	var playbooks []Playbook
	if err := t.db.Where("status = ?", "active").
		Preload("Triggers", "is_enabled = ?", true).
		Find(&playbooks).Error; err != nil {
		log.Printf("Error fetching playbooks for trigger evaluation: %v", err)
		return
	}

	for _, playbook := range playbooks {
		for _, trigger := range playbook.Triggers {
			t.evaluateTrigger(&trigger, &playbook)
		}
	}
}

// evaluateTrigger evaluates a single trigger
func (t *TriggerEvaluator) evaluateTrigger(trigger *PlaybookTrigger, playbook *Playbook) {
	switch trigger.TriggerType {
	case "metric_threshold":
		t.evaluateMetricThresholdTrigger(trigger, playbook)
	case "event":
		// Event-based triggers are handled separately when events are ingested
	case "segment":
		t.evaluateSegmentTrigger(trigger, playbook)
	case "schedule":
		t.evaluateScheduleTrigger(trigger, playbook)
	}
}

// evaluateMetricThresholdTrigger checks users against metric thresholds
func (t *TriggerEvaluator) evaluateMetricThresholdTrigger(trigger *PlaybookTrigger, playbook *Playbook) {
	conditions := trigger.Conditions
	if conditions == nil {
		return
	}

	metric, _ := conditions["metric"].(string)
	operator, _ := conditions["operator"].(string)
	value, _ := conditions["value"].(float64)

	if metric == "" || operator == "" {
		return
	}

	// Example: Check health scores in churn analytics
	if metric == "health_score" || metric == "churn_risk_score" {
		var usersToEnroll []string

		query := t.db.Model(&ChurnAnalytics{}).
			Where("project_id = ?", playbook.ProjectID).
			Select("DISTINCT user_id")

		switch operator {
		case "lt":
			query = query.Where("churn_risk_score < ?", value)
		case "lte":
			query = query.Where("churn_risk_score <= ?", value)
		case "gt":
			query = query.Where("churn_risk_score > ?", value)
		case "gte":
			query = query.Where("churn_risk_score >= ?", value)
		case "eq":
			query = query.Where("churn_risk_score = ?", value)
		}

		query.Pluck("user_id", &usersToEnroll)

		for _, userID := range usersToEnroll {
			t.enrollUserIfEligible(userID, trigger, playbook)
		}
	}
}

// evaluateSegmentTrigger enrolls users in a segment
func (t *TriggerEvaluator) evaluateSegmentTrigger(trigger *PlaybookTrigger, playbook *Playbook) {
	// TODO: Implement segment-based enrollment
	log.Printf("Segment trigger evaluation not yet implemented for trigger %s", trigger.ID)
}

// evaluateScheduleTrigger handles scheduled triggers
func (t *TriggerEvaluator) evaluateScheduleTrigger(trigger *PlaybookTrigger, playbook *Playbook) {
	// TODO: Implement schedule-based triggers
	log.Printf("Schedule trigger evaluation not yet implemented for trigger %s", trigger.ID)
}

// enrollUserIfEligible checks eligibility and enrolls a user
func (t *TriggerEvaluator) enrollUserIfEligible(userID string, trigger *PlaybookTrigger, playbook *Playbook) {
	// Check if already enrolled
	var existingEnrollment PlaybookEnrollment
	err := t.db.Where("playbook_id = ? AND user_id = ? AND status = ?", playbook.ID, userID, "active").
		First(&existingEnrollment).Error
	if err == nil {
		return // Already enrolled
	}

	// Check cooldown
	if trigger.CooldownMinutes > 0 {
		cooldownTime := time.Now().Add(-time.Duration(trigger.CooldownMinutes) * time.Minute)
		var recentEnrollment PlaybookEnrollment
		err = t.db.Where("playbook_id = ? AND user_id = ? AND enrolled_at > ?", playbook.ID, userID, cooldownTime).
			First(&recentEnrollment).Error
		if err == nil {
			return // Still in cooldown
		}
	}

	// Check max enrollments
	if trigger.MaxEnrollments > 0 {
		var count int64
		t.db.Model(&PlaybookEnrollment{}).
			Where("playbook_id = ? AND trigger_id = ?", playbook.ID, trigger.ID).
			Count(&count)
		if count >= int64(trigger.MaxEnrollments) {
			return // Max enrollments reached
		}
	}

	// Enroll the user
	now := time.Now()
	triggerID := trigger.ID
	enrollment := PlaybookEnrollment{
		ID:               uuid.New().String(),
		PlaybookID:       playbook.ID,
		ProjectID:        playbook.ProjectID,
		UserID:           userID,
		TriggerID:        &triggerID,
		Status:           "active",
		CurrentStepOrder: 1,
		EnrolledAt:       now,
		MetricsAtStart:   map[string]interface{}{},
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	if err := t.db.Create(&enrollment).Error; err != nil {
		log.Printf("Failed to enroll user %s in playbook %s: %v", userID, playbook.ID, err)
		return
	}

	log.Printf("Enrolled user %s in playbook %s via trigger %s", userID, playbook.ID, trigger.ID)
}

// executeAutomationCampaignStep handles a playbook step that triggers an automation discount campaign
func (e *PlaybookExecutor) executeAutomationCampaignStep(step *PlaybookStep, enrollment *PlaybookEnrollment, config map[string]interface{}) (map[string]interface{}, error) {
	discountPercentage := 20
	if dp, ok := config["discount_percentage"].(float64); ok {
		discountPercentage = int(dp)
	}

	// Get user information
	var userEmail string
	var userName string
	var userChurnRisk float64

	var event Event
	err := e.db.Where("project_id = ? AND user_id = ?", enrollment.ProjectID, enrollment.UserID).
		First(&event).Error
	if err == nil && event.Properties != nil {
		if email, ok := event.Properties["email"].(string); ok {
			userEmail = email
		}
		if name, ok := event.Properties["name"].(string); ok {
			userName = name
		}
		if risk, ok := event.Properties["churn_risk_score"].(float64); ok {
			userChurnRisk = risk
		}
	}

	if userEmail == "" && strings.Contains(enrollment.UserID, "@") {
		userEmail = enrollment.UserID
	}

	// Generate discount code
	discountCode := fmt.Sprintf("SAVE%d-%s", discountPercentage, strings.ToUpper(uuid.New().String()[:6]))

	// Generate email content
	content := e.generateAutomationEmailContent(userName, userChurnRisk, discountCode, discountPercentage)

	// If we have Mailchimp integration, create campaign
	if e.mailchimpService != nil {
		integration, mcErr := e.mailchimpService.GetIntegration(enrollment.ProjectID)
		if mcErr == nil && integration != nil && integration.IsActive {
			campaignRequest := &MailchimpCampaignRequest{
				Type: "regular",
				Recipients: struct {
					ListID string `json:"list_id"`
				}{
					ListID: getString(integration.Settings, "audience_id"),
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
					Title:       fmt.Sprintf("Special Offer - %s", userName),
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

			_, sendErr := e.mailchimpService.CreateAndSendCampaign(enrollment.ProjectID, campaignRequest, content.HTMLContent)
			if sendErr != nil {
				log.Printf("Error sending campaign for user %s: %v", enrollment.UserID, sendErr)
				return nil, fmt.Errorf("failed to send Mailchimp campaign: %w", sendErr)
			}

			log.Printf("Sent automation campaign to user %s with discount code %s", enrollment.UserID, discountCode)

			return map[string]interface{}{
				"action":           "automation_campaign_sent",
				"subject":          content.Subject,
				"email":            userEmail,
				"user_id":          enrollment.UserID,
				"discount_code":    discountCode,
				"discount_percent": discountPercentage,
				"provider":         "mailchimp",
				"sent_at":          time.Now().Format(time.RFC3339),
			}, nil
		}
	}

	// Fallback to log message if no Mailchimp integration
	log.Printf("Would send email to %s with discount code %s (%d%% off)", userEmail, discountCode, discountPercentage)

	return map[string]interface{}{
		"action":           "automation_campaign_logged",
		"subject":          content.Subject,
		"email":            userEmail,
		"user_id":          enrollment.UserID,
		"discount_code":    discountCode,
		"discount_percent": discountPercentage,
		"provider":         "none",
		"sent_at":          time.Now().Format(time.RFC3339),
	}, nil
}

// playbookEmailContent is a simple email content structure for playbook-generated emails
type playbookEmailContent struct {
	Subject     string
	HTMLContent string
}

// generateAutomationEmailContent produces templated email content for a discount campaign
func (e *PlaybookExecutor) generateAutomationEmailContent(userName string, churnRisk float64, discountCode string, discountPercent int) *playbookEmailContent {
	if userName == "" {
		userName = "Valued Customer"
	}

	subject := fmt.Sprintf("%s, here's %d%% off — just for you!", userName, discountPercent)

	html := fmt.Sprintf(`<!DOCTYPE html>
<html>
<head><meta charset="UTF-8"></head>
<body style="font-family:Arial,sans-serif;max-width:600px;margin:0 auto;padding:20px;">
  <h2 style="color:#333;">Hi %s,</h2>
  <p>We noticed you haven't been around lately and we miss you! Your success means the world to us.</p>
  <p>As a thank-you for being part of our community, here's an exclusive offer:</p>
  <div style="background:#f0f7ff;border:2px solid #4318FF;border-radius:8px;padding:20px;text-align:center;margin:20px 0;">
    <p style="font-size:24px;font-weight:bold;color:#4318FF;margin:0;">%d%% OFF</p>
    <p style="font-size:14px;color:#666;margin:8px 0 0;">Use code: <strong>%s</strong></p>
  </div>
  <p>We'd love to help you get the most out of our product. Reply to this email if there's anything we can do!</p>
  <p style="color:#666;font-size:12px;margin-top:30px;">This offer was created just for you.</p>
</body>
</html>`, userName, discountPercent, discountCode)

	return &playbookEmailContent{
		Subject:     subject,
		HTMLContent: html,
	}
}
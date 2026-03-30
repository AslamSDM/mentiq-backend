package main

import (
	"fmt"
	"log"
	"strings"
	"time"

	"gorm.io/gorm"
)

// AutomationService handles automation execution and LLM-generated email content
type AutomationService struct {
	llmService       *LLMService
	mailchimpService *MailchimpService
	db               *gorm.DB
}

// NewAutomationService creates a new automation service
func NewAutomationService(db *gorm.DB, llmService *LLMService, mailchimpService *MailchimpService) *AutomationService {
	return &AutomationService{
		llmService:       llmService,
		mailchimpService: mailchimpService,
		db:               db,
	}
}

// GenerateEmailContentRequest represents a request to generate email content via LLM
type GenerateEmailContentRequest struct {
	TemplateType        string                 `json:"template_type"` // churn_prevention, feature_adoption, engagement, discount
	UserContext         map[string]interface{} `json:"user_context"`
	ProductContext      map[string]interface{} `json:"product_context"`
	Personalization     map[string]interface{} `json:"personalization"`
	PersonalizationVars []string               `json:"personalization_vars"`
	CustomPrompt        string                 `json:"custom_prompt,omitempty"` // User-provided prompt override
}

// GeneratedEmailContent represents LLM-generated email content
type GeneratedEmailContent struct {
	Subject         string                 `json:"subject"`
	HTMLContent     string                 `json:"html_content"`
	PlainText       string                 `json:"plain_text"`
	Personalization map[string]interface{} `json:"personalization"`
	GeneratedAt     time.Time              `json:"generated_at"`
}

// GenerateEmailContent generates personalized email content using Claude
func (s *AutomationService) GenerateEmailContent(req GenerateEmailContentRequest) (*GeneratedEmailContent, error) {
	// Use custom prompt if provided, otherwise build default
	var prompt string
	if req.CustomPrompt != "" {
		prompt = s.buildCustomPrompt(req)
	} else {
		prompt = s.buildEmailPrompt(req)
	}

	// Call LLM service
	llmResponse, err := s.llmService.generateContent(prompt)
	if err != nil {
		return nil, fmt.Errorf("failed to generate email content: %w", err)
	}

	// Parse response
	content, err := s.parseGeneratedContent(llmResponse, req)
	if err != nil {
		return nil, fmt.Errorf("failed to parse generated content: %w", err)
	}

	return content, nil
}

// buildCustomPrompt wraps the user's custom prompt with context and output format requirements
func (s *AutomationService) buildCustomPrompt(req GenerateEmailContentRequest) string {
	var prompt strings.Builder

	// Inject user context so the custom prompt can reference it
	prompt.WriteString("You are generating a personalized email for a SaaS product.\n\n")
	prompt.WriteString("=== USER CONTEXT ===\n")
	if userName, ok := req.UserContext["name"].(string); ok && userName != "" {
		prompt.WriteString(fmt.Sprintf("User Name: %s\n", userName))
	}
	if userEmail, ok := req.UserContext["email"].(string); ok && userEmail != "" {
		prompt.WriteString(fmt.Sprintf("User Email: %s\n", userEmail))
	}
	if riskScore, ok := req.UserContext["churn_risk_score"].(float64); ok {
		prompt.WriteString(fmt.Sprintf("Churn Risk Score: %.0f%%\n", riskScore))
	}
	if lastActive, ok := req.UserContext["last_active_days"].(int); ok {
		prompt.WriteString(fmt.Sprintf("Days Since Last Active: %d\n", lastActive))
	}
	if productName, ok := req.ProductContext["product_name"].(string); ok {
		prompt.WriteString(fmt.Sprintf("Product Name: %s\n", productName))
	}

	prompt.WriteString("\n=== YOUR INSTRUCTIONS ===\n")
	prompt.WriteString(req.CustomPrompt)

	// Always append output format requirements
	prompt.WriteString("\n\n=== OUTPUT FORMAT (REQUIRED) ===\n")
	prompt.WriteString("1. Subject line (max 60 characters, compelling and personalized)\n")
	prompt.WriteString("2. HTML email content with proper formatting\n")
	prompt.WriteString("3. Plain text version\n")
	if len(req.PersonalizationVars) > 0 {
		prompt.WriteString(fmt.Sprintf("4. Include personalization placeholders for: %s\n", strings.Join(req.PersonalizationVars, ", ")))
	}
	prompt.WriteString("5. Use proper email HTML structure with inline CSS\n")
	prompt.WriteString("6. Include clear call-to-action button\n")
	prompt.WriteString("7. Mobile-responsive design\n")

	return prompt.String()
}

// buildEmailPrompt builds a personalized prompt for Claude based on template type
func (s *AutomationService) buildEmailPrompt(req GenerateEmailContentRequest) string {
	var prompt strings.Builder

	// Base context
	prompt.WriteString(fmt.Sprintf("You are an expert email marketing copywriter specializing in SaaS user engagement. "))
	prompt.WriteString(fmt.Sprintf("Generate a personalized email for a user with the following context:\n\n"))

	// User context
	if userName, ok := req.UserContext["name"].(string); ok && userName != "" {
		prompt.WriteString(fmt.Sprintf("User Name: %s\n", userName))
	}
	if userEmail, ok := req.UserContext["email"].(string); ok && userEmail != "" {
		prompt.WriteString(fmt.Sprintf("User Email: %s\n", userEmail))
	}
	if riskScore, ok := req.UserContext["churn_risk_score"].(float64); ok {
		prompt.WriteString(fmt.Sprintf("Churn Risk Score: %.0f%%\n", riskScore))
	}
	if lastActive, ok := req.UserContext["last_active_days"].(int); ok {
		prompt.WriteString(fmt.Sprintf("Days Since Last Active: %d\n", lastActive))
	}

	// Product context
	if productName, ok := req.ProductContext["product_name"].(string); ok {
		prompt.WriteString(fmt.Sprintf("Product Name: %s\n", productName))
	}
	if features, ok := req.ProductContext["features"].([]string); ok {
		prompt.WriteString(fmt.Sprintf("Product Features: %s\n", strings.Join(features, ", ")))
	}

	// Template-specific instructions
	switch req.TemplateType {
	case "churn_prevention":
		prompt.WriteString(fmt.Sprintf("\nEmail Purpose: Churn Prevention\n"))
		prompt.WriteString(fmt.Sprintf("Goal: Persuade the user to stay by highlighting value and offering assistance.\n"))
		prompt.WriteString(fmt.Sprintf("Tone: Empathetic, supportive, and value-focused\n"))
		if discountPercent, ok := req.Personalization["discount_percent"].(int); ok && discountPercent > 0 {
			prompt.WriteString(fmt.Sprintf("Special Offer: %d%% discount on their next billing cycle\n", discountPercent))
		}

	case "feature_adoption":
		prompt.WriteString(fmt.Sprintf("\nEmail Purpose: Feature Adoption\n"))
		prompt.WriteString(fmt.Sprintf("Goal: Introduce unused features that would benefit the user based on their usage patterns.\n"))
		prompt.WriteString(fmt.Sprintf("Tone: Helpful, educational, and excited\n"))
		if unusedFeatures, ok := req.Personalization["unused_features"].([]string); ok {
			prompt.WriteString(fmt.Sprintf("Unused Features to Promote: %s\n", strings.Join(unusedFeatures, ", ")))
		}

	case "engagement":
		prompt.WriteString(fmt.Sprintf("\nEmail Purpose: Re-engagement\n"))
		prompt.WriteString(fmt.Sprintf("Goal: Bring the user back to the product with compelling reasons and social proof.\n"))
		prompt.WriteString(fmt.Sprintf("Tone: Energetic, encouraging, and community-focused\n"))

	case "discount":
		prompt.WriteString(fmt.Sprintf("\nEmail Purpose: Special Discount Offer\n"))
		prompt.WriteString(fmt.Sprintf("Goal: Convert the user with a time-limited discount offer.\n"))
		prompt.WriteString(fmt.Sprintf("Tone: Urgent but not pushy, value-focused\n"))
		if discountCode, ok := req.Personalization["discount_code"].(string); ok {
			prompt.WriteString(fmt.Sprintf("Discount Code: %s\n", discountCode))
		}
		if discountPercent, ok := req.Personalization["discount_percent"].(int); ok {
			prompt.WriteString(fmt.Sprintf("Discount Percentage: %d%%\n", discountPercent))
		}
	}

	// Output requirements
	prompt.WriteString(fmt.Sprintf("\nOutput Format:\n"))
	prompt.WriteString(fmt.Sprintf("1. Subject line (max 60 characters, compelling and personalized)\n"))
	prompt.WriteString(fmt.Sprintf("2. HTML email content with proper formatting\n"))
	prompt.WriteString(fmt.Sprintf("3. Plain text version\n"))
	prompt.WriteString(fmt.Sprintf("4. Include personalization placeholders for: %s\n", strings.Join(req.PersonalizationVars, ", ")))
	prompt.WriteString(fmt.Sprintf("5. Use proper email HTML structure with inline CSS\n"))
	prompt.WriteString(fmt.Sprintf("6. Include clear call-to-action button\n"))
	prompt.WriteString(fmt.Sprintf("7. Mobile-responsive design\n"))

	return prompt.String()
}

// parseGeneratedContent parses the LLM response into structured email content
func (s *AutomationService) parseGeneratedContent(llmResponse string, req GenerateEmailContentRequest) (*GeneratedEmailContent, error) {
	// Simple parsing - in production, you'd want more robust parsing
	lines := strings.Split(llmResponse, "\n")

	var subject string
	var htmlContent strings.Builder
	var plainText strings.Builder
	inHTML := false
	inPlainText := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Extract subject
		if strings.HasPrefix(trimmed, "Subject:") || strings.HasPrefix(trimmed, "1.") && subject == "" {
			parts := strings.SplitN(trimmed, ":", 2)
			if len(parts) > 1 {
				subject = strings.TrimSpace(parts[1])
			}
		}

		// Detect sections
		if strings.Contains(trimmed, "HTML Content:") || strings.Contains(trimmed, "HTML Version:") {
			inHTML = true
			inPlainText = false
			continue
		}

		if strings.Contains(trimmed, "Plain Text:") || strings.Contains(trimmed, "Text Version:") {
			inHTML = false
			inPlainText = true
			continue
		}

		// Collect content based on section
		if inHTML && !strings.Contains(trimmed, "---") && !strings.Contains(trimmed, "===") {
			htmlContent.WriteString(line + "\n")
		}

		if inPlainText && !strings.Contains(trimmed, "---") && !strings.Contains(trimmed, "===") {
			plainText.WriteString(line + "\n")
		}
	}

	// If we didn't get structured content, use the full response as HTML
	if htmlContent.Len() == 0 {
		htmlContent.WriteString(llmResponse)
	}
	if plainText.Len() == 0 {
		plainText.WriteString(stripHTML(llmResponse))
	}

	// Perform variable substitution
	personalization := make(map[string]interface{})
	for key, value := range req.Personalization {
		personalization[key] = value
	}

	// Substitute variables in content
	finalHTML := s.substituteVariables(htmlContent.String(), personalization)
	finalPlainText := s.substituteVariables(plainText.String(), personalization)
	finalSubject := s.substituteVariables(subject, personalization)

	return &GeneratedEmailContent{
		Subject:         finalSubject,
		HTMLContent:     finalHTML,
		PlainText:       finalPlainText,
		Personalization: personalization,
		GeneratedAt:     time.Now(),
	}, nil
}

// stripHTML removes HTML tags for plain text version
func stripHTML(html string) string {
	// Simple implementation - in production, use a proper HTML parser
	result := html
	result = strings.ReplaceAll(result, "<br>", "\n")
	result = strings.ReplaceAll(result, "<br/>", "\n")
	result = strings.ReplaceAll(result, "<p>", "\n\n")
	result = strings.ReplaceAll(result, "</p>", "")

	// Quick and dirty tag removal
	inTag := false
	var output strings.Builder
	for _, ch := range result {
		if ch == '<' {
			inTag = true
			continue
		}
		if ch == '>' {
			inTag = false
			continue
		}
		if !inTag {
			output.WriteRune(ch)
		}
	}

	return strings.TrimSpace(output.String())
}

// substituteVariables replaces placeholders in content with actual values
func (s *AutomationService) substituteVariables(content string, vars map[string]interface{}) string {
	result := content
	for key, value := range vars {
		placeholder := fmt.Sprintf("{{%s}}", key)
		result = strings.ReplaceAll(result, placeholder, fmt.Sprintf("%v", value))
	}
	return result
}

// GeneratePersonalizedCampaign creates a personalized campaign using LLM
func (s *AutomationService) GeneratePersonalizedCampaign(projectID string, automation *AutomationSettings, userInfo map[string]interface{}) (*GeneratedEmailContent, error) {
	// Extract user context
	userContext := map[string]interface{}{
		"name":             getString(userInfo, "name"),
		"email":            getString(userInfo, "email"),
		"user_id":          getString(userInfo, "user_id"),
		"churn_risk_score": getFloat(userInfo, "churn_risk_score"),
		"last_active_days": getInt(userInfo, "last_active_days"),
		"feature_usage":    getInt(userInfo, "feature_usage"),
	}

	// Extract product context
	productContext := map[string]interface{}{
		"product_name":      getString(userInfo, "product_name"),
		"features":          getStringSlice(userInfo, "features"),
		"subscription_type": getString(userInfo, "subscription_type"),
	}

	// Prepare personalization data
	personalization := map[string]interface{}{}

	// Add automation-specific personalization
	switch automation.Type {
	case "churn_prevention":
		if config, ok := automation.Config["discount_percentage"].(float64); ok {
			personalization["discount_percent"] = int(config)
		}
		// Generate discount code
		discountCode := fmt.Sprintf("SAVE%d-%s",
			getInt(userInfo, "churn_risk_score")*10,
			randomString(6),
		)
		personalization["discount_code"] = discountCode

	case "feature_adoption":
		if unusedFeatures, ok := userInfo["unused_features"].([]interface{}); ok {
			features := make([]string, len(unusedFeatures))
			for i, f := range unusedFeatures {
				features[i] = fmt.Sprintf("%v", f)
			}
			personalization["unused_features"] = features
			personalization["feature_count"] = len(features)
		}

	case "engagement":
		personalization["last_login_days"] = getInt(userInfo, "last_active_days")
		personalization["missed_features"] = getStringSlice(userInfo, "missed_features")
	}

	// Load or create email template
	var template EmailTemplate
	templateType := automation.Type
	if templateID, ok := automation.Config["email_template_id"].(string); ok && templateID != "" {
		s.db.Where("id = ? AND type = ?", templateID, templateType).First(&template)
	}

	// If no template found, create one based on type
	if template.ID == "" {
		template = s.createDefaultTemplate(templateType)
	}

	// Build generation request
	req := GenerateEmailContentRequest{
		TemplateType:    templateType,
		UserContext:     userContext,
		ProductContext:  productContext,
		Personalization: personalization,
		CustomPrompt:    automation.CustomPrompt,
	}

	// Add personalization vars from template
	if template.PersonalizationVars != nil {
		req.PersonalizationVars = template.PersonalizationVars
	}

	// Generate content
	content, err := s.GenerateEmailContent(req)
	if err != nil {
		return nil, fmt.Errorf("failed to generate personalized content: %w", err)
	}

	log.Printf("Generated personalized email for user %s using automation %s",
		userInfo["user_id"], automation.ID)

	return content, nil
}

// Helper functions for safe type extraction
func getString(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func getFloat(m map[string]interface{}, key string) float64 {
	if v, ok := m[key].(float64); ok {
		return v
	}
	return 0
}

func getInt(m map[string]interface{}, key string) int {
	if v, ok := m[key].(int); ok {
		return v
	}
	if v, ok := m[key].(float64); ok {
		return int(v)
	}
	if v, ok := m[key].(string); ok {
		var result int
		fmt.Sscanf(v, "%d", &result)
		return result
	}
	return 0
}

func getStringOr(m map[string]interface{}, key, defaultVal string) string {
	if v, ok := m[key].(string); ok && v != "" {
		return v
	}
	return defaultVal
}

func getStringSlice(m map[string]interface{}, key string) []string {
	if v, ok := m[key].([]interface{}); ok {
		result := make([]string, len(v))
		for i, item := range v {
			result[i] = fmt.Sprintf("%v", item)
		}
		return result
	}
	return []string{}
}

// createDefaultTemplate creates a default email template for a given type
func (s *AutomationService) createDefaultTemplate(templateType string) EmailTemplate {
	switch templateType {
	case "churn_prevention":
		return EmailTemplate{
			Name:                "Default Churn Prevention",
			Type:                "churn_prevention",
			SubjectTemplate:     "{{user_name}}, we miss you! Here's a special offer",
			ContentTemplate:     "Hi {{user_name}},\n\nWe noticed you haven't been active lately and wanted to reach out personally. Your success is important to us!\n\nHere's a {{discount_percent}}% discount code just for you: {{discount_code}}\n\nUse it on your next billing cycle. We're here to help you succeed!",
			PersonalizationVars: []string{"user_name", "discount_percent", "discount_code", "product_name"},
			IsActive:            true,
		}

	case "feature_adoption":
		return EmailTemplate{
			Name:                "Default Feature Adoption",
			Type:                "feature_adoption",
			SubjectTemplate:     "{{user_name}}, discover features you're missing!",
			ContentTemplate:     "Hi {{user_name}},\n\nDid you know that {{feature_count}} powerful features are waiting for you?\n\nTry {{unused_features}} today and unlock more value from {{product_name}}!",
			PersonalizationVars: []string{"user_name", "unused_features", "feature_count", "product_name"},
			IsActive:            true,
		}

	case "engagement":
		return EmailTemplate{
			Name:                "Default Re-engagement",
			Type:                "engagement",
			SubjectTemplate:     "{{user_name}}, what's new since you were last here?",
			ContentTemplate:     "Hi {{user_name}},\n\nIt's been {{last_login_days}} days since your last visit.\n\nWe've added exciting new features and improvements to {{product_name}}!",
			PersonalizationVars: []string{"user_name", "last_login_days", "product_name", "missed_features"},
			IsActive:            true,
		}

	default:
		return EmailTemplate{
			Name:                "Default Email",
			Type:                templateType,
			SubjectTemplate:     "Important update from {{product_name}}",
			ContentTemplate:     "Hi {{user_name}},\n\nWe have an important update for you about {{product_name}}.",
			PersonalizationVars: []string{"user_name", "product_name"},
			IsActive:            true,
		}
	}
}

// randomString generates a random string of specified length
func randomString(length int) string {
	const charset = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, length)
	for i := range b {
		b[i] = charset[time.Now().UnixNano()%int64(len(charset))]
	}
	return string(b)
}

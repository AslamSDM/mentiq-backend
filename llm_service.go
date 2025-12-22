package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// LLMService handles interactions with the Anthropic Claude API
type LLMService struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
	db         *gorm.DB
}

// NewLLMService creates a new LLM service instance
func NewLLMService(db *gorm.DB) *LLMService {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		fmt.Println("Warning: ANTHROPIC_API_KEY not set. LLM features will be disabled.")
	}

	return &LLMService{
		apiKey:  apiKey,
		baseURL: "https://api.anthropic.com/v1",
		httpClient: &http.Client{
			Timeout: 120 * time.Second, // Claude can take a while for complex prompts
		},
		db: db,
	}
}

// ClaudeRequest represents a request to Claude API
type ClaudeRequest struct {
	Model     string          `json:"model"`
	MaxTokens int             `json:"max_tokens"`
	Messages  []ClaudeMessage `json:"messages"`
}

// ClaudeMessage represents a message in the Claude API request
type ClaudeMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ClaudeResponse represents Claude's API response
type ClaudeResponse struct {
	ID           string `json:"id"`
	Type         string `json:"type"`
	Role         string `json:"role"`
	Content      []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Model        string `json:"model"`
	StopReason   string `json:"stop_reason"`
	StopSequence string `json:"stop_sequence"`
	Usage        struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// GeneratePlaybookRequest represents the request to generate a playbook
type GeneratePlaybookLLMRequest struct {
	PlaybookType string                 `json:"playbook_type" binding:"required,oneof=churn_prevention growth_expansion"`
	ContextData  map[string]interface{} `json:"context_data"`
	Goals        []string               `json:"goals"`
}

// ParsedPlaybook represents the structured output from LLM
type ParsedPlaybook struct {
	Name                string                   `json:"name"`
	Description         string                   `json:"description"`
	Steps               []ParsedStep             `json:"steps"`
	RecommendedTriggers []ParsedTrigger          `json:"recommended_triggers"`
	ExpectedOutcomes    map[string]interface{}   `json:"expected_outcomes"`
}

// ParsedStep represents a step parsed from LLM output
type ParsedStep struct {
	StepOrder    int                    `json:"step_order"`
	Name         string                 `json:"name"`
	Description  string                 `json:"description"`
	ActionType   string                 `json:"action_type"`
	ActionConfig map[string]interface{} `json:"action_config"`
	DelayMinutes int                    `json:"delay_minutes"`
	Conditions   map[string]interface{} `json:"conditions,omitempty"`
}

// ParsedTrigger represents a trigger parsed from LLM output
type ParsedTrigger struct {
	Name        string                 `json:"name"`
	TriggerType string                 `json:"trigger_type"`
	Conditions  map[string]interface{} `json:"conditions"`
}

// GeneratePlaybook calls Claude to generate a playbook
func (s *LLMService) GeneratePlaybook(projectID string, req GeneratePlaybookLLMRequest) (*LLMPlaybookGeneration, error) {
	if s.apiKey == "" {
		return nil, fmt.Errorf("Anthropic API key not configured")
	}

	// Build the prompt
	prompt := s.buildPlaybookPrompt(req)

	// Create generation record
	generation := LLMPlaybookGeneration{
		ID:           uuid.New().String(),
		ProjectID:    projectID,
		PlaybookType: req.PlaybookType,
		InputContext: req.ContextData,
		PromptUsed:   prompt,
		Status:       "generating",
		ModelUsed:    "claude-3-5-sonnet-20241022",
		CreatedAt:    time.Now(),
	}

	if err := s.db.Create(&generation).Error; err != nil {
		return nil, fmt.Errorf("failed to create generation record: %w", err)
	}

	// Call Claude API
	claudeReq := ClaudeRequest{
		Model:     "claude-3-5-sonnet-20241022",
		MaxTokens: 4096,
		Messages: []ClaudeMessage{
			{
				Role:    "user",
				Content: prompt,
			},
		},
	}

	reqBody, err := json.Marshal(claudeReq)
	if err != nil {
		s.updateGenerationStatus(&generation, "failed", err.Error())
		return nil, err
	}

	httpReq, err := http.NewRequest("POST", s.baseURL+"/messages", bytes.NewBuffer(reqBody))
	if err != nil {
		s.updateGenerationStatus(&generation, "failed", err.Error())
		return nil, err
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", s.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		s.updateGenerationStatus(&generation, "failed", err.Error())
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		s.updateGenerationStatus(&generation, "failed", err.Error())
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		errMsg := fmt.Sprintf("Claude API error: %s", string(body))
		s.updateGenerationStatus(&generation, "failed", errMsg)
		return nil, fmt.Errorf(errMsg)
	}

	var claudeResp ClaudeResponse
	if err := json.Unmarshal(body, &claudeResp); err != nil {
		s.updateGenerationStatus(&generation, "failed", err.Error())
		return nil, err
	}

	// Extract text content
	var llmResponse string
	for _, content := range claudeResp.Content {
		if content.Type == "text" {
			llmResponse = content.Text
			break
		}
	}

	// Parse the JSON from the response
	parsedPlaybook, err := s.parsePlaybookResponse(llmResponse)
	if err != nil {
		s.updateGenerationStatus(&generation, "failed", "Failed to parse LLM response: "+err.Error())
		return nil, err
	}

	// Convert to map for storage
	parsedMap := make(map[string]interface{})
	parsedBytes, _ := json.Marshal(parsedPlaybook)
	json.Unmarshal(parsedBytes, &parsedMap)

	// Update generation record
	now := time.Now()
	generation.LLMResponse = llmResponse
	generation.ParsedPlaybook = parsedMap
	generation.Status = "completed"
	generation.CompletedAt = &now
	generation.TokensUsed = claudeResp.Usage.InputTokens + claudeResp.Usage.OutputTokens

	// Estimate cost (Claude 3.5 Sonnet pricing: $3/MTok input, $15/MTok output)
	inputCost := float64(claudeResp.Usage.InputTokens) / 1000000 * 3
	outputCost := float64(claudeResp.Usage.OutputTokens) / 1000000 * 15
	generation.CostCents = int((inputCost + outputCost) * 100)

	s.db.Save(&generation)

	return &generation, nil
}

// updateGenerationStatus updates the generation status with error
func (s *LLMService) updateGenerationStatus(gen *LLMPlaybookGeneration, status, errorMsg string) {
	gen.Status = status
	gen.ErrorMessage = errorMsg
	s.db.Save(gen)
}

// buildPlaybookPrompt creates the prompt for playbook generation
func (s *LLMService) buildPlaybookPrompt(req GeneratePlaybookLLMRequest) string {
	playbookTypeDesc := "churn prevention"
	if req.PlaybookType == "growth_expansion" {
		playbookTypeDesc = "growth and expansion"
	}

	contextJSON, _ := json.MarshalIndent(req.ContextData, "", "  ")
	goalsStr := "Not specified"
	if len(req.Goals) > 0 {
		goalsStr = strings.Join(req.Goals, "\n- ")
	}

	return fmt.Sprintf(`You are an expert SaaS customer success strategist. Based on the following analytics data, generate a structured playbook for %s.

## Analytics Context
%s

## Business Goals
- %s

## Task
Generate a %s playbook with actionable steps.

Return ONLY a valid JSON object (no markdown, no code blocks, just raw JSON) with this exact structure:
{
  "name": "descriptive playbook name",
  "description": "1-2 sentence description of the playbook's purpose",
  "steps": [
    {
      "step_order": 1,
      "name": "Step name",
      "description": "What this step accomplishes",
      "action_type": "email|in_app_message|webhook|wait",
      "action_config": {
        "subject": "For emails - the subject line",
        "message_content": "The message body - personalized and empathetic",
        "cta_text": "Call to action button text",
        "cta_url": "/path/to/action"
      },
      "delay_minutes": 0,
      "conditions": {}
    }
  ],
  "recommended_triggers": [
    {
      "name": "Trigger name",
      "trigger_type": "metric_threshold",
      "conditions": {
        "metric": "health_score",
        "operator": "lt",
        "value": 50
      }
    }
  ],
  "expected_outcomes": {
    "target_completion_rate": 80,
    "expected_impact": "Description of expected impact"
  }
}

Important guidelines:
1. Create 3-5 actionable steps
2. Include appropriate delays between steps (in minutes: 0, 60, 1440 for 1 day, 10080 for 1 week)
3. Make messages empathetic and value-focused
4. Include at least 1-2 recommended triggers based on the context
5. action_type must be one of: email, in_app_message, webhook, wait
6. For wait steps, set action_config to {} and use delay_minutes for the wait duration

Return ONLY the JSON object, nothing else.`, playbookTypeDesc, string(contextJSON), goalsStr, playbookTypeDesc)
}

// parsePlaybookResponse extracts and parses JSON from Claude's response
func (s *LLMService) parsePlaybookResponse(response string) (*ParsedPlaybook, error) {
	// Try to extract JSON from the response
	// Sometimes Claude wraps it in markdown code blocks
	jsonStr := response

	// Remove markdown code blocks if present
	codeBlockRegex := regexp.MustCompile("```(?:json)?\\s*([\\s\\S]*?)\\s*```")
	matches := codeBlockRegex.FindStringSubmatch(response)
	if len(matches) > 1 {
		jsonStr = matches[1]
	}

	// Trim whitespace
	jsonStr = strings.TrimSpace(jsonStr)

	// Parse JSON
	var parsed ParsedPlaybook
	if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %w, response: %s", err, jsonStr[:min(500, len(jsonStr))])
	}

	return &parsed, nil
}

// ApplyGeneratedPlaybook converts a generation to an actual playbook
func (s *LLMService) ApplyGeneratedPlaybook(generation *LLMPlaybookGeneration, accountID string) (*Playbook, error) {
	if generation.Status != "completed" {
		return nil, fmt.Errorf("generation is not completed")
	}

	// Parse the stored playbook
	parsedBytes, _ := json.Marshal(generation.ParsedPlaybook)
	var parsed ParsedPlaybook
	if err := json.Unmarshal(parsedBytes, &parsed); err != nil {
		return nil, fmt.Errorf("failed to parse stored playbook: %w", err)
	}

	// Create the playbook
	playbook := Playbook{
		ID:              uuid.New().String(),
		ProjectID:       generation.ProjectID,
		Name:            parsed.Name,
		Description:     parsed.Description,
		Type:            generation.PlaybookType,
		Status:          "draft",
		Source:          "llm_generated",
		LLMPromptUsed:   generation.PromptUsed,
		LLMModelVersion: generation.ModelUsed,
		CreatedBy:       accountID,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}

	if err := s.db.Create(&playbook).Error; err != nil {
		return nil, fmt.Errorf("failed to create playbook: %w", err)
	}

	// Create steps
	for _, step := range parsed.Steps {
		playbookStep := PlaybookStep{
			ID:           uuid.New().String(),
			PlaybookID:   playbook.ID,
			StepOrder:    step.StepOrder,
			Name:         step.Name,
			Description:  step.Description,
			ActionType:   step.ActionType,
			ActionConfig: step.ActionConfig,
			DelayMinutes: step.DelayMinutes,
			Conditions:   step.Conditions,
			IsRequired:   true,
			CreatedAt:    time.Now(),
			UpdatedAt:    time.Now(),
		}
		s.db.Create(&playbookStep)
	}

	// Create recommended triggers
	for _, trigger := range parsed.RecommendedTriggers {
		playbookTrigger := PlaybookTrigger{
			ID:          uuid.New().String(),
			PlaybookID:  playbook.ID,
			Name:        trigger.Name,
			TriggerType: trigger.TriggerType,
			Conditions:  trigger.Conditions,
			IsEnabled:   false, // Start disabled
			Priority:    0,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}
		s.db.Create(&playbookTrigger)
	}

	// Update generation with playbook ID
	generation.PlaybookID = &playbook.ID
	generation.Status = "applied"
	s.db.Save(generation)

	// Load relations
	s.db.Where("id = ?", playbook.ID).
		Preload("Steps").
		Preload("Triggers").
		First(&playbook)

	return &playbook, nil
}

// ==================
// HTTP Handlers
// ==================

// generatePlaybookHandler handles the LLM playbook generation request
func (s *Server) generatePlaybookHandler(c *gin.Context) {
	projectID := c.Param("project_id")
	accountID := c.GetString("account_id")

	if accountID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	// Verify project belongs to account
	var project Project
	if err := s.db.Where("id = ? AND account_id = ?", projectID, accountID).First(&project).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Project not found"})
		return
	}

	var req GeneratePlaybookLLMRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Initialize LLM service if not already done
	if s.llmService == nil {
		s.llmService = NewLLMService(s.db)
	}

	generation, err := s.llmService.GeneratePlaybook(projectID, req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, generation)
}

// getGenerationStatusHandler gets the status of a generation request
func (s *Server) getGenerationStatusHandler(c *gin.Context) {
	projectID := c.Param("project_id")
	generationID := c.Param("generation_id")
	accountID := c.GetString("account_id")

	if accountID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	// Verify project belongs to account
	var project Project
	if err := s.db.Where("id = ? AND account_id = ?", projectID, accountID).First(&project).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Project not found"})
		return
	}

	var generation LLMPlaybookGeneration
	if err := s.db.Where("id = ? AND project_id = ?", generationID, projectID).First(&generation).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Generation not found"})
		return
	}

	c.JSON(http.StatusOK, generation)
}

// applyGeneratedPlaybookHandler converts a generation to an actual playbook
func (s *Server) applyGeneratedPlaybookHandler(c *gin.Context) {
	projectID := c.Param("project_id")
	generationID := c.Param("generation_id")
	accountID := c.GetString("account_id")

	if accountID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	// Verify project belongs to account
	var project Project
	if err := s.db.Where("id = ? AND account_id = ?", projectID, accountID).First(&project).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Project not found"})
		return
	}

	var generation LLMPlaybookGeneration
	if err := s.db.Where("id = ? AND project_id = ?", generationID, projectID).First(&generation).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Generation not found"})
		return
	}

	if generation.Status != "completed" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Generation is not completed yet"})
		return
	}

	if generation.PlaybookID != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Generation has already been applied"})
		return
	}

	// Initialize LLM service if not already done
	if s.llmService == nil {
		s.llmService = NewLLMService(s.db)
	}

	playbook, err := s.llmService.ApplyGeneratedPlaybook(&generation, accountID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, playbook)
}

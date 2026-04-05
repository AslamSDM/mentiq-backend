package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
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
	ID      string `json:"id"`
	Type    string `json:"type"`
	Role    string `json:"role"`
	Content []struct {
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

// GeneratePlaybookLLMRequest represents the request to generate a playbook
type GeneratePlaybookLLMRequest struct {
	PlaybookType string                 `json:"playbook_type" binding:"required,oneof=churn_prevention growth_expansion onboarding feature_adoption"`
	ContextData  map[string]interface{} `json:"context_data"`
	Goals        []string               `json:"goals"`
}

// ProjectMetrics contains all gathered SaaS metrics for AI analysis
type ProjectMetrics struct {
	// Churn & Retention
	ChurnRate30d        float64 `json:"churn_rate_30d"`
	AtRiskUserCount     int     `json:"at_risk_user_count"`
	HighRiskUserCount   int     `json:"high_risk_user_count"`
	TotalActiveUsers    int     `json:"total_active_users"`
	ChurnedUsersLast30d int     `json:"churned_users_last_30d"`

	// Feature Adoption
	CoreFeatureAdoptionRate float64 `json:"core_feature_adoption_rate"`
	FeatureUsersWithin7Days int     `json:"feature_users_within_7_days"`
	TotalNewUsers7Days      int     `json:"total_new_users_7_days"`

	// Time to First Value
	MedianTTFVDays         float64 `json:"median_ttfv_days"`
	UsersWithFirstValue    int     `json:"users_with_first_value"`
	UsersWithoutFirstValue int     `json:"users_without_first_value"`

	// Session Frequency
	AvgSessionsPerUser7d  float64 `json:"avg_sessions_per_user_7d"`
	AvgSessionsPerUser30d float64 `json:"avg_sessions_per_user_30d"`
	SessionFrequencyDecay float64 `json:"session_frequency_decay_pct"`

	// Health Score Distribution
	HealthScoreHigh   int `json:"health_score_high_count"`
	HealthScoreMedium int `json:"health_score_medium_count"`
	HealthScoreLow    int `json:"health_score_low_count"`

	// Activation & Engagement
	ActivationRate float64 `json:"activation_rate"`
	Day1Retention  float64 `json:"day_1_retention"`
	Day7Retention  float64 `json:"day_7_retention"`
	Day30Retention float64 `json:"day_30_retention"`

	// Revenue (if available)
	MRR                float64 `json:"mrr"`
	MRRGrowthRate      float64 `json:"mrr_growth_rate"`
	ExpansionRevenue   float64 `json:"expansion_revenue"`
	ContractionRevenue float64 `json:"contraction_revenue"`
}

// ParsedPlaybook represents the structured output from LLM - NEW FORMAT
type ParsedPlaybook struct {
	Title            string   `json:"title"`
	ConfidenceLevel  string   `json:"confidence_level"`
	Impact           string   `json:"impact"`
	WhySeeingThis    string   `json:"why_seeing_this"`
	WhatDataShows    []string `json:"what_data_shows"`
	ChurnRiskIgnored struct {
		Summary     string   `json:"summary"`
		Projections []string `json:"projections"`
	} `json:"churn_risk_if_ignored"`
	RecommendedActions struct {
		Product   []string `json:"product"`
		Messaging []string `json:"messaging"`
		Lifecycle []string `json:"lifecycle"`
	} `json:"recommended_actions"`
	ExecutionChecklist []struct {
		Task      string `json:"task"`
		Completed bool   `json:"completed"`
	} `json:"execution_checklist"`
	SuccessMetrics []string `json:"success_metrics"`
}

// Legacy structures for backward compatibility
type ParsedStep struct {
	StepOrder    int                    `json:"step_order"`
	Name         string                 `json:"name"`
	Description  string                 `json:"description"`
	ActionType   string                 `json:"action_type"`
	ActionConfig map[string]interface{} `json:"action_config"`
	DelayMinutes int                    `json:"delay_minutes"`
	Conditions   map[string]interface{} `json:"conditions,omitempty"`
}

type ParsedTrigger struct {
	Name        string                 `json:"name"`
	TriggerType string                 `json:"trigger_type"`
	Conditions  map[string]interface{} `json:"conditions"`
}

// gatherProjectMetrics collects all relevant SaaS metrics for the project
func (s *LLMService) gatherProjectMetrics(projectID string) (*ProjectMetrics, error) {
	metrics := &ProjectMetrics{}
	now := time.Now()
	thirtyDaysAgo := now.AddDate(0, 0, -30)
	sevenDaysAgo := now.AddDate(0, 0, -7)

	// Get total active users (users with activity in last 30 days)
	var activeUserCount int64
	s.db.Model(&Event{}).
		Where("project_id = ? AND timestamp > ?", projectID, thirtyDaysAgo).
		Distinct("user_id").
		Count(&activeUserCount)
	metrics.TotalActiveUsers = int(activeUserCount)

	// Get churn risk data
	type ChurnStats struct {
		TotalAtRisk  int64
		HighRisk     int64
		ChurnedCount int64
	}
	var churnStats ChurnStats

	// Count users who haven't been active in 30+ days (churned)
	s.db.Raw(`
		SELECT 
			COUNT(DISTINCT CASE WHEN days_inactive > 14 THEN user_id END) as total_at_risk,
			COUNT(DISTINCT CASE WHEN days_inactive > 30 THEN user_id END) as high_risk,
			COUNT(DISTINCT CASE WHEN days_inactive > 60 THEN user_id END) as churned_count
		FROM (
			SELECT user_id, EXTRACT(DAY FROM NOW() - MAX(timestamp)) as days_inactive
			FROM events
			WHERE project_id = ? AND user_id IS NOT NULL AND user_id != ''
			GROUP BY user_id
		) user_stats
	`, projectID).Scan(&churnStats)

	metrics.AtRiskUserCount = int(churnStats.TotalAtRisk)
	metrics.HighRiskUserCount = int(churnStats.HighRisk)
	metrics.ChurnedUsersLast30d = int(churnStats.ChurnedCount)

	if metrics.TotalActiveUsers > 0 {
		metrics.ChurnRate30d = float64(metrics.ChurnedUsersLast30d) / float64(metrics.TotalActiveUsers) * 100
	}

	// Get new users in last 7 days and their feature adoption
	var newUserStats struct {
		TotalNewUsers    int64
		UsersWithFeature int64
	}
	s.db.Raw(`
		WITH new_users AS (
			SELECT DISTINCT user_id, MIN(timestamp) as first_seen
			FROM events
			WHERE project_id = ? AND user_id IS NOT NULL AND user_id != ''
			GROUP BY user_id
			HAVING MIN(timestamp) > ?
		),
		feature_users AS (
			SELECT DISTINCT e.user_id
			FROM events e
			INNER JOIN new_users nu ON e.user_id = nu.user_id
			WHERE e.project_id = ?
				AND e.event_type NOT IN ('page_view', 'pageview', 'session_start', 'session_end')
				AND e.timestamp BETWEEN nu.first_seen AND nu.first_seen + INTERVAL '7 days'
		)
		SELECT 
			(SELECT COUNT(*) FROM new_users) as total_new_users,
			(SELECT COUNT(*) FROM feature_users) as users_with_feature
	`, projectID, sevenDaysAgo, projectID).Scan(&newUserStats)

	metrics.TotalNewUsers7Days = int(newUserStats.TotalNewUsers)
	metrics.FeatureUsersWithin7Days = int(newUserStats.UsersWithFeature)
	if metrics.TotalNewUsers7Days > 0 {
		metrics.CoreFeatureAdoptionRate = float64(metrics.FeatureUsersWithin7Days) / float64(metrics.TotalNewUsers7Days) * 100
	}

	// Calculate Time to First Value (median days to first non-pageview action)
	var ttfvStats struct {
		MedianDays float64
		WithValue  int64
		NoValue    int64
	}
	s.db.Raw(`
		WITH user_first_action AS (
			SELECT user_id,
				MIN(timestamp) as first_seen,
				MIN(CASE WHEN event_type NOT IN ('page_view', 'pageview', 'session_start') THEN timestamp END) as first_action
			FROM events
			WHERE project_id = ? AND user_id IS NOT NULL AND user_id != ''
			GROUP BY user_id
		)
		SELECT 
			COALESCE(PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY EXTRACT(DAY FROM first_action - first_seen)), 0) as median_days,
			COUNT(CASE WHEN first_action IS NOT NULL THEN 1 END) as with_value,
			COUNT(CASE WHEN first_action IS NULL THEN 1 END) as no_value
		FROM user_first_action
	`, projectID).Scan(&ttfvStats)

	metrics.MedianTTFVDays = ttfvStats.MedianDays
	metrics.UsersWithFirstValue = int(ttfvStats.WithValue)
	metrics.UsersWithoutFirstValue = int(ttfvStats.NoValue)

	// Calculate session frequency
	var sessionStats struct {
		AvgSessions7d  float64
		AvgSessions30d float64
	}
	s.db.Raw(`
		SELECT 
			COALESCE(AVG(sessions_7d), 0) as avg_sessions_7d,
			COALESCE(AVG(sessions_30d), 0) as avg_sessions_30d
		FROM (
			SELECT user_id,
				COUNT(DISTINCT CASE WHEN timestamp > ? THEN session_id END) as sessions_7d,
				COUNT(DISTINCT session_id) as sessions_30d
			FROM events
			WHERE project_id = ? AND timestamp > ? AND user_id IS NOT NULL AND user_id != ''
			GROUP BY user_id
		) user_sessions
	`, sevenDaysAgo, projectID, thirtyDaysAgo).Scan(&sessionStats)

	metrics.AvgSessionsPerUser7d = sessionStats.AvgSessions7d
	metrics.AvgSessionsPerUser30d = sessionStats.AvgSessions30d

	// Calculate decay: (avg 30d / 4 weeks) vs avg 7d
	if metrics.AvgSessionsPerUser30d > 0 {
		weeklyAvg30d := metrics.AvgSessionsPerUser30d / 4
		if weeklyAvg30d > 0 {
			metrics.SessionFrequencyDecay = (weeklyAvg30d - metrics.AvgSessionsPerUser7d) / weeklyAvg30d * 100
		}
	}

	// Health score distribution (based on activity patterns)
	var healthDist struct {
		HighCount   int64
		MediumCount int64
		LowCount    int64
	}
	s.db.Raw(`
		SELECT 
			COUNT(CASE WHEN days_inactive < 7 AND session_count >= 3 THEN 1 END) as high_count,
			COUNT(CASE WHEN days_inactive BETWEEN 7 AND 14 OR session_count BETWEEN 1 AND 2 THEN 1 END) as medium_count,
			COUNT(CASE WHEN days_inactive > 14 OR session_count < 1 THEN 1 END) as low_count
		FROM (
			SELECT user_id,
				EXTRACT(DAY FROM NOW() - MAX(timestamp)) as days_inactive,
				COUNT(DISTINCT session_id) as session_count
			FROM events
			WHERE project_id = ? AND timestamp > ? AND user_id IS NOT NULL AND user_id != ''
			GROUP BY user_id
		) user_health
	`, projectID, thirtyDaysAgo).Scan(&healthDist)

	metrics.HealthScoreHigh = int(healthDist.HighCount)
	metrics.HealthScoreMedium = int(healthDist.MediumCount)
	metrics.HealthScoreLow = int(healthDist.LowCount)

	// Calculate retention rates
	var retentionStats struct {
		Day1  float64
		Day7  float64
		Day30 float64
	}
	s.db.Raw(`
		WITH user_cohort AS (
			SELECT user_id, MIN(timestamp)::date as signup_date
			FROM events
			WHERE project_id = ? AND user_id IS NOT NULL AND user_id != ''
			GROUP BY user_id
		),
		user_returns AS (
			SELECT uc.user_id, uc.signup_date,
				MAX(CASE WHEN e.timestamp::date = uc.signup_date + 1 THEN 1 ELSE 0 END) as day_1,
				MAX(CASE WHEN e.timestamp::date = uc.signup_date + 7 THEN 1 ELSE 0 END) as day_7,
				MAX(CASE WHEN e.timestamp::date = uc.signup_date + 30 THEN 1 ELSE 0 END) as day_30
			FROM user_cohort uc
			LEFT JOIN events e ON uc.user_id = e.user_id AND e.project_id = ?
			GROUP BY uc.user_id, uc.signup_date
		)
		SELECT 
			COALESCE(AVG(day_1) * 100, 0) as day_1,
			COALESCE(AVG(day_7) * 100, 0) as day_7,
			COALESCE(AVG(day_30) * 100, 0) as day_30
		FROM user_returns
	`, projectID, projectID).Scan(&retentionStats)

	metrics.Day1Retention = retentionStats.Day1
	metrics.Day7Retention = retentionStats.Day7
	metrics.Day30Retention = retentionStats.Day30

	// Activation rate (users who took action beyond pageviews)
	if metrics.TotalActiveUsers > 0 {
		metrics.ActivationRate = float64(metrics.UsersWithFirstValue) / float64(metrics.TotalActiveUsers) * 100
	}

	log.Printf("Gathered metrics for project %s: %+v", projectID, metrics)
	return metrics, nil
}

// GeneratePlaybook calls Claude to generate a playbook
func (s *LLMService) GeneratePlaybook(projectID string, req GeneratePlaybookLLMRequest) (*LLMPlaybookGeneration, error) {
	if s.apiKey == "" {
		return nil, fmt.Errorf("Anthropic API key not configured")
	}

	// Auto-gather project metrics
	metrics, err := s.gatherProjectMetrics(projectID)
	if err != nil {
		log.Printf("Warning: Failed to gather metrics for project %s: %v", projectID, err)
		metrics = &ProjectMetrics{} // Use empty metrics if gathering fails
	}

	// Merge auto-gathered metrics with any provided context
	if req.ContextData == nil {
		req.ContextData = make(map[string]interface{})
	}
	metricsJSON, _ := json.Marshal(metrics)
	var metricsMap map[string]interface{}
	json.Unmarshal(metricsJSON, &metricsMap)
	for k, v := range metricsMap {
		req.ContextData[k] = v
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
		ModelUsed:    "claude-haiku-4-5-20251001",
		CreatedAt:    time.Now(),
	}

	if err := s.db.Create(&generation).Error; err != nil {
		return nil, fmt.Errorf("failed to create generation record: %w", err)
	}

	// Call Claude API
	claudeReq := ClaudeRequest{
		Model:     "claude-haiku-4-5-20251001",
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
		return nil, fmt.Errorf("%s", errMsg)
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

// buildPlaybookPrompt creates the prompt for playbook generation - NEW FORMAT
func (s *LLMService) buildPlaybookPrompt(req GeneratePlaybookLLMRequest) string {
	playbookTypeDesc := map[string]string{
		"churn_prevention": "churn prevention",
		"growth_expansion": "growth and expansion",
		"onboarding":       "user onboarding optimization",
		"feature_adoption": "feature adoption improvement",
	}[req.PlaybookType]
	if playbookTypeDesc == "" {
		playbookTypeDesc = "user engagement"
	}

	contextJSON, _ := json.MarshalIndent(req.ContextData, "", "  ")

	return fmt.Sprintf(`You are an expert SaaS customer success strategist analyzing real product analytics data. Based on the following metrics, generate an actionable playbook for %s.

## Real SaaS Metrics from This Product
%s

## Task
Analyze these metrics and generate a data-driven playbook. Be specific and reference the actual numbers provided.

Return ONLY a valid JSON object (no markdown, no code blocks, just raw JSON) with this exact structure:

{
  "title": "Short, aggressive, outcome-driven title (e.g., 'Stop Silent Churn: Activate Dormant Users in 7 Days')",
  "confidence_level": "High|Medium|Low",
  "impact": "Brief impact statement (e.g., 'Immediate churn reduction', 'Revenue protection')",
  
  "why_seeing_this": "1-2 sentences explaining why this playbook was triggered based on the data. Reference specific metrics.",
  
  "what_data_shows": [
    "First key insight with specific number from the data",
    "Second key insight showing correlation or trend",
    "Third insight showing risk or opportunity",
    "Fourth insight with comparison or benchmark",
    "Fifth insight about user behavior pattern"
  ],
  
  "churn_risk_if_ignored": {
    "summary": "Plain-English explanation of what happens if no action is taken",
    "projections": [
      "Specific projection 1 (e.g., 'Monthly churn projected to increase by X%%')",
      "Specific projection 2",
      "Specific projection 3"
    ]
  },
  
  "recommended_actions": {
    "product": [
      "Concrete product change 1 (be specific about what to change)",
      "Concrete product change 2",
      "Concrete product change 3"
    ],
    "messaging": [
      "In-app messaging tactic 1",
      "In-app messaging tactic 2",
      "In-app messaging tactic 3"
    ],
    "lifecycle": [
      "Email/CS action 1 with timing",
      "Email/CS action 2 with timing",
      "Email/CS action 3"
    ]
  },
  
  "execution_checklist": [
    {"task": "Specific actionable task 1", "completed": false},
    {"task": "Specific actionable task 2", "completed": false},
    {"task": "Specific actionable task 3", "completed": false},
    {"task": "Specific actionable task 4", "completed": false},
    {"task": "Specific actionable task 5", "completed": false},
    {"task": "Specific actionable task 6", "completed": false}
  ],
  
  "success_metrics": [
    "Target metric 1 with specific number (e.g., 'Feature adoption ≥ 50%% within 7 days')",
    "Target metric 2 with specific number",
    "Target metric 3 with specific number"
  ]
}

Guidelines:
1. Reference ACTUAL numbers from the provided metrics - don't make up data
2. Be specific and actionable - no vague recommendations
3. Focus on the highest-impact opportunities based on the data
4. Make the title attention-grabbing and outcome-focused
5. Execution checklist should be copy-paste ready tasks
6. Success metrics should be measurable and time-bound

Return ONLY the JSON object, nothing else.`, playbookTypeDesc, string(contextJSON))
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

	// Create the playbook with new format data stored in description as JSON
	playbookData := map[string]interface{}{
		"title":                 parsed.Title,
		"confidence_level":      parsed.ConfidenceLevel,
		"impact":                parsed.Impact,
		"why_seeing_this":       parsed.WhySeeingThis,
		"what_data_shows":       parsed.WhatDataShows,
		"churn_risk_if_ignored": parsed.ChurnRiskIgnored,
		"recommended_actions":   parsed.RecommendedActions,
		"execution_checklist":   parsed.ExecutionChecklist,
		"success_metrics":       parsed.SuccessMetrics,
	}
	playbookDataJSON, _ := json.Marshal(playbookData)

	playbook := Playbook{
		ID:              uuid.New().String(),
		ProjectID:       generation.ProjectID,
		Name:            parsed.Title,
		Description:     string(playbookDataJSON), // Store full playbook data as JSON in description
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

	// Create action steps from recommended_actions
	stepOrder := 1

	// Product actions as steps
	for _, action := range parsed.RecommendedActions.Product {
		step := PlaybookStep{
			ID:          uuid.New().String(),
			PlaybookID:  playbook.ID,
			StepOrder:   stepOrder,
			Name:        fmt.Sprintf("Product: %s", truncateString(action, 50)),
			Description: action,
			ActionType:  "webhook", // Product changes often need webhook to trigger
			ActionConfig: map[string]interface{}{
				"category": "product",
				"action":   action,
			},
			DelayMinutes: 0,
			IsRequired:   true,
			CreatedAt:    time.Now(),
			UpdatedAt:    time.Now(),
		}
		s.db.Create(&step)
		stepOrder++
	}

	// Messaging actions as steps
	for _, action := range parsed.RecommendedActions.Messaging {
		step := PlaybookStep{
			ID:          uuid.New().String(),
			PlaybookID:  playbook.ID,
			StepOrder:   stepOrder,
			Name:        fmt.Sprintf("Messaging: %s", truncateString(action, 50)),
			Description: action,
			ActionType:  "in_app_message",
			ActionConfig: map[string]interface{}{
				"category":        "messaging",
				"message_content": action,
			},
			DelayMinutes: 0,
			IsRequired:   true,
			CreatedAt:    time.Now(),
			UpdatedAt:    time.Now(),
		}
		s.db.Create(&step)
		stepOrder++
	}

	// Lifecycle actions as steps
	for i, action := range parsed.RecommendedActions.Lifecycle {
		delayMinutes := i * 1440 // Each lifecycle step 1 day apart
		step := PlaybookStep{
			ID:          uuid.New().String(),
			PlaybookID:  playbook.ID,
			StepOrder:   stepOrder,
			Name:        fmt.Sprintf("Lifecycle: %s", truncateString(action, 50)),
			Description: action,
			ActionType:  "email",
			ActionConfig: map[string]interface{}{
				"category":        "lifecycle",
				"message_content": action,
			},
			DelayMinutes: delayMinutes,
			IsRequired:   true,
			CreatedAt:    time.Now(),
			UpdatedAt:    time.Now(),
		}
		s.db.Create(&step)
		stepOrder++
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

// Helper function to truncate strings
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// generateContent generates content using Claude API for automation service
func (s *LLMService) generateContent(prompt string) (string, error) {
	if s.apiKey == "" {
		return "", fmt.Errorf("LLM service not configured - ANTHROPIC_API_KEY not set")
	}

	claudeReq := ClaudeRequest{
		Model:     "claude-haiku-4-5-20251001",
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
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequest("POST", s.baseURL+"/messages", bytes.NewBuffer(reqBody))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", s.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Claude API error: %s", string(body))
	}

	var claudeResp ClaudeResponse
	if err := json.Unmarshal(body, &claudeResp); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	if len(claudeResp.Content) > 0 && claudeResp.Content[0].Type == "text" {
		return claudeResp.Content[0].Text, nil
	}

	return "", fmt.Errorf("no content in Claude response")
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

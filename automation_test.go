package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// ============================================================================
// TEST SETUP & MOCKS
// ============================================================================

// MockLLMService mocks the LLM service for testing
type MockLLMService struct {
	mock.Mock
}

func (m *MockLLMService) generateContent(prompt string) (string, error) {
	args := m.Called(prompt)
	return args.String(0), args.Error(1)
}

// MockMailchimpService mocks the Mailchimp service for testing
type MockMailchimpService struct {
	mock.Mock
}

func (m *MockMailchimpService) CreateAndSendCampaign(projectID string, req *MailchimpCampaignRequest, htmlContent string) (string, error) {
	args := m.Called(projectID, req, htmlContent)
	return args.String(0), args.Error(1)
}

// setupTestDB creates an in-memory SQLite database for testing
func setupTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	// Auto-migrate test tables
	err = db.AutoMigrate(
		&AutomationSettings{},
		&EmailTemplate{},
		&DiscountCode{},
		&AutomationExecution{},
		&Project{},
		&Account{},
	)
	require.NoError(t, err)

	return db
}

// setupTestServer creates a test server with router
func setupTestServer(t *testing.T) (*Server, *gin.Engine) {
	gin.SetMode(gin.TestMode)
	db := setupTestDB(t)

	server := &Server{
		db: db,
	}

	router := gin.New()
	return server, router
}

// ============================================================================
// UNIT TESTS: Helper Functions
// ============================================================================

func TestGetString(t *testing.T) {
	tests := []struct {
		name     string
		input    map[string]interface{}
		key      string
		expected string
	}{
		{
			name:     "string value exists",
			input:    map[string]interface{}{"name": "John"},
			key:      "name",
			expected: "John",
		},
		{
			name:     "key does not exist",
			input:    map[string]interface{}{"other": "value"},
			key:      "name",
			expected: "",
		},
		{
			name:     "value is not string",
			input:    map[string]interface{}{"name": 123},
			key:      "name",
			expected: "",
		},
		{
			name:     "empty map",
			input:    map[string]interface{}{},
			key:      "name",
			expected: "",
		},
		{
			name:     "nil value",
			input:    map[string]interface{}{"name": nil},
			key:      "name",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getString(tt.input, tt.key)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetFloat(t *testing.T) {
	tests := []struct {
		name     string
		input    map[string]interface{}
		key      string
		expected float64
	}{
		{
			name:     "float value exists",
			input:    map[string]interface{}{"score": 85.5},
			key:      "score",
			expected: 85.5,
		},
		{
			name:     "key does not exist",
			input:    map[string]interface{}{"other": 100.0},
			key:      "score",
			expected: 0,
		},
		{
			name:     "value is not float",
			input:    map[string]interface{}{"score": "85.5"},
			key:      "score",
			expected: 0,
		},
		{
			name:     "zero value",
			input:    map[string]interface{}{"score": 0.0},
			key:      "score",
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getFloat(tt.input, tt.key)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetInt(t *testing.T) {
	tests := []struct {
		name     string
		input    map[string]interface{}
		key      string
		expected int
	}{
		{
			name:     "int value exists",
			input:    map[string]interface{}{"count": 42},
			key:      "count",
			expected: 42,
		},
		{
			name:     "float64 value (JSON default)",
			input:    map[string]interface{}{"count": float64(42)},
			key:      "count",
			expected: 42,
		},
		{
			name:     "string value parseable",
			input:    map[string]interface{}{"count": "42"},
			key:      "count",
			expected: 42,
		},
		{
			name:     "key does not exist",
			input:    map[string]interface{}{"other": 100},
			key:      "count",
			expected: 0,
		},
		{
			name:     "unparseable string",
			input:    map[string]interface{}{"count": "not a number"},
			key:      "count",
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getInt(tt.input, tt.key)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetStringSlice(t *testing.T) {
	tests := []struct {
		name     string
		input    map[string]interface{}
		key      string
		expected []string
	}{
		{
			name:     "slice exists",
			input:    map[string]interface{}{"features": []interface{}{"a", "b", "c"}},
			key:      "features",
			expected: []string{"a", "b", "c"},
		},
		{
			name:     "mixed types in slice",
			input:    map[string]interface{}{"features": []interface{}{"a", 123, true}},
			key:      "features",
			expected: []string{"a", "123", "true"},
		},
		{
			name:     "key does not exist",
			input:    map[string]interface{}{"other": []interface{}{"x"}},
			key:      "features",
			expected: []string{},
		},
		{
			name:     "empty slice",
			input:    map[string]interface{}{"features": []interface{}{}},
			key:      "features",
			expected: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getStringSlice(tt.input, tt.key)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// ============================================================================
// UNIT TESTS: stripHTML
// ============================================================================

func TestStripHTML(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "simple HTML",
			input:    "<p>Hello World</p>",
			expected: "Hello World",
		},
		{
			name:     "nested HTML",
			input:    "<div><p>Hello <strong>World</strong></p></div>",
			expected: "Hello World",
		},
		{
			name:     "br tags converted to newlines",
			input:    "Line 1<br>Line 2<br/>Line 3",
			expected: "Line 1\nLine 2\nLine 3",
		},
		{
			name:     "p tags add double newlines",
			input:    "<p>Paragraph 1</p><p>Paragraph 2</p>",
			expected: "Paragraph 1\n\nParagraph 2",
		},
		{
			name:     "no HTML",
			input:    "Plain text",
			expected: "Plain text",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "HTML with attributes",
			input:    `<a href="https://example.com" class="link">Click here</a>`,
			expected: "Click here",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := stripHTML(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// ============================================================================
// UNIT TESTS: substituteVariables
// ============================================================================

func TestSubstituteVariables(t *testing.T) {
	service := &AutomationService{}

	tests := []struct {
		name     string
		content  string
		vars     map[string]interface{}
		expected string
	}{
		{
			name:    "single variable",
			content: "Hello {{user_name}}!",
			vars:    map[string]interface{}{"user_name": "John"},
			expected: "Hello John!",
		},
		{
			name:    "multiple variables",
			content: "Hello {{user_name}}, your discount is {{discount_percent}}%",
			vars: map[string]interface{}{
				"user_name":        "John",
				"discount_percent": 20,
			},
			expected: "Hello John, your discount is 20%",
		},
		{
			name:    "variable not found",
			content: "Hello {{user_name}}!",
			vars:    map[string]interface{}{},
			expected: "Hello {{user_name}}!",
		},
		{
			name:     "no variables in content",
			content:  "Hello World!",
			vars:     map[string]interface{}{"user_name": "John"},
			expected: "Hello World!",
		},
		{
			name:     "empty content",
			content:  "",
			vars:     map[string]interface{}{"user_name": "John"},
			expected: "",
		},
		{
			name:    "repeated variable",
			content: "{{name}} said: Hello {{name}}!",
			vars:    map[string]interface{}{"name": "John"},
			expected: "John said: Hello John!",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := service.substituteVariables(tt.content, tt.vars)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// ============================================================================
// UNIT TESTS: createDefaultTemplate
// ============================================================================

func TestCreateDefaultTemplate(t *testing.T) {
	service := &AutomationService{}

	tests := []struct {
		name         string
		templateType string
		checkName    string
		checkType    string
	}{
		{
			name:         "churn prevention template",
			templateType: "churn_prevention",
			checkName:    "Default Churn Prevention",
			checkType:    "churn_prevention",
		},
		{
			name:         "feature adoption template",
			templateType: "feature_adoption",
			checkName:    "Default Feature Adoption",
			checkType:    "feature_adoption",
		},
		{
			name:         "engagement template",
			templateType: "engagement",
			checkName:    "Default Re-engagement",
			checkType:    "engagement",
		},
		{
			name:         "unknown type falls back to default",
			templateType: "unknown",
			checkName:    "Default Email",
			checkType:    "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			template := service.createDefaultTemplate(tt.templateType)

			assert.Equal(t, tt.checkName, template.Name)
			assert.Equal(t, tt.checkType, template.Type)
			assert.True(t, template.IsActive)
			assert.NotEmpty(t, template.SubjectTemplate)
			assert.NotEmpty(t, template.ContentTemplate)
			assert.NotEmpty(t, template.PersonalizationVars)
		})
	}
}

// ============================================================================
// UNIT TESTS: buildEmailPrompt
// ============================================================================

func TestBuildEmailPrompt(t *testing.T) {
	service := &AutomationService{}

	tests := []struct {
		name         string
		req          GenerateEmailContentRequest
		mustContain  []string
		mustNotContain []string
	}{
		{
			name: "churn prevention prompt",
			req: GenerateEmailContentRequest{
				TemplateType: "churn_prevention",
				UserContext: map[string]interface{}{
					"name":             "John Doe",
					"email":            "john@example.com",
					"churn_risk_score": float64(85),
					"last_active_days": 14,
				},
				ProductContext: map[string]interface{}{
					"product_name": "MentiQ",
					"features":     []string{"Analytics", "Heatmaps"},
				},
				Personalization: map[string]interface{}{
					"discount_percent": 20,
				},
			},
			mustContain: []string{
				"John Doe",
				"john@example.com",
				"Churn Prevention",
				"85%",
				"MentiQ",
				"20%",
			},
		},
		{
			name: "feature adoption prompt",
			req: GenerateEmailContentRequest{
				TemplateType: "feature_adoption",
				UserContext: map[string]interface{}{
					"name": "Jane",
				},
				Personalization: map[string]interface{}{
					"unused_features": []string{"Heatmaps", "A/B Testing"},
				},
			},
			mustContain: []string{
				"Feature Adoption",
				"Jane",
				"Heatmaps",
			},
		},
		{
			name: "engagement prompt",
			req: GenerateEmailContentRequest{
				TemplateType: "engagement",
				UserContext: map[string]interface{}{
					"name": "Bob",
				},
			},
			mustContain: []string{
				"Re-engagement",
				"Bob",
			},
		},
		{
			name: "discount prompt",
			req: GenerateEmailContentRequest{
				TemplateType: "discount",
				Personalization: map[string]interface{}{
					"discount_code":    "SAVE20",
					"discount_percent": 20,
				},
			},
			mustContain: []string{
				"Discount",
				"SAVE20",
				"20%",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prompt := service.buildEmailPrompt(tt.req)

			for _, s := range tt.mustContain {
				assert.Contains(t, prompt, s, "Prompt should contain: %s", s)
			}
		})
	}
}

// ============================================================================
// INTEGRATION TESTS: Automation Handlers
// ============================================================================

func TestCreateAutomationHandler(t *testing.T) {
	server, router := setupTestServer(t)

	// Setup route
	router.POST("/projects/:project_id/automations", server.createAutomationHandler)

	tests := []struct {
		name           string
		projectID      string
		body           AutomationRequest
		expectedStatus int
	}{
		{
			name:      "valid churn prevention automation",
			projectID: "proj_123",
			body: AutomationRequest{
				Name:        "Churn Prevention Campaign",
				Description: "Prevent user churn",
				Type:        "churn_prevention",
				IsEnabled:   true,
				Config: map[string]interface{}{
					"risk_threshold":    70,
					"discount_percent":  20,
				},
			},
			expectedStatus: http.StatusCreated,
		},
		{
			name:      "valid feature adoption automation",
			projectID: "proj_123",
			body: AutomationRequest{
				Name:      "Feature Adoption Campaign",
				Type:      "feature_adoption",
				IsEnabled: false,
			},
			expectedStatus: http.StatusCreated,
		},
		{
			name:      "missing name",
			projectID: "proj_123",
			body: AutomationRequest{
				Type:      "churn_prevention",
				IsEnabled: true,
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:      "invalid type",
			projectID: "proj_123",
			body: AutomationRequest{
				Name:      "Invalid Automation",
				Type:      "invalid_type",
				IsEnabled: true,
			},
			expectedStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bodyBytes, _ := json.Marshal(tt.body)
			req := httptest.NewRequest(http.MethodPost, "/projects/"+tt.projectID+"/automations", bytes.NewReader(bodyBytes))
			req.Header.Set("Content-Type", "application/json")

			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)

			if tt.expectedStatus == http.StatusCreated {
				var result AutomationSettings
				err := json.Unmarshal(w.Body.Bytes(), &result)
				require.NoError(t, err)
				assert.Equal(t, tt.body.Name, result.Name)
				assert.Equal(t, tt.body.Type, result.Type)
				assert.Equal(t, tt.projectID, result.ProjectID)
				assert.NotEmpty(t, result.ID)
			}
		})
	}
}

func TestGetAutomationsHandler(t *testing.T) {
	server, router := setupTestServer(t)

	// Setup route
	router.GET("/projects/:project_id/automations", server.getAutomationsHandler)

	// Seed test data
	projectID := "proj_test_123"
	automations := []AutomationSettings{
		{
			ID:        "auto_1",
			ProjectID: projectID,
			Name:      "Churn Prevention",
			Type:      "churn_prevention",
			IsEnabled: true,
		},
		{
			ID:        "auto_2",
			ProjectID: projectID,
			Name:      "Feature Adoption",
			Type:      "feature_adoption",
			IsEnabled: false,
		},
		{
			ID:        "auto_3",
			ProjectID: "other_project",
			Name:      "Other Project Automation",
			Type:      "engagement",
			IsEnabled: true,
		},
	}

	for _, a := range automations {
		server.db.Create(&a)
	}

	t.Run("get automations for project", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/projects/"+projectID+"/automations", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var result struct {
			Automations []AutomationSettings `json:"automations"`
		}
		err := json.Unmarshal(w.Body.Bytes(), &result)
		require.NoError(t, err)

		assert.Len(t, result.Automations, 2)
	})

	t.Run("get automations for empty project", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/projects/nonexistent/automations", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var result struct {
			Automations []AutomationSettings `json:"automations"`
		}
		err := json.Unmarshal(w.Body.Bytes(), &result)
		require.NoError(t, err)

		assert.Len(t, result.Automations, 0)
	})
}

func TestUpdateAutomationHandler(t *testing.T) {
	server, router := setupTestServer(t)

	router.PUT("/projects/:project_id/automations/:automation_id", server.updateAutomationHandler)

	// Seed test data
	automation := AutomationSettings{
		ID:          "auto_update_test",
		ProjectID:   "proj_123",
		Name:        "Original Name",
		Description: "Original Description",
		Type:        "churn_prevention",
		IsEnabled:   false,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	server.db.Create(&automation)

	t.Run("successful update", func(t *testing.T) {
		updateReq := AutomationRequest{
			Name:        "Updated Name",
			Description: "Updated Description",
			Type:        "churn_prevention",
			IsEnabled:   true,
		}
		bodyBytes, _ := json.Marshal(updateReq)

		req := httptest.NewRequest(http.MethodPut, "/projects/proj_123/automations/auto_update_test", bytes.NewReader(bodyBytes))
		req.Header.Set("Content-Type", "application/json")

		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var result AutomationSettings
		err := json.Unmarshal(w.Body.Bytes(), &result)
		require.NoError(t, err)

		assert.Equal(t, "Updated Name", result.Name)
		assert.Equal(t, "Updated Description", result.Description)
		assert.True(t, result.IsEnabled)
	})

	t.Run("automation not found", func(t *testing.T) {
		updateReq := AutomationRequest{
			Name:      "Updated Name",
			Type:      "churn_prevention",
			IsEnabled: true,
		}
		bodyBytes, _ := json.Marshal(updateReq)

		req := httptest.NewRequest(http.MethodPut, "/projects/proj_123/automations/nonexistent", bytes.NewReader(bodyBytes))
		req.Header.Set("Content-Type", "application/json")

		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
	})
}

func TestDeleteAutomationHandler(t *testing.T) {
	server, router := setupTestServer(t)

	router.DELETE("/projects/:project_id/automations/:automation_id", server.deleteAutomationHandler)

	// Seed test data
	automation := AutomationSettings{
		ID:        "auto_delete_test",
		ProjectID: "proj_123",
		Name:      "To Be Deleted",
		Type:      "engagement",
	}
	server.db.Create(&automation)

	t.Run("successful delete", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/projects/proj_123/automations/auto_delete_test", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		// Verify deletion
		var count int64
		server.db.Model(&AutomationSettings{}).Where("id = ?", "auto_delete_test").Count(&count)
		assert.Equal(t, int64(0), count)
	})
}

// ============================================================================
// INTEGRATION TESTS: Email Template Handlers
// ============================================================================

func TestEmailTemplateHandlers(t *testing.T) {
	server, router := setupTestServer(t)

	router.POST("/projects/:project_id/email-templates", server.createEmailTemplateHandler)
	router.GET("/projects/:project_id/email-templates", server.getEmailTemplatesHandler)
	router.PUT("/projects/:project_id/email-templates/:template_id", server.updateEmailTemplateHandler)
	router.DELETE("/projects/:project_id/email-templates/:template_id", server.deleteEmailTemplateHandler)

	projectID := "proj_email_test"

	t.Run("create email template", func(t *testing.T) {
		req := EmailTemplateRequest{
			Name:                "Churn Email",
			Type:                "churn_prevention",
			SubjectTemplate:     "Hey {{user_name}}, we miss you!",
			ContentTemplate:     "Hi {{user_name}}, please come back!",
			PersonalizationVars: []string{"user_name", "discount_code"},
			IsActive:            true,
		}
		bodyBytes, _ := json.Marshal(req)

		httpReq := httptest.NewRequest(http.MethodPost, "/projects/"+projectID+"/email-templates", bytes.NewReader(bodyBytes))
		httpReq.Header.Set("Content-Type", "application/json")

		w := httptest.NewRecorder()
		router.ServeHTTP(w, httpReq)

		assert.Equal(t, http.StatusCreated, w.Code)

		var result EmailTemplate
		err := json.Unmarshal(w.Body.Bytes(), &result)
		require.NoError(t, err)
		assert.Equal(t, req.Name, result.Name)
		assert.Equal(t, projectID, result.ProjectID)
	})

	t.Run("get email templates", func(t *testing.T) {
		httpReq := httptest.NewRequest(http.MethodGet, "/projects/"+projectID+"/email-templates", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httpReq)

		assert.Equal(t, http.StatusOK, w.Code)

		var result struct {
			Templates []EmailTemplate `json:"templates"`
		}
		err := json.Unmarshal(w.Body.Bytes(), &result)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(result.Templates), 1)
	})

	t.Run("get email templates filtered by type", func(t *testing.T) {
		httpReq := httptest.NewRequest(http.MethodGet, "/projects/"+projectID+"/email-templates?type=churn_prevention", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httpReq)

		assert.Equal(t, http.StatusOK, w.Code)
	})
}

// ============================================================================
// INTEGRATION TESTS: Discount Code Handlers
// ============================================================================

func TestDiscountCodeHandlers(t *testing.T) {
	server, router := setupTestServer(t)

	router.POST("/projects/:project_id/discount-codes", server.createDiscountCodeHandler)
	router.GET("/projects/:project_id/discount-codes", server.getDiscountCodesHandler)
	router.PUT("/projects/:project_id/discount-codes/:code_id", server.updateDiscountCodeHandler)

	projectID := "proj_discount_test"

	t.Run("create discount code", func(t *testing.T) {
		validUntil := time.Now().Add(30 * 24 * time.Hour).Format(time.RFC3339)
		req := DiscountCodeRequest{
			Code:            "SAVE20",
			DiscountPercent: 20,
			ValidUntil:      &validUntil,
			MaxUses:         100,
			IsActive:        true,
		}
		bodyBytes, _ := json.Marshal(req)

		httpReq := httptest.NewRequest(http.MethodPost, "/projects/"+projectID+"/discount-codes", bytes.NewReader(bodyBytes))
		httpReq.Header.Set("Content-Type", "application/json")

		w := httptest.NewRecorder()
		router.ServeHTTP(w, httpReq)

		assert.Equal(t, http.StatusCreated, w.Code)

		var result DiscountCode
		err := json.Unmarshal(w.Body.Bytes(), &result)
		require.NoError(t, err)
		assert.Equal(t, "SAVE20", result.Code)
		assert.Equal(t, 20, result.DiscountPercent)
		assert.Equal(t, 0, result.UsedCount)
	})

	t.Run("create discount code with invalid percent", func(t *testing.T) {
		req := DiscountCodeRequest{
			Code:            "INVALID",
			DiscountPercent: 150, // Invalid: > 100
			MaxUses:         100,
			IsActive:        true,
		}
		bodyBytes, _ := json.Marshal(req)

		httpReq := httptest.NewRequest(http.MethodPost, "/projects/"+projectID+"/discount-codes", bytes.NewReader(bodyBytes))
		httpReq.Header.Set("Content-Type", "application/json")

		w := httptest.NewRecorder()
		router.ServeHTTP(w, httpReq)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("get discount codes", func(t *testing.T) {
		httpReq := httptest.NewRequest(http.MethodGet, "/projects/"+projectID+"/discount-codes", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httpReq)

		assert.Equal(t, http.StatusOK, w.Code)
	})
}

// ============================================================================
// INTEGRATION TESTS: Automation Execution Handler
// ============================================================================

func TestGetAutomationExecutionsHandler(t *testing.T) {
	server, router := setupTestServer(t)

	router.GET("/projects/:project_id/automation-executions", server.getAutomationExecutionsHandler)

	projectID := "proj_exec_test"
	now := time.Now()

	// Seed test data
	executions := []AutomationExecution{
		{
			ID:            "exec_1",
			AutomationID:  "auto_1",
			ProjectID:     projectID,
			UserID:        "user_1",
			Status:        "sent",
			TriggerReason: "churn_risk_85",
			SentAt:        &now,
			CreatedAt:     now,
		},
		{
			ID:            "exec_2",
			AutomationID:  "auto_1",
			ProjectID:     projectID,
			UserID:        "user_2",
			Status:        "pending",
			TriggerReason: "churn_risk_90",
			CreatedAt:     now,
		},
		{
			ID:            "exec_3",
			AutomationID:  "auto_2",
			ProjectID:     projectID,
			UserID:        "user_1",
			Status:        "failed",
			TriggerReason: "feature_adoption",
			FailureReason: "Mailchimp API error",
			CreatedAt:     now,
		},
	}

	for _, e := range executions {
		server.db.Create(&e)
	}

	t.Run("get all executions", func(t *testing.T) {
		httpReq := httptest.NewRequest(http.MethodGet, "/projects/"+projectID+"/automation-executions", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httpReq)

		assert.Equal(t, http.StatusOK, w.Code)

		var result struct {
			Executions []AutomationExecution `json:"executions"`
		}
		err := json.Unmarshal(w.Body.Bytes(), &result)
		require.NoError(t, err)
		assert.Len(t, result.Executions, 3)
	})

	t.Run("filter by status", func(t *testing.T) {
		httpReq := httptest.NewRequest(http.MethodGet, "/projects/"+projectID+"/automation-executions?status=sent", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httpReq)

		assert.Equal(t, http.StatusOK, w.Code)

		var result struct {
			Executions []AutomationExecution `json:"executions"`
		}
		err := json.Unmarshal(w.Body.Bytes(), &result)
		require.NoError(t, err)
		assert.Len(t, result.Executions, 1)
		assert.Equal(t, "sent", result.Executions[0].Status)
	})

	t.Run("filter by automation_id", func(t *testing.T) {
		httpReq := httptest.NewRequest(http.MethodGet, "/projects/"+projectID+"/automation-executions?automation_id=auto_1", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httpReq)

		assert.Equal(t, http.StatusOK, w.Code)

		var result struct {
			Executions []AutomationExecution `json:"executions"`
		}
		err := json.Unmarshal(w.Body.Bytes(), &result)
		require.NoError(t, err)
		assert.Len(t, result.Executions, 2)
	})

	t.Run("filter by user_id", func(t *testing.T) {
		httpReq := httptest.NewRequest(http.MethodGet, "/projects/"+projectID+"/automation-executions?user_id=user_1", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httpReq)

		assert.Equal(t, http.StatusOK, w.Code)

		var result struct {
			Executions []AutomationExecution `json:"executions"`
		}
		err := json.Unmarshal(w.Body.Bytes(), &result)
		require.NoError(t, err)
		assert.Len(t, result.Executions, 2)
	})
}

// ============================================================================
// UNIT TESTS: AutomationExecutor
// ============================================================================

func TestAutomationExecutorStartStop(t *testing.T) {
	db := setupTestDB(t)

	executor := NewAutomationExecutor(db, nil, nil)

	// Test that Start doesn't panic
	executor.Start()

	// Give it a moment to start
	time.Sleep(100 * time.Millisecond)

	// Test that Stop doesn't panic
	executor.Stop()

	// Give it a moment to stop
	time.Sleep(100 * time.Millisecond)
}

func TestProcessAutomation(t *testing.T) {
	db := setupTestDB(t)

	executor := &AutomationExecutor{
		db:                db,
		automationService: nil,
		mailchimpService:  nil,
	}

	tests := []struct {
		name       string
		automation AutomationSettings
	}{
		{
			name: "churn prevention type",
			automation: AutomationSettings{
				ID:        "test_churn",
				Type:      "churn_prevention",
				ProjectID: "proj_1",
				Config:    map[string]interface{}{},
			},
		},
		{
			name: "feature adoption type",
			automation: AutomationSettings{
				ID:        "test_feature",
				Type:      "feature_adoption",
				ProjectID: "proj_1",
				Config:    map[string]interface{}{},
			},
		},
		{
			name: "engagement type",
			automation: AutomationSettings{
				ID:        "test_engagement",
				Type:      "engagement",
				ProjectID: "proj_1",
				Config:    map[string]interface{}{},
			},
		},
		{
			name: "unknown type",
			automation: AutomationSettings{
				ID:        "test_unknown",
				Type:      "unknown_type",
				ProjectID: "proj_1",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// This should not panic even with nil services
			// (it will fail gracefully when trying to process)
			assert.NotPanics(t, func() {
				executor.processAutomation(&tt.automation)
			})
		})
	}
}

// ============================================================================
// BENCHMARK TESTS
// ============================================================================

func BenchmarkStripHTML(b *testing.B) {
	html := `<html><body><div class="container"><h1>Hello World</h1><p>This is a <strong>test</strong> with <a href="#">links</a>.</p></div></body></html>`

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		stripHTML(html)
	}
}

func BenchmarkSubstituteVariables(b *testing.B) {
	service := &AutomationService{}
	content := "Hello {{user_name}}, your discount code is {{discount_code}} for {{discount_percent}}% off {{product_name}}!"
	vars := map[string]interface{}{
		"user_name":        "John Doe",
		"discount_code":    "SAVE20",
		"discount_percent": 20,
		"product_name":     "MentiQ Pro",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		service.substituteVariables(content, vars)
	}
}

func BenchmarkBuildEmailPrompt(b *testing.B) {
	service := &AutomationService{}
	req := GenerateEmailContentRequest{
		TemplateType: "churn_prevention",
		UserContext: map[string]interface{}{
			"name":             "John Doe",
			"email":            "john@example.com",
			"churn_risk_score": float64(85),
			"last_active_days": 14,
		},
		ProductContext: map[string]interface{}{
			"product_name": "MentiQ",
			"features":     []string{"Analytics", "Heatmaps", "Session Replay"},
		},
		Personalization: map[string]interface{}{
			"discount_percent": 20,
			"discount_code":    "SAVE20",
		},
		PersonalizationVars: []string{"user_name", "discount_percent", "discount_code"},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		service.buildEmailPrompt(req)
	}
}

package main

import (
	"net/http"
	"sort"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ==================
// Playbook CRUD Handlers
// ==================

// CreatePlaybookRequest represents the request body for creating a playbook
type CreatePlaybookRequest struct {
	Name        string `json:"name" binding:"required"`
	Description string `json:"description"`
	Type        string `json:"type" binding:"required,oneof=churn_prevention growth_expansion onboarding engagement"`
}

// UpdatePlaybookRequest represents the request body for updating a playbook
type UpdatePlaybookRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Type        string `json:"type"`
}

// createPlaybookHandler creates a new playbook
func (s *Server) createPlaybookHandler(c *gin.Context) {
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

	var req CreatePlaybookRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	playbook := Playbook{
		ID:          uuid.New().String(),
		ProjectID:   projectID,
		Name:        req.Name,
		Description: req.Description,
		Type:        req.Type,
		Status:      "draft",
		Source:      "manual",
		CreatedBy:   accountID,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	if err := s.db.Create(&playbook).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create playbook"})
		return
	}

	c.JSON(http.StatusCreated, playbook)
}

// getPlaybooksHandler lists all playbooks for a project
func (s *Server) getPlaybooksHandler(c *gin.Context) {
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

	var playbooks []Playbook
	if err := s.db.Where("project_id = ?", projectID).
		Preload("Steps").
		Preload("Triggers").
		Order("created_at DESC").
		Find(&playbooks).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch playbooks"})
		return
	}

	// Add enrollment counts and stats
	type PlaybookWithStats struct {
		Playbook
		TotalEnrolled   int64   `json:"total_enrolled"`
		ActiveEnrolled  int64   `json:"active_enrolled"`
		CompletedCount  int64   `json:"completed_count"`
		CompletionRate  float64 `json:"completion_rate"`
	}

	result := make([]PlaybookWithStats, len(playbooks))
	for i, pb := range playbooks {
		result[i].Playbook = pb

		// Count enrollments
		s.db.Model(&PlaybookEnrollment{}).Where("playbook_id = ?", pb.ID).Count(&result[i].TotalEnrolled)
		s.db.Model(&PlaybookEnrollment{}).Where("playbook_id = ? AND status = ?", pb.ID, "active").Count(&result[i].ActiveEnrolled)
		s.db.Model(&PlaybookEnrollment{}).Where("playbook_id = ? AND status = ?", pb.ID, "completed").Count(&result[i].CompletedCount)

		if result[i].TotalEnrolled > 0 {
			result[i].CompletionRate = float64(result[i].CompletedCount) / float64(result[i].TotalEnrolled) * 100
		}
	}

	c.JSON(http.StatusOK, result)
}

// getPlaybookHandler gets a single playbook by ID
func (s *Server) getPlaybookHandler(c *gin.Context) {
	projectID := c.Param("project_id")
	playbookID := c.Param("playbook_id")
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

	var playbook Playbook
	if err := s.db.Where("id = ? AND project_id = ?", playbookID, projectID).
		Preload("Steps").
		Preload("Triggers").
		First(&playbook).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Playbook not found"})
		return
	}

	// Sort steps by order
	sort.Slice(playbook.Steps, func(i, j int) bool {
		return playbook.Steps[i].StepOrder < playbook.Steps[j].StepOrder
	})

	c.JSON(http.StatusOK, playbook)
}

// updatePlaybookHandler updates a playbook
func (s *Server) updatePlaybookHandler(c *gin.Context) {
	projectID := c.Param("project_id")
	playbookID := c.Param("playbook_id")
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

	var playbook Playbook
	if err := s.db.Where("id = ? AND project_id = ?", playbookID, projectID).First(&playbook).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Playbook not found"})
		return
	}

	var req UpdatePlaybookRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	updates := map[string]interface{}{
		"updated_at": time.Now(),
	}

	if req.Name != "" {
		updates["name"] = req.Name
	}
	if req.Description != "" {
		updates["description"] = req.Description
	}
	if req.Type != "" {
		updates["type"] = req.Type
	}

	if err := s.db.Model(&playbook).Updates(updates).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update playbook"})
		return
	}

	// Reload with relations
	s.db.Where("id = ?", playbookID).Preload("Steps").Preload("Triggers").First(&playbook)

	c.JSON(http.StatusOK, playbook)
}

// deletePlaybookHandler deletes a playbook
func (s *Server) deletePlaybookHandler(c *gin.Context) {
	projectID := c.Param("project_id")
	playbookID := c.Param("playbook_id")
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

	var playbook Playbook
	if err := s.db.Where("id = ? AND project_id = ?", playbookID, projectID).First(&playbook).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Playbook not found"})
		return
	}

	// Delete related records first
	s.db.Where("playbook_id = ?", playbookID).Delete(&PlaybookStep{})
	s.db.Where("playbook_id = ?", playbookID).Delete(&PlaybookTrigger{})
	s.db.Where("playbook_id = ?", playbookID).Delete(&PlaybookEnrollment{})
	s.db.Where("playbook_id = ?", playbookID).Delete(&PlaybookAnalytics{})

	if err := s.db.Delete(&playbook).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete playbook"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Playbook deleted successfully"})
}

// UpdateStatusRequest represents the request to update playbook status
type UpdateStatusRequest struct {
	Status string `json:"status" binding:"required,oneof=draft active paused archived"`
}

// updatePlaybookStatusHandler updates the status of a playbook (activate/pause/archive)
func (s *Server) updatePlaybookStatusHandler(c *gin.Context) {
	projectID := c.Param("project_id")
	playbookID := c.Param("playbook_id")
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

	var playbook Playbook
	if err := s.db.Where("id = ? AND project_id = ?", playbookID, projectID).First(&playbook).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Playbook not found"})
		return
	}

	var req UpdateStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	updates := map[string]interface{}{
		"status":     req.Status,
		"updated_at": time.Now(),
	}

	// Set activated_at when activating for the first time
	if req.Status == "active" && playbook.ActivatedAt == nil {
		now := time.Now()
		updates["activated_at"] = now
	}

	if err := s.db.Model(&playbook).Updates(updates).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update status"})
		return
	}

	s.db.Where("id = ?", playbookID).First(&playbook)
	c.JSON(http.StatusOK, playbook)
}

// ==================
// Step Handlers
// ==================

// CreateStepRequest represents the request to create a playbook step
type CreateStepRequest struct {
	Name         string                 `json:"name" binding:"required"`
	Description  string                 `json:"description"`
	ActionType   string                 `json:"action_type" binding:"required,oneof=email in_app_message webhook wait condition feature_flag"`
	ActionConfig map[string]interface{} `json:"action_config"`
	DelayMinutes int                    `json:"delay_minutes"`
	Conditions   map[string]interface{} `json:"conditions"`
	IsRequired   bool                   `json:"is_required"`
}

// addStepHandler adds a step to a playbook
func (s *Server) addStepHandler(c *gin.Context) {
	projectID := c.Param("project_id")
	playbookID := c.Param("playbook_id")
	accountID := c.GetString("account_id")

	if accountID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	// Verify access
	var project Project
	if err := s.db.Where("id = ? AND account_id = ?", projectID, accountID).First(&project).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Project not found"})
		return
	}

	var playbook Playbook
	if err := s.db.Where("id = ? AND project_id = ?", playbookID, projectID).First(&playbook).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Playbook not found"})
		return
	}

	var req CreateStepRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Get the next step order
	var maxOrder int
	s.db.Model(&PlaybookStep{}).Where("playbook_id = ?", playbookID).Select("COALESCE(MAX(step_order), 0)").Scan(&maxOrder)

	step := PlaybookStep{
		ID:           uuid.New().String(),
		PlaybookID:   playbookID,
		StepOrder:    maxOrder + 1,
		Name:         req.Name,
		Description:  req.Description,
		ActionType:   req.ActionType,
		ActionConfig: req.ActionConfig,
		DelayMinutes: req.DelayMinutes,
		Conditions:   req.Conditions,
		IsRequired:   req.IsRequired,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}

	if err := s.db.Create(&step).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create step"})
		return
	}

	c.JSON(http.StatusCreated, step)
}

// updateStepHandler updates a playbook step
func (s *Server) updateStepHandler(c *gin.Context) {
	projectID := c.Param("project_id")
	playbookID := c.Param("playbook_id")
	stepID := c.Param("step_id")
	accountID := c.GetString("account_id")

	if accountID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	// Verify access
	var project Project
	if err := s.db.Where("id = ? AND account_id = ?", projectID, accountID).First(&project).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Project not found"})
		return
	}

	var playbook Playbook
	if err := s.db.Where("id = ? AND project_id = ?", playbookID, projectID).First(&playbook).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Playbook not found"})
		return
	}

	var step PlaybookStep
	if err := s.db.Where("id = ? AND playbook_id = ?", stepID, playbookID).First(&step).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Step not found"})
		return
	}

	var req CreateStepRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	updates := map[string]interface{}{
		"name":          req.Name,
		"description":   req.Description,
		"action_type":   req.ActionType,
		"action_config": req.ActionConfig,
		"delay_minutes": req.DelayMinutes,
		"conditions":    req.Conditions,
		"is_required":   req.IsRequired,
		"updated_at":    time.Now(),
	}

	if err := s.db.Model(&step).Updates(updates).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update step"})
		return
	}

	s.db.Where("id = ?", stepID).First(&step)
	c.JSON(http.StatusOK, step)
}

// deleteStepHandler deletes a playbook step
func (s *Server) deleteStepHandler(c *gin.Context) {
	projectID := c.Param("project_id")
	playbookID := c.Param("playbook_id")
	stepID := c.Param("step_id")
	accountID := c.GetString("account_id")

	if accountID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	// Verify access
	var project Project
	if err := s.db.Where("id = ? AND account_id = ?", projectID, accountID).First(&project).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Project not found"})
		return
	}

	var playbook Playbook
	if err := s.db.Where("id = ? AND project_id = ?", playbookID, projectID).First(&playbook).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Playbook not found"})
		return
	}

	var step PlaybookStep
	if err := s.db.Where("id = ? AND playbook_id = ?", stepID, playbookID).First(&step).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Step not found"})
		return
	}

	if err := s.db.Delete(&step).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete step"})
		return
	}

	// Reorder remaining steps
	var remainingSteps []PlaybookStep
	s.db.Where("playbook_id = ?", playbookID).Order("step_order").Find(&remainingSteps)
	for i, rs := range remainingSteps {
		s.db.Model(&rs).Update("step_order", i+1)
	}

	c.JSON(http.StatusOK, gin.H{"message": "Step deleted successfully"})
}

// ==================
// Trigger Handlers
// ==================

// CreateTriggerRequest represents the request to create a trigger
type CreateTriggerRequest struct {
	Name            string                 `json:"name" binding:"required"`
	TriggerType     string                 `json:"trigger_type" binding:"required,oneof=event metric_threshold segment schedule"`
	Conditions      map[string]interface{} `json:"conditions" binding:"required"`
	IsEnabled       bool                   `json:"is_enabled"`
	Priority        int                    `json:"priority"`
	CooldownMinutes int                    `json:"cooldown_minutes"`
	MaxEnrollments  int                    `json:"max_enrollments"`
}

// createTriggerHandler creates a new trigger for a playbook
func (s *Server) createTriggerHandler(c *gin.Context) {
	projectID := c.Param("project_id")
	playbookID := c.Param("playbook_id")
	accountID := c.GetString("account_id")

	if accountID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	// Verify access
	var project Project
	if err := s.db.Where("id = ? AND account_id = ?", projectID, accountID).First(&project).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Project not found"})
		return
	}

	var playbook Playbook
	if err := s.db.Where("id = ? AND project_id = ?", playbookID, projectID).First(&playbook).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Playbook not found"})
		return
	}

	var req CreateTriggerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	trigger := PlaybookTrigger{
		ID:              uuid.New().String(),
		PlaybookID:      playbookID,
		Name:            req.Name,
		TriggerType:     req.TriggerType,
		Conditions:      req.Conditions,
		IsEnabled:       req.IsEnabled,
		Priority:        req.Priority,
		CooldownMinutes: req.CooldownMinutes,
		MaxEnrollments:  req.MaxEnrollments,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}

	if err := s.db.Create(&trigger).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create trigger"})
		return
	}

	c.JSON(http.StatusCreated, trigger)
}

// getTriggersHandler lists all triggers for a playbook
func (s *Server) getTriggersHandler(c *gin.Context) {
	projectID := c.Param("project_id")
	playbookID := c.Param("playbook_id")
	accountID := c.GetString("account_id")

	if accountID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	// Verify access
	var project Project
	if err := s.db.Where("id = ? AND account_id = ?", projectID, accountID).First(&project).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Project not found"})
		return
	}

	var playbook Playbook
	if err := s.db.Where("id = ? AND project_id = ?", playbookID, projectID).First(&playbook).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Playbook not found"})
		return
	}

	var triggers []PlaybookTrigger
	if err := s.db.Where("playbook_id = ?", playbookID).Order("priority DESC").Find(&triggers).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch triggers"})
		return
	}

	c.JSON(http.StatusOK, triggers)
}

// updateTriggerHandler updates a trigger
func (s *Server) updateTriggerHandler(c *gin.Context) {
	projectID := c.Param("project_id")
	playbookID := c.Param("playbook_id")
	triggerID := c.Param("trigger_id")
	accountID := c.GetString("account_id")

	if accountID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	// Verify access
	var project Project
	if err := s.db.Where("id = ? AND account_id = ?", projectID, accountID).First(&project).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Project not found"})
		return
	}

	var trigger PlaybookTrigger
	if err := s.db.Where("id = ? AND playbook_id = ?", triggerID, playbookID).First(&trigger).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Trigger not found"})
		return
	}

	var req CreateTriggerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	updates := map[string]interface{}{
		"name":             req.Name,
		"trigger_type":     req.TriggerType,
		"conditions":       req.Conditions,
		"is_enabled":       req.IsEnabled,
		"priority":         req.Priority,
		"cooldown_minutes": req.CooldownMinutes,
		"max_enrollments":  req.MaxEnrollments,
		"updated_at":       time.Now(),
	}

	if err := s.db.Model(&trigger).Updates(updates).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update trigger"})
		return
	}

	s.db.Where("id = ?", triggerID).First(&trigger)
	c.JSON(http.StatusOK, trigger)
}

// deleteTriggerHandler deletes a trigger
func (s *Server) deleteTriggerHandler(c *gin.Context) {
	projectID := c.Param("project_id")
	playbookID := c.Param("playbook_id")
	triggerID := c.Param("trigger_id")
	accountID := c.GetString("account_id")

	if accountID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	// Verify access
	var project Project
	if err := s.db.Where("id = ? AND account_id = ?", projectID, accountID).First(&project).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Project not found"})
		return
	}

	var trigger PlaybookTrigger
	if err := s.db.Where("id = ? AND playbook_id = ?", triggerID, playbookID).First(&trigger).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Trigger not found"})
		return
	}

	if err := s.db.Delete(&trigger).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete trigger"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Trigger deleted successfully"})
}

// toggleTriggerHandler enables/disables a trigger
func (s *Server) toggleTriggerHandler(c *gin.Context) {
	projectID := c.Param("project_id")
	playbookID := c.Param("playbook_id")
	triggerID := c.Param("trigger_id")
	accountID := c.GetString("account_id")

	if accountID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	// Verify access
	var project Project
	if err := s.db.Where("id = ? AND account_id = ?", projectID, accountID).First(&project).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Project not found"})
		return
	}

	var trigger PlaybookTrigger
	if err := s.db.Where("id = ? AND playbook_id = ?", triggerID, playbookID).First(&trigger).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Trigger not found"})
		return
	}

	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := s.db.Model(&trigger).Updates(map[string]interface{}{
		"is_enabled": req.Enabled,
		"updated_at": time.Now(),
	}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to toggle trigger"})
		return
	}

	s.db.Where("id = ?", triggerID).First(&trigger)
	c.JSON(http.StatusOK, trigger)
}

// ==================
// Enrollment Handlers
// ==================

// EnrollUsersRequest represents the request to enroll users
type EnrollUsersRequest struct {
	UserIDs []string `json:"user_ids" binding:"required"`
}

// enrollUsersHandler manually enrolls users in a playbook
func (s *Server) enrollUsersHandler(c *gin.Context) {
	projectID := c.Param("project_id")
	playbookID := c.Param("playbook_id")
	accountID := c.GetString("account_id")

	if accountID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	// Verify access
	var project Project
	if err := s.db.Where("id = ? AND account_id = ?", projectID, accountID).First(&project).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Project not found"})
		return
	}

	var playbook Playbook
	if err := s.db.Where("id = ? AND project_id = ?", playbookID, projectID).First(&playbook).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Playbook not found"})
		return
	}

	var req EnrollUsersRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	enrollments := make([]PlaybookEnrollment, 0, len(req.UserIDs))
	now := time.Now()

	for _, userID := range req.UserIDs {
		// Check if already enrolled
		var existing PlaybookEnrollment
		if err := s.db.Where("playbook_id = ? AND user_id = ? AND status = ?", playbookID, userID, "active").First(&existing).Error; err == nil {
			continue // Already enrolled
		}

		enrollment := PlaybookEnrollment{
			ID:               uuid.New().String(),
			PlaybookID:       playbookID,
			ProjectID:        projectID,
			UserID:           userID,
			Status:           "active",
			CurrentStepOrder: 1,
			EnrolledAt:       now,
			MetricsAtStart:   map[string]interface{}{},
			CreatedAt:        now,
			UpdatedAt:        now,
		}

		if err := s.db.Create(&enrollment).Error; err == nil {
			enrollments = append(enrollments, enrollment)
		}
	}

	c.JSON(http.StatusCreated, gin.H{
		"enrolled_count": len(enrollments),
		"enrollments":    enrollments,
	})
}

// getEnrollmentsHandler lists enrollments for a playbook
func (s *Server) getEnrollmentsHandler(c *gin.Context) {
	projectID := c.Param("project_id")
	playbookID := c.Param("playbook_id")
	accountID := c.GetString("account_id")

	if accountID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	// Verify access
	var project Project
	if err := s.db.Where("id = ? AND account_id = ?", projectID, accountID).First(&project).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Project not found"})
		return
	}

	status := c.Query("status") // Optional filter

	query := s.db.Where("playbook_id = ?", playbookID)
	if status != "" {
		query = query.Where("status = ?", status)
	}

	var enrollments []PlaybookEnrollment
	if err := query.Order("enrolled_at DESC").Find(&enrollments).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch enrollments"})
		return
	}

	c.JSON(http.StatusOK, enrollments)
}

// exitEnrollmentHandler exits a user from a playbook
func (s *Server) exitEnrollmentHandler(c *gin.Context) {
	projectID := c.Param("project_id")
	playbookID := c.Param("playbook_id")
	enrollmentID := c.Param("enrollment_id")
	accountID := c.GetString("account_id")

	if accountID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	// Verify access
	var project Project
	if err := s.db.Where("id = ? AND account_id = ?", projectID, accountID).First(&project).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Project not found"})
		return
	}

	var enrollment PlaybookEnrollment
	if err := s.db.Where("id = ? AND playbook_id = ?", enrollmentID, playbookID).First(&enrollment).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Enrollment not found"})
		return
	}

	var req struct {
		Reason string `json:"reason"`
	}
	c.ShouldBindJSON(&req)

	now := time.Now()
	if err := s.db.Model(&enrollment).Updates(map[string]interface{}{
		"status":      "exited",
		"exited_at":   now,
		"exit_reason": req.Reason,
		"updated_at":  now,
	}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to exit enrollment"})
		return
	}

	s.db.Where("id = ?", enrollmentID).First(&enrollment)
	c.JSON(http.StatusOK, enrollment)
}

// ==================
// Analytics Handlers
// ==================

// getPlaybookAnalyticsHandler returns analytics for a playbook
func (s *Server) getPlaybookAnalyticsHandler(c *gin.Context) {
	projectID := c.Param("project_id")
	playbookID := c.Param("playbook_id")
	accountID := c.GetString("account_id")

	if accountID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	// Verify access
	var project Project
	if err := s.db.Where("id = ? AND account_id = ?", projectID, accountID).First(&project).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Project not found"})
		return
	}

	var playbook Playbook
	if err := s.db.Where("id = ? AND project_id = ?", playbookID, projectID).First(&playbook).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Playbook not found"})
		return
	}

	// Calculate analytics
	var totalEnrollments int64
	var activeEnrollments int64
	var completedEnrollments int64
	var exitedEnrollments int64

	s.db.Model(&PlaybookEnrollment{}).Where("playbook_id = ?", playbookID).Count(&totalEnrollments)
	s.db.Model(&PlaybookEnrollment{}).Where("playbook_id = ? AND status = ?", playbookID, "active").Count(&activeEnrollments)
	s.db.Model(&PlaybookEnrollment{}).Where("playbook_id = ? AND status = ?", playbookID, "completed").Count(&completedEnrollments)
	s.db.Model(&PlaybookEnrollment{}).Where("playbook_id = ? AND status = ?", playbookID, "exited").Count(&exitedEnrollments)

	completionRate := float64(0)
	if totalEnrollments > 0 {
		completionRate = float64(completedEnrollments) / float64(totalEnrollments) * 100
	}

	// Get step counts
	var stepCount int64
	s.db.Model(&PlaybookStep{}).Where("playbook_id = ?", playbookID).Count(&stepCount)

	// Get trigger count
	var triggerCount int64
	s.db.Model(&PlaybookTrigger{}).Where("playbook_id = ? AND is_enabled = ?", playbookID, true).Count(&triggerCount)

	c.JSON(http.StatusOK, gin.H{
		"playbook_id":           playbookID,
		"total_enrollments":     totalEnrollments,
		"active_enrollments":    activeEnrollments,
		"completed_enrollments": completedEnrollments,
		"exited_enrollments":    exitedEnrollments,
		"completion_rate":       completionRate,
		"step_count":            stepCount,
		"active_trigger_count":  triggerCount,
	})
}

// getPlaybooksSummaryHandler returns summary analytics for all playbooks
func (s *Server) getPlaybooksSummaryHandler(c *gin.Context) {
	projectID := c.Param("project_id")
	accountID := c.GetString("account_id")

	if accountID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	// Verify access
	var project Project
	if err := s.db.Where("id = ? AND account_id = ?", projectID, accountID).First(&project).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Project not found"})
		return
	}

	var activePlaybooks int64
	var totalTriggers int64
	var totalEnrollments int64
	var completedEnrollments int64

	s.db.Model(&Playbook{}).Where("project_id = ? AND status = ?", projectID, "active").Count(&activePlaybooks)
	s.db.Model(&PlaybookTrigger{}).
		Joins("JOIN playbook ON playbook.id = playbook_trigger.playbook_id").
		Where("playbook.project_id = ?", projectID).
		Count(&totalTriggers)
	s.db.Model(&PlaybookEnrollment{}).Where("project_id = ?", projectID).Count(&totalEnrollments)
	s.db.Model(&PlaybookEnrollment{}).Where("project_id = ? AND status = ?", projectID, "completed").Count(&completedEnrollments)

	completionRate := float64(0)
	if totalEnrollments > 0 {
		completionRate = float64(completedEnrollments) / float64(totalEnrollments) * 100
	}

	// Count in-progress
	var inProgress int64
	s.db.Model(&PlaybookEnrollment{}).Where("project_id = ? AND status = ?", projectID, "active").Count(&inProgress)

	c.JSON(http.StatusOK, gin.H{
		"active_playbooks":   activePlaybooks,
		"total_triggers":     totalTriggers,
		"completion_rate":    completionRate,
		"in_progress":        inProgress,
		"total_enrollments":  totalEnrollments,
	})
}

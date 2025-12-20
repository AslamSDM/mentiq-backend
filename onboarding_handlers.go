package main

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// getOnboardingStatusHandler retrieves the onboarding status for the authenticated account
func (s *Server) getOnboardingStatusHandler(c *gin.Context) {
	accountID := c.GetString("account_id")
	if accountID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	var status OnboardingStatus
	err := s.db.Where("account_id = ?", accountID).First(&status).Error
	if err != nil {
		// If not found, create a new onboarding status
		status = OnboardingStatus{
			ID:        uuid.New().String(),
			AccountID: accountID,
		}
		if err := s.db.Create(&status).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create onboarding status"})
			return
		}
	}

	c.JSON(http.StatusOK, status)
}

// updateOnboardingStatusHandler updates specific onboarding steps
func (s *Server) updateOnboardingStatusHandler(c *gin.Context) {
	accountID := c.GetString("account_id")
	if accountID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	var req struct {
		DataConnectionShown bool   `json:"data_connection_shown"`
		DataConnected       bool   `json:"data_connected"`
		PlatformSelected    string `json:"platform_selected"`
		SDKInstalled        bool   `json:"sdk_installed"`
		FirstEventTracked   bool   `json:"first_event_tracked"`
		StripeConnected     bool   `json:"stripe_connected"`
		TeamMembersInvited  bool   `json:"team_members_invited"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var status OnboardingStatus
	err := s.db.Where("account_id = ?", accountID).First(&status).Error
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Onboarding status not found"})
		return
	}

	now := time.Now()
	updates := make(map[string]interface{})

	// Update fields if they've changed
	if req.DataConnectionShown && !status.DataConnectionShown {
		updates["data_connection_shown"] = true
	}

	if req.DataConnected && !status.DataConnected {
		updates["data_connected"] = true
		updates["data_connected_at"] = now
	}

	if req.PlatformSelected != "" && req.PlatformSelected != status.PlatformSelected {
		updates["platform_selected"] = req.PlatformSelected
	}

	if req.SDKInstalled && !status.SDKInstalled {
		updates["sdk_installed"] = true
		updates["sdk_installed_at"] = now
	}

	if req.FirstEventTracked && !status.FirstEventTracked {
		updates["first_event_tracked"] = true
		updates["first_event_tracked_at"] = now
	}

	if req.StripeConnected && !status.StripeConnected {
		updates["stripe_connected"] = true
		updates["stripe_connected_at"] = now
	}

	if req.TeamMembersInvited && !status.TeamMembersInvited {
		updates["team_members_invited"] = true
		updates["team_members_invited_at"] = now
	}

	// Check if all tasks are complete
	allComplete := (req.DataConnected || status.DataConnected) &&
		(req.FirstEventTracked || status.FirstEventTracked) &&
		(req.StripeConnected || status.StripeConnected) &&
		(req.TeamMembersInvited || status.TeamMembersInvited)

	if allComplete && !status.OnboardingComplete {
		updates["onboarding_complete"] = true
		updates["completed_at"] = now
	}

	if len(updates) > 0 {
		if err := s.db.Model(&status).Updates(updates).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update onboarding status"})
			return
		}

		// Fetch updated status
		s.db.Where("account_id = ?", accountID).First(&status)
	}

	c.JSON(http.StatusOK, status)
}

// markTaskCompleteHandler marks a specific onboarding task as complete
func (s *Server) markTaskCompleteHandler(c *gin.Context) {
	accountID := c.GetString("account_id")
	if accountID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	taskName := c.Param("task")
	if taskName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Task name is required"})
		return
	}

	var status OnboardingStatus
	err := s.db.Where("account_id = ?", accountID).First(&status).Error
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Onboarding status not found"})
		return
	}

	now := time.Now()
	updates := make(map[string]interface{})

	switch taskName {
	case "data_connected":
		updates["data_connected"] = true
		updates["data_connected_at"] = now
	case "sdk_installed":
		updates["sdk_installed"] = true
		updates["sdk_installed_at"] = now
	case "first_event":
		updates["first_event_tracked"] = true
		updates["first_event_tracked_at"] = now
	case "stripe_connected":
		updates["stripe_connected"] = true
		updates["stripe_connected_at"] = now
	case "team_invited":
		updates["team_members_invited"] = true
		updates["team_members_invited_at"] = now
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid task name"})
		return
	}

	if err := s.db.Model(&status).Updates(updates).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to mark task complete"})
		return
	}

	// Fetch updated status and check if all complete
	s.db.Where("account_id = ?", accountID).First(&status)

	allComplete := status.DataConnected &&
		status.FirstEventTracked &&
		status.StripeConnected &&
		status.TeamMembersInvited

	if allComplete && !status.OnboardingComplete {
		s.db.Model(&status).Updates(map[string]interface{}{
			"onboarding_complete": true,
			"completed_at":        now,
		})
		s.db.Where("account_id = ?", accountID).First(&status)
	}

	c.JSON(http.StatusOK, status)
}

// getOnboardingTasksHandler returns the list of pending onboarding tasks
func (s *Server) getOnboardingTasksHandler(c *gin.Context) {
	accountID := c.GetString("account_id")
	if accountID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	var status OnboardingStatus
	err := s.db.Where("account_id = ?", accountID).First(&status).Error
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Onboarding status not found"})
		return
	}

	type Task struct {
		ID          string     `json:"id"`
		Title       string     `json:"title"`
		Description string     `json:"description"`
		Completed   bool       `json:"completed"`
		CompletedAt *time.Time `json:"completed_at,omitempty"`
		Route       string     `json:"route"`
	}

	tasks := []Task{
		{
			ID:          "first_event",
			Title:       "Start tracking events",
			Description: "Install the SDK and track your first user event",
			Completed:   status.FirstEventTracked,
			CompletedAt: status.FirstEventTrackedAt,
			Route:       "/dashboard/onboarding/setup",
		},
		{
			ID:          "stripe_connected",
			Title:       "Connect Stripe",
			Description: "Add your Stripe API key to track revenue and churn",
			Completed:   status.StripeConnected,
			CompletedAt: status.StripeConnectedAt,
			Route:       "/dashboard/settings?tab=integrations",
		},
		{
			ID:          "team_invited",
			Title:       "Invite team members",
			Description: "Collaborate with your team on analytics and insights",
			Completed:   status.TeamMembersInvited,
			CompletedAt: status.TeamMembersInvitedAt,
			Route:       "/dashboard/settings?tab=team",
		},
	}

	pendingTasks := []Task{}
	completedTasks := []Task{}

	for _, task := range tasks {
		if task.Completed {
			completedTasks = append(completedTasks, task)
		} else {
			pendingTasks = append(pendingTasks, task)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"pending":             pendingTasks,
		"completed":           completedTasks,
		"onboarding_complete": status.OnboardingComplete,
		"platform_selected":   status.PlatformSelected,
	})
}

package main

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Project Member Management Handlers

// addProjectMemberHandler adds a user to a project
func (s *Server) addProjectMemberHandler(c *gin.Context) {
	projectID := c.Param("id")
	accountID, _ := c.Get("account_id")

	var req struct {
		UserID string `json:"user_id" binding:"required"`
		Role   string `json:"role"` // owner, admin, member, viewer
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Default role if not specified
	if req.Role == "" {
		req.Role = "member"
	}

	// Validate role
	validRoles := map[string]bool{
		"owner":  true,
		"admin":  true,
		"member": true,
		"viewer": true,
	}
	if !validRoles[req.Role] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid role. Must be: owner, admin, member, or viewer"})
		return
	}

	// Verify project exists and belongs to user's account
	var project Project
	if err := s.db.Where("id = ? AND account_id = ?", projectID, accountID).First(&project).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Project not found"})
		return
	}

	// Verify user exists and belongs to the same account
	var user User
	if err := s.db.Where("id = ? AND account_id = ?", req.UserID, accountID).First(&user).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found in this account"})
		return
	}

	// Check if user is already a member
	var existingMember ProjectMember
	if err := s.db.Where("project_id = ? AND user_id = ?", projectID, req.UserID).First(&existingMember).Error; err == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "User is already a member of this project"})
		return
	}

	// Create project member
	member := ProjectMember{
		ID:        uuid.New().String(),
		ProjectID: projectID,
		UserID:    req.UserID,
		Role:      req.Role,
	}

	if err := s.db.Create(&member).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to add user to project"})
		return
	}

	// Fetch the member with user details
	if err := s.db.Preload("User").First(&member, "id = ?", member.ID).Error; err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "User added to project", "member": member})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"message": "User added to project successfully",
		"member":  member,
	})
}

// listProjectMembersHandler lists all members of a project
func (s *Server) listProjectMembersHandler(c *gin.Context) {
	projectID := c.Param("id")
	accountID, _ := c.Get("account_id")

	// Verify project exists and belongs to user's account
	var project Project
	if err := s.db.Where("id = ? AND account_id = ?", projectID, accountID).First(&project).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Project not found"})
		return
	}

	// Get all members with user details
	var members []ProjectMember
	if err := s.db.Preload("User").Where("project_id = ?", projectID).Find(&members).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch project members"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"project_id": projectID,
		"members":    members,
		"total":      len(members),
	})
}

// updateProjectMemberHandler updates a member's role in a project
func (s *Server) updateProjectMemberHandler(c *gin.Context) {
	projectID := c.Param("id")
	memberID := c.Param("member_id")
	accountID, _ := c.Get("account_id")

	var req struct {
		Role string `json:"role" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Validate role
	validRoles := map[string]bool{
		"owner":  true,
		"admin":  true,
		"member": true,
		"viewer": true,
	}
	if !validRoles[req.Role] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid role. Must be: owner, admin, member, or viewer"})
		return
	}

	// Verify project exists and belongs to user's account
	var project Project
	if err := s.db.Where("id = ? AND account_id = ?", projectID, accountID).First(&project).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Project not found"})
		return
	}

	// Find and update the member
	var member ProjectMember
	if err := s.db.Where("id = ? AND project_id = ?", memberID, projectID).First(&member).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Member not found"})
		return
	}

	// Update role
	member.Role = req.Role
	if err := s.db.Save(&member).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update member role"})
		return
	}

	// Fetch updated member with user details
	s.db.Preload("User").First(&member, "id = ?", member.ID)

	c.JSON(http.StatusOK, gin.H{
		"message": "Member role updated successfully",
		"member":  member,
	})
}

// removeProjectMemberHandler removes a user from a project
func (s *Server) removeProjectMemberHandler(c *gin.Context) {
	projectID := c.Param("id")
	memberID := c.Param("member_id")
	accountID, _ := c.Get("account_id")

	// Verify project exists and belongs to user's account
	var project Project
	if err := s.db.Where("id = ? AND account_id = ?", projectID, accountID).First(&project).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Project not found"})
		return
	}

	// Find the member
	var member ProjectMember
	if err := s.db.Where("id = ? AND project_id = ?", memberID, projectID).First(&member).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Member not found"})
		return
	}

	// Check if this is the last owner
	var ownerCount int64
	s.db.Model(&ProjectMember{}).Where("project_id = ? AND role = ?", projectID, "owner").Count(&ownerCount)
	
	if member.Role == "owner" && ownerCount <= 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Cannot remove the last owner from the project"})
		return
	}

	// Delete the member
	if err := s.db.Delete(&member).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to remove member from project"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Member removed from project successfully",
	})
}

// getUserProjectsHandler gets all projects a user has access to (via membership)
func (s *Server) getUserProjectsHandler(c *gin.Context) {
	userID := c.Param("user_id")
	accountID, _ := c.Get("account_id")

	// Verify user exists and belongs to the account
	var user User
	if err := s.db.Where("id = ? AND account_id = ?", userID, accountID).First(&user).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		return
	}

	// Get all project memberships with project details
	var memberships []ProjectMember
	if err := s.db.Preload("Project").Where("user_id = ?", userID).Find(&memberships).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch user projects"})
		return
	}

	// Extract projects
	projects := make([]map[string]interface{}, len(memberships))
	for i, membership := range memberships {
		projects[i] = map[string]interface{}{
			"id":          membership.Project.ID,
			"name":        membership.Project.Name,
			"description": membership.Project.Description,
			"role":        membership.Role,
			"created_at":  membership.Project.CreatedAt,
			"updated_at":  membership.Project.UpdatedAt,
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"user_id":  userID,
		"projects": projects,
		"total":    len(projects),
	})
}
